package browse

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

// keybarLine is the rendered screen's last line — the keybar band.
func keybarLine(m Model) string {
	lines := strings.Split(plain(m.Render()), "\n")
	return lines[len(lines)-1]
}

// TestFailedDeleteShowsErrorOnKeybar pins review 10 H1: a failed delete
// used to close the inline y/N exactly like success. The server's error
// must reach Render(), stay through unrelated messages, and clear only on
// the next key.
func TestFailedDeleteShowsErrorOnKeybar(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-0")},
	}}
	mut := &fakeMutator{err: errors.New(`pods "api-0" is forbidden`)}
	m := New(Config{Session: newSession(), Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "D"})
	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	bar := keybarLine(m)
	if !strings.Contains(bar, "forbidden") || !strings.Contains(bar, "Delete") {
		t.Fatalf("expected the failed delete's error on the keybar, got %q", bar)
	}
	m = step(t, m, kube.ResourceChangedMsg{Kind: kube.KindPod})
	if !strings.Contains(keybarLine(m), "forbidden") {
		t.Fatalf("the error must persist until the user acts, got %q", keybarLine(m))
	}
	m = step(t, m, tea.KeyPressMsg{Text: "j"})
	if strings.Contains(keybarLine(m), "forbidden") {
		t.Fatalf("the next key should clear the error, got %q", keybarLine(m))
	}
}

// TestFailedDrainShowsErrorOnKeybar: a PDB-blocked drain leaves the node
// cordoned and half-drained, so its failure is the one that most needs to
// be on screen.
func TestFailedDrainShowsErrorOnKeybar(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {nodeObj("node-a", true, false)},
		kube.KindPod:  {schedPod("default", "p1", "node-a")},
	}}
	mut := &fakeMutator{err: errors.New("cannot evict pod as it would violate the pod's disruption budget")}
	session := newSession()
	session.Location.Kind = kube.KindNode
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(160, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+d"})
	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if bar := keybarLine(m); !strings.Contains(bar, "disruption budget") {
		t.Fatalf("expected the drain error on the keybar, got %q", bar)
	}
}

// TestSuccessfulDeleteShowsResult: success answers too, in place of the
// confirm it closed.
func TestSuccessfulDeleteShowsResult(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-0")},
	}}
	m := New(Config{Session: newSession(), Lister: lister, Mutator: &fakeMutator{}})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "D"})
	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if bar := keybarLine(m); !strings.Contains(bar, "✓") || !strings.Contains(bar, "api-0") {
		t.Fatalf("expected a success line naming api-0, got %q", bar)
	}
}
