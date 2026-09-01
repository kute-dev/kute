package kube

import (
	"iter"
	"slices"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	sigsyaml "sigs.k8s.io/yaml"
)

// HelmReleasePodSource is a manifest object which can directly, or through
// its controller children, create Pods. It is deliberately distinct from
// WorkloadRef: the latter contains only the three rolling workload kinds.
type HelmReleasePodSource struct {
	Kind      ResourceKind
	Namespace string
	Name      string
}

var helmPodSourceKinds = map[string]ResourceKind{
	"Deployment":  KindDeployment,
	"StatefulSet": KindStatefulSet,
	"DaemonSet":   KindDaemonSet,
	"Job":         KindJob,
	"CronJob":     KindCronJob,
	"Pod":         KindPod,
}

// HelmReleasePodSources finds the Pod-producing objects in a release's saved
// rendered manifest. The manifest, not release-name-shaped Pod names or chart
// labels, is Helm's authoritative inventory.
func HelmReleasePodSources(r HelmRelease) []HelmReleasePodSource {
	return slices.Collect(HelmReleasePodSourcesSeq(r))
}

func HelmReleasePodSourcesSeq(r HelmRelease) iter.Seq[HelmReleasePodSource] {
	return func(yield func(HelmReleasePodSource) bool) {
		for doc := range splitYAMLDocuments(r.Manifest) {
			kind, ok := helmPodSourceKinds[topLevelKind(doc)]
			if !ok {
				continue
			}
			var meta struct {
				Metadata struct {
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"metadata"`
			}
			if err := sigsyaml.Unmarshal([]byte(doc), &meta); err != nil || meta.Metadata.Name == "" {
				continue
			}
			namespace := meta.Metadata.Namespace
			if namespace == "" {
				namespace = r.Namespace
			}
			if !yield(HelmReleasePodSource{Kind: kind, Namespace: namespace, Name: meta.Metadata.Name}) {
				return
			}
		}
	}
}

// HelmReleasePodKeys resolves manifest sources to the Pods their Kubernetes
// controller chains own. Inputs are informer-cache snapshots supplied by the
// caller; this helper does no cluster I/O.
func HelmReleasePodKeys(sources []HelmReleasePodSource, pods, replicaSets, jobs []runtime.Object) map[string]struct{} {
	type key struct{ namespace, name string }
	deployments := map[key]struct{}{}
	directOwners := map[string]map[key]struct{}{"StatefulSet": {}, "DaemonSet": {}, "Job": {}}
	cronJobs := map[key]struct{}{}
	directPods := map[key]struct{}{}
	for _, source := range sources {
		k := key{source.Namespace, source.Name}
		switch source.Kind {
		case KindDeployment:
			deployments[k] = struct{}{}
		case KindStatefulSet:
			directOwners["StatefulSet"][k] = struct{}{}
		case KindDaemonSet:
			directOwners["DaemonSet"][k] = struct{}{}
		case KindJob:
			directOwners["Job"][k] = struct{}{}
		case KindCronJob:
			cronJobs[k] = struct{}{}
		case KindPod:
			directPods[k] = struct{}{}
		}
	}

	replicaSetOwners := map[key]struct{}{}
	for _, obj := range replicaSets {
		rs, ok := obj.(*appsv1.ReplicaSet)
		if !ok {
			continue
		}
		owner := metav1.GetControllerOf(rs)
		if owner != nil && owner.Kind == "Deployment" {
			if _, ok := deployments[key{rs.Namespace, owner.Name}]; ok {
				replicaSetOwners[key{rs.Namespace, rs.Name}] = struct{}{}
			}
		}
	}
	for _, obj := range jobs {
		job, ok := obj.(*batchv1.Job)
		if !ok {
			continue
		}
		owner := metav1.GetControllerOf(job)
		if owner != nil && owner.Kind == "CronJob" {
			if _, ok := cronJobs[key{job.Namespace, owner.Name}]; ok {
				directOwners["Job"][key{job.Namespace, job.Name}] = struct{}{}
			}
		}
	}

	out := map[string]struct{}{}
	for _, obj := range pods {
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			continue
		}
		k := key{pod.Namespace, pod.Name}
		if _, ok := directPods[k]; ok {
			out[PodKey(pod.Namespace, pod.Name)] = struct{}{}
			continue
		}
		owner := metav1.GetControllerOf(pod)
		if owner == nil {
			continue
		}
		ownerKey := key{pod.Namespace, owner.Name}
		if owner.Kind == "ReplicaSet" {
			if _, ok := replicaSetOwners[ownerKey]; ok {
				out[PodKey(pod.Namespace, pod.Name)] = struct{}{}
			}
			continue
		}
		if owners, ok := directOwners[owner.Kind]; ok {
			if _, ok := owners[ownerKey]; ok {
				out[PodKey(pod.Namespace, pod.Name)] = struct{}{}
			}
		}
	}
	return out
}
