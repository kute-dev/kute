package podlogs

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestPasteIntoFilterNarrowsLines: a pasted query narrows the log view the
// same way a typed one does — the paste arrives as one message that never
// reaches updateFilterKey, so both the insert and the viewport clamp have to
// happen off the paste path.
func TestPasteIntoFilterNarrowsLines(t *testing.T) {
	t.Parallel()

	model := testModel()
	model.stream = StreamStreaming
	model.buffer.Append(LogEntry{Message: "starting up"})
	model.buffer.Append(LogEntry{Message: "request failed"})

	if _, cmd := model.Update(tea.KeyPressMsg{Text: "/"}); cmd != nil || !model.filterActive {
		t.Fatalf("filterActive = %v cmd = %v", model.filterActive, cmd)
	}
	if _, _ = model.Update(tea.PasteMsg{Content: "failed"}); model.filterInput.Value() != "failed" {
		t.Fatalf("filter buffer = %q, want %q", model.filterInput.Value(), "failed")
	}
	if len(model.filteredEntries()) != 1 {
		t.Fatalf("filtered = %+v, want 1 match", model.filteredEntries())
	}

	if _, cmd := model.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'v'}); cmd == nil {
		t.Fatal("ctrl+v with the filter open returned no cmd, want the clipboard read")
	}
}

// TestPasteOutsideFilterIsIgnored: 5b's other keys are single-letter toggles,
// so a paste with no filter open has nowhere to go.
func TestPasteOutsideFilterIsIgnored(t *testing.T) {
	t.Parallel()

	model := testModel()
	if _, _ = model.Update(tea.PasteMsg{Content: "failed"}); model.filterActive {
		t.Fatal("paste must not open the filter")
	}
	if got := model.filterInput.Value(); got != "" {
		t.Fatalf("filter buffer = %q, want empty", got)
	}
}
