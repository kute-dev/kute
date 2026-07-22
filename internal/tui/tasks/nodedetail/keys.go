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
	if len(m.pods) > 0 {
		podGroup := []tui.KeyHint{}
		if m.openPod != nil {
			podGroup = append(podGroup, verbs.Open.Hint())
		}
		if m.openLogs != nil {
			podGroup = append(podGroup, verbs.Logs.Hint())
		}
		podGroup = append(podGroup, verbs.Exec.Hint())
		if m.openForward != nil {
			podGroup = append(podGroup, verbs.Forward.Hint())
		}
		groups = append(groups, podGroup)
	}
	groups = append(groups, []tui.KeyHint{verbs.NodeShell.Hint()})
	offline := m.conn.Offline()
	if m.mutator != nil && !verbs.Cordon.HiddenWhileOffline(offline) && !verbs.Drain.HiddenWhileOffline(offline) {
		groups = append(groups, []tui.KeyHint{verbs.Cordon.Hint(), verbs.Drain.Hint()})
	}

	// 4a's offline treatment (docs/design README.md §52, §301): mutating
	// verbs disappear from the keybar the same way browse's own list does,
	// not just at the actions.Controller gate.
	pill, pillText, rightNote := tui.ModeBrowse, "NODE", m.execFeedback
	if m.conn.Offline() {
		pill, pillText, rightNote = tui.ModeOffline, "OFFLINE", "mutating actions disabled"
	}
	return tui.Keybar{
		Pill:      pill,
		PillText:  pillText,
		Groups:    groups,
		RightNote: rightNote,
	}
}

// CapturingInput reports whether a confirm card is open, so the root shell
// lets y/n reach nodedetail's own key handling instead of treating them as
// global shortcuts (mirrors browse.CapturingInput).
func (m Model) CapturingInput() bool {
	return m.actions.Active() || m.filterActive || m.pendingEdit != nil
}
