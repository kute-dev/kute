package jobattempts

import (
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// Keybar composes the bottom band from verb references only, per the
// registry invariant — mirrors cronjobdetail's own; pill is ATTEMPTS.
func (m Model) Keybar() tui.Keybar {
	if m.pendingRerun != nil {
		return m.rerunKeybar()
	}
	if m.actions.Active() {
		if m.actions.Tier() == actions.TierModal {
			return tui.Keybar{Pill: tui.ModeConfirm, PillText: "CONFIRM"}
		}
		note := m.actions.Prompt()
		if pending := m.actions.Pending(); pending != nil {
			switch pending.Scope.Verb {
			case "job-retry":
				note = jobRetryWillRunLine(pending.Scope)
			case "job-replace":
				note = jobReplaceWillRunLine(pending.Scope)
			}
		}
		return tui.Keybar{
			Pill:      tui.ModeConfirm,
			PillText:  "CONFIRM",
			Groups:    [][]tui.KeyHint{{{Key: "y", Label: "confirm"}, {Key: "esc", Label: "cancel"}}},
			RightNote: note,
		}
	}
	if !m.found {
		return tui.Keybar{
			Pill:      tui.ModeBrowse,
			PillText:  "ATTEMPTS",
			RightNote: "job deleted · press any key to go back",
		}
	}
	if m.diffMode {
		return tui.Keybar{
			Pill:      tui.ModeBrowse,
			PillText:  "ATTEMPTS",
			Groups:    [][]tui.KeyHint{{{Key: "d/esc", Label: "back"}}},
			RightNote: "comparing selected attempt vs attempt 1",
		}
	}

	groups := [][]tui.KeyHint{}
	attemptGroup := []tui.KeyHint{{Key: "l", Label: "logs"}}
	if len(m.summary.Attempts) >= 2 {
		attemptGroup = append(attemptGroup, tui.KeyHint{Key: "d", Label: "diff vs attempt 1"})
	}
	groups = append(groups, attemptGroup)
	groups = append(groups, []tui.KeyHint{{Key: "e", Label: "events"}})
	if m.mutator != nil && !m.conn.Offline() {
		groups = append(groups, []tui.KeyHint{{Key: verbs.JobRetry.Key, Label: verbs.JobRetry.Label}})
	}
	if m.summary.Indexed {
		failedLabel := "failed only"
		if m.failedOnly {
			failedLabel = "all indexes"
		}
		groups = append(groups, []tui.KeyHint{{Key: "f", Label: failedLabel}})
	}
	if len(m.siblings) > 1 {
		groups = append(groups, []tui.KeyHint{{Key: "[/]", Label: "sibling job"}})
	}

	pill, pillText, rightNote := tui.ModeBrowse, "ATTEMPTS", m.execFeedback
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
