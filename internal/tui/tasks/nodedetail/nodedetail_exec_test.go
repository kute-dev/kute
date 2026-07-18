package nodedetail

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

// schedPodWithContainers is schedPod without the resource requests but with
// named containers, for the single- vs multi-container exec branching.
func schedPodWithContainers(ns, name, node string, containers ...string) *corev1.Pod {
	cs := make([]corev1.Container, len(containers))
	statuses := make([]corev1.ContainerStatus, len(containers))
	for i, c := range containers {
		cs[i] = corev1.Container{Name: c, Image: c + ":latest"}
		statuses[i] = corev1.ContainerStatus{Name: c, Ready: true}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       corev1.PodSpec{NodeName: node, Containers: cs},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: statuses},
	}
}

// TestExecSingleContainerRunsDirectly confirms 'x' on a single-container pod
// row execs immediately (docs/design README.md §10a: "skipped entirely for
// single-container pods") — no execpicker task is pushed, and the Cmd
// returned is the tea.ExecProcess wrapping kube.ExecSpec. Mirrors browse's
// test of the same name.
func TestExecSingleContainerRunsDirectly(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {testNode("node-a")},
		kube.KindPod:  {schedPodWithContainers("default", "api-0", "node-a", "app")},
	}}
	openExecCalled := false
	m := New(Config{
		Session: newSession(), Lister: lister, NodeName: "node-a",
		OpenExec: func(string, string, []kube.ContainerInfo, int, int) (tea.Model, tea.Cmd) {
			openExecCalled = true
			return sentinelTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "x"})
	if _, ok := updated.(*Model); !ok {
		t.Fatalf("expected nodedetail to stay the active task for a single-container pod, got %T", updated)
	}
	if cmd == nil {
		t.Fatal("expected a non-nil exec Cmd")
	}
	if openExecCalled {
		t.Fatal("OpenExec must not be called for a single-container pod")
	}
}

// TestExecMultiContainerPushesPicker confirms 'x' on a multi-container pod
// row pushes tasks/execpicker via OpenExec instead of execing directly.
func TestExecMultiContainerPushesPicker(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {testNode("node-a")},
		kube.KindPod:  {schedPodWithContainers("default", "api-0", "node-a", "app", "sidecar")},
	}}
	var gotContainers []kube.ContainerInfo
	m := New(Config{
		Session: newSession(), Lister: lister, NodeName: "node-a",
		OpenExec: func(ns, name string, containers []kube.ContainerInfo, w, h int) (tea.Model, tea.Cmd) {
			gotContainers = containers
			return sentinelTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tea.KeyPressMsg{Text: "x"})
	if _, ok := updated.(sentinelTask); !ok {
		t.Fatalf("expected execpicker's sentinel task to be pushed, got %T", updated)
	}
	if len(gotContainers) != 2 {
		t.Fatalf("expected both containers handed to OpenExec, got %v", gotContainers)
	}
}

// TestOpenLogsHandoff confirms 'l' on a pod row hands off to the log-stream
// task with the selected row's pod — no poddetail detour.
func TestOpenLogsHandoff(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {testNode("node-a")},
		kube.KindPod:  {schedPod("default", "big", "node-a", "1Gi")},
	}}
	var openedPod kube.Pod
	openLogs := func(pod kube.Pod, _, _ int) (tea.Model, tea.Cmd) {
		openedPod = pod
		return sentinelTask{}, nil
	}
	m := New(Config{Session: newSession(), Lister: lister, NodeName: "node-a", OpenLogs: openLogs})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tea.KeyPressMsg{Text: "l"})
	if _, ok := updated.(sentinelTask); !ok {
		t.Fatalf("expected 'l' to hand off to the logs task, got %T", updated)
	}
	if openedPod.Name != "big" || openedPod.Namespace != "default" {
		t.Fatalf("openLogs called with %s/%s, want default/big", openedPod.Namespace, openedPod.Name)
	}
}

// TestExecResultFeedbackSurfacesInKeybar confirms a non-zero direct-exec
// exit sets nodedetail's own execFeedback, surfaced via Keybar's RightNote
// (docs/design README.md §10a: "feedback line on non-zero exit").
func TestExecResultFeedbackSurfacesInKeybar(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {testNode("node-a")},
		kube.KindPod:  {schedPodWithContainers("default", "api-0", "node-a", "app")},
	}}
	m := New(Config{Session: newSession(), Lister: lister, NodeName: "node-a"})
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

// TestNodeShellKeyRunsDirectly confirms 's' hands the tty to kubectl debug
// for the node itself: nodedetail stays the active task and the Cmd is the
// tea.ExecProcess wrapping kube.NodeShellSpec.
func TestNodeShellKeyRunsDirectly(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {testNode("node-a")},
	}}
	m := New(Config{Session: newSession(), Lister: lister, NodeName: "node-a"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "s"})
	if _, ok := updated.(*Model); !ok {
		t.Fatalf("expected nodedetail to stay the active task, got %T", updated)
	}
	if cmd == nil {
		t.Fatal("expected a non-nil node-shell Cmd")
	}
}

// TestNodeShellResultFeedbackSurfacesInKeybar confirms a non-zero kubectl
// debug exit sets execFeedback, surfaced via Keybar's RightNote and naming
// the node shell rather than exec.
func TestNodeShellResultFeedbackSurfacesInKeybar(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {testNode("node-a")},
	}}
	m := New(Config{Session: newSession(), Lister: lister, NodeName: "node-a"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, nodeShellResultMsg{err: errExitStatus{}})
	if note := m.Keybar().RightNote; !strings.Contains(note, "node shell exited") {
		t.Fatalf("expected node-shell feedback in Keybar RightNote, got %q", note)
	}
}
