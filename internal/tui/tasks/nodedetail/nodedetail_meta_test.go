package nodedetail

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

// metaTestNode is testNode plus a label — the panel's grid needs at least one
// real row to render, and a node fresh out of testNode carries none.
func metaTestNode(name string) *corev1.Node {
	node := testNode(name)
	node.Labels = map[string]string{"topology.kubernetes.io/zone": "us-east-1a"}
	return node
}

// The panel's own behavior is tested in internal/tui/metapanel — what's
// covered here is nodedetail's hosting glue: 'm' opens it on the loaded node,
// it captures keys ('y' stops meaning YAML, esc closes the panel rather than
// popping the screen), the keybar swaps to META, and paste reaches its
// focused buffer through nodedetail's own pasteTarget resolver.
func TestMetaKeyOpensPanelOnNode(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {metaTestNode("node-a")},
		kube.KindPod:  {schedPod("default", "big", "node-a", "2Gi")},
	}}
	m := New(Config{Session: newSession(), Lister: lister, Mutator: &fakeMutator{}, NodeName: "node-a"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "m"})
	if m.meta == nil {
		t.Fatal("'m' should open the labels/annotations panel")
	}
	if !m.CapturingInput() {
		t.Error("the open panel should capture input (no global g/n/c routing)")
	}
	if kb := m.Keybar(); kb.PillText != "META" {
		t.Errorf("keybar pill = %q, want META", kb.PillText)
	}
	body := m.Body(120, 30)
	if !strings.Contains(plain(body), "LABELS · 1") || !strings.Contains(plain(body), "topology.kubernetes.io/zone") {
		t.Fatalf("panel body missing the labels grid:\n%s", plain(body))
	}
	// The title line stays frozen above the panel (the detail-screen
	// analogue of §26a's selected-row framing).
	if !strings.Contains(plain(body), "node-a") {
		t.Error("panel body should keep the node's own title line above the grid")
	}

	// While open, 'y' belongs to the panel (copy key=value), never YAML.
	updated, cmd := m.updateKey(tea.KeyPressMsg{Text: "y"})
	m = *updated.(*Model)
	if cmd == nil {
		t.Error("'y' with the panel open should return the panel's clipboard cmd")
	}

	// Paste routes into the panel's focused buffer via pasteTarget.
	m = step(t, m, tea.KeyPressMsg{Text: "a"})
	m = step(t, m, tea.PasteMsg{Content: "pastedkey"})
	if !strings.Contains(plain(m.Body(120, 30)), "pastedkey") {
		t.Error("paste should land in the panel's focused add buffer")
	}
	m = step(t, m, tea.KeyPressMsg{Text: "esc"}) // close add row

	// esc closes the panel and stays on the detail screen — it must not
	// bubble into nodedetail's own esc-goes-back binding.
	m = step(t, m, tea.KeyPressMsg{Text: "esc"})
	if m.meta != nil {
		t.Fatal("esc should close the panel")
	}
	if !strings.Contains(plain(m.Body(120, 30)), "CONDITIONS") {
		t.Error("closing the panel should land back on the detail body")
	}
}

func TestMetaKeyIsNoOpWithoutMutator(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {metaTestNode("node-a")},
	}}
	m := New(Config{Session: newSession(), Lister: lister, NodeName: "node-a"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "m"})
	if m.meta != nil {
		t.Error("'m' without a mutator should stay a no-op (read-only session)")
	}
}
