package execpicker

import (
	"github.com/kute-dev/kute/internal/tui"
)

// Keybar composes the bottom band from the panel's own key list (docs/design
// README.md §10a: "↵ open shell · ↑↓ move · esc cancel").
func (m Model) Keybar() tui.Keybar {
	kb := tui.Keybar{
		Pill:     tui.ModeBrowse,
		PillText: "EXEC",
		Groups: [][]tui.KeyHint{{
			{Key: "↵", Label: "open shell"},
			{Key: "↑↓", Label: "move"},
		}},
		RightHints: []tui.KeyHint{{Key: "esc", Label: "cancel"}},
	}
	if m.feedback != "" {
		kb.RightNote = m.feedback
	}
	return kb
}

// CapturingInput reports false: the picker has no free-text input, every
// key is a fixed binding.
func (m Model) CapturingInput() bool { return false }
