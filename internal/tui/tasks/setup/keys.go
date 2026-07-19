package setup

import tea "charm.land/bubbletea/v2"

func (m *Model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.editing {
		return m.updateEditKey(msg)
	}
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "r":
		return m.doRetry("")
	case "up":
		if m.state == Unreachable {
			m.moveSwitchSelection(-1)
		}
	case "down":
		if m.state == Unreachable {
			m.moveSwitchSelection(1)
		}
	case "k":
		switch m.state {
		case NoConfig:
			m.startEdit()
		case Unreachable:
			// 4c's SWITCH CONTEXT list: j/k ≡ ↑↓ (CLAUDE.md convention),
			// distinct from NoConfig's own 'k' = "enter kubeconfig path".
			m.moveSwitchSelection(-1)
		}
	case "j":
		if m.state == Unreachable {
			m.moveSwitchSelection(1)
		}
	case "enter":
		if m.state == Unreachable {
			if cmd, ok := m.connectToSelected(); ok {
				return m, cmd
			}
		}
	case "e":
		if m.state == Unreachable {
			m.startEdit()
		}
	}
	return m, nil
}

func (m *Model) startEdit() {
	m.editing = true
	m.pathInput = m.kubeconfigPath
	m.retryErr = nil
}

// doRetry is 'r”s plain retry (path=="") and the edit input's submit
// (path==typed value): Unreachable's plain retry re-probes the existing
// cluster in place (RetryNow, no rebuild); every other case — NoConfig's
// 'r'/'k', or Unreachable with an edited path — rebuilds via Reconnect.
func (m *Model) doRetry(path string) (tea.Model, tea.Cmd) {
	switch {
	case path == "" && m.state == Unreachable && m.retryNow != nil:
		m.retryNow()
		return m, nil
	case m.reconnect != nil:
		m.retrying = true
		m.retryErr = nil
		return m, m.reconnect(path)
	}
	return m, nil
}

func (m *Model) updateEditKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.editing = false
	case "enter":
		m.editing = false
		return m.doRetry(m.pathInput)
	case "backspace":
		if len(m.pathInput) > 0 {
			m.pathInput = m.pathInput[:len(m.pathInput)-1]
		}
	default:
		if msg.Text != "" {
			m.pathInput += msg.Text
		}
	}
	return m, nil
}
