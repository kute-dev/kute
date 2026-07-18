package nodedetail

import (
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// Keybar composes the bottom band from verb references only, per the
// registry invariant — 11b's pill is NODE (docs/design README.md §11b).
func (m Model) Keybar() tui.Keybar {
	if m.pendingEdit != nil {
		return tui.Keybar{
			Pill:      tui.ModeConfirm,
			PillText:  "CONFIRM",
			Groups:    [][]tui.KeyHint{{{Key: "y", Label: "confirm"}, {Key: "n", Label: "cancel"}}},
			RightNote: m.editConfirmPrompt(),
		}
	}
	if m.actions.Active() {
		return tui.Keybar{Pill: tui.ModeConfirm, PillText: "CONFIRM"}
	}

	if m.state == tui.TaskStateLoading {
		// 15a applied to a detail screen: only esc-back is live before the
		// node/pods land — every row/node-scoped verb stays dark
		// (docs/design README.md §15a: "row actions enable when data
		// lands").
		return tui.Keybar{
			Pill:      tui.ModeBrowse,
			PillText:  "NODE",
			Groups:    [][]tui.KeyHint{{{Key: "esc", Label: "back"}}},
			RightNote: "facts & pods enable when data lands",
		}
	}

	if m.filterActive {
		return tui.Keybar{
			Pill:      tui.ModeFilter,
			PillText:  "FILTER",
			Groups:    [][]tui.KeyHint{{{Key: "esc", Label: "clear"}}},
			RightNote: "type to narrow",
		}
	}

	groups := [][]tui.KeyHint{{{Key: "esc", Label: "back"}}}
	if len(m.allPods) > 0 {
		groups = append(groups, []tui.KeyHint{verbs.Filter.Hint()})
	}
	if len(m.pods) > 0 {
		podGroup := []tui.KeyHint{}
		if m.openPod != nil {
			podGroup = append(podGroup, verbs.Open.Hint())
		}
		if m.openLogs != nil {
			podGroup = append(podGroup, verbs.Logs.Hint())
		}
		podGroup = append(podGroup, verbs.Exec.Hint())
		groups = append(groups, podGroup)
	}
	groups = append(groups, []tui.KeyHint{verbs.NodeShell.Hint()})
	if m.node != nil {
		groups = append(groups, []tui.KeyHint{verbs.Edit.Hint()})
	}
	if m.mutator != nil {
		groups = append(groups, []tui.KeyHint{verbs.Cordon.Hint(), verbs.Drain.Hint()})
	}
	if m.openYAML != nil {
		groups = append(groups, []tui.KeyHint{verbs.YAML.Hint()})
	}
	if m.openEvents != nil {
		groups = append(groups, []tui.KeyHint{verbs.Events.Hint()})
	}
	if m.openTimeline != nil {
		groups = append(groups, []tui.KeyHint{verbs.Timeline.Hint()})
	}

	return tui.Keybar{
		Pill:      tui.ModeBrowse,
		PillText:  "NODE",
		Groups:    groups,
		RightNote: m.execFeedback,
	}
}

// CapturingInput reports whether a confirm card is open, so the root shell
// lets y/n reach nodedetail's own key handling instead of treating them as
// global shortcuts (mirrors browse.CapturingInput).
func (m Model) CapturingInput() bool {
	return m.actions.Active() || m.filterActive || m.pendingEdit != nil
}
