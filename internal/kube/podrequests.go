package kube

import (
	corev1 "k8s.io/api/core/v1"
)

// isRestartableInit reports whether c is a native sidecar (KEP-753): an
// init container with restartPolicy: Always, which keeps running alongside
// the regular containers instead of finishing before them. Same predicate
// buildContainerInfos uses to decide a container belongs in the running
// grid.
func isRestartableInit(c corev1.Container) bool {
	return c.RestartPolicy != nil && *c.RestartPolicy == corev1.ContainerRestartPolicyAlways
}

// PodEffectiveRequests is what a pod actually reserves on a node, by the
// scheduler's own rule — the number `kubectl describe node` reports under
// "Allocated resources".
//
// It is deliberately not the same as Pod.CPURequestMilli/MEMRequestBytes,
// which are the literal sum over spec.containers and describe the pod
// itself. Three things make a node's reservation larger than that sum:
// native sidecars (initContainers with restartPolicy: Always) run for the
// pod's whole life and so add to it; a plain init container can demand more
// on its own than the regular containers ever do together, and the node
// must hold that peak; and spec.overhead is the RuntimeClass's own charge
// for the sandbox. Summing spec.containers alone understates every
// mesh-injected pod, which is what made 11b's panel read low against
// kubectl.
func PodEffectiveRequests(pod *corev1.Pod) (cpuMilli, memBytes int64) {
	// Sidecars accumulate as we walk the init list in order: a plain init
	// container at position i runs while every sidecar declared before it
	// is already up, so its peak is its own request plus that running
	// total — not plus every sidecar in the pod.
	var sidecarCPU, sidecarMem int64
	var peakCPU, peakMem int64
	for _, c := range pod.Spec.InitContainers {
		cpu := c.Resources.Requests.Cpu().MilliValue()
		mem := c.Resources.Requests.Memory().Value()
		if isRestartableInit(c) {
			sidecarCPU += cpu
			sidecarMem += mem
			peakCPU = max(peakCPU, sidecarCPU)
			peakMem = max(peakMem, sidecarMem)
			continue
		}
		peakCPU = max(peakCPU, sidecarCPU+cpu)
		peakMem = max(peakMem, sidecarMem+mem)
	}

	var regularCPU, regularMem int64
	for _, c := range pod.Spec.Containers {
		regularCPU += c.Resources.Requests.Cpu().MilliValue()
		regularMem += c.Resources.Requests.Memory().Value()
	}

	cpuMilli = max(peakCPU, regularCPU+sidecarCPU)
	memBytes = max(peakMem, regularMem+sidecarMem)

	if pod.Spec.Overhead != nil {
		cpuMilli += pod.Spec.Overhead.Cpu().MilliValue()
		memBytes += pod.Spec.Overhead.Memory().Value()
	}
	return cpuMilli, memBytes
}
