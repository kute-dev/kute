// Package resources: §37b/§37d's own Job attempt-history aggregation — the
// unit tasks/jobattempts projects from. Pure data, same contract as
// cronjobs.go/jobs.go: no cluster I/O, no clock reads except where `now` is
// an explicit parameter.
//
// Scope note (v0.9.0, decided with the user before implementation): there is
// no retained-pod ledger. Attempts are built from whatever ListRaw(Pod)
// still has in cache — a pod already garbage-collected by Kubernetes simply
// isn't in Attempts; the Job's own Reason/Message condition stays the
// authoritative fallback for the failure card exactly the way
// docs/lazy-informers.md §6 already documents for CronJob's own best-effort
// Pod read. A future retained ledger is exactly the kind of addition that
// slots into BuildJobAttempts' pods parameter without changing its
// signature — this file does not foreclose it, it just doesn't build it.
package resources

import (
	"fmt"
	"sort"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

// jobCompletionIndexAnnotation is the pod annotation the Job controller
// stamps on an Indexed job's pods (batchv1's own doc comment on
// JobSpec.CompletionMode names it; there is no exported Go constant for it
// in k8s.io/api/batch/v1).
const jobCompletionIndexAnnotation = "batch.kubernetes.io/job-completion-index"

// JobAttemptResult is one attempt's terminal outcome.
type JobAttemptResult int

const (
	JobAttemptRunning JobAttemptResult = iota
	JobAttemptSucceeded
	JobAttemptFailed
	// JobAttemptQueued is §37d-only: an Indexed job's index with no pod yet
	// (still waiting on parallelism).
	JobAttemptQueued
)

// JobAttempt is one pod-attempt's row/cell data — §37b's flat table row, or
// one of §37d's index-map cells.
type JobAttempt struct {
	Ordinal   int    // 1-based, oldest first — assigned by BuildJobAttempts.
	Index     *int32 // completion index; non-nil only for an Indexed job.
	PodName   string
	Node      string
	Result    JobAttemptResult
	ExitCode  *int32
	StartedAt time.Time
	EndedAt   time.Time // zero while running.
}

// Duration is EndedAt-StartedAt for a terminal attempt, now-StartedAt for a
// running one, 0 when it hasn't started (StartedAt zero). Exported for
// jobattempts' own view.go (index-grid/diff rendering).
func (a JobAttempt) Duration(now time.Time) time.Duration {
	if a.StartedAt.IsZero() {
		return 0
	}
	if !a.EndedAt.IsZero() {
		return a.EndedAt.Sub(a.StartedAt)
	}
	return now.Sub(a.StartedAt)
}

// JobAttemptsSummary is one Job's own attempt history — what §37b/§37d
// project from.
type JobAttemptsSummary struct {
	Object *batchv1.Job // deep copy

	// Attempts is every pod still in cache, oldest first (Ordinal-ordered).
	// May under-report FailedAttempts/BackoffLimit when Kubernetes has
	// already garbage-collected some of the Job's pods — see the package
	// doc comment.
	Attempts []JobAttempt

	// Indexed gates §37d's grid rendering vs §37b's flat table in the
	// shared view — true when Spec.CompletionMode == Indexed.
	Indexed bool

	BackoffLimit            int32
	Completions             *int32
	Parallelism             int32
	ActiveDeadlineSeconds   *int64
	TTLSecondsAfterFinished *int32

	Succeeded, Failed, Active bool
	Reason, Message           string // the Job's own Complete/Failed condition.
	StartedAt, CompletedAt    time.Time

	// RetriesRemain is the status banner's "no retries remain" clause:
	// Failed and FailedAttempts hasn't yet exhausted BackoffLimit.
	RetriesRemain bool

	// FailedAttempts is Job.Status.Failed — see jobs.go's own field for the
	// same "pod attempts, not a bool" reasoning; kept here too since the
	// status banner ("3 of 3 attempts used") reads it directly.
	FailedAttempts int32

	// Source/CronJobName back the status banner's "from cronjob/x ·
	// scheduled 01:30" line — jobs.go's own jobSource, reused rather than
	// re-derived.
	SourceKind  JobSourceKind
	CronJobName string
}

// jobIndex reads an Indexed job's pod's own completion index annotation. ok
// is false when the pod carries none (a NonIndexed job, or a malformed
// fixture).
func jobIndex(p *corev1.Pod) (index int32, ok bool) {
	raw, present := p.Annotations[jobCompletionIndexAnnotation]
	if !present {
		return 0, false
	}
	var n int
	if _, err := fmt.Sscanf(raw, "%d", &n); err != nil {
		return 0, false
	}
	return int32(n), true
}

// buildAttempt projects one pod into a JobAttempt. ordinal/index are
// assigned by the caller (BuildJobAttempts), which has the full sorted set.
func buildAttempt(p *corev1.Pod, ordinal int) JobAttempt {
	a := JobAttempt{
		Ordinal: ordinal,
		PodName: p.Name,
		Node:    p.Spec.NodeName,
	}
	if idx, ok := jobIndex(p); ok {
		a.Index = &idx
	}
	if p.Status.StartTime != nil {
		a.StartedAt = p.Status.StartTime.Time
	}
	switch p.Status.Phase {
	case corev1.PodSucceeded:
		a.Result = JobAttemptSucceeded
	case corev1.PodFailed:
		a.Result = JobAttemptFailed
	default:
		a.Result = JobAttemptRunning
	}
	if code, ok := terminatedExitCode(p); ok {
		c := code
		a.ExitCode = &c
	}
	// EndedAt: the latest terminated container's own finish time, when one
	// exists — the closest available stand-in for "when this attempt
	// ended" (Pod carries no single completion timestamp of its own).
	for _, cs := range p.Status.ContainerStatuses {
		if cs.State.Terminated == nil {
			continue
		}
		if t := cs.State.Terminated.FinishedAt.Time; t.After(a.EndedAt) {
			a.EndedAt = t
		}
	}
	return a
}

// BuildJobAttempts joins one named Job with its owned pods (already
// read once, best-effort, from the informer cache — never called per-row)
// into a JobAttemptsSummary. Pure: no cluster I/O, no clock read. ok is
// false when job is nil/wrong type.
func BuildJobAttempts(job *batchv1.Job, pods []runtime.Object) (JobAttemptsSummary, bool) {
	if job == nil {
		return JobAttemptsSummary{}, false
	}
	j := job.DeepCopy()

	var owned []*corev1.Pod
	key := ownerKey(j.Namespace, j.UID, j.Name)
	for _, obj := range pods {
		p, ok := obj.(*corev1.Pod)
		if !ok {
			continue
		}
		ref := controllerOwnerRef(p.OwnerReferences)
		if ref == nil || ref.Kind != kube.KindJob.APIKind() {
			continue
		}
		if ownerKey(p.Namespace, ref.UID, ref.Name) != key {
			continue
		}
		owned = append(owned, p.DeepCopy())
	}
	sort.SliceStable(owned, func(i, k int) bool {
		ti, tk := owned[i].Status.StartTime, owned[k].Status.StartTime
		switch {
		case ti == nil && tk == nil:
			return owned[i].Name < owned[k].Name
		case ti == nil:
			return true
		case tk == nil:
			return false
		case !ti.Time.Equal(tk.Time):
			return ti.Time.Before(tk.Time)
		default:
			return owned[i].Name < owned[k].Name
		}
	})

	attempts := make([]JobAttempt, 0, len(owned))
	for i, p := range owned {
		attempts = append(attempts, buildAttempt(p, i+1))
	}

	succeeded, failed, reason, message := jobTerminalOutcome(j)
	active := j.Status.Active > 0

	var completedAt time.Time
	switch {
	case j.Status.CompletionTime != nil:
		completedAt = j.Status.CompletionTime.Time
	case succeeded || failed:
		completedAt = jobTerminalTime(j)
	}
	var startedAt time.Time
	if j.Status.StartTime != nil {
		startedAt = j.Status.StartTime.Time
	}

	backoffLimit := int32(6) // batchv1's own documented default.
	if j.Spec.BackoffLimit != nil {
		backoffLimit = *j.Spec.BackoffLimit
	}

	indexed := j.Spec.CompletionMode != nil && *j.Spec.CompletionMode == batchv1.IndexedCompletion

	sourceKind, cronJobName, _, _, _ := jobSource(j)

	return JobAttemptsSummary{
		Object:                  j,
		Attempts:                attempts,
		Indexed:                 indexed,
		BackoffLimit:            backoffLimit,
		Completions:             j.Spec.Completions,
		Parallelism:             int32ptr(j.Spec.Parallelism),
		ActiveDeadlineSeconds:   j.Spec.ActiveDeadlineSeconds,
		TTLSecondsAfterFinished: j.Spec.TTLSecondsAfterFinished,
		Succeeded:               succeeded,
		Failed:                  failed,
		Active:                  active,
		Reason:                  reason,
		Message:                 message,
		StartedAt:               startedAt,
		CompletedAt:             completedAt,
		RetriesRemain:           failed && j.Status.Failed < backoffLimit,
		FailedAttempts:          j.Status.Failed,
		SourceKind:              sourceKind,
		CronJobName:             cronJobName,
	}, true
}

// attemptResultCell renders one attempt's RESULT/EXIT cells.
func attemptResultCell(a JobAttempt) (result, exit string) {
	switch a.Result {
	case JobAttemptSucceeded:
		result = "Succeeded"
	case JobAttemptFailed:
		result = "Error"
	default:
		result = "Running"
	}
	if a.ExitCode != nil {
		exit = fmt.Sprintf("%d", *a.ExitCode)
	} else {
		exit = "–"
	}
	return result, exit
}

// ProjectJobAttemptTable turns summary's Attempts into §37b's flat Row
// slice: # · POD · RESULT · EXIT · RAN · NODE · WHEN.
func ProjectJobAttemptTable(summary JobAttemptsSummary, now time.Time) []Row {
	rows := make([]Row, 0, len(summary.Attempts))
	for _, a := range summary.Attempts {
		result, exit := attemptResultCell(a)
		status := StatusWarn
		switch a.Result {
		case JobAttemptSucceeded:
			status = StatusOK
		case JobAttemptFailed:
			status = StatusFail
		}
		ran := "–"
		if !a.StartedAt.IsZero() {
			ran = shortAge(a.Duration(now))
		}
		when := "–"
		if !a.StartedAt.IsZero() {
			when = shortAge(now.Sub(a.StartedAt)) + " ago"
		}
		node := a.Node
		if node == "" {
			node = "–"
		}
		rows = append(rows, Row{
			Name:   a.PodName,
			Status: status,
			Cells:  []string{fmt.Sprintf("%d", a.Ordinal), a.PodName, result, exit, ran, node, when},
		})
	}
	return rows
}

// JobIndexCell is one cell of §37d's index map — one per completion index
// from 0 to Completions-1.
type JobIndexCell struct {
	Index int32
	// Attempts is every pod that ran this index, oldest first — more than
	// one when the index retried.
	Attempts []JobAttempt
}

// Result is the index's own current state: the newest attempt's Result, or
// JobAttemptQueued when no pod has run this index yet.
func (c JobIndexCell) Result() JobAttemptResult {
	if len(c.Attempts) == 0 {
		return JobAttemptQueued
	}
	return c.Attempts[len(c.Attempts)-1].Result
}

// ProjectJobIndexGrid turns summary's Attempts into §37d's per-index cells —
// only meaningful when summary.Indexed. Indexes beyond what any attempt
// covers, up to Completions, render as JobAttemptQueued cells with no
// attempts (a genuinely unstarted index, not a gap in the read — see the
// package doc comment for what a GC'd index's own last-known state means
// here: simply absent, not reconstructed).
func ProjectJobIndexGrid(summary JobAttemptsSummary) []JobIndexCell {
	byIndex := map[int32][]JobAttempt{}
	maxIndex := int32(-1)
	for _, a := range summary.Attempts {
		if a.Index == nil {
			continue
		}
		byIndex[*a.Index] = append(byIndex[*a.Index], a)
		if *a.Index > maxIndex {
			maxIndex = *a.Index
		}
	}
	total := maxIndex + 1
	if summary.Completions != nil && *summary.Completions > total {
		total = *summary.Completions
	}
	if total <= 0 {
		return nil
	}
	cells := make([]JobIndexCell, total)
	for i := int32(0); i < total; i++ {
		attempts := byIndex[i]
		sort.SliceStable(attempts, func(x, y int) bool { return attempts[x].Ordinal < attempts[y].Ordinal })
		cells[i] = JobIndexCell{Index: i, Attempts: attempts}
	}
	return cells
}

// JobAttemptETA estimates §37d's ETA line: nil until enough indexes have
// finished to be a meaningful sample (at least 20% of the total, and at
// least 2 — a single early finisher tells you nothing about the other
// 11 indexes' likely duration). Pure arithmetic from observed durations and
// Parallelism; never polled, caller-supplied now.
func JobAttemptETA(summary JobAttemptsSummary, now time.Time) (eta time.Duration, ok bool) {
	if !summary.Indexed || summary.Completions == nil || *summary.Completions <= 0 {
		return 0, false
	}
	total := *summary.Completions
	cells := ProjectJobIndexGrid(summary)

	var finishedDurations []time.Duration
	remaining := int32(0)
	for _, c := range cells {
		if c.Result() == JobAttemptSucceeded && len(c.Attempts) > 0 {
			last := c.Attempts[len(c.Attempts)-1]
			if !last.EndedAt.IsZero() {
				finishedDurations = append(finishedDurations, last.Duration(now))
			}
			continue
		}
		remaining++
	}
	minSample := max(2, int((total*20)/100))
	if len(finishedDurations) < minSample {
		return 0, false
	}
	var sum time.Duration
	for _, d := range finishedDurations {
		sum += d
	}
	mean := sum / time.Duration(len(finishedDurations))
	parallelism := summary.Parallelism
	if parallelism <= 0 {
		parallelism = 1
	}
	waves := (remaining + parallelism - 1) / parallelism
	return mean * time.Duration(waves), true
}
