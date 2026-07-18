package execpicker

import (
	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// execResultMsg carries kube.ExecSpec's exit outcome. nil means a clean
// exit — the picker pops back to the pod that opened it (docs/design
// README.md §10a: "exit returns to the same pod"). Non-nil (a non-zero
// exit, or kubectl failing to start) stays on the picker with a feedback
// line so the user can retry a different container or back out.
type execResultMsg struct{ err error }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
	case kube.ConnStateMsg:
		m.conn = kube.ConnState(msg)
	case execResultMsg:
		if msg.err != nil {
			m.feedback = "exec exited: " + msg.err.Error()
			return m, nil
		}
		return m, func() tea.Msg { return tui.BackMsg{} }
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m *Model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		return m, func() tea.Msg { return tui.BackMsg{} }
	case "up", "k":
		m.moveSelection(-1)
	case "down", "j":
		m.moveSelection(1)
	case "enter":
		return m, m.execSelected()
	}
	return m, nil
}

func (m *Model) moveSelection(delta int) {
	if len(m.containers) == 0 {
		return
	}
	m.selected = clamp(m.selected+delta, 0, len(m.containers)-1)
	m.feedback = ""
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// execSelected suspends the program and hands the tty to kubectl for the
// highlighted container (tea.ExecProcess); nil when nothing's selected.
func (m Model) execSelected() tea.Cmd {
	if m.selected < 0 || m.selected >= len(m.containers) {
		return nil
	}
	container := m.containers[m.selected].Name
	cmd := kube.ExecSpec(m.namespace, m.podName, container, "")
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return execResultMsg{err: err}
	})
}
