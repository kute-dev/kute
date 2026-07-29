package browse

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
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

// TestNodeShellRefusedWhileOffline pins the 4a gate for 's': NodeShell is a
// Mutating verb (kubectl debug creates a privileged node-debugger pod, so it
// writes to the cluster before the user types anything), and a broken
// connection must refuse it outright — docs/design README.md §52's "mutating
// actions disabled" while OFFLINE.
func TestNodeShellRefusedWhileOffline(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {nodeObj("node-a", true, false)},
	}}
	session := newSession()
	session.Location.Kind = kube.KindNode
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnReconnecting, Err: "i/o timeout"})

	if _, cmd := m.Update(tea.KeyPressMsg{Text: "s"}); cmd != nil {
		t.Error("'s' spawned a node-debugger pod while offline")
	}
	if hints := strings.Join(keybarKeys(m.Keybar()), " "); strings.Contains(hints, "s") && strings.Contains(hints, "node shell") {
		t.Errorf("node-shell hint still advertised while offline: %q", hints)
	}

	// Back online the same key works again — the gate is the connection
	// state, not a latch.
	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnConnected})
	if _, cmd := m.Update(tea.KeyPressMsg{Text: "s"}); cmd == nil {
		t.Error("'s' stayed refused after reconnecting")
	}
}

// keybarKeys flattens a Keybar's hint groups to "key label" pairs.
func keybarKeys(kb tui.Keybar) []string {
	var out []string
	for _, g := range kb.Groups {
		for _, h := range g {
			out = append(out, h.Key+" "+h.Label)
		}
	}
	return out
}

// docs/managed-clusters.md §3: node shell can't work on GKE Autopilot or EKS
// Fargate, and the honest handling is an error naming the reason rather than
// a key that quietly does nothing (or worse, hands kubectl a command the
// platform will refuse after the screen has already been torn down).
func TestNodeShellExplainsItselfOnFargate(t *testing.T) {
	node := nodeObj("fargate-ip-10-0-1-23.eu-west-1.compute.internal", true, false)
	node.Labels = map[string]string{"eks.amazonaws.com/compute-type": "fargate"}
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {node},
	}}
	session := newSession()
	session.Location.Kind = kube.KindNode
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "s"})
	if cmd != nil {
		t.Error("'s' ran kubectl debug against a Fargate node")
	}
	m = *updated.(*Model)
	note := m.Keybar().RightNote
	if !strings.Contains(note, "EKS Fargate") {
		t.Errorf("Keybar RightNote = %q, want it to name EKS Fargate", note)
	}
	// The key itself stays advertised — it works on every other cluster.
	if hints := strings.Join(keybarKeys(m.Keybar()), " "); !strings.Contains(hints, "node shell") {
		t.Errorf("node-shell hint disappeared: %q", hints)
	}
}
