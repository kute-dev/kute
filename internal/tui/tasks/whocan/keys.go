package whocan

import (
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// Keybar composes the bottom band from verb references only, per the
// registry invariant — 22a's pill is WHO CAN (docs/design README.md §22a).
// v/k are screen-local (there's no cross-screen verb to register for them,
// unlike Namespace, which is shared with every other list screen).
func (m Model) Keybar() tui.Keybar {
	if m.state != tui.TaskStateReady && m.state != tui.TaskStateEmpty {
		return tui.Keybar{Pill: tui.ModeBrowse, PillText: "WHO CAN"}
	}
	if m.state == tui.TaskStateEmpty {
		return tui.Keybar{Pill: tui.ModeBrowse, PillText: "WHO CAN", RightNote: "0 subjects"}
	}

	groups := [][]tui.KeyHint{
		{{Key: "esc", Label: "back"}},
		{{Key: "v", Label: "verb"}, {Key: "k", Label: "resource"}, verbs.Namespace.Hint()},
	}
	if row, ok := m.selectedRow(); ok && row.subject.BindingName != "" {
		groups = append(groups, []tui.KeyHint{{Key: "↵", Label: "open binding yaml"}})
	}

	return tui.Keybar{
		Pill:       tui.ModeBrowse,
		PillText:   "WHO CAN",
		Groups:     groups,
		RightHints: append(tui.UpdateRightHints(m.session), verbs.Help.Hint()),
	}
}
