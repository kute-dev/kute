package timeline

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

// TestPasteIntoFilterNarrowsFeed: a pasted query narrows the merged feed the
// same way a typed one does.
func TestPasteIntoFilterNarrowsFeed(t *testing.T) {
	events := []kube.Event{
		{Type: "Warning", Reason: "BackOff", Object: "Pod/worker-0", Message: "restarting", Count: 1, LastSeen: time.Now()},
		{Type: "Normal", Reason: "Scheduled", Object: "Pod/api-0", Message: "assigned", Count: 1, LastSeen: time.Now()},
	}
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {testPod("worker-0", "node-a", 5*time.Minute)},
	}}
	m := New(Config{Session: newSession(), Events: fakeEvents{events: events}, Lister: lister, Namespace: "default"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	before := len(m.rows)
	m = step(t, m, tea.KeyPressMsg{Text: "/"})
	m = step(t, m, tea.PasteMsg{Content: "BackOff"})
	if got := m.filterInput.Value(); got != "BackOff" {
		t.Fatalf("filter buffer = %q, want %q", got, "BackOff")
	}
	if len(m.rows) != 1 {
		t.Fatalf("rows = %d (was %d), want 1 — paste did not re-apply the filter", len(m.rows), before)
	}

	if _, cmd := m.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'v'}); cmd == nil {
		t.Fatal("ctrl+v with the filter open returned no cmd, want the clipboard read")
	}
}

// TestPasteWithoutBufferIsIgnored: with neither the filter nor a PROD
// type-the-name confirm open there's nothing to paste into.
func TestPasteWithoutBufferIsIgnored(t *testing.T) {
	m := New(Config{Session: newSession(), Events: fakeEvents{}, Lister: fakeLister{}, Namespace: "default"})
	m.SetSize(120, 36)
	m = step(t, m, tea.PasteMsg{Content: "BackOff"})
	if m.filterActive || m.filterInput.Value() != "" {
		t.Fatalf("filterActive=%v query=%q, want both zero", m.filterActive, m.filterInput.Value())
	}
}
