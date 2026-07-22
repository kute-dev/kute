package poddetail

import (
	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// Keybar composes the bottom band from verb references only, per the
// registry invariant — 5a's pill is POD (docs/design README.md §5a).
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
		if m.actions.Tier() == actions.TierInline {
			if m.actions.ForceArmed() {
				// force-delete staged inside this same inline confirm
				// (ctrl-k, actions.Controller.ArmForceDelete) rather than the
				// PROD type-the-name modal — browse's own delete confirm
				// mirrors this exact treatment.
				note := ""
				if pending := m.actions.Pending(); pending != nil {
					note = kube.ForceDeleteCommandString(kube.ResourceKind(pending.Scope.ResourceKind), pending.Scope.Namespace, pending.Scope.ResourceName)
				}
				return tui.Keybar{
					Pill:      tui.ModeConfirm,
					PillText:  "FORCE DELETE",
					Groups:    [][]tui.KeyHint{{{Key: "y", Label: "force delete"}, {Key: "n", Label: "back"}}},
					RightNote: note,
				}
			}
			hints := []tui.KeyHint{{Key: "y", Label: "confirm"}, {Key: "n", Label: "cancel"}}
			if pending := m.actions.Pending(); pending != nil && pending.Scope.Verb == "delete" && pending.Scope.ResourceKind == string(kube.KindPod) {
				hints = append(hints, verbs.ForceDelete.Hint())
			}
			return tui.Keybar{
				Pill:      tui.ModeConfirm,
				PillText:  "CONFIRM",
				Groups:    [][]tui.KeyHint{hints},
				RightNote: m.actions.Prompt(),
			}
		}
		return tui.Keybar{Pill: tui.ModeConfirm, PillText: "CONFIRM"}
	}
	if m.gone {
		return tui.Keybar{
			Pill:      tui.ModeBrowse,
			PillText:  "POD",
			RightNote: "pod deleted · press any key to go back",
		}
	}

	groups := [][]tui.KeyHint{{{Key: "esc", Label: "back"}}}
	if len(m.siblings) > 1 {
		groups = append(groups, []tui.KeyHint{{Key: "j/k", Label: "next/prev"}})
	}
	verbGroup := []tui.KeyHint{}
	if m.openLogs != nil {
		verbGroup = append(verbGroup, verbs.Logs.Hint())
	}
	if m.found && len(m.pod.ContainerInfos) > 0 {
		verbGroup = append(verbGroup, verbs.Exec.Hint())
	}
	if m.found && m.openForward != nil {
		verbGroup = append(verbGroup, verbs.Forward.Hint())
	}
	// alt+o/i share one hint slot (owner/ingress jump, mvp-tasks.md poddetail
	// follow-up) — spelling them out as two separate entries doesn't fit
	// this band's width budget alongside everything else already curated
	// here (5a's kitchen-sink fixture already renders at ~zero slack).
	if m.pod.Owner != "" {
		verbGroup = append(verbGroup, tui.KeyHint{Key: "alt+o/i", Label: "related"})
	}
	if len(m.pod.ContainerInfos) > 1 {
		verbGroup = append(verbGroup, tui.KeyHint{Key: "tab", Label: "cycle"})
	}
	if len(verbGroup) > 0 {
		groups = append(groups, verbGroup)
	}
	if m.mutator != nil && !verbs.Delete.HiddenWhileOffline(m.conn.Offline()) {
		groups = append(groups, []tui.KeyHint{verbs.Delete.Hint()})
	}

	// 4a's offline treatment (docs/design README.md §52, §301): mutating
	// verbs disappear from the keybar the same way browse's own list does,
	// not just at the actions.Controller gate.
	pill, pillText, rightNote := tui.ModeBrowse, "POD", m.execFeedback
	if m.conn.Offline() {
		pill, pillText, rightNote = tui.ModeOffline, "OFFLINE", "mutating actions disabled"
	}
	return tui.Keybar{
		Pill:       pill,
		PillText:   pillText,
		Groups:     groups,
		RightNote:  rightNote,
		RightHints: append(tui.UpdateRightHints(m.session), verbs.Help.Hint()),
	}
}

// CapturingInput reports whether a confirm card is open, or the pod-gone
// state is showing (every key becomes "go back") — either way the root
// shell should let poddetail's own key handling see every keystroke instead
// of treating them as global g/n/c/? shortcuts.
func (m Model) CapturingInput() bool {
	return m.actions.Active() || m.gone || m.pendingEdit != nil
}
