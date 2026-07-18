package browse

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

// TestNodeShellKeyRunsDirectly confirms 's' on a Nodes row hands the tty to
// kubectl debug: no task is pushed — browse stays the active task and the
// Cmd returned is the tea.ExecProcess wrapping kube.NodeShellSpec. Mirrors
// TestExecSingleContainerRunsDirectly's shape.
func TestNodeShellKeyRunsDirectly(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {nodeObj("node-a", true, false)},
	}}
	session := newSession()
	session.Location.Kind = kube.KindNode
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "s"})
	if _, ok := updated.(*Model); !ok {
		t.Fatalf("expected browse to stay the active task, got %T", updated)
	}
	if cmd == nil {
		t.Fatal("expected a non-nil node-shell Cmd")
	}
}

// TestNodeShellKeyIgnoredOffNodesKind confirms 's' is a no-op for any other
// kind — the verb is registered Nodes-only.
func TestNodeShellKeyIgnoredOffNodesKind(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-1")},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	_, cmd := m.Update(tea.KeyPressMsg{Text: "s"})
	if cmd != nil {
		t.Fatal("expected 's' to be a no-op on the Pods kind")
	}
}

// TestNodeShellExitFeedbackSurfacesInKeybar confirms a non-zero kubectl
// debug exit lands in the keybar's RightNote, naming the node shell rather
// than exec.
func TestNodeShellExitFeedbackSurfacesInKeybar(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {nodeObj("node-a", true, false)},
	}}
	session := newSession()
	session.Location.Kind = kube.KindNode
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, nodeShellResultMsg{err: errExit{}})
	if note := m.Keybar().RightNote; !strings.Contains(note, "node shell exited") {
		t.Fatalf("expected node-shell feedback in Keybar RightNote, got %q", note)
	}
}

type errExit struct{}

func (errExit) Error() string { return "exit status 1" }
