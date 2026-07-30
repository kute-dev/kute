package yamlview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestPasteIntoSearchJumpsToMatch: a pasted search query moves the cursor to
// the match, which is the recompute the paste path has to carry out itself —
// jumpToMatch normally runs from updateSearchKey.
func TestPasteIntoSearchJumpsToMatch(t *testing.T) {
	m, _ := newModel(fixtureYAML)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "/"})
	m = step(t, m, tea.PasteMsg{Content: "worker"})
	if got := m.searchInput.Value(); got != "worker" {
		t.Fatalf("search buffer = %q, want %q", got, "worker")
	}
	rendered := m.rendered()
	if !strings.Contains(strings.ToLower(rendered[m.cursor].Text), "worker") {
		t.Fatalf("paste did not jump to the match, cursor on %q", rendered[m.cursor].Text)
	}

	if _, cmd := m.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'v'}); cmd == nil {
		t.Fatal("ctrl+v with search open returned no cmd, want the clipboard read")
	}
}

// TestPasteOutsideSearchIsIgnored: 8a's bare letters are navigation, so a
// paste with no search open must not open one or move the cursor.
func TestPasteOutsideSearchIsIgnored(t *testing.T) {
	m, _ := newModel(fixtureYAML)
	m = step(t, m, m.Init()())

	before := m.cursor
	m = step(t, m, tea.PasteMsg{Content: "worker"})
	if m.searchActive || m.searchInput.Value() != "" {
		t.Fatalf("searchActive=%v query=%q, want both zero", m.searchActive, m.searchInput.Value())
	}
	if m.cursor != before {
		t.Fatalf("cursor moved to %d from %d on a stray paste", m.cursor, before)
	}
}
