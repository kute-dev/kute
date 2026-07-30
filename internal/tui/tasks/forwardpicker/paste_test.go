package forwardpicker

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestPasteIntoLocalPortIsDigitGated pins the numeric field's paste rule:
// surrounding whitespace is trimmed (a copied port almost always carries a
// newline), but anything else non-digit rejects the paste whole — the same
// all-or-nothing rule the typed path applies per keypress.
func TestPasteIntoLocalPortIsDigitGated(t *testing.T) {
	t.Parallel()
	tests := []struct {
		paste string
		want  string
	}{
		{paste: "8080", want: "98080"}, // inserted at the cursor, after the typed 9
		{paste: " 8080\n", want: "98080"},
		{paste: "80a80", want: "9"}, // rejected whole
		{paste: "80 80", want: "9"},
	}
	for _, tc := range tests {
		m := newModel(podWithPort("default", "web-0", 80))
		m = loadPorts(t, m)

		updated, _ := m.Update(tea.KeyPressMsg{Text: "9"})
		next := updated.(*Model)
		if !next.rows[0].editing {
			t.Fatal("expected a digit to begin the local-port edit")
		}
		updated, _ = next.Update(tea.PasteMsg{Content: tc.paste})
		if got := updated.(*Model).rows[0].editInput.Value(); got != tc.want {
			t.Errorf("paste %q -> %q, want %q", tc.paste, got, tc.want)
		}
	}
}

// TestPasteChordRequestsClipboardWhileEditing: ctrl+v while the port buffer is
// open answers with the clipboard read.
func TestPasteChordRequestsClipboardWhileEditing(t *testing.T) {
	t.Parallel()
	m := newModel(podWithPort("default", "web-0", 80))
	m = loadPorts(t, m)

	updated, _ := m.Update(tea.KeyPressMsg{Text: "9"})
	if _, cmd := updated.(*Model).Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'v'}); cmd == nil {
		t.Fatal("ctrl+v while editing returned no cmd, want the clipboard read")
	}
}

// TestPasteWithNoEditOpenIsIgnored: the row list's own keys are navigation, so
// a paste with no buffer open must not start an edit.
func TestPasteWithNoEditOpenIsIgnored(t *testing.T) {
	t.Parallel()
	m := newModel(podWithPort("default", "web-0", 80))
	m = loadPorts(t, m)

	updated, _ := m.Update(tea.PasteMsg{Content: "8080"})
	if updated.(*Model).rows[0].editing {
		t.Fatal("paste must not begin a local-port edit")
	}
}
