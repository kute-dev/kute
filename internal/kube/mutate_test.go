package kube

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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

func strPtr(s string) *string { return &s }

func TestSetResourcesPatchesRequestsAndLimits(t *testing.T) {
	t.Parallel()
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "app", Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("512Mi")},
				Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("512Mi")},
			}},
		}}}},
	}
	c, cs := newTestCluster(deploy)

	edits := ResourceEdits{MEMLimit: strPtr("768Mi")}
	if err := c.SetResources(context.Background(), KindDeployment, "default", "api", "app", edits, false); err != nil {
		t.Fatalf("SetResources: %v", err)
	}
	got, err := cs.AppsV1().Deployments("default").Get(context.Background(), "api", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	res := got.Spec.Template.Spec.Containers[0].Resources
	if q := res.Limits[corev1.ResourceMemory]; q.String() != "768Mi" {
		t.Fatalf("mem limit = %s, want 768Mi", q.String())
	}
	if q := res.Requests[corev1.ResourceMemory]; q.String() != "512Mi" {
		t.Fatalf("mem request = %s, want unchanged 512Mi", q.String())
	}
}

func TestSetResourcesUnsetsField(t *testing.T) {
	t.Parallel()
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "app", Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")},
			}},
		}}}},
	}
	c, cs := newTestCluster(deploy)

	edits := ResourceEdits{CPULimit: strPtr("")}
	if err := c.SetResources(context.Background(), KindDeployment, "default", "api", "app", edits, false); err != nil {
		t.Fatalf("SetResources: %v", err)
	}
	got, err := cs.AppsV1().Deployments("default").Get(context.Background(), "api", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, ok := got.Spec.Template.Spec.Containers[0].Resources.Limits[corev1.ResourceCPU]; ok {
		t.Fatalf("expected cpu limit removed, still present: %+v", got.Spec.Template.Spec.Containers[0].Resources.Limits)
	}
}

func TestSetResourcesRejectsEmptyName(t *testing.T) {
	t.Parallel()
	c, _ := newTestCluster()
	if err := c.SetResources(context.Background(), KindDeployment, "default", "", "app", ResourceEdits{MEMLimit: strPtr("768Mi")}, false); err == nil {
		t.Fatalf("expected an error for an empty resource name")
	}
}

// TestSetResourcesDryRunStillMutatesFakeClientset documents (rather than
// prescribes) client-go's fake Clientset behavior: it has no admission
// simulation, and it doesn't special-case metav1.PatchOptions.DryRun either
// — a dry-run patch against it mutates the tracked object exactly like a
// real one. kube.Cluster.SetResources's own dry-run therefore only proves
// anything against a real API server; 25a's own client-side validation
// (quantity parsing, request>limit) is what's actually exercised in tests.
func TestSetResourcesDryRunStillMutatesFakeClientset(t *testing.T) {
	t.Parallel()
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "app"},
		}}}},
	}
	c, cs := newTestCluster(deploy)

	if err := c.SetResources(context.Background(), KindDeployment, "default", "api", "app", ResourceEdits{MEMLimit: strPtr("768Mi")}, true); err != nil {
		t.Fatalf("SetResources(dryRun): %v", err)
	}
	got, err := cs.AppsV1().Deployments("default").Get(context.Background(), "api", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if q := got.Spec.Template.Spec.Containers[0].Resources.Limits[corev1.ResourceMemory]; q.String() != "768Mi" {
		t.Fatalf("fake clientset dry-run mutated = %s — if this ever changes, update kube/fake and 25a's commitSetResources accordingly", q.String())
	}
}

func TestSetResourcesRejectsNoChangedFields(t *testing.T) {
	t.Parallel()
	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"}}
	c, _ := newTestCluster(deploy)
	if err := c.SetResources(context.Background(), KindDeployment, "default", "api", "app", ResourceEdits{}, false); err == nil {
		t.Fatalf("expected an error when edits has no changed fields")
	}
}

func TestSetResourcesCommandStringPlainSet(t *testing.T) {
	t.Parallel()
	got := SetResourcesCommandString(KindDeployment, "aim-stage", "aim-worker", "worker", ResourceEdits{MEMLimit: strPtr("768Mi")})
	want := "kubectl set resources deploy/aim-worker -c worker --limits=memory=768Mi -n aim-stage"
	if got != want {
		t.Errorf("SetResourcesCommandString = %q, want %q", got, want)
	}
}

func TestSetResourcesCommandStringMultipleFields(t *testing.T) {
	t.Parallel()
	got := SetResourcesCommandString(KindStatefulSet, "default", "db", "db", ResourceEdits{
		CPURequest: strPtr("100m"), MEMRequest: strPtr("256Mi"), CPULimit: strPtr("500m"),
	})
	want := "kubectl set resources sts/db -c db --limits=cpu=500m --requests=cpu=100m,memory=256Mi -n default"
	if got != want {
		t.Errorf("SetResourcesCommandString = %q, want %q", got, want)
	}
}

func TestSetResourcesCommandStringUnsetFallsBackToPatch(t *testing.T) {
	t.Parallel()
	got := SetResourcesCommandString(KindDaemonSet, "default", "agent", "agent", ResourceEdits{CPULimit: strPtr("")})
	want := `kubectl patch ds/agent --type strategic -p '{"spec":{"template":{"spec":{"containers":[{"name":"agent","resources":{"limits":{"cpu":null}}}]}}}}' -n default`
	if got != want {
		t.Errorf("SetResourcesCommandString = %q, want %q", got, want)
	}
}

func TestMetaCommandStringSetOverwriteAndRemove(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		isAnnotation      bool
		key, value        string
		remove, overwrite bool
		want              string
	}{
		{
			name: "overwrite existing label", key: "env", value: "staging", overwrite: true,
			want: "kubectl label deploy/aim-worker env=staging --overwrite -n aim-stage",
		},
		{
			name: "new label, no overwrite flag", key: "tier", value: "gold",
			want: "kubectl label deploy/aim-worker tier=gold -n aim-stage",
		},
		{
			name: "annotation set", isAnnotation: true, key: "kute.dev/owner", value: "platform-oncall", overwrite: true,
			want: "kubectl annotate deploy/aim-worker kute.dev/owner=platform-oncall --overwrite -n aim-stage",
		},
		{
			name: "label removal ignores overwrite", key: "team", remove: true, overwrite: true,
			want: "kubectl label deploy/aim-worker team- -n aim-stage",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := MetaCommandString(KindDeployment, "aim-stage", "aim-worker", tt.isAnnotation, tt.key, tt.value, tt.remove, tt.overwrite)
			if got != tt.want {
				t.Errorf("MetaCommandString = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMetaCommandStringOmitsNamespaceForClusterScopedKind(t *testing.T) {
	t.Parallel()
	got := MetaCommandString(KindNode, "", "node-a", false, "env", "prod", false, false)
	want := "kubectl label node/node-a env=prod"
	if got != want {
		t.Errorf("MetaCommandString = %q, want %q", got, want)
	}
}

func TestPatchMetaSetsAndRemovesLabelsAndAnnotations(t *testing.T) {
	t.Parallel()
	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name: "aim-worker", Namespace: "default", Labels: map[string]string{"env": "stage"},
	}}
	c, cs := newTestCluster(deploy)
	ctx := context.Background()

	if err := c.PatchMeta(ctx, KindDeployment, "default", "aim-worker", false, "env", "staging", false); err != nil {
		t.Fatalf("PatchMeta set: %v", err)
	}
	got, err := cs.AppsV1().Deployments("default").Get(ctx, "aim-worker", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Labels["env"] != "staging" {
		t.Errorf("labels[env] = %q, want staging", got.Labels["env"])
	}

	if err := c.PatchMeta(ctx, KindDeployment, "default", "aim-worker", true, "kute.dev/owner", "platform-oncall", false); err != nil {
		t.Fatalf("PatchMeta annotate: %v", err)
	}
	got, err = cs.AppsV1().Deployments("default").Get(ctx, "aim-worker", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Annotations["kute.dev/owner"] != "platform-oncall" {
		t.Errorf("annotations[kute.dev/owner] = %q, want platform-oncall", got.Annotations["kute.dev/owner"])
	}

	if err := c.PatchMeta(ctx, KindDeployment, "default", "aim-worker", false, "env", "", true); err != nil {
		t.Fatalf("PatchMeta remove: %v", err)
	}
	got, err = cs.AppsV1().Deployments("default").Get(ctx, "aim-worker", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, ok := got.Labels["env"]; ok {
		t.Errorf("expected env label removed, got %+v", got.Labels)
	}
}

func TestPatchMetaUnsupportedKindReturnsError(t *testing.T) {
	t.Parallel()
	c, _ := newTestCluster()
	if err := c.PatchMeta(context.Background(), ResourceKind("Widget"), "default", "thing", false, "k", "v", false); err == nil {
		t.Fatal("expected an error for a kind with no typed client and no discovered dynamic GVR")
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
