package events

import (
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// Keybar composes the bottom band from verb references only, per the
// registry invariant — 9b's pill is EVENTS (docs/design README.md §9b).
func (m Model) Keybar() tui.Keybar {
	if m.state == tui.TaskStateEmpty {
		return tui.Keybar{Pill: tui.ModeBrowse, PillText: "EVENTS", RightNote: "0 events · watching"}
	}
	if m.filterActive {
		return tui.Keybar{
			Pill:      tui.ModeFilter,
			PillText:  "FILTER",
			Groups:    [][]tui.KeyHint{{{Key: "esc", Label: "clear"}}},
			RightNote: "type to narrow",
		}
	}

	warnLabel := "warnings only"
	if m.warningsOnly {
		warnLabel = "all events"
	}
	groups := [][]tui.KeyHint{
		{{Key: "esc", Label: "back"}, verbs.Open.Hint()},
		{{Key: "w", Label: warnLabel}, {Key: "t", Label: "time window"}},
	}
	if row, ok := m.selectedRow(); ok && row.kind == rowGroup {
		groups = append(groups, []tui.KeyHint{verbs.YAML.Hint()})
	}
	if m.hasNormal() {
		groups = append(groups, []tui.KeyHint{{Key: "tab", Label: "expand/collapse normal"}})
	}

	return tui.Keybar{
		Pill:       tui.ModeBrowse,
		PillText:   "EVENTS",
		Groups:     groups,
		RightHints: append(tui.UpdateRightHints(m.session), verbs.Help.Hint()),
	}
}

// hasNormal reports whether the current window/filter has any non-warning
// groups to fold/expand, so the keybar only advertises 'tab' when it does
// something.
func (m Model) hasNormal() bool {
	for _, r := range m.rows {
		if r.kind == rowFolded || (r.kind == rowGroup && r.group.Type != "Warning") {
			return true
		}
	}
	return false
}
