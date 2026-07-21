package update

import (
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// Keybar composes the bottom band from verb references plus this screen's
// own y/o/x/r — 28b's pill is UPDATE (docs/design README.md §28b). The key
// group shown is state-conditional: "y copy · o release notes · x skip ·
// esc back" while an update is available, "r re-check now · esc back"
// in the empty (already-current) state — 'r' only exists there, per the
// §28b keys line ("the only place a manual check exists").
func (m Model) Keybar() tui.Keybar {
	kb := tui.Keybar{
		Pill:       tui.ModeUpdate,
		PillText:   "UPDATE",
		RightNote:  m.feedback,
		RightHints: []tui.KeyHint{verbs.Help.Hint()},
	}

	switch m.state() {
	case tui.TaskStateReady:
		kb.Groups = [][]tui.KeyHint{{
			{Key: "y", Label: "copy command"},
			{Key: "o", Label: "release notes ↗"},
			{Key: "x", Label: "skip"},
			{Key: "esc", Label: "back"},
		}}
	case tui.TaskStateEmpty:
		if m.checkDisabled() {
			kb.Groups = [][]tui.KeyHint{{{Key: "esc", Label: "back"}}}
			break
		}
		kb.Groups = [][]tui.KeyHint{{
			{Key: "r", Label: "re-check now"},
			{Key: "esc", Label: "back"},
		}}
	default: // loading
		kb.Groups = [][]tui.KeyHint{{{Key: "esc", Label: "back"}}}
	}
	return kb
}
