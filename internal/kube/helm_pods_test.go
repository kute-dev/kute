package kube

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func helmController(kind, name string) metav1.OwnerReference {
	controller := true
	return metav1.OwnerReference{Kind: kind, Name: name, Controller: &controller}
}

func TestHelmReleasePodKeysFollowsManifestOwnerChains(t *testing.T) {
	release := HelmRelease{Namespace: "default", Manifest: `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: aim-bp-app
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: database
---
apiVersion: batch/v1
kind: CronJob
metadata:
  name: report
`}
	sources := HelmReleasePodSources(release)
	replicaSets := []runtime.Object{
		&appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "aim-bp-app-6bdbbbd5fd", Namespace: "default", OwnerReferences: []metav1.OwnerReference{helmController("Deployment", "aim-bp-app")}}},
		&appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "aim-bp-preference-survey-app-5947cfcf64", Namespace: "default", OwnerReferences: []metav1.OwnerReference{helmController("Deployment", "aim-bp-preference-survey-app")}}},
	}
	jobs := []runtime.Object{
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "report-123", Namespace: "default", OwnerReferences: []metav1.OwnerReference{helmController("CronJob", "report")}}},
	}
	pods := []runtime.Object{
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "aim-bp-app-6bdbbbd5fd-sgrh5", Namespace: "default", OwnerReferences: []metav1.OwnerReference{helmController("ReplicaSet", "aim-bp-app-6bdbbbd5fd")}}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "aim-bp-preference-survey-app-5947cfcf64-f72h9", Namespace: "default", OwnerReferences: []metav1.OwnerReference{helmController("ReplicaSet", "aim-bp-preference-survey-app-5947cfcf64")}}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "not-a-database-name", Namespace: "default", OwnerReferences: []metav1.OwnerReference{helmController("StatefulSet", "database")}}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "report-123-abcd", Namespace: "default", OwnerReferences: []metav1.OwnerReference{helmController("Job", "report-123")}}},
	}

	keys := HelmReleasePodKeys(sources, pods, replicaSets, jobs)
	for _, name := range []string{"aim-bp-app-6bdbbbd5fd-sgrh5", "not-a-database-name", "report-123-abcd"} {
		if _, ok := keys[PodKey("default", name)]; !ok {
			t.Errorf("%s missing from release Pods", name)
		}
	}
	if _, ok := keys[PodKey("default", "aim-bp-preference-survey-app-5947cfcf64-f72h9")]; ok {
		t.Error("unrelated similarly named Pod matched the release")
	}
}

func TestHelmReleasePodKeysRejectsNonControllerReference(t *testing.T) {
	sources := []HelmReleasePodSource{{Kind: KindDaemonSet, Namespace: "default", Name: "agent"}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "agent-looking", Namespace: "default", OwnerReferences: []metav1.OwnerReference{{Kind: "DaemonSet", Name: "agent"}}}}
	if got := HelmReleasePodKeys(sources, []runtime.Object{pod}, nil, nil); len(got) != 0 {
		t.Fatalf("non-controller owner matched: %v", got)
	}
}
