package execpicker

import (
	"github.com/kute-dev/kute/internal/tui"
)

// Keybar composes the bottom band from the panel's own key list (docs/design
// README.md §10a: "↵ open shell · ↑↓ move · esc cancel"). The ↵ label
// follows the cursor (docs/design v.0.11.0.dc.html §41a: "one container has
// a shell, one doesn't — the ↵ label follows the cursor") — "debug
// <container>" on a shell-less row, "open shell" everywhere else.
func (m Model) Keybar() tui.Keybar {
	enterLabel := "open shell"
	if m.selectedShellless() {
		enterLabel = "debug " + m.selectedContainerName()
	}
	kb := tui.Keybar{
		Pill:     tui.ModeBrowse,
		PillText: "EXEC",
		Groups: [][]tui.KeyHint{{
			{Key: "↵", Label: enterLabel},
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
