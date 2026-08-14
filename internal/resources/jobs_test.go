package resources

import (
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/kute-dev/kute/internal/kube"
)

func plainJob(namespace, name string) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, UID: types.UID(name + "-uid")},
		Spec:       batchv1.JobSpec{Completions: ptr32(1)},
	}
}

func TestJobSourceCronJobOwnerRefWins(t *testing.T) {
	j := plainJob("default", "report-nightly-1")
	j.OwnerReferences = []metav1.OwnerReference{controllerRef("CronJob", "report-nightly", "cj-uid")}
	j.Annotations = map[string]string{kube.HelmHookAnnotation: "post-install"} // must lose to the owner ref
	kind, cronJobName, _, _, _ := jobSource(j)
	if kind != JobSourceCronJob || cronJobName != "report-nightly" {
		t.Fatalf("jobSource = (%v, %q), want (JobSourceCronJob, %q)", kind, cronJobName, "report-nightly")
	}
}

func TestJobSourceKuteCronJobAnnotationAssociation(t *testing.T) {
	j := plainJob("default", "report-nightly-manual-1")
	j.Annotations = map[string]string{
		kube.AnnotationCronJobUID:  "cj-uid",
		kube.AnnotationCronJobName: "report-nightly",
	}
	kind, cronJobName, _, _, _ := jobSource(j)
	if kind != JobSourceCronJob || cronJobName != "report-nightly" {
		t.Fatalf("jobSource = (%v, %q), want (JobSourceCronJob, %q)", kind, cronJobName, "report-nightly")
	}
}

func TestJobSourceHelmHookOverManualTag(t *testing.T) {
	j := plainJob("default", "migrate-hook")
	j.Annotations = map[string]string{
		kube.HelmHookAnnotation:        "post-install,post-upgrade",
		kube.HelmReleaseNameAnnotation: "nva-stage",
		kube.AnnotationTriggeredBy:     "someone", // must lose to the helm hook
	}
	kind, _, release, hook, _ := jobSource(j)
	if kind != JobSourceHelmHook || release != "nva-stage" || hook != "post-install" {
		t.Fatalf("jobSource = (%v, release=%q, hook=%q), want (JobSourceHelmHook, nva-stage, post-install)", kind, release, hook)
	}
}

func TestJobSourceManualTag(t *testing.T) {
	j := plainJob("default", "migrate-schema-v42-rerun-1")
	j.Annotations = map[string]string{kube.AnnotationTriggeredBy: "operator@example.com"}
	kind, _, _, _, creator := jobSource(j)
	if kind != JobSourceManualTag || creator != "operator@example.com" {
		t.Fatalf("jobSource = (%v, creator=%q), want (JobSourceManualTag, operator@example.com)", kind, creator)
	}
}

func TestJobSourceStandaloneWhenNothingMatches(t *testing.T) {
	j := plainJob("default", "one-off")
	kind, _, _, _, _ := jobSource(j)
	if kind != JobSourceStandalone {
		t.Fatalf("jobSource = %v, want JobSourceStandalone", kind)
	}
}

func TestJobSourceLabelFormatting(t *testing.T) {
	cases := []struct {
		name string
		s    JobListSummary
		user string
		want string
	}{
		{"cronjob", JobListSummary{SourceKind: JobSourceCronJob, CronJobName: "nightly"}, "", "cronjob/nightly"},
		{"standalone", JobListSummary{SourceKind: JobSourceStandalone}, "", "— standalone"},
		{"helm", JobListSummary{SourceKind: JobSourceHelmHook, HelmRelease: "nva-stage", HelmHook: "post-install"}, "", "helm/nva-stage post-install"},
		{"manual-you", JobListSummary{SourceKind: JobSourceManualTag, ManualCreator: "you@example.com"}, "you@example.com", "manual · you"},
		{"manual-other", JobListSummary{SourceKind: JobSourceManualTag, ManualCreator: "alice@example.com"}, "you@example.com", "manual · alice@example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := jobSourceLabel(tc.s, tc.user); got != tc.want {
				t.Errorf("jobSourceLabel = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildJobListSummariesResolvesNewestPod(t *testing.T) {
	job := plainJob("default", "reindex")
	older := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default", Name: "reindex-aaa",
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-time.Hour)},
			OwnerReferences:   []metav1.OwnerReference{controllerRef("Job", "reindex", job.UID)},
		},
	}
	newer := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default", Name: "reindex-bbb",
			CreationTimestamp: metav1.Time{Time: time.Now()},
			OwnerReferences:   []metav1.OwnerReference{controllerRef("Job", "reindex", job.UID)},
		},
	}
	summaries := BuildJobListSummaries([]runtime.Object{job}, []runtime.Object{older, newer}, nil)
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	if summaries[0].NewestPodName != "reindex-bbb" {
		t.Errorf("NewestPodName = %q, want %q", summaries[0].NewestPodName, "reindex-bbb")
	}
}

func TestProjectJobListDurationRedAtDeadline(t *testing.T) {
	now := time.Now()
	job := plainJob("default", "migrate-schema-v42")
	deadline := int64(600)
	job.Spec.ActiveDeadlineSeconds = &deadline
	job.Status = batchv1.JobStatus{
		Failed:    1,
		StartTime: &metav1.Time{Time: now.Add(-10 * time.Minute)},
		Conditions: []batchv1.JobCondition{{
			Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "DeadlineExceeded",
			LastTransitionTime: metav1.Time{Time: now}, // exactly 10m elapsed since StartedAt == the deadline
		}},
	}
	summaries := BuildJobListSummaries([]runtime.Object{job}, nil, nil)
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	row := ProjectJobList(summaries[0], now, "")
	if row.DurationClass != StatusFail {
		t.Errorf("DurationClass = %v, want StatusFail (duration >= activeDeadlineSeconds)", row.DurationClass)
	}
}

func TestProjectJobListHealthAndSuspended(t *testing.T) {
	suspend := true
	job := plainJob("default", "paused")
	job.Spec.Suspend = &suspend
	summaries := BuildJobListSummaries([]runtime.Object{job}, nil, nil)
	row := ProjectJobList(summaries[0], time.Now(), "")
	if !row.Suspended {
		t.Errorf("expected Row.Suspended = true")
	}
	if row.Status != StatusNeutral {
		t.Errorf("Status = %v, want StatusNeutral for a suspended, non-failed job", row.Status)
	}
}

func TestJobHealthTalliesCompleteRunningFailedSuspended(t *testing.T) {
	rows := []Row{
		{Status: StatusOK},
		{Status: StatusWarn},
		{Status: StatusFail},
		{Status: StatusNeutral, Suspended: true},
	}
	h := jobHealth(rows)
	if h.OK != 1 || h.Warn != 1 || h.Fail != 1 || h.Neutral != 1 || h.Suspended != 1 {
		t.Errorf("jobHealth = %+v, want OK=1 Warn=1 Fail=1 Neutral=1 Suspended=1", h)
	}
}
