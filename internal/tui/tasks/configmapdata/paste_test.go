package configmapdata

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestPasteIntoInlineEditBuffer: '↵' on a short value edits it in place, and a
// paste lands in that buffer.
func TestPasteIntoInlineEditBuffer(t *testing.T) {
	cm := cmObj("aim-stage", "aim-config", map[string]string{"LOG_LEVEL": "info"})
	m := newModel(t, newSession(), cm, &fakeMutator{cm: cm}, nil)

	m = step(t, m, tea.KeyPressMsg{Text: "enter"})
	if m.editing == nil {
		t.Fatal("expected '↵' to open the inline edit buffer")
	}
	m.editing.valueInput.SetValue("")
	m = step(t, m, tea.PasteMsg{Content: "debug"})
	if got := m.editing.valueInput.Value(); got != "debug" {
		t.Fatalf("edit buffer = %q, want %q", got, "debug")
	}

	if _, cmd := m.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'v'}); cmd == nil {
		t.Fatal("ctrl+v with the edit buffer open returned no cmd, want the clipboard read")
	}
}

// TestPasteIntoBufferEditorKeepsNewlines is the one buffer in the app where a
// pasted newline must stay a newline — 'e' opens a textarea, so pasting a
// whole config file is the point of it.
func TestPasteIntoBufferEditorKeepsNewlines(t *testing.T) {
	cm := cmObj("aim-stage", "aim-config", map[string]string{"nginx.conf": "a\nb"})
	m := newModel(t, newSession(), cm, &fakeMutator{cm: cm}, nil)

	m = step(t, m, tea.KeyPressMsg{Text: "e"})
	if m.multiline == nil {
		t.Fatal("expected 'e' to open the buffer editor")
	}
	m.multiline.textarea.SetValue("")
	m = step(t, m, tea.PasteMsg{Content: "server {\n  listen 80;\n}"})
	if lines := strings.Split(m.multiline.value(), "\n"); len(lines) != 3 || lines[1] != "  listen 80;" {
		t.Fatalf("pasted buffer = %+v, want 3 lines with the newlines intact", lines)
	}
}

// TestPasteIntoAddRowBuffers: 'a' opens the two-buffer add row; a paste
// follows focus across tab.
func TestPasteIntoAddRowBuffers(t *testing.T) {
	cm := cmObj("aim-stage", "aim-config", map[string]string{"LOG_LEVEL": "info"})
	m := newModel(t, newSession(), cm, &fakeMutator{cm: cm}, nil)

	m = step(t, m, tea.KeyPressMsg{Text: "a"})
	if m.adding == nil {
		t.Fatal("expected 'a' to open the add row")
	}
	m = step(t, m, tea.PasteMsg{Content: "TIMEOUT"})
	if got := m.adding.keyInput.Value(); got != "TIMEOUT" {
		t.Fatalf("key buffer = %q, want %q", got, "TIMEOUT")
	}
	m = step(t, m, tea.KeyPressMsg{Text: "tab"})
	m = step(t, m, tea.PasteMsg{Content: "30s"})
	if got := m.adding.valueInput.Value(); got != "30s" {
		t.Fatalf("value buffer = %q, want %q", got, "30s")
	}
}

// TestPasteWithNoBufferOpenIsIgnored: the grid's own keys are bare letters, so
// a stray paste must go nowhere.
func TestPasteWithNoBufferOpenIsIgnored(t *testing.T) {
	cm := cmObj("aim-stage", "aim-config", map[string]string{"LOG_LEVEL": "info"})
	m := newModel(t, newSession(), cm, &fakeMutator{cm: cm}, nil)

	m = step(t, m, tea.PasteMsg{Content: "nope"})
	if m.adding != nil || m.editing != nil || m.multiline != nil {
		t.Fatal("paste must not open any buffer")
	}
}
