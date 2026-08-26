package kube

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// TestNodeCapacityIsNotAllocatable is the guard for the bug that started
// this: a metrics-server usage bar drawn against Allocatable reports a node
// hotter than `kubectl top node` does, because the usage includes the very
// reservations Allocatable subtracts. A managed node's gap is large — this
// fixture is a real AKS 16Gi node — so the two must not collapse into one.
func TestNodeCapacityIsNotAllocatable(t *testing.T) {
	n := &corev1.Node{Status: corev1.NodeStatus{
		Capacity: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("2"),
			corev1.ResourceMemory: resource.MustParse("16Gi"),
			corev1.ResourcePods:   resource.MustParse("110"),
		},
		Allocatable: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("1900m"),
			corev1.ResourceMemory: resource.MustParse("13337Mi"),
			corev1.ResourcePods:   resource.MustParse("110"),
		},
	}}

	if got := NodeCapacity(n); got.CPUMilli != 2000 || got.MemBytes != 16*1024*1024*1024 {
		t.Errorf("NodeCapacity = %+v, want 2000m / 16Gi", got)
	}
	if got := NodeAllocatable(n); got.CPUMilli != 1900 || got.MemBytes != 13337*1024*1024 {
		t.Errorf("NodeAllocatable = %+v, want 1900m / 13337Mi", got)
	}

	// 8Gi of usage is 50% of the machine and 60% of allocatable. The bar has
	// to say 50%, the number kubectl top prints.
	used := int64(8 * 1024 * 1024 * 1024)
	if pct := used * 100 / NodeCapacity(n).MemBytes; pct != 50 {
		t.Errorf("usage over capacity = %d%%, want 50%%", pct)
	}
}

// TestNodeCapacityFallsBackToAllocatable: a kubelet that hasn't reported
// Capacity should degrade to a slightly-wrong bar, never to a "–".
func TestNodeCapacityFallsBackToAllocatable(t *testing.T) {
	n := &corev1.Node{Status: corev1.NodeStatus{
		Allocatable: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("4"),
			corev1.ResourceMemory: resource.MustParse("8Gi"),
		},
	}}
	got := NodeCapacity(n)
	if got.CPUMilli != 4000 || got.MemBytes != 8*1024*1024*1024 {
		t.Errorf("NodeCapacity = %+v, want the Allocatable values", got)
	}
}

// TestNodeResourcesAbsentReportsZero — a missing key must stay 0 so
// components.MiniBar renders its "–" rather than dividing by nothing.
func TestNodeResourcesAbsentReportsZero(t *testing.T) {
	if got := NodeAllocatable(&corev1.Node{}); got != (NodeResources{}) {
		t.Errorf("NodeAllocatable of a bare node = %+v, want zero", got)
	}
}
