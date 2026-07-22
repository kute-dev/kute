package timeline

import (
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// Keybar composes the bottom band from verb references only, per the
// registry invariant — 16a/16b's pill is TIMELINE (docs/design README.md
// §16a).
func (m Model) Keybar() tui.Keybar {
	if m.state == tui.TaskStateEmpty {
		return tui.Keybar{Pill: tui.ModeBrowse, PillText: "TIMELINE", RightNote: "0 changes · watching"}
	}
	if m.filterActive {
		return tui.Keybar{
			Pill:      tui.ModeFilter,
			PillText:  "FILTER",
			Groups:    [][]tui.KeyHint{{{Key: "esc", Label: "clear"}}},
			RightNote: "type to narrow",
		}
	}

	return tui.Keybar{
		Pill:     tui.ModeBrowse,
		PillText: "TIMELINE",
		Groups: [][]tui.KeyHint{
			{{Key: "esc", Label: "back"}, verbs.Open.Hint()},
			{{Key: "t", Label: "time window"}},
		},
		RightHints: append(tui.UpdateRightHints(m.session), verbs.Help.Hint()),
	}
}
