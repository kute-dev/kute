// Package resources: §37a's own Job list aggregation — a v0.9.0 sibling of
// cronjobs.go's CronJob aggregation, one level down (Job's own pod-attempt
// counters instead of a CronJob's associated-Job history). Pure data, same
// contract as cronjobs.go's own package doc comment: every exported
// function takes its inputs as parameters and returns a value; nothing here
// performs cluster I/O or reads the wall clock except ProjectJobList's own
// `now` parameter, supplied by the caller (browse's UI clock tick) rather
// than read internally.
//
// BuildJobListSummaries joins Job/Pod/CronJob snapshots (already read once
// by browse's own load()) into one JobListSummary per Job; ProjectJobList
// turns a summary into §37a's display Row given an explicit `now` and
// `currentUser` (for the SOURCE column's "manual · you" reading).
package resources

import (
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

// JobSourceKind is §37a's SOURCE taxonomy — resolved from ownerRefs, Helm's
// own hook annotations, and Kute's manual-run tag, in that precedence order
// (jobSource's own doc comment has the reasoning).
type JobSourceKind int

const (
	// JobSourceStandalone is a Job with no CronJob association, no Helm hook
	// annotation, and no Kute manual-run tag — "— standalone" in the SOURCE
	// column. The common case for a hand-authored migration/one-off Job.
	JobSourceStandalone JobSourceKind = iota
	// JobSourceCronJob is a Job associated with a CronJob — via a real
	// controller owner reference or Kute's own AnnotationCronJobUID/
	// AnnotationCronJobName (cronJobAssociation, cronjobs.go) — rendered
	// "cronjob/<name>".
	JobSourceCronJob
	// JobSourceHelmHook is a Job Helm itself created to run a chart hook
	// (kube.HelmHookAnnotation set) — rendered "helm/<release> <hook>".
	JobSourceHelmHook
	// JobSourceManualTag is a standalone Job Kute itself created — §37c's
	// rerun "create" path, which always stamps AnnotationTriggeredBy/
	// AnnotationTriggeredAt and never a CronJob association — rendered
	// "manual · <creator>" ("manual · you" when creator is the viewing
	// user).
	JobSourceManualTag
)

// JobListSummary is one Job's own §37a row data — pure display data, built
// once per load and re-projected on every UI clock tick.
type JobListSummary struct {
	Object *batchv1.Job // deep copy

	SourceKind JobSourceKind
	// CronJobName/HelmRelease/HelmHook/ManualCreator carry the one field
	// SourceKind's own case needs — ProjectJobList formats the label rather
	// than this struct precomputing it, since the "· you" reading depends on
	// the viewing user, a projection-time fact this aggregation step has no
	// business knowing.
	CronJobName   string
	HelmRelease   string
	HelmHook      string
	ManualCreator string

	Completions string // "N/M" — Status.Succeeded over Spec.Completions.
	// FailedAttempts is Job.Status.Failed — a count of failed *pod* attempts,
	// not a Job-level bool, and deliberately a different number from
	// Completions (§37a: "two numbers that mean different things get two
	// columns, never one 'status' string").
	FailedAttempts int32

	StartedAt   time.Time
	CompletedAt time.Time // zero while active or not yet started
	Active      bool
	Succeeded   bool
	Failed      bool
	Suspended   bool

	// NewestPodName is the newest pod this Job has created regardless of its
	// state (representativePod, cronjobs.go) — browse's own 'l' key target,
	// resolved here so browse doesn't need its own owner-ref-matching logic
	// (kept in sync with §37b's own attempt sequencing rather than
	// duplicating the "newest by creation time" rule a third time).
	NewestPodName string
}

// jobSource resolves job's SOURCE per §37a's precedence: a CronJob
// association is the most concrete, structural signal (an owner reference
// or Kute's own durable annotation pair); a Helm hook annotation is also
// chart-authored and structural; Kute's own manual-run tag is the weakest
// signal (pure Kute bookkeeping) and only applies once the first two don't;
// everything else is genuinely standalone. A Job cannot carry a real
// CronJob owner reference and a Helm hook annotation at once in practice —
// this order exists for the rare hand-crafted fixture/object where more
// than one applies, so behavior is deterministic rather than map-iteration-
// order dependent.
func jobSource(job *batchv1.Job) (kind JobSourceKind, cronJobName, helmRelease, helmHook, manualCreator string) {
	if key, _, ok := cronJobAssociation(job); ok {
		// cronJobAssociation returns a join key, not a name — but for §37a's
		// display purposes (never resolved against a live CronJob list, only
		// browse's own real CronJob join in BuildJobListSummaries does that)
		// the annotation/owner-ref name is the honest, always-available
		// fallback label even before that join runs.
		_ = key
		name := ""
		if ref := controllerOwnerRef(job.OwnerReferences); ref != nil && ref.Kind == kube.KindCronJob.APIKind() {
			name = ref.Name
		} else {
			name = job.Annotations[kube.AnnotationCronJobName]
		}
		return JobSourceCronJob, name, "", "", ""
	}
	if hook := job.Annotations[kube.HelmHookAnnotation]; hook != "" {
		release := job.Annotations[kube.HelmReleaseNameAnnotation]
		// A hook can list more than one phase, comma-separated
		// ("post-install,post-upgrade") — the first is representative for a
		// single-word display cell.
		if i := strings.IndexByte(hook, ','); i >= 0 {
			hook = hook[:i]
		}
		return JobSourceHelmHook, "", release, hook, ""
	}
	if creator := job.Annotations[kube.AnnotationTriggeredBy]; creator != "" {
		return JobSourceManualTag, "", "", "", creator
	}
	return JobSourceStandalone, "", "", "", ""
}

// buildJobListSummary projects one deep-copied Job (plus its already-indexed
// owned pods, for NewestPodName) into a JobListSummary.
func buildJobListSummary(job *batchv1.Job, pods []*corev1.Pod) JobListSummary {
	job = job.DeepCopy()
	succeeded, failed, _, _ := jobTerminalOutcome(job)
	active := job.Status.Active > 0
	suspended := job.Spec.Suspend != nil && *job.Spec.Suspend

	var completedAt time.Time
	switch {
	case job.Status.CompletionTime != nil:
		completedAt = job.Status.CompletionTime.Time
	case succeeded || failed:
		completedAt = jobTerminalTime(job)
	}
	var startedAt time.Time
	if job.Status.StartTime != nil {
		startedAt = job.Status.StartTime.Time
	}

	sourceKind, cronJobName, helmRelease, helmHook, manualCreator := jobSource(job)

	var newestPod string
	if len(pods) > 0 {
		newestPod = representativePod(pods).Name
	}

	return JobListSummary{
		Object:         job,
		SourceKind:     sourceKind,
		CronJobName:    cronJobName,
		HelmRelease:    helmRelease,
		HelmHook:       helmHook,
		ManualCreator:  manualCreator,
		Completions:    fmt.Sprintf("%d/%d", job.Status.Succeeded, int32ptr(job.Spec.Completions)),
		FailedAttempts: job.Status.Failed,
		StartedAt:      startedAt,
		CompletedAt:    completedAt,
		Active:         active,
		Succeeded:      succeeded,
		Failed:         failed,
		Suspended:      suspended,
		NewestPodName:  newestPod,
	}
}

// BuildJobListSummaries joins jobs/pods (already read once from the
// informer cache by browse's own load() — never called per-row) into one
// JobListSummary per Job object in jobs, in the same order. cronJobs is
// accepted for symmetry with BuildCronJobSummaries' own signature and
// future SOURCE cross-checks against a real CronJob's current name, but
// today's resolution (jobSource) already has everything it needs from the
// Job object itself — a nil/empty cronJobs is not a degraded read.
func BuildJobListSummaries(jobs, pods, _cronJobs []runtime.Object) []JobListSummary {
	podsByJobKey := indexPodsByJob(pods)
	out := make([]JobListSummary, 0, len(jobs))
	for _, obj := range jobs {
		j, ok := obj.(*batchv1.Job)
		if !ok {
			continue
		}
		key := ownerKey(j.Namespace, j.UID, j.Name)
		out = append(out, buildJobListSummary(j, podsByJobKey[key]))
	}
	return out
}

// jobSourceLabel renders §37a's SOURCE cell — "cronjob/x", "— standalone",
// "manual · you"/"manual · <creator>", or "helm/<release> <hook>".
func jobSourceLabel(s JobListSummary, currentUser string) string {
	switch s.SourceKind {
	case JobSourceCronJob:
		if s.CronJobName == "" {
			return "cronjob/?"
		}
		return "cronjob/" + s.CronJobName
	case JobSourceHelmHook:
		release := s.HelmRelease
		if release == "" {
			release = "?"
		}
		return "helm/" + release + " " + s.HelmHook
	case JobSourceManualTag:
		who := s.ManualCreator
		if who == currentUser || who == "" {
			who = "you"
		}
		return "manual · " + who
	default:
		return "— standalone"
	}
}

// jobDuration renders §37a's DURATION cell: elapsed between start and
// completion for a terminal Job, between start and now for an active one,
// "–" for one that hasn't started yet (queued on parallelism/concurrency).
// Mirrors associatedJobDuration's own three-way switch (cronjobs.go).
func jobDuration(s JobListSummary, now time.Time) time.Duration {
	switch {
	case s.StartedAt.IsZero():
		return -1
	case !s.CompletedAt.IsZero():
		return s.CompletedAt.Sub(s.StartedAt)
	case s.Active:
		return now.Sub(s.StartedAt)
	default:
		return -1
	}
}

// ProjectJobList turns summary into §37a's list Row — NAME · COMPL ·
// DURATION · FAILED · SOURCE · AGE (registry.go's Job Columns) — using now
// for every relative string and currentUser for the SOURCE column's
// "manual · you" reading. Never reads the clock itself.
func ProjectJobList(summary JobListSummary, now time.Time, currentUser string) Row {
	j := summary.Object
	if j == nil {
		return Row{}
	}

	status := StatusWarn
	switch {
	case summary.Succeeded:
		status = StatusOK
	case summary.Suspended:
		// A deliberately-paused Job isn't warning-worthy — same "parked,
		// benign, nothing to see" neutral projectJobFallback's predecessor
		// (the old single-object projectJob) already used. Failed still
		// wins below even over a suspended Job that recorded a real failure
		// before being paused.
		status = StatusNeutral
	}
	if summary.Failed {
		status = StatusFail
	}

	durationValue := jobDuration(summary, now)
	durationCell := "–"
	durationClass := StatusNeutral
	if durationValue >= 0 {
		durationCell = shortAge(durationValue)
		if j.Spec.ActiveDeadlineSeconds != nil && durationValue >= time.Duration(*j.Spec.ActiveDeadlineSeconds)*time.Second {
			durationClass = StatusFail
		}
	}

	age := time.Duration(0)
	if !j.CreationTimestamp.IsZero() {
		age = now.Sub(j.CreationTimestamp.Time)
	}

	return Row{
		Namespace: j.Namespace,
		Name:      j.Name,
		Suspended: summary.Suspended,
		Active:    summary.Active,
		Cells: []string{
			j.Name,
			summary.Completions,
			durationCell,
			itoa32(summary.FailedAttempts),
			jobSourceLabel(summary, currentUser),
			shortAge(age),
		},
		Status:        status,
		DurationClass: durationClass,
	}
}

// itoa32 is strconv.Itoa for an int32, without importing strconv into this
// file solely for FAILED's cell.
func itoa32(n int32) string {
	return fmt.Sprintf("%d", n)
}

// jobHealth is Job's Health implementation (§37a: "complete/running/failed/
// suspended" health strip): the usual OK/Fail/Neutral tally off Status (see
// ProjectJobList — Warn is "running", not folded away), plus Suspended
// counted a second time as a cross-cutting extra, same shape as
// cronJobHealth's own Active/Suspended tally.
func jobHealth(rows []Row) HealthCounts {
	h := StatusHealth(rows)
	for _, r := range rows {
		if r.Suspended {
			h.Suspended++
		}
	}
	return h
}

// jobHealthLabel is §37a's health-strip wording: "5 complete", "2 running",
// "2 failed". Suspended's own cross-cutting segment carries its own literal
// "suspended" label where browse renders it, the same place
// cronJobHealthLabel's own doc comment describes for CronJob's Active/
// Suspended segments.
func jobHealthLabel(class StatusClass) string {
	switch class {
	case StatusOK:
		return "complete"
	case StatusWarn:
		return "running"
	case StatusFail:
		return "failed"
	default:
		return "pending"
	}
}
