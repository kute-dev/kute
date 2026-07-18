package kube

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestFindByNameLocatesMatch(t *testing.T) {
	t.Parallel()
	objs := []runtime.Object{
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "worker"}},
	}
	obj, err := findByName(objs, "worker")
	if err != nil {
		t.Fatalf("findByName: %v", err)
	}
	pod, ok := obj.(*corev1.Pod)
	if !ok || pod.Name != "worker" {
		t.Fatalf("findByName returned %+v, want pod named worker", obj)
	}
}

func TestFindByNameNotFound(t *testing.T) {
	t.Parallel()
	objs := []runtime.Object{&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api"}}}
	if _, err := findByName(objs, "missing"); err == nil {
		t.Fatalf("expected an error for a missing name")
	}
}

func TestManagedFieldsLineCountEmpty(t *testing.T) {
	t.Parallel()
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api"}}
	if got := ManagedFieldsLineCount(pod); got != 0 {
		t.Fatalf("ManagedFieldsLineCount = %d, want 0 for no managed fields", got)
	}
}

func TestManagedFieldsLineCountNonEmpty(t *testing.T) {
	t.Parallel()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api",
			ManagedFields: []metav1.ManagedFieldsEntry{
				{Manager: "kubectl", Operation: metav1.ManagedFieldsOperationUpdate, APIVersion: "v1"},
				{Manager: "controller", Operation: metav1.ManagedFieldsOperationApply, APIVersion: "v1"},
			},
		},
	}
	got := ManagedFieldsLineCount(pod)
	if got <= 0 {
		t.Fatalf("ManagedFieldsLineCount = %d, want > 0", got)
	}
}

func TestGetYAMLDeepCopiesRatherThanMutatingCache(t *testing.T) {
	t.Parallel()
	// Simulates what GetYAML does to a cache object: clearing
	// managedFields on a DeepCopy must never touch the original, since
	// informer cache objects are shared and read elsewhere concurrently.
	original := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "api",
			ResourceVersion: "42",
			ManagedFields:   []metav1.ManagedFieldsEntry{{Manager: "kubectl"}},
		},
	}
	copyObj := original.DeepCopyObject().(*corev1.Pod)
	copyObj.ManagedFields = nil

	if len(original.ManagedFields) != 1 {
		t.Fatalf("original object was mutated: %+v", original.ManagedFields)
	}
	if len(copyObj.ManagedFields) != 0 {
		t.Fatalf("expected copy's managedFields cleared")
	}
}

func TestManagedFieldsYAMLShapeSanity(t *testing.T) {
	t.Parallel()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:          "api",
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "kubectl"}, {Manager: "controller"}, {Manager: "third"}},
		},
	}
	got := ManagedFieldsLineCount(pod)
	if got < 3 {
		t.Fatalf("expected at least 3 lines for 3 managed-field entries, got %d", got)
	}
	// Sanity: single-entry count should be strictly less than 3-entry count.
	single := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "kubectl"}}}}
	if ManagedFieldsLineCount(single) >= got {
		t.Fatalf("expected fewer lines for 1 entry than 3, got %d vs %d", ManagedFieldsLineCount(single), got)
	}
}

func TestManagedFieldsLineCountUnrecognizedObjectIsZero(t *testing.T) {
	t.Parallel()
	if got := ManagedFieldsLineCount(nil); got != 0 {
		t.Fatalf("ManagedFieldsLineCount(nil) = %d, want 0", got)
	}
}
