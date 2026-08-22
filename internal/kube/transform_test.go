package kube

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
)

func podWithManagedFields() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api",
			Namespace: "default",
			ManagedFields: []metav1.ManagedFieldsEntry{
				{Manager: "kube-controller-manager", Operation: metav1.ManagedFieldsOperationApply},
				{Manager: "kubelet", Operation: metav1.ManagedFieldsOperationUpdate},
			},
		},
		Spec: corev1.PodSpec{NodeName: "node-1"},
	}
}

func TestStripManagedFieldsDropsThemAndKeepsEverythingElse(t *testing.T) {
	t.Parallel()
	out, err := stripManagedFields(podWithManagedFields())
	if err != nil {
		t.Fatalf("stripManagedFields: %v", err)
	}
	pod, ok := out.(*corev1.Pod)
	if !ok {
		t.Fatalf("transform returned %T, want *corev1.Pod", out)
	}
	if len(pod.ManagedFields) != 0 {
		t.Fatalf("managedFields survived the transform: %d entries", len(pod.ManagedFields))
	}
	// Everything the app actually reads must be untouched.
	if pod.Name != "api" || pod.Namespace != "default" || pod.Spec.NodeName != "node-1" {
		t.Fatalf("transform altered fields beyond managedFields: %+v", pod.ObjectMeta)
	}
}

// TestStripManagedFieldsPassesThroughTombstones: a delete whose final state
// was missed arrives wrapped, and unwrapping it here would corrupt the
// delete handler's view of what went away.
func TestStripManagedFieldsPassesThroughTombstones(t *testing.T) {
	t.Parallel()
	tombstone := cache.DeletedFinalStateUnknown{Key: "default/api", Obj: podWithManagedFields()}
	out, err := stripManagedFields(tombstone)
	if err != nil {
		t.Fatalf("stripManagedFields: %v", err)
	}
	if _, ok := out.(cache.DeletedFinalStateUnknown); !ok {
		t.Fatalf("tombstone was unwrapped into %T", out)
	}
}

// TestStripManagedFieldsKeepsUnknownObjects: a transform that can't read an
// object must hand it back, never drop it — returning an error here would
// make the reflector discard the object entirely.
func TestStripManagedFieldsKeepsUnknownObjects(t *testing.T) {
	t.Parallel()
	notAnObject := "just a string"
	out, err := stripManagedFields(notAnObject)
	if err != nil {
		t.Fatalf("stripManagedFields returned an error for an unrecognized object: %v", err)
	}
	if out != any(notAnObject) {
		t.Fatalf("unrecognized object was altered: %v", out)
	}
}

func TestStripManagedFieldsIsIdempotent(t *testing.T) {
	t.Parallel()
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api", CreationTimestamp: metav1.NewTime(time.Now())}}
	out, err := stripManagedFields(pod)
	if err != nil {
		t.Fatalf("stripManagedFields: %v", err)
	}
	if out.(*corev1.Pod).Name != "api" {
		t.Fatal("transform altered an object that had no managedFields to begin with")
	}
}

// TestInformerCacheStripsManagedFields is the end-to-end check: the
// transform has to be wired into the factory, not merely exist.
func TestInformerCacheStripsManagedFields(t *testing.T) {
	c, _ := newLazyTestCluster(podWithManagedFields())
	defer c.Stop()
	if err := c.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	objs, err := c.ListRaw(t.Context(), KindPod, "default")
	if err != nil {
		t.Fatalf("ListRaw: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("got %d pods, want 1", len(objs))
	}
	pod, ok := objs[0].(*corev1.Pod)
	if !ok {
		t.Fatalf("got %T, want *corev1.Pod", objs[0])
	}
	if len(pod.ManagedFields) != 0 {
		t.Fatalf("informer cache retained %d managedFields entries", len(pod.ManagedFields))
	}
	if pod.Spec.NodeName != "node-1" {
		t.Fatalf("the transform damaged the object: NodeName = %q", pod.Spec.NodeName)
	}
}
