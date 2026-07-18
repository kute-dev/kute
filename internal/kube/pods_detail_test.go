package kube

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPodFromObjectPopulatesDetailFields(t *testing.T) {
	t.Parallel()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-1",
			Namespace: "default",
			Labels:    map[string]string{"app": "api"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Image: "app:1.0"}},
			Tolerations: []corev1.Toleration{
				{Key: "node-role", Operator: corev1.TolerationOpEqual, Value: "edge", Effect: corev1.TaintEffectNoSchedule},
				{Key: "dedicated", Operator: corev1.TolerationOpExists},
			},
		},
		Status: corev1.PodStatus{
			PodIP:    "10.0.0.5",
			QOSClass: corev1.PodQOSGuaranteed,
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "app", Ready: true, RestartCount: 2, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			},
		},
	}

	got := PodFromObject(pod)
	if got.IP != "10.0.0.5" {
		t.Errorf("IP = %q, want 10.0.0.5", got.IP)
	}
	if got.QoSClass != "Guaranteed" {
		t.Errorf("QoSClass = %q, want Guaranteed", got.QoSClass)
	}
	if got.Labels["app"] != "api" {
		t.Errorf("Labels[app] = %q, want api", got.Labels["app"])
	}
	if len(got.Tolerations) != 2 {
		t.Fatalf("Tolerations = %v, want 2 entries", got.Tolerations)
	}
	if got.Tolerations[0] != "node-role=edge:NoSchedule" {
		t.Errorf("Tolerations[0] = %q, want node-role=edge:NoSchedule", got.Tolerations[0])
	}
	if got.Tolerations[1] != "dedicated (exists):All" {
		t.Errorf("Tolerations[1] = %q, want dedicated (exists):All", got.Tolerations[1])
	}
	if len(got.ContainerInfos) != 1 || got.ContainerInfos[0].State != "Running" || got.ContainerInfos[0].Restarts != 2 {
		t.Errorf("ContainerInfos = %+v, want one Running container with 2 restarts", got.ContainerInfos)
	}
	if got.LastTermination != nil {
		t.Errorf("LastTermination = %+v, want nil (no container has terminated)", got.LastTermination)
	}
}

func TestPodFromObjectDetectsLastTermination(t *testing.T) {
	t.Parallel()
	older := metav1.NewTime(time.Now().Add(-time.Hour))
	newer := metav1.NewTime(time.Now().Add(-time.Minute))
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "crash-loop"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app"}, {Name: "sidecar"}},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "app",
					LastTerminationState: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{ExitCode: 137, Reason: "OOMKilled", FinishedAt: newer},
					},
				},
				{
					Name: "sidecar",
					LastTerminationState: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "Error", FinishedAt: older},
					},
				},
			},
		},
	}

	got := PodFromObject(pod)
	if got.LastTermination == nil {
		t.Fatalf("expected a LastTermination")
	}
	if got.LastTermination.Container != "app" {
		t.Errorf("Container = %q, want app (the most recent termination)", got.LastTermination.Container)
	}
	if got.LastTermination.ExitCode != 137 || got.LastTermination.Reason != "OOMKilled" {
		t.Errorf("LastTermination = %+v, want exit 137 OOMKilled", got.LastTermination)
	}
}

func TestPodFromObjectContainerWithoutStatusYet(t *testing.T) {
	t.Parallel()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pending"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "app:1.0"}}},
	}
	got := PodFromObject(pod)
	if len(got.ContainerInfos) != 1 {
		t.Fatalf("expected one ContainerInfo even with no status yet, got %d", len(got.ContainerInfos))
	}
	if got.ContainerInfos[0].State != "" {
		t.Errorf("State = %q, want empty (no status reported)", got.ContainerInfos[0].State)
	}
}

func TestPodFromObjectSharesListProjectionFields(t *testing.T) {
	t.Parallel()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: "app",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
					Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m")},
				},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	got := PodFromObject(pod)
	if got.CPURequestMilli != 100 || got.CPULimitMilli != 200 {
		t.Errorf("CPURequestMilli/CPULimitMilli = %d/%d, want 100/200", got.CPURequestMilli, got.CPULimitMilli)
	}
	if got.Status != "Running" {
		t.Errorf("Status = %q, want Running", got.Status)
	}
}
