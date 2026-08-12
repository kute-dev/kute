// golden_states_cronjob_test.go pins §36a/§36b/§36c's own rendered states
// (0.8.0 plan Phase 9) the same way golden_states_test.go pins every other
// non-resting state: one goldenXModel builder per state, wired into that
// file's shared goldenStatePrefixes/goldenStateModel/goldenStateFixtures
// machinery rather than duplicating it here.
package browse

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/config"
	"github.com/kute-dev/kute/internal/kube"
)

// goldenCronJobFixtureSet builds the four-row scenario every §36a golden
// state shares (fixed name order: backup, nightly, report, sync):
//   - "backup"  suspended, with a Kute suspend timestamp/generation so the
//     resume preview can show a truthful duration + missed-run count.
//   - "nightly" a plain healthy row: last run succeeded, nothing active.
//   - "report"  last run failed — the one row exercising the selected-only
//     failure continuation line.
//   - "sync"    an active run under Forbid concurrency over a prior success
//     (§4.3: "an active row can read 2m ago ✓ in LAST RUN while ACT reads
//     1"), the fixture §36b's overlap-warning golden stages ctrl-r against.
func goldenCronJobFixtureSet() (cronJobs, jobs []runtime.Object) {
	now := time.Now()

	backup := suspendedCronJobObj("default", "backup", true)
	backup.Generation = 2
	backup.Annotations = map[string]string{
		kube.AnnotationSuspendedAt:         now.Add(-3 * time.Hour).UTC().Format(time.RFC3339),
		kube.AnnotationSuspendedGeneration: "2",
	}

	nightly := cronJobObj("default", "nightly")
	nightlyDone := jobObj("default", "nightly-29310600")
	nightlyDone.OwnerReferences = []metav1.OwnerReference{controllerRefFor(nightly)}
	nightlyDone.Status = batchv1.JobStatus{
		Succeeded:      1,
		CompletionTime: &metav1.Time{Time: now.Add(-6 * time.Minute)},
		Conditions: []batchv1.JobCondition{{
			Type: batchv1.JobComplete, Status: "True",
			LastTransitionTime: metav1.Time{Time: now.Add(-6 * time.Minute)},
		}},
	}

	report := cronJobObj("default", "report")
	reportFailed := jobObj("default", "report-29310550")
	reportFailed.OwnerReferences = []metav1.OwnerReference{controllerRefFor(report)}
	reportFailed.Status = batchv1.JobStatus{
		Failed: 3,
		Conditions: []batchv1.JobCondition{{
			Type: batchv1.JobFailed, Status: "True",
			Reason: "BackoffLimitExceeded", Message: "Job has reached the specified backoff limit",
			LastTransitionTime: metav1.Time{Time: now.Add(-40 * time.Minute)},
		}},
	}

	sync := cronJobObj("default", "sync")
	sync.Spec.ConcurrencyPolicy = batchv1.ForbidConcurrent
	syncDone := jobObj("default", "sync-29310500")
	syncDone.OwnerReferences = []metav1.OwnerReference{controllerRefFor(sync)}
	syncDone.Status = batchv1.JobStatus{
		Succeeded:      1,
		CompletionTime: &metav1.Time{Time: now.Add(-25 * time.Minute)},
		Conditions: []batchv1.JobCondition{{
			Type: batchv1.JobComplete, Status: "True",
			LastTransitionTime: metav1.Time{Time: now.Add(-25 * time.Minute)},
		}},
	}
	syncActive := jobObj("default", "sync-29310612")
	syncActive.OwnerReferences = []metav1.OwnerReference{controllerRefFor(sync)}
	syncActive.Status = batchv1.JobStatus{Active: 1, StartTime: &metav1.Time{Time: now.Add(-2 * time.Minute)}}

	return []runtime.Object{backup, nightly, report, sync},
		[]runtime.Object{nightlyDone, reportFailed, syncDone, syncActive}
}

func goldenCronJobModel(t *testing.T, width, height int) Model {
	t.Helper()
	cronJobs, jobs := goldenCronJobFixtureSet()
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindCronJob: cronJobs,
		kube.KindJob:     jobs,
	}}
	sess := newSession()
	sess.Location.Kind = kube.KindCronJob
	m := New(Config{Session: sess, Lister: lister, Mutator: &fakeMutator{}})
	m.SetSize(width, height)
	m = step(t, m, m.load()())
	return m
}

// --- 36a: selected failed row (continuation line + behavior strip) ---

func goldenCronJobFailedModel(t *testing.T, width, height int) Model {
	t.Helper()
	m := goldenCronJobModel(t, width, height)
	// Fixed name order: backup, nightly, report, sync — two downs from
	// backup lands on report, the failed row.
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	return m
}

// --- 36a: selected suspended row (dim/strike name, amber SUSP) ---

func goldenCronJobSuspendedModel(t *testing.T, width, height int) Model {
	t.Helper()
	// backup is already first in fixed name order — no cursor move needed.
	return goldenCronJobModel(t, width, height)
}

// --- 36b: ctrl-r overlap warning (Forbid concurrency, one active run) ---

func goldenCronJobRunNowModel(t *testing.T, width, height int) Model {
	t.Helper()
	m := goldenCronJobModel(t, width, height)
	// backup, nightly, report, sync — three downs lands on sync.
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = step(t, m, tea.KeyPressMsg{Text: "R"})
	return m
}

// --- 36c: suspend danger confirmation (PROD type-the-name modal) ---

func goldenCronJobSuspendConfirmModel(t *testing.T, width, height int) Model {
	t.Helper()
	cronJobs, jobs := goldenCronJobFixtureSet()
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindCronJob: cronJobs,
		kube.KindJob:     jobs,
	}}
	sess := newSession()
	sess.Location.Kind = kube.KindCronJob
	sess.Config = config.Config{ProdContexts: []string{sess.Location.Context}}
	m := New(Config{Session: sess, Lister: lister, Mutator: &fakeMutator{}})
	m.SetSize(width, height)
	m = step(t, m, m.load()())
	// nightly is the second row in fixed name order — a plain healthy row
	// with nothing active, so the modal renders without the sync row's own
	// overlap chrome underneath it.
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = step(t, m, tea.KeyPressMsg{Text: "s"})
	return m
}

// --- 36c: resume preflight (suspended duration + missed runs + next) ---

// goldenCronJobResumeModel deliberately doesn't reuse goldenCronJobFixtureSet:
// an explicit timezone plus a short, still-within-schedule suspend window
// keeps the resume preview's own RightNote ("resume backup? suspended 2m ·
// no schedules missed while suspended · next in 3m") short enough to survive
// insetChromeLine's own "drop the whole note rather than truncate it" rule
// at 120 columns — the controller-local/unknown-timezone phrasing
// goldenCronJobFixtureSet's "backup" carries is realistic but too long to
// render at all here, which would defeat this state's own purpose.
func goldenCronJobResumeModel(t *testing.T, width, height int) Model {
	t.Helper()
	// A schedule boundary two minutes in the past: suspended right at the
	// boundary, "now" two minutes into the current window, so no */5
	// occurrence has been missed and the next one is three minutes out.
	// Pinned to a fixed instant rather than time.Now() — this feeds
	// directly into the rendered "clock HH:MM:SS UTC" header text (view.go's
	// m.now), so a real wall-clock read here made the fixture non-
	// deterministic across CI runs at different times of day.
	boundary := time.Date(2024, 1, 1, 12, 10, 0, 0, time.UTC)
	current := boundary.Add(2 * time.Minute)

	backup := suspendedCronJobObj("default", "backup", true)
	tz := "UTC"
	backup.Spec.TimeZone = &tz
	backup.Generation = 2
	backup.Annotations = map[string]string{
		kube.AnnotationSuspendedAt:         boundary.UTC().Format(time.RFC3339),
		kube.AnnotationSuspendedGeneration: "2",
	}

	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindCronJob: {backup},
	}}
	sess := newSession()
	sess.Location.Kind = kube.KindCronJob
	m := New(Config{Session: sess, Lister: lister, Mutator: &fakeMutator{}})
	m.SetSize(width, height)
	m = step(t, m, m.load()())
	m.now = current
	m = step(t, m, tea.KeyPressMsg{Text: "s"})
	return m
}
