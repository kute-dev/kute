package podlogs

import (
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// Keybar composes the bottom band — 5b's pill is LOGS. Every key here is
// screen-local (tab/space/w/e/s/ctrl-y), same as poddetail's inline "tab
// cycle container" hint, since none of them are registered verbs.
func (m Model) Keybar() tui.Keybar {
	if m.filterActive {
		return tui.Keybar{
			Pill:      tui.ModeFilter,
			PillText:  "FILTER",
			Groups:    [][]tui.KeyHint{{{Key: "esc", Label: "clear"}}},
			RightNote: "type to narrow",
		}
	}

	groups := [][]tui.KeyHint{{{Key: "esc", Label: "back"}}}
	groups = append(groups, []tui.KeyHint{
		{Key: "space", Label: followLabel(m.view.AutoScroll)},
		{Key: "w/e", Label: "prev/next warn/err"},
		verbs.Filter.Hint(),
	})
	nav := []tui.KeyHint{{Key: "s", Label: "since " + m.sinceLabel()}}
	if len(m.pod.Containers) > 1 {
		nav = append(nav, tui.KeyHint{Key: "tab", Label: "cycle container"})
	}
	nav = append(nav, tui.KeyHint{Key: "ctrl-y", Label: "copy view"})
	groups = append(groups, nav)

	return tui.Keybar{
		Pill:       tui.ModeBrowse,
		PillText:   "LOGS",
		Groups:     groups,
		RightHints: append(tui.UpdateRightHints(m.session), verbs.Help.Hint()),
	}
}

func followLabel(following bool) string {
	if following {
		return "pause"
	}
	return "follow"
}
