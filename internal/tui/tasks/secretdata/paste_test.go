package secretdata

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestPasteIntoAddRowBuffers pins the add flow's two buffers: a paste lands in
// whichever has focus, and follows tab across. This is the screen whose keybar
// already advertises "ctrl-v paste (never echoed)" (docs/design README.md
// §27b), and a pasted secret value is the realistic case — nobody retypes a
// generated password.
func TestPasteIntoAddRowBuffers(t *testing.T) {
	secret := secretObj("aim-stage", "aim-secrets", map[string][]byte{"DATABASE_URL": []byte("postgres://old")})
	m := newModel(t, newSession(), secret, &fakeMutator{secret: secret})

	m = step(t, m, tea.KeyPressMsg{Text: "a"})
	m = step(t, m, tea.PasteMsg{Content: "SMTP_PASSWORD"})
	if got := m.adding.keyInput.Value(); got != "SMTP_PASSWORD" {
		t.Fatalf("key buffer = %q, want %q", got, "SMTP_PASSWORD")
	}

	m = step(t, m, tea.KeyPressMsg{Text: "tab"})
	m = step(t, m, tea.PasteMsg{Content: "hunter2-staging"})
	if got := m.adding.valueInput.Value(); got != "hunter2-staging" {
		t.Fatalf("value buffer = %q, want %q", got, "hunter2-staging")
	}
	if got := m.adding.keyInput.Value(); got != "SMTP_PASSWORD" {
		t.Fatalf("key buffer changed to %q after the value paste", got)
	}

	if _, cmd := m.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'v'}); cmd == nil {
		t.Fatal("ctrl+v with the add row open returned no cmd, want the clipboard read")
	}
}

// TestPasteIntoEditBuffer: '↵' on an existing row decodes the real value for a
// rewrite, and a paste replaces/extends it like typing does.
func TestPasteIntoEditBuffer(t *testing.T) {
	secret := secretObj("aim-stage", "aim-secrets", map[string][]byte{"DATABASE_URL": []byte("postgres://old")})
	m := newModel(t, newSession(), secret, &fakeMutator{secret: secret})

	m = step(t, m, tea.KeyPressMsg{Text: "enter"})
	if m.editing == nil {
		t.Fatal("expected '↵' to open the edit buffer")
	}
	m.editing.valueInput.SetValue("")
	m = step(t, m, tea.PasteMsg{Content: "postgres://new"})
	if got := m.editing.valueInput.Value(); got != "postgres://new" {
		t.Fatalf("edit buffer = %q, want %q", got, "postgres://new")
	}
}

// TestPasteWithNoBufferOpenIsIgnored: the grid's own keys are bare letters, so
// a stray paste must not open a buffer or land anywhere.
func TestPasteWithNoBufferOpenIsIgnored(t *testing.T) {
	secret := secretObj("aim-stage", "aim-secrets", map[string][]byte{"DATABASE_URL": []byte("postgres://old")})
	m := newModel(t, newSession(), secret, &fakeMutator{secret: secret})

	m = step(t, m, tea.PasteMsg{Content: "nope"})
	if m.adding != nil || m.editing != nil {
		t.Fatal("paste must not open an add/edit buffer")
	}
}
