package cronjobschedule

import (
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// Keybar composes the bottom band from verb references only, per the
// registry invariant — 36d's pill is SCHEDULE. 'y' copy and 'u' undo render
// Disabled while the timezone buffer has focus (updateKey's own comment has
// the reasoning: both letters are common inside real IANA zone names, so
// they type into that buffer instead of firing there), and 'tab'/'u' are
// omitted outright rather than disabled when they don't apply at all yet
// (tzEditable()/m.previous == nil respectively) — a key nothing on this
// object can ever make available isn't worth a greyed-out hint.
func (m Model) Keybar() tui.Keybar {
	if m.state != tui.TaskStateReady {
		return tui.Keybar{
			Pill:       tui.ModeBrowse,
			PillText:   "SCHEDULE",
			RightHints: append(tui.UpdateRightHints(m.session), verbs.Help.Hint()),
		}
	}

	apply := verbs.Open
	apply.Label = "apply"
	hints := []tui.KeyHint{apply.Hint()}

	copyHint := verbs.CronJobCopyCommand.Hint()
	copyHint.Disabled = m.tzFocused
	hints = append(hints, copyHint)

	if m.tzEditable() {
		hints = append(hints, verbs.CronJobFocusTimezone.Hint())
	}
	if m.previous != nil {
		undoHint := verbs.CronJobScheduleUndo.Hint()
		undoHint.Disabled = m.tzFocused || m.conn.Offline()
		hints = append(hints, undoHint)
	}
	if m.mutator != nil && !verbs.CronJobScheduleFullEdit.HiddenWhileOffline(m.conn.Offline()) {
		hints = append(hints, verbs.CronJobScheduleFullEdit.Hint())
	}
	hints = append(hints, tui.KeyHint{Key: "esc", Label: "back"})

	pill, pillText := tui.ModeBrowse, "SCHEDULE"
	rightNote, rightWarn := m.message, ""
	switch {
	case m.conn.Offline():
		pill, pillText = tui.ModeOffline, "OFFLINE"
		rightNote = "mutating actions disabled"
	case m.parseErr != nil:
		rightWarn = "invalid schedule: " + m.parseErr.Error()
	case m.tzErr != nil:
		rightWarn = "invalid time zone: " + m.tzErr.Error()
	case m.lastError != "":
		rightWarn = m.lastError
	}

	return tui.Keybar{
		Pill:          pill,
		PillText:      pillText,
		Groups:        [][]tui.KeyHint{hints},
		RightNote:     rightNote,
		RightWarnNote: rightWarn,
		RightHints:    append(tui.UpdateRightHints(m.session), verbs.Help.Hint()),
	}
}
