package resources

import (
	"strconv"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

func attemptPod(namespace, jobName, podName string, jobUID types.UID, startedAt time.Time, phase corev1.PodPhase, exitCode int32) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace, Name: podName,
			OwnerReferences: []metav1.OwnerReference{controllerRef("Job", jobName, jobUID)},
		},
		Spec: corev1.PodSpec{NodeName: "worker-1"},
		Status: corev1.PodStatus{
			Phase:     phase,
			StartTime: &metav1.Time{Time: startedAt},
			ContainerStatuses: []corev1.ContainerStatus{{
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
					ExitCode: exitCode, FinishedAt: metav1.Time{Time: startedAt.Add(30 * time.Second)},
				}},
			}},
		},
	}
}

func TestBuildJobAttemptsOrdersOldestFirstAndOnlyLivePods(t *testing.T) {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "migrate", UID: "j-uid"},
		Spec:       batchv1.JobSpec{Completions: ptr32(1)},
		Status: batchv1.JobStatus{
			Failed: 3,
			Conditions: []batchv1.JobCondition{{
				Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded",
			}},
		},
	}
	now := time.Now()
	p1 := attemptPod("default", "migrate", "migrate-aaa", job.UID, now.Add(-3*time.Minute), corev1.PodFailed, 1)
	p2 := attemptPod("default", "migrate", "migrate-bbb", job.UID, now.Add(-2*time.Minute), corev1.PodFailed, 1)
	// Only 2 of the 3 failed attempts are still in cache — under-reports
	// FailedAttempts, which must stay honest (see the package doc comment).
	summary, ok := BuildJobAttempts(job, []runtime.Object{p2, p1})
	if !ok {
		t.Fatalf("BuildJobAttempts returned ok=false")
	}
	if len(summary.Attempts) != 2 {
		t.Fatalf("expected 2 attempts (only what's in cache), got %d", len(summary.Attempts))
	}
	if summary.Attempts[0].PodName != "migrate-aaa" || summary.Attempts[0].Ordinal != 1 {
		t.Errorf("expected migrate-aaa as attempt 1 (oldest first), got %+v", summary.Attempts[0])
	}
	if summary.Attempts[1].PodName != "migrate-bbb" || summary.Attempts[1].Ordinal != 2 {
		t.Errorf("expected migrate-bbb as attempt 2, got %+v", summary.Attempts[1])
	}
	if summary.FailedAttempts != 3 {
		t.Errorf("FailedAttempts = %d, want 3 (from Job.Status.Failed, not len(Attempts))", summary.FailedAttempts)
	}
	if !summary.RetriesRemain {
		t.Errorf("expected RetriesRemain = true (Failed=true and FailedAttempts 3 < default backoffLimit 6)")
	}
}

func TestBuildJobAttemptsIgnoresUnrelatedPods(t *testing.T) {
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "migrate", UID: "j-uid"}}
	unrelated := attemptPod("default", "other-job", "other-aaa", "other-uid", time.Now(), corev1.PodFailed, 1)
	summary, ok := BuildJobAttempts(job, []runtime.Object{unrelated})
	if !ok {
		t.Fatalf("BuildJobAttempts returned ok=false")
	}
	if len(summary.Attempts) != 0 {
		t.Errorf("expected 0 attempts for an unrelated pod, got %d", len(summary.Attempts))
	}
}

func TestBuildJobAttemptsNilJobReturnsNotOK(t *testing.T) {
	if _, ok := BuildJobAttempts(nil, nil); ok {
		t.Errorf("expected ok=false for a nil Job")
	}
}

func TestBuildJobAttemptsDefaultBackoffLimit(t *testing.T) {
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "migrate"}}
	summary, ok := BuildJobAttempts(job, nil)
	if !ok {
		t.Fatalf("BuildJobAttempts returned ok=false")
	}
	if summary.BackoffLimit != 6 {
		t.Errorf("BackoffLimit = %d, want batchv1's documented default 6", summary.BackoffLimit)
	}
}

func TestBuildJobAttemptsIndexedCompletionMode(t *testing.T) {
	indexed := batchv1.IndexedCompletion
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "reindex", UID: "j-uid"},
		Spec:       batchv1.JobSpec{Completions: ptr32(3), CompletionMode: &indexed},
	}
	now := time.Now()
	p0 := attemptPod("default", "reindex", "reindex-0-aaa", job.UID, now, corev1.PodSucceeded, 0)
	p0.Annotations = map[string]string{jobCompletionIndexAnnotation: "0"}
	summary, ok := BuildJobAttempts(job, []runtime.Object{p0})
	if !ok {
		t.Fatalf("BuildJobAttempts returned ok=false")
	}
	if !summary.Indexed {
		t.Fatalf("expected Indexed = true for completionMode: Indexed")
	}
	if len(summary.Attempts) != 1 || summary.Attempts[0].Index == nil || *summary.Attempts[0].Index != 0 {
		t.Fatalf("expected one attempt with Index=0, got %+v", summary.Attempts)
	}
	cells := ProjectJobIndexGrid(summary)
	if len(cells) != 3 {
		t.Fatalf("expected 3 index cells (Completions=3), got %d", len(cells))
	}
	if cells[0].Result() != JobAttemptSucceeded {
		t.Errorf("cell 0 result = %v, want Succeeded", cells[0].Result())
	}
	if cells[1].Result() != JobAttemptQueued || cells[2].Result() != JobAttemptQueued {
		t.Errorf("expected cells 1/2 queued (no pod yet), got %v/%v", cells[1].Result(), cells[2].Result())
	}
}

func TestProjectJobAttemptTableCells(t *testing.T) {
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "migrate", UID: "j-uid"}}
	now := time.Now()
	p := attemptPod("default", "migrate", "migrate-aaa", job.UID, now.Add(-time.Minute), corev1.PodFailed, 1)
	summary, _ := BuildJobAttempts(job, []runtime.Object{p})
	rows := ProjectJobAttemptTable(summary, now)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0]
	if row.Cells[1] != "migrate-aaa" || row.Cells[2] != "Error" || row.Cells[3] != "1" || row.Cells[5] != "worker-1" {
		t.Errorf("unexpected cells: %+v", row.Cells)
	}
	if row.Status != StatusFail {
		t.Errorf("Status = %v, want StatusFail", row.Status)
	}
}

func TestJobAttemptETARequiresMeaningfulSample(t *testing.T) {
	indexed := batchv1.IndexedCompletion
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "reindex", UID: "j-uid"},
		Spec:       batchv1.JobSpec{Completions: ptr32(10), Parallelism: ptr32(2), CompletionMode: &indexed},
	}
	now := time.Now()
	p0 := attemptPod("default", "reindex", "reindex-0-aaa", job.UID, now.Add(-time.Minute), corev1.PodSucceeded, 0)
	p0.Annotations = map[string]string{jobCompletionIndexAnnotation: "0"}
	summary, _ := BuildJobAttempts(job, []runtime.Object{p0})
	if _, ok := JobAttemptETA(summary, now); ok {
		t.Errorf("expected ok=false with only 1/10 indexes finished (below the meaningful-sample threshold)")
	}
}

func TestJobAttemptETAComputesFromObservedDurations(t *testing.T) {
	indexed := batchv1.IndexedCompletion
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "reindex", UID: "j-uid"},
		Spec:       batchv1.JobSpec{Completions: ptr32(4), Parallelism: ptr32(2), CompletionMode: &indexed},
	}
	now := time.Now()
	var pods []runtime.Object
	for i := int32(0); i < 2; i++ {
		p := attemptPod("default", "reindex", "reindex-x", job.UID, now.Add(-2*time.Minute), corev1.PodSucceeded, 0)
		p.Name = "reindex-" + string(rune('a'+i))
		p.Annotations = map[string]string{jobCompletionIndexAnnotation: strconv.Itoa(int(i))}
		pods = append(pods, p)
	}
	summary, _ := BuildJobAttempts(job, pods)
	eta, ok := JobAttemptETA(summary, now)
	if !ok {
		t.Fatalf("expected ok=true with 2/4 indexes finished")
	}
	if eta <= 0 {
		t.Errorf("expected a positive ETA, got %v", eta)
	}
}
