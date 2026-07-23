package overview

import (
	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/components"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
	case kube.ResourceChangedMsg:
		if isOverviewKind(msg.Kind) && m.lister != nil {
			return m, m.load()
		}
	case kube.ConnStateMsg:
		m.conn = kube.ConnState(msg)
	case loadedMsg:
		return m.applyLoaded(msg)
	case components.SpinnerTickMsg:
		if m.state != tui.TaskStateLoading {
			return m, nil
		}
		m.spinner = m.spinner.Advance()
		return m, components.SpinnerTick()
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

// isOverviewKind reports whether kind's change should trigger a reload —
// every kind 19a's panels read from (Node/Pod/Namespace/ReplicaSet), not
// every possible kind change.
func isOverviewKind(kind kube.ResourceKind) bool {
	switch kind {
	case kube.KindNode, kube.KindPod, kube.KindNamespace, kube.KindReplicaSet:
		return true
	default:
		return false
	}
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
	d := msg.data
	m.version = d.version
	m.nodeCount = d.nodeCount
	m.podCount = d.podCount
	m.nsCount = d.nsCount
	m.metricsAvailable = d.metricsAvailable
	m.capCPUUsed, m.capCPUTotal = d.capCPUUsed, d.capCPUTotal
	m.capMemUsed, m.capMemTotal = d.capMemUsed, d.capMemTotal
	m.capPodsUsed, m.capPodsTotal = d.capPodsUsed, d.capPodsTotal
	m.nodeTrouble, m.nodeHealthy = d.nodeTrouble, d.nodeHealthy
	m.podTrouble, m.podHealthy = d.podTrouble, d.podHealthy
	m.changes = d.changes
	m.nodesSel = clamp(m.nodesSel, 0, cappedMax(len(m.nodeTrouble)))
	m.troubleSel = clamp(m.troubleSel, 0, cappedMax(len(m.podTrouble)))
	m.changesSel = clamp(m.changesSel, 0, cappedMax(len(m.changes)))
	m.state = tui.TaskStateReady
	m.feedback = ""
	return m, nil
}

func (m *Model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.state != tui.TaskStateReady {
		switch msg.String() {
		case "ctrl+q", "ctrl+c":
			return m, tea.Quit
		case "esc", "backspace":
			return m, func() tea.Msg { return tui.BackMsg{} }
		}
		return m, nil
	}

	switch msg.String() {
	case "ctrl+q", "ctrl+c":
		return m, tea.Quit
	case "esc", "backspace":
		return m, func() tea.Msg { return tui.BackMsg{} }
	case "tab":
		m.nextPanel()
	case "up", "k":
		m.moveSelection(-1)
	case "down", "j":
		m.moveSelection(1)
	case "enter":
		if task, cmd, ok := m.openSelected(); ok {
			return task, cmd
		}
	case "t":
		if m.openTimeline != nil {
			task, cmd := m.openTimeline("", m.width, m.height)
			if task != nil {
				return task, cmd
			}
		}
	case "e":
		if m.openEvents != nil {
			task, cmd := m.openEvents("", m.width, m.height)
			if task != nil {
				return task, cmd
			}
		}
	}
	return m, nil
}

// nextPanel is 19a's `↹` — cycles focus NODES → TROUBLE → CHANGES → NODES,
// skipping an empty panel so the cursor never lands somewhere with nothing
// to select.
func (m *Model) nextPanel() {
	order := []panel{panelNodes, panelTrouble, panelChanges}
	for range order {
		m.focus = order[(int(m.focus)+1)%len(order)]
		if m.panelHasRows(m.focus) {
			return
		}
	}
}

func (m Model) panelHasRows(p panel) bool {
	switch p {
	case panelNodes:
		return len(m.nodeTrouble) > 0
	case panelTrouble:
		return len(m.podTrouble) > 0
	case panelChanges:
		return len(m.changes) > 0
	default:
		return false
	}
}

func (m *Model) moveSelection(delta int) {
	switch m.focus {
	case panelNodes:
		m.nodesSel = clamp(m.nodesSel+delta, 0, cappedMax(len(m.nodeTrouble)))
	case panelTrouble:
		m.troubleSel = clamp(m.troubleSel+delta, 0, cappedMax(len(m.podTrouble)))
	case panelChanges:
		m.changesSel = clamp(m.changesSel+delta, 0, cappedMax(len(m.changes)))
	}
}

// cappedMax is the highest selectable index in a panel whose display caps
// at maxPanelRows (view.go): the cursor must never move past what's
// actually rendered, so selection clamps against the same cap the fold-line
// ("+N more") accounts for.
func cappedMax(n int) int {
	return max(min(n, maxPanelRows)-1, 0)
}

// openSelected dispatches ↵ for whichever panel is focused: NODES pushes
// 11b directly; TROUBLE/CHANGES pop back to whatever pushed this screen
// (tasks/browse) and jump to the object there — the same tea.Sequence
// (BackMsg, GotoResourceMsg) pair tasks/timeline's own openSelectedObject
// already establishes for the identical "pushed on top of browse" shape.
func (m Model) openSelected() (tea.Model, tea.Cmd, bool) {
	switch m.focus {
	case panelNodes:
		row, ok := m.selectedNode()
		if !ok || m.openNodeDetail == nil {
			return nil, nil, false
		}
		task, cmd := m.openNodeDetail(row.Name, m.width, m.height)
		return task, cmd, task != nil
	case panelTrouble:
		row, ok := m.selectedTrouble()
		if !ok {
			return nil, nil, false
		}
		return nil, m.jumpTo(kube.KindPod, row.Namespace, row.Name), true
	case panelChanges:
		entry, ok := m.selectedChange()
		if !ok {
			return nil, nil, false
		}
		kind, name := splitObject(entry.Object)
		if kind == "" || name == "" {
			return nil, nil, false
		}
		return nil, m.jumpTo(kind, entry.Namespace, name), true
	default:
		return nil, nil, false
	}
}

func (m Model) jumpTo(kind kube.ResourceKind, namespace, name string) tea.Cmd {
	return tea.Sequence(
		func() tea.Msg { return tui.BackMsg{} },
		func() tea.Msg { return tui.GotoResourceMsg{Kind: kind, Namespace: namespace, Name: name} },
	)
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
