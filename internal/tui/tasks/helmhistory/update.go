package helmhistory

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
	"github.com/kute-dev/kute/internal/tui/components"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
	case kube.ResourceChangedMsg:
		if msg.Kind == kube.KindSecret || msg.Kind == kube.KindHelmRelease {
			m.reloadEpoch++
			return m, m.load()
		}
	case kube.ConnStateMsg:
		m.conn = kube.ConnState(msg)
		m.actions.SetOffline(m.conn.Offline())
	case loadedMsg:
		return m.applyLoaded(msg)
	case components.SpinnerTickMsg:
		if m.state != tui.TaskStateLoading {
			return m, nil
		}
		m.spinner = m.spinner.Advance()
		return m, components.SpinnerTick()
	case actions.ResultMsg:
		m.actions.HandleResult(msg)
		if msg.Err == nil {
			m.reloadEpoch++
			return m, m.load()
		}
		m.feedback = "rollback failed: " + msg.Err.Error()
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m *Model) applyLoaded(msg loadedMsg) (tea.Model, tea.Cmd) {
	if msg.epoch != m.reloadEpoch {
		return m, nil
	}
	if msg.err != nil {
		m.state = tui.TaskStateError
		if kube.IsPermissionError(msg.err) {
			m.state = tui.TaskStatePermissionDenied
		}
		m.feedback = msg.err.Error()
		return m, nil
	}
	m.revisions = msg.revisions
	m.state = tui.TaskStateReady
	if len(m.revisions) == 0 {
		m.state = tui.TaskStateEmpty
	}
	m.feedback = ""
	if m.selected >= len(m.revisions) {
		m.selected = max(len(m.revisions)-1, 0)
	}
	return m, nil
}

func (m *Model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.actions.Active() {
		return m.updateConfirmKey(msg)
	}
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc", "backspace":
		return m, func() tea.Msg { return tui.BackMsg{} }
	case "up", "k":
		m.moveSelection(-1)
	case "down", "j":
		m.moveSelection(1)
	case "R":
		if m.mutator != nil && m.state == tui.TaskStateReady {
			if rev, ok := m.selectedRevision(); ok {
				return m, m.beginRollback(rev)
			}
		}
	}
	return m, nil
}

func (m *Model) updateConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		return m, m.actions.Confirm()
	case "n", "esc":
		m.actions.Cancel()
	}
	return m, nil
}

func (m *Model) moveSelection(delta int) {
	if len(m.revisions) == 0 {
		m.selected = 0
		return
	}
	m.selected = clamp(m.selected+delta, 0, len(m.revisions)-1)
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// beginRollback confirms rolling back to rev — the same tier/friction
// tasks/browse's own beginRollback uses ("Rollback inherits 8b friction"),
// duplicated here per the repo's package-local-seam convention since this
// screen has its own actions.Controller.
func (m *Model) beginRollback(rev kube.HelmRelease) tea.Cmd {
	tier := verbs.TierFor(verbs.Rollback, m.isProd())
	return m.actions.Begin(tier, tui.TaskAction{
		ID:    fmt.Sprintf("rollback-%s/%s-r%d", m.namespace, m.name, rev.Revision),
		Label: fmt.Sprintf("Rollback %s to revision %d?", m.name, rev.Revision),
		Scope: tui.TaskScope{
			ResourceKind: string(kube.KindHelmRelease),
			ResourceName: m.name,
			Namespace:    m.namespace,
			Verb:         "rollback",
			IsMutating:   true,
			Revision:     rev.Revision,
		},
	})
}
