package podlogs

import (
	"fmt"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/components"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
	case streamStartedMsg:
		m.stream = msg.state
		m.feedback = ""
	case components.SpinnerTickMsg:
		if m.taskState() != tui.TaskStateLoading {
			return m, nil
		}
		m.spinner = m.spinner.Advance()
		return m, components.SpinnerTick()
	case logLineMsg:
		if msg.streamID != 0 && msg.streamID != m.streamID {
			return m, m.nextStreamCmd()
		}
		m.appendEntry(msg.entry)
		return m, m.nextStreamCmd()
	case streamEmptyMsg:
		if msg.streamID != 0 && msg.streamID != m.streamID {
			return m, m.nextStreamCmd()
		}
		m.stream = StreamEmpty
		m.feedback = fmt.Sprintf("No logs found for %s.", m.scope())
	case streamErrorMsg:
		if msg.streamID != 0 && msg.streamID != m.streamID {
			return m, m.nextStreamCmd()
		}
		m.stream = StreamError
		m.lastError = msg.err.Error()
		m.permDenied = kube.IsPermissionError(msg.err)
		m.feedback = m.lastError
		if m.permDenied {
			m.feedback = "Permission denied reading logs for " + m.scope() + ": " + msg.err.Error()
		}
	case streamClosedMsg:
		if msg.streamID != 0 && msg.streamID != m.streamID {
			return m, nil
		}
		m.stream = StreamClosed
		m.feedback = "Log stream closed."
	case streamWaitMsg:
		return m, nil
	case rateTickMsg:
		if msg.gen != m.rateGen {
			return m, nil // stale generation from a since-superseded restartStream — drop, don't reschedule
		}
		m.lastRate = m.linesSinceTick
		m.linesSinceTick = 0
		return m, rateTickCmd(m.rateGen)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.filterActive {
		return m.updateFilterKey(msg)
	}

	switch msg.String() {
	case "ctrl+q", "ctrl+c":
		m.cancelStream()
		m.stream = StreamClosed
		m.feedback = "Log stream closed."
		return m, tea.Quit
	case "esc":
		m.cancelStream()
		m.stream = StreamClosed
		m.feedback = "Log stream closed."
		return m, func() tea.Msg { return tui.BackMsg{} }
	case "j", "down":
		m.moveVertical(1)
	case "k", "up":
		m.moveVertical(-1)
	case "h", "left":
		m.moveHorizontal(-1)
	case "l", "right":
		m.moveHorizontal(1)
	case "home":
		m.view.VerticalOffset = 0
		m.view.AutoScroll = false
	case "G", "end":
		m.view.AutoScroll = false
		m.view.VerticalOffset = m.maxVerticalOffset()
	case "pgdown", "ctrl+f":
		m.moveVertical(m.entryViewportHeight())
	case "pgup", "ctrl+b":
		m.moveVertical(-m.entryViewportHeight())
	case "ctrl+d":
		m.moveVertical(max(1, m.entryViewportHeight()/2))
	case "ctrl+u":
		m.moveVertical(-max(1, m.entryViewportHeight()/2))
	case "space":
		m.view.AutoScroll = !m.view.AutoScroll
		if m.view.AutoScroll {
			m.view.VerticalOffset = m.maxVerticalOffset()
		}
	case "W":
		m.view.Wrap = !m.view.Wrap
		if m.view.Wrap {
			m.view.HorizontalOffset = 0
		}
	case "t":
		m.view.Timestamps = !m.view.Timestamps
	case "tab":
		m.cycleContainer()
		return m, m.restartStream(StreamReconnecting)
	case "s":
		m.cycleSince()
		return m, m.restartStream(StreamReconnecting)
	case "w":
		m.jumpSeverity(SeverityWarn)
	case "e":
		m.jumpSeverity(SeverityErr)
	case "/":
		if m.stream != StreamLoading {
			m.filterActive = true
			m.filterInput = textinput.New()
			m.filterInput.SetStyles(tui.TextInputStyles(m.Theme()))
			m.filterInput.Prompt = ""
			m.filterInput.Focus()
		}
	case "ctrl+y":
		return m, tea.SetClipboard(m.visibleViewText())
	}
	m.clampOffsets()
	return m, nil
}

func (m *Model) updateFilterKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filterActive = false
		m.filterInput.SetValue("")
		m.filterInput.Blur()
		m.clampOffsets()
	// alt+j/k/h/l are safe alongside plain j/k/h/l typing into the query —
	// an alt-modified key never carries Text (charm.land/bubbletea/v2's
	// Key.Text doc), so it can't reach the default typing branch below.
	case "up", "alt+k":
		m.moveVertical(-1)
	case "down", "alt+j":
		m.moveVertical(1)
	case "alt+h":
		m.moveHorizontal(-1)
	case "alt+l":
		m.moveHorizontal(1)
	default:
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		m.clampOffsets()
		return m, cmd
	}
	return m, nil
}

func (m *Model) moveVertical(delta int) {
	m.view.AutoScroll = false
	m.view.VerticalOffset += delta
	m.clampOffsets()
}

func (m *Model) moveHorizontal(delta int) {
	if m.view.Wrap {
		return
	}
	m.view.HorizontalOffset += delta
	if m.view.HorizontalOffset < 0 {
		m.view.HorizontalOffset = 0
	}
}

// jumpSeverity moves the viewport to the next entry (after the current
// top-of-view position, wrapping to the start) carrying severity —
// docs/design README.md §5b's "w/e jump to previous/next warning/error".
func (m *Model) jumpSeverity(severity string) {
	entries := m.filteredEntries()
	if len(entries) == 0 {
		return
	}
	start := m.view.VerticalOffset
	for i := 1; i <= len(entries); i++ {
		idx := (start + i) % len(entries)
		if entries[idx].Severity == severity {
			m.view.AutoScroll = false
			m.view.VerticalOffset = idx
			m.clampOffsets()
			return
		}
	}
}

// CapturingInput reports whether the '/' filter input is open, so the root
// shell lets every keystroke reach podlogs' own key handling instead of
// treating letters as global g/n/c/? shortcuts (mirrors browse.
// CapturingInput).
func (m Model) CapturingInput() bool {
	return m.filterActive
}
