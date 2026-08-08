package certchain

import (
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// Keybar is §35a's: esc/↵/y/e always available once the chain has loaded,
// nothing mutating — §35c's renew is out of scope for this screen.
func (m Model) Keybar() tui.Keybar {
	if m.state != tui.TaskStateReady {
		return tui.Keybar{Pill: tui.ModeBrowse, PillText: "CHAIN"}
	}
	groups := [][]tui.KeyHint{{{Key: "esc", Label: "back"}}}
	if m.selectableCount() > 0 {
		groups[0] = append(groups[0], verbs.Open.Hint())
	}
	groups = append(groups, []tui.KeyHint{verbs.YAML.Hint(), verbs.Events.Hint()})
	return tui.Keybar{
		Pill:       tui.ModeBrowse,
		PillText:   "CHAIN",
		Groups:     groups,
		RightNote:  m.feedback,
		RightHints: append(tui.UpdateRightHints(m.session), verbs.Help.Hint()),
	}
}
