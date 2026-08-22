package execpicker

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// execResultMsg carries kube.ExecSpec's exit outcome. nil means a clean
// exit — the picker pops back to the pod that opened it (docs/design
// README.md §10a: "exit returns to the same pod"). Non-nil (a non-zero
// exit, or kubectl failing to start) stays on the picker with a feedback
// line so the user can retry a different container or back out.
type execResultMsg struct{ err error }

// shellsMsg carries one container's shell-detection outcome back from
// detectShellsCmd (docs/design README.md §10a's shells column).
type shellsMsg struct {
	container string
	shells    []string
	err       error
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
	case kube.ConnStateMsg:
		m.conn = kube.ConnState(msg)
	case shellsMsg:
		if m.detected == nil {
			m.detected = map[string]shellResult{}
		}
		m.detected[msg.container] = shellResult{shells: msg.shells, err: msg.err}
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
	case "ctrl+q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		return m, func() tea.Msg { return tui.BackMsg{} }
	case "up", "k":
		m.moveSelection(-1)
	case "down", "j":
		m.moveSelection(1)
	case "ctrl+d":
		m.moveSelection(max(1, m.height/2))
	case "ctrl+u":
		m.moveSelection(-max(1, m.height/2))
	case "enter":
		// Exec is Mutating, so it's refused while offline (docs/design
		// README.md §52). Unlike the list screens this one can't lean on a
		// keybar note the user is already looking at — the connection may
		// well have dropped after the picker was pushed — so it says so in
		// the panel's own feedback line. The same gate covers the debug
		// fork below: both are kubectl subprocess handoffs.
		if verbs.Exec.HiddenWhileOffline(m.conn.Offline()) {
			m.feedback = "Exec is disabled while offline."
			return m, nil
		}
		// §41a's fork: a container this picker already knows has no shell
		// routes to the debug panel instead of a dead exec attempt — "a
		// shell-less row is a route, not a dead ↵" (docs/design
		// v.0.11.0.dc.html §41a).
		if m.selectedShellless() {
			container := m.containers[m.selected].Name
			if m.openDebug == nil {
				m.feedback = fmt.Sprintf("no sh or bash in %s — nothing to exec into", container)
				return m, nil
			}
			task, cmd := m.openDebug(m.namespace, m.podName, m.containers, container, m.width, m.height)
			if task != nil {
				return task, cmd
			}
			return m, cmd
		}
		return m, m.execSelected()
	}
	return m, nil
}

// selectedShellless reports whether the highlighted row's detection is a
// real "no shell" answer (nil err, empty shells — distroless) rather than
// unresolved/unknown (still probing, or the probe itself failed).
func (m Model) selectedShellless() bool {
	if m.selected < 0 || m.selected >= len(m.containers) {
		return false
	}
	res, ok := m.detected[m.containers[m.selected].Name]
	return ok && res.err == nil && len(res.shells) == 0
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
// Passes the detected shell when there is one, so what runs is what the row
// and the "will run" line said would run.
func (m Model) execSelected() tea.Cmd {
	if m.selected < 0 || m.selected >= len(m.containers) {
		return nil
	}
	if m.demo {
		return func() tea.Msg { return execResultMsg{err: kube.ErrDemoUnavailable} }
	}
	container := m.containers[m.selected].Name
	cmd := kube.ExecSpec(m.namespace, m.podName, container, m.preferredShell(m.selected))
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return execResultMsg{err: err}
	})
}
