package poddetail

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

func multiContainerPod(name, ns, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, CreationTimestamp: metav1.Now()},
		Spec: corev1.PodSpec{
			NodeName: node,
			Containers: []corev1.Container{
				{Name: "app", Image: "example.com/app:v1"},
				{Name: "sidecar", Image: "example.com/sidecar:v1"},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "app", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
				{Name: "sidecar", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			},
		},
	}
}

// TestExecSingleContainerRunsDirectly confirms 'x' execs immediately for a
// single-container pod (docs/design README.md §10a) without pushing
// execpicker.
func TestExecSingleContainerRunsDirectly(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {runningPod("api-0", "default", "node-a")},
	}}
	openExecCalled := false
	m := New(Config{
		Session: newSession(), Lister: lister,
		OpenExec: func(string, string, []kube.ContainerInfo, int, int) (tea.Model, tea.Cmd) {
			openExecCalled = true
			return sentinelTask{}, nil
		},
		Namespace: "default", Name: "api-0",
	})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "x"})
	if _, ok := updated.(*Model); !ok {
		t.Fatalf("expected poddetail to stay the active task for a single-container pod, got %T", updated)
	}
	if cmd == nil {
		t.Fatal("expected a non-nil exec Cmd")
	}
	if openExecCalled {
		t.Fatal("OpenExec must not be called for a single-container pod")
	}
}

// TestExecMultiContainerPushesPicker confirms 'x' pushes tasks/execpicker
// via OpenExec for a multi-container pod.
func TestExecMultiContainerPushesPicker(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {multiContainerPod("api-0", "default", "node-a")},
	}}
	var gotContainers []kube.ContainerInfo
	m := New(Config{
		Session: newSession(), Lister: lister,
		OpenExec: func(ns, name string, containers []kube.ContainerInfo, w, h int) (tea.Model, tea.Cmd) {
			gotContainers = containers
			return sentinelTask{}, nil
		},
		Namespace: "default", Name: "api-0",
	})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tea.KeyPressMsg{Text: "x"})
	if _, ok := updated.(sentinelTask); !ok {
		t.Fatalf("expected execpicker's sentinel task to be pushed, got %T", updated)
	}
	if len(gotContainers) != 2 {
		t.Fatalf("expected both containers handed to OpenExec, got %v", gotContainers)
	}
}
