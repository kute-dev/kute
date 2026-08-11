package cronjobdetail

import (
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// Keybar composes the bottom band from verb references only, per the
// registry invariant — 36e's pill is DETAIL (docs/design README.md §36e).
func (m Model) Keybar() tui.Keybar {
	if m.pendingRun != nil {
		return m.runKeybar()
	}
	if m.pendingResume != nil {
		return m.resumeKeybar()
	}
	if m.actions.Active() {
		if m.actions.Tier() == actions.TierModal {
			return tui.Keybar{Pill: tui.ModeConfirm, PillText: "CONFIRM"}
		}
		note := m.actions.Prompt()
		if pending := m.actions.Pending(); pending != nil && pending.Scope.Verb == "cronjob-suspend" {
			// Only reached outside PROD — verbs.TierForCronJobSuspend
			// escalates to TierModal in PROD instead (this screen's own
			// suspendConfirmModal). cronjob-resume never appears here, fixed
			// TierNone (staged, reversible, immediate) the same way
			// cronjob-run-now is.
			note = cronJobSuspendWillRunLine(pending.Scope)
			if m.summary.Object != nil {
				note += "   " + cronJobSuspendDangerNote(m.summary, m.now)
			}
		}
		return tui.Keybar{
			Pill:      tui.ModeConfirm,
			PillText:  "CONFIRM",
			Groups:    [][]tui.KeyHint{{{Key: "y", Label: "confirm"}, {Key: "n", Label: "cancel"}}},
			RightNote: note,
		}
	}
	if !m.found {
		return tui.Keybar{
			Pill:      tui.ModeBrowse,
			PillText:  "DETAIL",
			RightNote: "cronjob deleted · press any key to go back",
		}
	}

	groups := [][]tui.KeyHint{}
	if len(m.summary.Runs) > 0 {
		jobGroup := []tui.KeyHint{{Key: "↵", Label: "open job"}}
		if m.openLogs != nil {
			jobGroup = append(jobGroup, verbs.Logs.Hint())
		}
		groups = append(groups, jobGroup)
	}
	if m.mutator != nil && !m.conn.Offline() {
		groups = append(groups, m.cronJobKeybarGroup())
	}
	if len(m.siblings) > 1 {
		groups = append(groups, []tui.KeyHint{{Key: "[/]", Label: "sibling cronjob"}})
	}

	pill, pillText, rightNote := tui.ModeBrowse, "DETAIL", m.execFeedback
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
