package kube

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func newTestCluster(objs ...runtime.Object) (*Cluster, *fake.Clientset) {
	cs := fake.NewSimpleClientset(objs...)
	return &Cluster{clientset: cs}, cs
}

func TestRolloutRestartPatchesTemplateAnnotation(t *testing.T) {
	t.Parallel()
	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"}}
	c, cs := newTestCluster(deploy)

	if err := c.RolloutRestart(context.Background(), "default", "api"); err != nil {
		t.Fatalf("RolloutRestart: %v", err)
	}
	got, err := cs.AppsV1().Deployments("default").Get(context.Background(), "api", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, ok := got.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"]; !ok {
		t.Fatalf("expected restartedAt annotation, got %+v", got.Spec.Template.Annotations)
	}
}

func TestRolloutRestartRejectsEmptyName(t *testing.T) {
	t.Parallel()
	c, _ := newTestCluster()
	if err := c.RolloutRestart(context.Background(), "default", ""); err == nil {
		t.Fatalf("expected an error for an empty deployment name")
	}
}

func TestCordonSetsUnschedulable(t *testing.T) {
	t.Parallel()
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}
	c, cs := newTestCluster(node)

	if err := c.Cordon(context.Background(), "node-1", true); err != nil {
		t.Fatalf("Cordon: %v", err)
	}
	got, err := cs.CoreV1().Nodes().Get(context.Background(), "node-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Spec.Unschedulable {
		t.Fatalf("expected node to be unschedulable after Cordon(true)")
	}

	if err := c.Cordon(context.Background(), "node-1", false); err != nil {
		t.Fatalf("Uncordon: %v", err)
	}
	got, _ = cs.CoreV1().Nodes().Get(context.Background(), "node-1", metav1.GetOptions{})
	if got.Spec.Unschedulable {
		t.Fatalf("expected node schedulable after Cordon(false)")
	}
}

func TestDrainCordonsAndSkipsDaemonSetAndMirrorPods(t *testing.T) {
	t.Parallel()
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}
	evictable := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app-1", Namespace: "default"},
		Spec:       corev1.PodSpec{NodeName: "node-1"},
	}
	daemonPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ds-1", Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{Kind: "DaemonSet", Name: "ds"}},
		},
		Spec: corev1.PodSpec{NodeName: "node-1"},
	}
	mirrorPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "static-1", Namespace: "default",
			Annotations: map[string]string{corev1.MirrorPodAnnotationKey: "true"},
		},
		Spec: corev1.PodSpec{NodeName: "node-1"},
	}
	otherNodePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "elsewhere", Namespace: "default"},
		Spec:       corev1.PodSpec{NodeName: "node-2"},
	}
	c, cs := newTestCluster(node, evictable, daemonPod, mirrorPod, otherNodePod)

	evicted, err := c.Drain(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if evicted != 1 {
		t.Fatalf("evicted = %d, want 1 (only the non-daemonset, non-mirror pod)", evicted)
	}

	gotNode, _ := cs.CoreV1().Nodes().Get(context.Background(), "node-1", metav1.GetOptions{})
	if !gotNode.Spec.Unschedulable {
		t.Fatalf("expected Drain to cordon the node")
	}
}

func TestDrainRejectsEmptyNodeName(t *testing.T) {
	t.Parallel()
	c, _ := newTestCluster()
	if _, err := c.Drain(context.Background(), ""); err == nil {
		t.Fatalf("expected an error for an empty node name")
	}
}

func TestDeleteResourceForcedUsesZeroGracePeriod(t *testing.T) {
	t.Parallel()
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"}}
	c, cs := newTestCluster(pod)

	if err := c.DeleteResourceForced(context.Background(), KindPod, "default", "api"); err != nil {
		t.Fatalf("DeleteResourceForced: %v", err)
	}
	if _, err := cs.CoreV1().Pods("default").Get(context.Background(), "api", metav1.GetOptions{}); err == nil {
		t.Fatalf("expected pod to be deleted")
	}
}

func TestSetImagePatchesNamedContainer(t *testing.T) {
	t.Parallel()
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "app", Image: "app:1.0"},
			{Name: "sidecar", Image: "sidecar:1.0"},
		}}}},
	}
	c, cs := newTestCluster(deploy)

	if err := c.SetImage(context.Background(), KindDeployment, "default", "api", "app", "app:2.0"); err != nil {
		t.Fatalf("SetImage: %v", err)
	}
	got, err := cs.AppsV1().Deployments("default").Get(context.Background(), "api", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Spec.Template.Spec.Containers[0].Image != "app:2.0" {
		t.Fatalf("app image = %q, want app:2.0", got.Spec.Template.Spec.Containers[0].Image)
	}
	if got.Spec.Template.Spec.Containers[1].Image != "sidecar:1.0" {
		t.Fatalf("sidecar image = %q, want unchanged sidecar:1.0", got.Spec.Template.Spec.Containers[1].Image)
	}
}

func TestSetImageRejectsEmptyName(t *testing.T) {
	t.Parallel()
	c, _ := newTestCluster()
	if err := c.SetImage(context.Background(), KindDeployment, "default", "", "app", "app:2.0"); err == nil {
		t.Fatalf("expected an error for an empty resource name")
	}
}

func TestSetImageCommandStringAcrossKinds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		kind ResourceKind
		want string
	}{
		{KindDeployment, "kubectl set image deploy/api app=app:2.0 -n default"},
		{KindStatefulSet, "kubectl set image sts/api app=app:2.0 -n default"},
		{KindDaemonSet, "kubectl set image ds/api app=app:2.0 -n default"},
	}
	for _, tt := range tests {
		if got := SetImageCommandString(tt.kind, "default", "api", "app", "app:2.0"); got != tt.want {
			t.Errorf("SetImageCommandString(%s) = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestScaleCommandStringAcrossKinds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		kind ResourceKind
		want string
	}{
		{KindDeployment, "kubectl scale deploy/api --replicas=3 -n default"},
		{KindStatefulSet, "kubectl scale sts/api --replicas=3 -n default"},
	}
	for _, tt := range tests {
		if got := ScaleCommandString(tt.kind, "default", "api", 3); got != tt.want {
			t.Errorf("ScaleCommandString(%s) = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestIsDaemonSetOwnedPod(t *testing.T) {
	t.Parallel()
	owned := corev1.Pod{ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{Kind: "DaemonSet"}}}}
	if !isDaemonSetOwnedPod(owned) {
		t.Fatalf("expected DaemonSet-owned pod to be detected")
	}
	notOwned := corev1.Pod{ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet"}}}}
	if isDaemonSetOwnedPod(notOwned) {
		t.Fatalf("ReplicaSet-owned pod should not be treated as DaemonSet-owned")
	}
}

func TestIsMirrorPod(t *testing.T) {
	t.Parallel()
	mirror := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{corev1.MirrorPodAnnotationKey: "true"}}}
	if !isMirrorPod(mirror) {
		t.Fatalf("expected mirror pod to be detected")
	}
	normal := corev1.Pod{}
	if isMirrorPod(normal) {
		t.Fatalf("pod without the mirror annotation should not be detected as a mirror pod")
	}
}
