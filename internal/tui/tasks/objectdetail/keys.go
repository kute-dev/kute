package objectdetail

import (
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// Keybar composes the bottom band from verb references only, per the
// registry invariant — 14d's pill is DETAIL (docs/design README.md §14d).
func (m Model) Keybar() tui.Keybar {
	if m.actions.Active() {
		if m.actions.Tier() == actions.TierInline {
			return tui.Keybar{
				Pill:      tui.ModeConfirm,
				PillText:  "CONFIRM",
				Groups:    [][]tui.KeyHint{{{Key: "y", Label: "confirm"}, {Key: "n", Label: "cancel"}}},
				RightNote: m.actions.Prompt(),
			}
		}
		return tui.Keybar{Pill: tui.ModeConfirm, PillText: "CONFIRM"}
	}
	if m.gone {
		return tui.Keybar{
			Pill:      tui.ModeBrowse,
			PillText:  "DETAIL",
			RightNote: m.desc.Display + " deleted · press any key to go back",
		}
	}

	groups := [][]tui.KeyHint{{{Key: "esc", Label: "back"}}}
	if len(m.siblings) > 1 {
		groups = append(groups, []tui.KeyHint{{Key: "j/k", Label: "next/prev"}})
	}
	verbGroup := []tui.KeyHint{}
	if m.openYAML != nil {
		verbGroup = append(verbGroup, verbs.YAML.Hint())
	}
	if m.openEvents != nil {
		verbGroup = append(verbGroup, verbs.Events.Hint())
	}
	if len(verbGroup) > 0 {
		groups = append(groups, verbGroup)
	}
	if m.mutator != nil {
		groups = append(groups, []tui.KeyHint{verbs.Delete.Hint()})
	}

	return tui.Keybar{
		Pill:       tui.ModeBrowse,
		PillText:   "DETAIL",
		Groups:     groups,
		RightHints: []tui.KeyHint{verbs.Help.Hint()},
	}
}

// CapturingInput reports whether a confirm is active or the object-gone
// state is showing — mirrors poddetail's own reasoning.
func (m Model) CapturingInput() bool {
	return m.actions.Active() || m.gone
}
