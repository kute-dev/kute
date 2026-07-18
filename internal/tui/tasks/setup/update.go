package setup

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
)

// RetryFailedMsg reports a failed Reconnect attempt: setup stays on screen
// (the root shell only swaps away on ReplaceRootMsg, which Reconnect's
// success path sends instead) and shows the new error.
type RetryFailedMsg struct{ Err error }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
	case kube.ConnStateMsg:
		m.conn = kube.ConnState(msg)
		m.now = time.Now()
	case RetryFailedMsg:
		m.retrying = false
		m.retryErr = msg.Err
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}
	return m, nil
}
