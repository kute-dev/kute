package kube

import (
	corev1 "k8s.io/api/core/v1"
)

// NodeResources is one node's cpu/mem/pods triple — the shape every screen
// that draws a node bar needs. It exists so browse (11a), nodedetail (11b)
// and overview (19a) read the node's numbers through one function instead
// of each re-deriving Status.Allocatable inline, which is how they came to
// disagree about what a node's MEM bar measures in the first place.
type NodeResources struct {
	CPUMilli int64
	MemBytes int64
	Pods     int64
}

// NodeAllocatable is what the scheduler may hand out: capacity minus
// kube-reserved/system-reserved/eviction-threshold. It is the correct
// denominator for a bar whose numerator is a sum of pod *requests*.
func NodeAllocatable(n *corev1.Node) NodeResources {
	return nodeResources(n.Status.Allocatable)
}

// NodeCapacity is the machine's full size, and the correct denominator for
// a bar whose numerator is metrics-server *usage*: that usage is measured
// across the whole node, kubelet and container runtime included, so
// dividing it by Allocatable mixes two different units of measure and
// reports a node hotter than `kubectl top node` does. On AKS, where
// kube-reserved is ~2.7Gi of 16Gi, that overstated every MEM bar by around
// ten points.
//
// Falls back per-resource to Allocatable: a kubelet that hasn't reported
// Capacity yet should degrade to a slightly-wrong bar, not a "–".
func NodeCapacity(n *corev1.Node) NodeResources {
	capacity := nodeResources(n.Status.Capacity)
	allocatable := nodeResources(n.Status.Allocatable)
	if capacity.CPUMilli == 0 {
		capacity.CPUMilli = allocatable.CPUMilli
	}
	if capacity.MemBytes == 0 {
		capacity.MemBytes = allocatable.MemBytes
	}
	if capacity.Pods == 0 {
		capacity.Pods = allocatable.Pods
	}
	return capacity
}

func nodeResources(list corev1.ResourceList) NodeResources {
	var r NodeResources
	if q, ok := list[corev1.ResourceCPU]; ok {
		r.CPUMilli = q.MilliValue()
	}
	if q, ok := list[corev1.ResourceMemory]; ok {
		r.MemBytes = q.Value()
	}
	if q, ok := list[corev1.ResourcePods]; ok {
		r.Pods = q.Value()
	}
	return r
}
