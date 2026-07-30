package nodedetail

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

// TestPasteIntoPodsFilterNarrowsList: a pasted query narrows the node's pods
// table exactly as a typed one does, insert and recompute both.
func TestPasteIntoPodsFilterNarrowsList(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {testNode("node-a")},
		kube.KindPod: {
			schedPod("default", "api-server", "node-a", "1Gi"),
			schedPod("default", "worker-1", "node-a", "512Mi"),
			schedPod("default", "worker-2", "node-a", "256Mi"),
		},
	}}
	m := New(Config{Session: newSession(), Lister: lister, NodeName: "node-a"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "/"})
	m = step(t, m, tea.PasteMsg{Content: "worker"})
	if got := m.filterInput.Value(); got != "worker" {
		t.Fatalf("filter buffer = %q, want %q", got, "worker")
	}
	if len(m.pods) != 2 {
		t.Fatalf("filtered pods = %d, want 2 — paste did not re-apply the filter", len(m.pods))
	}
	if len(m.allPods) != 3 {
		t.Fatal("allPods must stay intact while filtering")
	}

	if _, cmd := m.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'v'}); cmd == nil {
		t.Fatal("ctrl+v with the filter open returned no cmd, want the clipboard read")
	}
}

// TestPasteWithoutFilterIsIgnored: 11b's other confirms (cordon/drain) are
// y/N cards with no text field, so a paste anywhere else here goes nowhere.
func TestPasteWithoutFilterIsIgnored(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {testNode("node-a")},
	}}
	m := New(Config{Session: newSession(), Lister: lister, NodeName: "node-a"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.PasteMsg{Content: "worker"})
	if m.filterActive || m.filterInput.Value() != "" {
		t.Fatalf("filterActive=%v query=%q, want both zero", m.filterActive, m.filterInput.Value())
	}
}
