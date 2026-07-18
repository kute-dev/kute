package browse

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

func podWithContainers(ns, name string, containers ...string) *corev1.Pod {
	cs := make([]corev1.Container, len(containers))
	statuses := make([]corev1.ContainerStatus, len(containers))
	for i, c := range containers {
		cs[i] = corev1.Container{Name: c, Image: c + ":latest"}
		statuses[i] = corev1.ContainerStatus{Name: c, Ready: true}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       corev1.PodSpec{Containers: cs},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: statuses},
	}
}

// TestExecSingleContainerRunsDirectly confirms 'x' on a single-container pod
// execs immediately (docs/design README.md §10a: "skipped entirely for
// single-container pods") — no execpicker task is pushed, and the Cmd
// returned is the tea.ExecProcess wrapping kube.ExecSpec.
func TestExecSingleContainerRunsDirectly(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-0")},
	}}
	openExecCalled := false
	m := New(Config{
		Session: newSession(), Lister: lister,
		OpenExec: func(string, string, []kube.ContainerInfo, int, int) (tea.Model, tea.Cmd) {
			openExecCalled = true
			return stubTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "x"})
	if _, ok := updated.(*Model); !ok {
		t.Fatalf("expected browse to stay the active task for a single-container pod, got %T", updated)
	}
	if cmd == nil {
		t.Fatal("expected a non-nil exec Cmd")
	}
	if openExecCalled {
		t.Fatal("OpenExec must not be called for a single-container pod")
	}
}

// TestExecMultiContainerPushesPicker confirms 'x' on a multi-container pod
// pushes tasks/execpicker via OpenExec instead of execing directly.
func TestExecMultiContainerPushesPicker(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {podWithContainers("default", "api-0", "app", "sidecar")},
	}}
	var gotContainers []kube.ContainerInfo
	m := New(Config{
		Session: newSession(), Lister: lister,
		OpenExec: func(ns, name string, containers []kube.ContainerInfo, w, h int) (tea.Model, tea.Cmd) {
			gotContainers = containers
			return stubTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tea.KeyPressMsg{Text: "x"})
	if _, ok := updated.(stubTask); !ok {
		t.Fatalf("expected execpicker's stub task to be pushed, got %T", updated)
	}
	if len(gotContainers) != 2 {
		t.Fatalf("expected both containers handed to OpenExec, got %v", gotContainers)
	}
}

// TestExecResultFeedbackSurfacesInKeybar confirms a non-zero direct-exec
// exit sets browse's own execFeedback, surfaced via Keybar's RightNote
// (docs/design README.md §10a: "feedback line on non-zero exit").
func TestExecResultFeedbackSurfacesInKeybar(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-0")},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, execResultMsg{err: errExitStatus{}})
	kb := m.Keybar()
	if kb.RightNote == "" {
		t.Fatal("expected the exec-exit feedback in Keybar RightNote")
	}
}

type errExitStatus struct{}

func (errExitStatus) Error() string { return "exit status 127" }
