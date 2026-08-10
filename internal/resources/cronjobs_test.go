package resources

import (
	"errors"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/kute-dev/kute/internal/kube"
)

func boolPtr(b bool) *bool { return &b }

// controllerRef builds a controller=true owner reference, the only kind
// cronJobAssociation/indexPodsByJob honor.
func controllerRef(kind, name string, uid types.UID) metav1.OwnerReference {
	return metav1.OwnerReference{Kind: kind, Name: name, UID: uid, Controller: boolPtr(true)}
}

func newCronJob(namespace, name string, uid types.UID, schedule string) *batchv1.CronJob {
	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, UID: uid},
		Spec:       batchv1.CronJobSpec{Schedule: schedule},
	}
}

func completeJob(namespace, name string, completedAt time.Time, refs []metav1.OwnerReference, annotations map[string]string) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, OwnerReferences: refs, Annotations: annotations},
		Spec:       batchv1.JobSpec{Completions: ptr32(1)},
		Status: batchv1.JobStatus{
			Succeeded:      1,
			CompletionTime: &metav1.Time{Time: completedAt},
			Conditions:     []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}},
		},
	}
}

// Test 1 (plan): a scheduled owner reference at any owner-reference index —
// not just index 0 — is included.
func TestBuildCronJobSummariesOwnerRefAtAnyIndex(t *testing.T) {
	cj := newCronJob("default", "nightly", "cj-1", "0 2 * * *")
	job := completeJob("default", "nightly-run", time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC), []metav1.OwnerReference{
		{Kind: "ReplicaSet", Name: "unrelated"}, // not a controller ref — must be skipped, not matched
		controllerRef("CronJob", cj.Name, cj.UID),
	}, nil)

	summaries := BuildCronJobSummaries([]runtime.Object{cj}, []runtime.Object{job}, nil)
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	if len(summaries[0].Runs) != 1 || summaries[0].Runs[0].Name != "nightly-run" {
		t.Fatalf("expected the owner ref at index 1 to associate the Job, got Runs=%+v", summaries[0].Runs)
	}
	if summaries[0].Runs[0].Source != JobSourceScheduled {
		t.Fatalf("expected JobSourceScheduled, got %v", summaries[0].Runs[0].Source)
	}
}

// Test 2 (plan): UID match is required when UIDs exist on both sides.
func TestBuildCronJobSummariesRequiresUIDMatchWhenBothPresent(t *testing.T) {
	cj := newCronJob("default", "nightly", "cj-real-uid", "0 2 * * *")
	matching := completeJob("default", "nightly-match", time.Now(), []metav1.OwnerReference{
		controllerRef("CronJob", cj.Name, cj.UID),
	}, nil)
	mismatched := completeJob("default", "nightly-mismatch", time.Now(), []metav1.OwnerReference{
		controllerRef("CronJob", cj.Name, "cj-different-uid"),
	}, nil)

	summaries := BuildCronJobSummaries([]runtime.Object{cj}, []runtime.Object{matching, mismatched}, nil)
	if len(summaries[0].Runs) != 1 || summaries[0].Runs[0].Name != "nightly-match" {
		t.Fatalf("expected only the UID-matching Job associated, got %+v", summaries[0].Runs)
	}
}

// Test 3 (plan): same namespace/name with a stale owner UID is excluded —
// delete/recreate must not inherit stale history.
func TestBuildCronJobSummariesRejectsStaleOwnerUID(t *testing.T) {
	cj := newCronJob("default", "nightly", "cj-v2-uid", "0 2 * * *")
	stale := completeJob("default", "leftover-run", time.Now(), []metav1.OwnerReference{
		// Same Kind/Name as the current CronJob, but a UID from before a
		// delete/recreate — must not be treated as this CronJob's history.
		controllerRef("CronJob", "nightly", "cj-v1-uid"),
	}, nil)

	summaries := BuildCronJobSummaries([]runtime.Object{cj}, []runtime.Object{stale}, nil)
	if len(summaries[0].Runs) != 0 {
		t.Fatalf("expected the stale-UID owner to be excluded, got %+v", summaries[0].Runs)
	}
}

// Test 4 (plan): a manual annotation includes the standalone Job; an
// unrelated Job merely sharing a name prefix (no owner ref, no
// annotations) is excluded.
func TestBuildCronJobSummariesManualAnnotationExcludesNamePrefixCollision(t *testing.T) {
	cj := newCronJob("default", "backup-postgres", "cj-1", "0 2 * * *")
	manual := completeJob("default", "backup-postgres-manual-1200", time.Now(), nil, map[string]string{
		kube.AnnotationCronJobName: "backup-postgres",
		kube.AnnotationCronJobUID:  "cj-1",
		kube.AnnotationTriggeredBy: "michael",
	})
	prefixCollision := completeJob("default", "backup-postgres-old-migration", time.Now(), nil, nil)

	summaries := BuildCronJobSummaries([]runtime.Object{cj}, []runtime.Object{manual, prefixCollision}, nil)
	if len(summaries[0].Runs) != 1 || summaries[0].Runs[0].Name != "backup-postgres-manual-1200" {
		t.Fatalf("expected only the annotated manual Job associated, got %+v", summaries[0].Runs)
	}
	got := summaries[0].Runs[0]
	if got.Source != JobSourceManual {
		t.Fatalf("expected JobSourceManual, got %v", got.Source)
	}
	if got.Creator != "michael" {
		t.Fatalf("expected Creator %q from %s, got %q", "michael", kube.AnnotationTriggeredBy, got.Creator)
	}
}

// Test 5 (plan): the newest terminal run ignores a newer active run when
// computing LAST RUN / LastTerminal (§4.3).
func TestBuildCronJobSummariesLastTerminalIgnoresNewerActiveRun(t *testing.T) {
	cj := newCronJob("default", "sync-inventory", "cj-1", "*/15 * * * *")
	older := completeJob("default", "sync-inventory-1", time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC), []metav1.OwnerReference{
		controllerRef("CronJob", cj.Name, cj.UID),
	}, nil)
	newerActive := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default", Name: "sync-inventory-2",
			CreationTimestamp: metav1.NewTime(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)),
			OwnerReferences:   []metav1.OwnerReference{controllerRef("CronJob", cj.Name, cj.UID)},
		},
		Status: batchv1.JobStatus{Active: 1, StartTime: &metav1.Time{Time: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}},
	}

	summaries := BuildCronJobSummaries([]runtime.Object{cj}, []runtime.Object{older, newerActive}, nil)
	s := summaries[0]
	if len(s.ActiveRuns) != 1 || s.ActiveRuns[0].Name != "sync-inventory-2" {
		t.Fatalf("expected the active run in ActiveRuns, got %+v", s.ActiveRuns)
	}
	if s.LastTerminal == nil || s.LastTerminal.Name != "sync-inventory-1" {
		t.Fatalf("expected LastTerminal to stay the older terminal run, got %+v", s.LastTerminal)
	}
}

// Test 6 (plan): active and suspended counts cross-cut terminal-outcome
// counts in HealthCounts — an active CronJob whose last terminal run
// succeeded contributes to both OK and Active.
func TestCronJobHealthActiveAndSuspendedCrossCutStatus(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	active := newCronJob("default", "sync-inventory", "cj-active", "*/15 * * * *")
	terminal := completeJob("default", "sync-inventory-1", now.Add(-2*time.Minute), []metav1.OwnerReference{
		controllerRef("CronJob", active.Name, active.UID),
	}, nil)
	inFlight := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "sync-inventory-2", OwnerReferences: []metav1.OwnerReference{controllerRef("CronJob", active.Name, active.UID)}},
		Status:     batchv1.JobStatus{Active: 1},
	}

	suspended := newCronJob("default", "warm-cache", "cj-suspended", "*/5 * * * *")
	suspended.Spec.Suspend = boolPtr(true)

	summaries := BuildCronJobSummaries(
		[]runtime.Object{active, suspended},
		[]runtime.Object{terminal, inFlight},
		nil,
	)
	rows := make([]Row, len(summaries))
	for i, s := range summaries {
		rows[i] = ProjectCronJob(s, now)
	}
	counts := cronJobHealth(rows)

	if counts.OK != 1 {
		t.Errorf("OK = %d, want 1 (active CronJob's last terminal run succeeded)", counts.OK)
	}
	if counts.Active != 1 {
		t.Errorf("Active = %d, want 1", counts.Active)
	}
	if counts.Neutral != 1 {
		t.Errorf("Neutral = %d, want 1 (suspended CronJob has no retained runs)", counts.Neutral)
	}
	if counts.Suspended != 1 {
		t.Errorf("Suspended = %d, want 1", counts.Suspended)
	}
	if total := counts.Total(); total != 2 {
		t.Errorf("Total() = %d, want 2 (Active/Suspended must not inflate it)", total)
	}
}

// Test 7 (plan): Job terminal conditions remain authoritative for
// status/reason/message; Pod termination only adds exit reason/code/logs
// target, never replaces the Job's own outcome.
func TestBuildCronJobSummariesPodAddsExitCodeNeverReplacesJobOutcome(t *testing.T) {
	cj := newCronJob("default", "report-nightly", "cj-1", "30 1 * * *")
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default", Name: "report-nightly-1", UID: "job-1",
			OwnerReferences: []metav1.OwnerReference{controllerRef("CronJob", cj.Name, cj.UID)},
		},
		Status: batchv1.JobStatus{
			Failed: 3,
			Conditions: []batchv1.JobCondition{{
				Type: batchv1.JobFailed, Status: corev1.ConditionTrue,
				Reason: "BackoffLimitExceeded", Message: "Job has reached the specified backoff limit",
			}},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default", Name: "report-nightly-1-abcde",
			OwnerReferences: []metav1.OwnerReference{controllerRef("Job", job.Name, job.UID)},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 137, Reason: "OOMKilled"}},
			}},
		},
	}

	summaries := BuildCronJobSummaries([]runtime.Object{cj}, []runtime.Object{job}, []runtime.Object{pod})
	lt := summaries[0].LastTerminal
	if lt == nil || !lt.Failed {
		t.Fatalf("expected a failed LastTerminal, got %+v", lt)
	}
	if lt.Reason != "BackoffLimitExceeded" {
		t.Errorf("Reason = %q, want the Job condition's own reason, not the Pod's (%q)", lt.Reason, "OOMKilled")
	}
	if lt.ExitCode == nil || *lt.ExitCode != 137 {
		t.Errorf("ExitCode = %v, want 137 from the Pod's terminated container", lt.ExitCode)
	}
	if lt.PodName != pod.Name {
		t.Errorf("PodName = %q, want %q (the logs target)", lt.PodName, pod.Name)
	}
}

// Test 8 (plan): missing Pods do not erase a valid Job failure.
func TestBuildCronJobSummariesMissingPodsKeepJobFailure(t *testing.T) {
	cj := newCronJob("default", "report-nightly", "cj-1", "30 1 * * *")
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default", Name: "report-nightly-1", UID: "job-1",
			OwnerReferences: []metav1.OwnerReference{controllerRef("CronJob", cj.Name, cj.UID)},
		},
		Status: batchv1.JobStatus{
			Failed:     3,
			Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded"}},
		},
	}

	// No pods at all — already garbage-collected.
	summaries := BuildCronJobSummaries([]runtime.Object{cj}, []runtime.Object{job}, nil)
	lt := summaries[0].LastTerminal
	if lt == nil || !lt.Failed || lt.Reason != "BackoffLimitExceeded" {
		t.Fatalf("expected the Job's own failure to survive with no Pod data, got %+v", lt)
	}
	if lt.ExitCode != nil {
		t.Errorf("ExitCode = %v, want nil — must not fabricate one with no Pod collected", lt.ExitCode)
	}
	if lt.PodName != "" {
		t.Errorf("PodName = %q, want empty", lt.PodName)
	}
}

// Test 9 (plan): invalid schedules/timezones produce a neutral NEXT value
// (not a panic) from ProjectCronJob/NextCronRuns. An *unset* timezone is a
// distinct, valid state (§3.9) and is checked alongside for contrast.
func TestProjectCronJobNextCellHandlesInvalidScheduleAndTimezone(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	badTZ := "Not/AZone"
	goodTZ := "UTC"

	tests := []struct {
		name     string
		schedule string
		timeZone *string
		want     string
	}{
		{"invalid schedule", "not a cron expression", &goodTZ, "—"},
		{"invalid timezone", "*/5 * * * *", &badTZ, "—"},
		{"unset timezone", "*/5 * * * *", nil, "controller local"},
		{"valid schedule and timezone", "*/5 * * * *", &goodTZ, "in 5m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cj := newCronJob("default", "x", "cj-1", tt.schedule)
			cj.Spec.TimeZone = tt.timeZone
			summaries := BuildCronJobSummaries([]runtime.Object{cj}, nil, nil)
			row := ProjectCronJob(summaries[0], now)
			if got := row.Cells[5]; got != tt.want {
				t.Errorf("NEXT cell = %q, want %q", got, tt.want)
			}
		})
	}

	// The same invalid inputs must not panic NextCronRuns directly either.
	if _, err := NextCronRuns("not a cron expression", "UTC", now, 5); err == nil {
		t.Error("NextCronRuns(invalid schedule) returned nil error, want a parse error")
	}
	if _, err := NextCronRuns("*/5 * * * *", "Not/AZone", now, 5); err == nil {
		t.Error("NextCronRuns(invalid timezone) returned nil error, want a parse error")
	}
	if _, err := NextCronRuns("*/5 * * * *", "", now, 5); !errors.Is(err, ErrTimeZoneUnset) {
		t.Errorf("NextCronRuns(unset timezone) error = %v, want ErrTimeZoneUnset", err)
	}
}

// Test 10 (plan): DST spring-forward/fall-back transitions and exact
// schedule boundaries produce correct next-run timestamps.
func TestNextCronRunsAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("America/New_York tzdata unavailable: %v", err)
	}

	t.Run("spring forward", func(t *testing.T) {
		// 2026-03-08 is the US spring-forward date: 02:00 EST jumps straight
		// to 03:00 EDT, so 02:00-02:59 never happens that day.
		from := time.Date(2026, 3, 7, 20, 0, 0, 0, loc) // 8pm EST, the day before
		got, err := NextCronRuns("0 3 * * *", "America/New_York", from, 1)
		if err != nil {
			t.Fatalf("NextCronRuns: %v", err)
		}
		want := time.Date(2026, 3, 8, 3, 0, 0, 0, loc)
		if len(got) != 1 || !got[0].Equal(want) {
			t.Fatalf("next run = %v, want %v", got, want)
		}
		// Only 6 real hours elapse (8pm EST -> 3am EDT), not the 7 the wall
		// clock difference alone would suggest, because the clocks skipped
		// an hour in between.
		if diff := got[0].Sub(from); diff != 6*time.Hour {
			t.Errorf("elapsed = %v, want 6h (the spring-forward gap must be reflected in absolute time)", diff)
		}
	})

	t.Run("fall back", func(t *testing.T) {
		// 2026-11-01 is the US fall-back date: clocks repeat 01:00-01:59
		// (first EDT, then EST), so a 3am fire is unambiguous but sits an
		// extra real hour after the day's start compared to a normal day.
		from := time.Date(2026, 10, 31, 20, 0, 0, 0, loc) // 8pm EDT, the day before
		got, err := NextCronRuns("0 3 * * *", "America/New_York", from, 1)
		if err != nil {
			t.Fatalf("NextCronRuns: %v", err)
		}
		want := time.Date(2026, 11, 1, 3, 0, 0, 0, loc)
		if len(got) != 1 || !got[0].Equal(want) {
			t.Fatalf("next run = %v, want %v", got, want)
		}
		if diff := got[0].Sub(from); diff != 8*time.Hour {
			t.Errorf("elapsed = %v, want 8h (the fall-back repeat must be reflected in absolute time)", diff)
		}
	})

	t.Run("exact boundary is exclusive", func(t *testing.T) {
		// Next() must return the *following* occurrence when `from` lands
		// exactly on a scheduled fire, never the same instant back.
		from := time.Date(2026, 6, 1, 0, 5, 0, 0, time.UTC)
		got, err := NextCronRuns("*/5 * * * *", "UTC", from, 1)
		if err != nil {
			t.Fatalf("NextCronRuns: %v", err)
		}
		want := time.Date(2026, 6, 1, 0, 10, 0, 0, time.UTC)
		if len(got) != 1 || !got[0].Equal(want) {
			t.Fatalf("next run from an exact boundary = %v, want %v (strictly after, not the same instant)", got, want)
		}
	})
}

// Test 11 (plan): every projected row (ProjectCronJob output) has exactly
// len(Cells) == 6, matching the six declared columns.
func TestProjectCronJobCellCountMatchesColumns(t *testing.T) {
	reg := DefaultRegistry()
	d, ok := reg.Descriptor(kube.KindCronJob)
	if !ok {
		t.Fatal("CronJob is not registered")
	}
	if len(d.Columns) != 6 {
		t.Fatalf("descriptor has %d columns, want 6: %v", len(d.Columns), d.Columns)
	}

	cj := newCronJob("default", "webhook-retry", "cj-1", "*/2 * * * *")
	summaries := BuildCronJobSummaries([]runtime.Object{cj}, nil, nil)
	row := ProjectCronJob(summaries[0], time.Now())
	if len(row.Cells) != len(d.Columns) {
		t.Fatalf("ProjectCronJob produced %d cells, descriptor declares %d columns", len(row.Cells), len(d.Columns))
	}
}

// TestBuildCronJobSummariesDeepCopiesObject guards task 12: the returned
// CronJobSummary.Object must not alias the slice element passed in, so a
// caller mutating its own snapshot afterward can't retroactively change an
// already-built summary.
func TestBuildCronJobSummariesDeepCopiesObject(t *testing.T) {
	cj := newCronJob("default", "nightly", "cj-1", "0 2 * * *")
	summaries := BuildCronJobSummaries([]runtime.Object{cj}, nil, nil)
	if summaries[0].Object == cj {
		t.Fatal("CronJobSummary.Object aliases the input pointer, want a deep copy")
	}
	cj.Spec.Schedule = "mutated"
	if summaries[0].Object.Spec.Schedule == "mutated" {
		t.Fatal("mutating the input CronJob after Build changed the summary — Object was not deep-copied")
	}
}

// TestProjectAssociatedJobCellCount checks ProjectAssociatedJob against its
// documented column set and a representative outcome per Source/state.
func TestProjectAssociatedJobCellCount(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		summary JobSummary
		wantOut string
	}{
		{"scheduled succeeded", JobSummary{Name: "run-1", Source: JobSourceScheduled, ScheduledAt: now.Add(-time.Hour), StartedAt: now.Add(-time.Hour), CompletedAt: now.Add(-50 * time.Minute), Succeeded: true, Completions: "1/1"}, "✓ 1/1"},
		{"manual failed", JobSummary{Name: "run-2", Source: JobSourceManual, StartedAt: now.Add(-time.Hour), CompletedAt: now.Add(-55 * time.Minute), Failed: true, Reason: "BackoffLimitExceeded"}, "✕ BackoffLimitExceeded"},
		{"active", JobSummary{Name: "run-3", Source: JobSourceScheduled, StartedAt: now.Add(-2 * time.Minute), Active: true}, "running"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := ProjectAssociatedJob(tt.summary, now, AssociatedJobColumns)
			if len(row.Cells) != len(AssociatedJobColumns) {
				t.Fatalf("got %d cells, want %d", len(row.Cells), len(AssociatedJobColumns))
			}
			if got := row.Cells[4]; got != tt.wantOut { // "Outcome" is index 4 in AssociatedJobColumns
				t.Errorf("OUTCOME cell = %q, want %q", got, tt.wantOut)
			}
		})
	}
}
