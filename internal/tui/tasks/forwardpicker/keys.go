package forwardpicker

import "github.com/kute-dev/kute/internal/tui"

// Keybar composes the bottom band from the panel's own key list (docs/design
// README.md §13a: "↵ start · type local port · ↑↓ port · esc cancel").
func (m Model) Keybar() tui.Keybar {
	kb := tui.Keybar{
		Pill:     tui.ModeBrowse,
		PillText: "FORWARD",
		Groups: [][]tui.KeyHint{{
			{Key: "↵", Label: "start"},
			{Key: "0-9", Label: "type local port"},
			{Key: "↑↓", Label: "port"},
		}},
		RightHints: []tui.KeyHint{{Key: "esc", Label: "cancel"}},
	}
	if m.feedback != "" {
		kb.RightNote = m.feedback
	}
	return kb
}

// CapturingInput reports true while a row's local port is being edited, so
// digit keys reach the buffer instead of the root shell's global shortcuts.
func (m Model) CapturingInput() bool { return m.editing() }
