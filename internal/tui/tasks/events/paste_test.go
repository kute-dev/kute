package events

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
)

// TestPasteIntoFilterNarrowsRows: a pasted query has to narrow the feed the
// same way a typed one does — a bracketed paste arrives as one message that
// never passes through the key handler, so the insert *and* the recompute
// both have to happen off the paste path.
func TestPasteIntoFilterNarrowsRows(t *testing.T) {
	events := []kube.Event{
		{Type: "Warning", Reason: "BackOff", Object: "Pod/worker-0", Message: "restarting", Count: 1, LastSeen: time.Now()},
		{Type: "Warning", Reason: "FailedScheduling", Object: "Pod/cache-0", Message: "insufficient cpu", Count: 1, LastSeen: time.Now()},
	}
	m := New(Config{Session: newSession(), Events: fakeEvents{namespaceEvents: events}, Namespace: "default"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "/"})
	m = step(t, m, tea.PasteMsg{Content: "worker"})
	if got := m.filterInput.Value(); got != "worker" {
		t.Fatalf("filter buffer = %q, want %q", got, "worker")
	}
	if len(m.rows) != 1 || m.rows[0].group.Reason != "BackOff" {
		t.Fatalf("paste did not re-apply the filter, got %+v", m.rows)
	}
}

// TestPasteOutsideFilterIsIgnored: with no buffer open a paste must not open
// one or leak into the query.
func TestPasteOutsideFilterIsIgnored(t *testing.T) {
	m := New(Config{Session: newSession(), Events: fakeEvents{}, Namespace: "default"})
	m.SetSize(120, 36)
	m = step(t, m, tea.PasteMsg{Content: "worker"})
	if m.filterActive || m.filterInput.Value() != "" {
		t.Fatalf("filterActive=%v query=%q, want both zero", m.filterActive, m.filterInput.Value())
	}
}

// TestPasteChordRequestsClipboard: ctrl+v with the filter open answers with
// the clipboard read rather than being swallowed by the text field.
func TestPasteChordRequestsClipboard(t *testing.T) {
	events := []kube.Event{
		{Type: "Warning", Reason: "BackOff", Object: "Pod/worker-0", Message: "restarting", Count: 1, LastSeen: time.Now()},
	}
	m := New(Config{Session: newSession(), Events: fakeEvents{namespaceEvents: events}, Namespace: "default"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m = step(t, m, tea.KeyPressMsg{Text: "/"})
	if !m.filterActive {
		t.Fatal("expected / to open the filter")
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'v'}); cmd == nil {
		t.Fatal("ctrl+v with the filter open returned no cmd, want the clipboard read")
	}
}
