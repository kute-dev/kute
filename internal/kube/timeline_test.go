package kube

import (
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestTimelineFromEvents(t *testing.T) {
	now := time.Now()
	groups := []EventGroup{
		{Type: "Warning", Reason: "BackOff", Object: "Pod/worker-0", Namespace: "default", Message: "m", LastSeen: now},
	}
	entries := TimelineFromEvents(groups)
	if len(entries) != 1 {
		t.Fatalf("len = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Kind != TimelineEvent || e.Severity != "Warning" || e.Object != "Pod/worker-0" || !e.Time.Equal(now) {
		t.Fatalf("entry = %+v, unexpected projection", e)
	}
}

func TestTimelineFromRestartsSkipsPodsWithoutTermination(t *testing.T) {
	pods := []Pod{
		{Name: "a", Namespace: "default"}, // no LastTermination
		{Name: "b", Namespace: "default", LastTermination: &LastTermination{
			Container: "app", Reason: "OOMKilled", ExitCode: 137, FinishedAt: time.Now(),
		}},
	}
	entries := TimelineFromRestarts(pods)
	if len(entries) != 1 {
		t.Fatalf("len = %d, want 1", len(entries))
	}
	if entries[0].Object != "Pod/b" || entries[0].Kind != TimelineRestart {
		t.Fatalf("entry = %+v, want restart for Pod/b", entries[0])
	}
}

func newReplicaSet(name, deployment string, revision string, image string, created time.Time) *appsv1.ReplicaSet {
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "default",
			CreationTimestamp: metav1.NewTime(created),
		},
	}
	if revision != "" {
		rs.Annotations = map[string]string{"deployment.kubernetes.io/revision": revision}
	}
	if deployment != "" {
		rs.OwnerReferences = []metav1.OwnerReference{{Kind: "Deployment", Name: deployment}}
	}
	if image != "" {
		rs.Spec.Template.Spec.Containers = []corev1.Container{{Image: image}}
	}
	return rs
}

func TestTimelineFromRolloutsFiltersToDeploymentOwnedReplicaSets(t *testing.T) {
	objs := []runtime.Object{
		newReplicaSet("nva-worker-1", "nva-worker", "3", "nginx:1.25", time.Now().Add(-time.Hour)),
		newReplicaSet("nva-worker-2", "nva-worker", "4", "nginx:1.26", time.Now()),
		newReplicaSet("orphan", "", "", "", time.Now()),                          // no Deployment owner
		newReplicaSet("no-revision", "nva-worker", "", "nginx:1.24", time.Now()), // no revision annotation
	}
	entries := TimelineFromRollouts(objs)
	if len(entries) != 2 {
		t.Fatalf("len = %d, want 2, got %+v", len(entries), entries)
	}
	for _, e := range entries {
		if e.Kind != TimelineRollout || e.Object != "Deployment/nva-worker" {
			t.Fatalf("entry = %+v, unexpected projection", e)
		}
	}
}

func TestMergeTimelineSortsNewestFirst(t *testing.T) {
	now := time.Now()
	a := []TimelineEntry{{Time: now.Add(-time.Hour), Reason: "old"}}
	b := []TimelineEntry{{Time: now, Reason: "new"}}
	c := []TimelineEntry{{Time: now.Add(-30 * time.Minute), Reason: "mid"}}

	merged := MergeTimeline(a, b, c)
	if len(merged) != 3 {
		t.Fatalf("len = %d, want 3", len(merged))
	}
	if merged[0].Reason != "new" || merged[1].Reason != "mid" || merged[2].Reason != "old" {
		t.Fatalf("merged not newest-first: %+v", merged)
	}
}
