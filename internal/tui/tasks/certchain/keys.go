package certchain

import (
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// Keybar is §35a's: esc/↵/y/e always available once the chain has loaded,
// plus §35c's 'r' force-renew when a mutator is wired.
func (m Model) Keybar() tui.Keybar {
	if m.actions.Active() {
		if m.actions.Tier() == actions.TierInline {
			note := m.actions.Prompt()
			if pending := m.actions.Pending(); pending != nil && pending.Scope.Verb == "cert-renew" {
				// 35c: the exact "will run: kubectl patch certificate/...
				// --subresource=status ..." line, same idiom as helmhistory's
				// own rollback confirm.
				note = certRenewWillRunLine(pending.Scope)
			}
			return tui.Keybar{
				Pill:      tui.ModeConfirm,
				PillText:  "CONFIRM",
				Groups:    [][]tui.KeyHint{{{Key: "y", Label: "confirm"}, {Key: "n", Label: "cancel"}}},
				RightNote: note,
			}
		}
		return tui.Keybar{Pill: tui.ModeConfirm, PillText: "CONFIRM"}
	}
	if m.state != tui.TaskStateReady {
		return tui.Keybar{Pill: tui.ModeBrowse, PillText: "CHAIN"}
	}
	groups := [][]tui.KeyHint{{{Key: "esc", Label: "back"}}}
	if m.selectableCount() > 0 {
		groups[0] = append(groups[0], verbs.Open.Hint())
	}
	verbGroup := []tui.KeyHint{verbs.YAML.Hint(), verbs.Events.Hint()}
	// 4a's offline treatment (docs/design README.md §52, §301): the renew
	// hint disappears from the keybar the same way browse's own list does,
	// not just at the actions.Controller gate.
	if m.mutator != nil && !verbs.CertRenew.HiddenWhileOffline(m.conn.Offline()) {
		verbGroup = append(verbGroup, verbs.CertRenew.Hint())
	}
	groups = append(groups, verbGroup)
	return tui.Keybar{
		Pill:       tui.ModeBrowse,
		PillText:   "CHAIN",
		Groups:     groups,
		RightNote:  m.feedback,
		RightHints: append(tui.UpdateRightHints(m.session), verbs.Help.Hint()),
	}
}
