package nodedetail

import (
	"fmt"
	"time"

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
	case kube.ConnStateMsg:
		m.conn = kube.ConnState(msg)
		m.now = time.Now()
	case loadedMsg:
		return m.applyLoaded(msg)
	case components.SpinnerTickMsg:
		if m.state != tui.TaskStateLoading {
			return m, nil
		}
		m.spinner = m.spinner.Advance()
		m.now = time.Now()
		return m, components.SpinnerTick()
	case actions.ResultMsg:
		m.actions.HandleResult(msg)
		if msg.Err == nil {
			return m, m.load()
		}
	case execResultMsg:
		if msg.err != nil {
			m.execFeedback = "exec exited: " + msg.err.Error()
		} else {
			m.execFeedback = ""
		}
	case nodeShellResultMsg:
		if msg.err != nil {
			m.execFeedback = "node shell exited: " + msg.err.Error()
		} else {
			m.execFeedback = ""
		}
	case editResultMsg:
		if msg.err != nil {
			m.execFeedback = "edit exited: " + msg.err.Error()
		} else {
			m.execFeedback = ""
		}
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

// execResultMsg carries a directly-run (single-container, no picker pushed)
// kubectl exec's exit outcome — same contract as browse's and poddetail's own
// execResultMsg, duplicated per the repo's package-local-seam convention.
type execResultMsg struct{ err error }

// nodeShellResultMsg carries the node-shell kubectl debug's exit outcome —
// same feedback channel as execResultMsg, kept as its own type so the
// keybar note can say which of the two exited.
type nodeShellResultMsg struct{ err error }

// editResultMsg carries a kubectl edit exit outcome (edit.go) — same
// feedback channel as execResultMsg/nodeShellResultMsg, kept as its own type
// for the same reason.
type editResultMsg struct{ err error }

func (m *Model) applyLoaded(msg loadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.state = tui.TaskStateError
		if kube.IsPermissionError(msg.err) {
			m.state = tui.TaskStatePermissionDenied
		}
		m.feedback = msg.err.Error()
		return m, nil
	}
	m.node = msg.node
	m.allocated = msg.allocated
	m.allocatable = msg.allocatable
	m.allPods = msg.pods
	m.recomputeFiltered()
	m.state = tui.TaskStateReady
	m.feedback = ""
	return m, nil
}

// recomputeFiltered reapplies filterQuery to allPods (called after a reload
// or a filter-query edit), trying to keep the same pod selected by name —
// mirrors browse's recomputeVisible/restoreSelection, flattened since this
// list has no grouping.
func (m *Model) recomputeFiltered() {
	name := m.selectedPodName()
	m.pods = applyPodFilter(m.allPods, m.filterQuery)
	m.restoreSelection(name)
}

func (m Model) selectedPodName() string {
	row, ok := m.selectedPod()
	if !ok {
		return ""
	}
	return row.pod.Name
}

// restoreSelection re-finds name among m.pods (just refiltered), falling
// back to a clamped index when it's gone (filtered out or deleted).
func (m *Model) restoreSelection(name string) {
	if name != "" {
		for i, row := range m.pods {
			if row.pod.Name == name {
				m.selected = i
				m.clampOffset()
				return
			}
		}
	}
	m.selected = clamp(m.selected, 0, len(m.pods)-1)
	m.clampOffset()
}

func (m *Model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.actions.Active() {
		return m.updateConfirmKey(msg)
	}
	if m.pendingEdit != nil {
		return m.updateEditConfirmKey(msg)
	}
	if m.filterActive {
		return m.updateFilterKey(msg)
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
	case "/":
		if m.state == tui.TaskStateReady {
			m.filterActive = true
		}
	case "enter":
		if task, cmd, ok := m.openSelectedPod(); ok {
			return task, cmd
		}
	case "l":
		if task, cmd, ok := m.openSelectedLogs(); ok {
			return task, cmd
		}
	case "x":
		if task, cmd, ok := m.openSelectedExec(); ok {
			if task != nil {
				return task, cmd
			}
			return m, cmd
		}
	case "s":
		return m, m.nodeShellCmd()
	case "C":
		return m, m.beginCordon()
	case "D":
		return m, m.beginDrain()
	case "E":
		if cmd, ok := m.beginEdit(); ok {
			return m, cmd
		}
	case "y":
		if task, cmd, ok := m.openSelectedYAML(); ok {
			return task, cmd
		}
	case "e":
		if task, cmd, ok := m.openSelectedEvents(); ok {
			return task, cmd
		}
	case "t":
		if task, cmd, ok := m.openSelectedTimeline(); ok {
			return task, cmd
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

// updateFilterKey drives the pods list's live "/" filter — same shape as
// browse's updateFilterKey, flattened for this package's ungrouped list:
// esc clears and exits, backspace edits the query, up/down still move the
// selection (plain j/k stay typeable into the query, same as browse).
func (m *Model) updateFilterKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filterActive = false
		m.filterQuery = ""
		m.recomputeFiltered()
	case "backspace":
		if len(m.filterQuery) > 0 {
			m.filterQuery = m.filterQuery[:len(m.filterQuery)-1]
			m.recomputeFiltered()
		}
	case "up":
		m.moveSelection(-1)
	case "down":
		m.moveSelection(1)
	default:
		if msg.Text != "" {
			m.filterQuery += msg.Text
			m.recomputeFiltered()
		}
	}
	return m, nil
}

func (m *Model) moveSelection(delta int) {
	if len(m.pods) == 0 {
		m.selected, m.offset = 0, 0
		return
	}
	m.selected = clamp(m.selected+delta, 0, len(m.pods)-1)
	m.clampOffset()
}

// clampOffset keeps the selected pod row within the table's rendered
// viewport — mirrors browse's own clampOffset/tableDataRows pattern
// (selection.go), against the bottom pane's actual visible row count.
func (m *Model) clampOffset() {
	rows := m.tableDataRows()
	if m.selected < m.offset {
		m.offset = m.selected
	}
	if m.selected >= m.offset+rows {
		m.offset = m.selected - rows + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
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

// openSelectedPod pushes tasks/poddetail (5a) for the selected pod row.
func (m Model) openSelectedPod() (tea.Model, tea.Cmd, bool) {
	if m.openPod == nil {
		return nil, nil, false
	}
	row, ok := m.selectedPod()
	if !ok {
		return nil, nil, false
	}
	task, cmd := m.openPod(row.pod, m.width, m.height)
	return task, cmd, task != nil
}

// openSelectedLogs pushes the log-stream screen for the selected pod row —
// same direct path browse's 'l' gives its Pods list, so tailing a node's
// pod doesn't require opening poddetail first. ok is false when logs aren't
// wired or nothing's selected, so 'l' stays a no-op.
func (m Model) openSelectedLogs() (tea.Model, tea.Cmd, bool) {
	if m.openLogs == nil {
		return nil, nil, false
	}
	row, ok := m.selectedPod()
	if !ok {
		return nil, nil, false
	}
	task, cmd := m.openLogs(row.pod, m.width, m.height)
	return task, cmd, task != nil
}

// openSelectedExec resolves 'x' for the selected pod row (docs/design
// README.md §10a), mirroring browse's openSelectedExec: a single container
// execs immediately via kube.ExecSpec — task is nil and cmd is the
// tea.ExecProcess Cmd, so nodedetail stays the active task and handles its
// own execResultMsg — while more than one container pushes tasks/execpicker.
// ok is false when nothing applies (no row selected or no containers known),
// so 'x' stays a no-op rather than the caller misreading a nil task as
// failure.
func (m Model) openSelectedExec() (tea.Model, tea.Cmd, bool) {
	row, ok := m.selectedPod()
	if !ok || len(row.pod.ContainerInfos) == 0 {
		return nil, nil, false
	}
	if len(row.pod.ContainerInfos) == 1 {
		return nil, execCmd(row.pod.Namespace, row.pod.Name, row.pod.ContainerInfos[0].Name), true
	}
	if m.openExec == nil {
		return nil, nil, false
	}
	task, cmd := m.openExec(row.pod.Namespace, row.pod.Name, row.pod.ContainerInfos, m.width, m.height)
	return task, cmd, task != nil
}

// execCmd suspends the program and hands the tty to kubectl for container
// (tea.ExecProcess over kube.ExecSpec) — shared shape with browse's and
// tasks/execpicker's own execCmd/execSelected, duplicated per the repo's
// package-local-seam convention.
func execCmd(namespace, pod, container string) tea.Cmd {
	spec := kube.ExecSpec(namespace, pod, container, "")
	return tea.ExecProcess(spec, func(err error) tea.Msg {
		return execResultMsg{err: err}
	})
}

// nodeShellCmd suspends the program and hands the tty to kubectl debug for
// the node itself ('s', kube.NodeShellSpec over tea.ExecProcess) — the same
// tty-handoff path the pod rows' exec takes, duplicated per the repo's
// package-local-seam convention. Nil (no-op) until the node has loaded.
func (m Model) nodeShellCmd() tea.Cmd {
	if m.node == nil {
		return nil
	}
	image := ""
	if m.session != nil {
		image = m.session.Config.NodeShellImage
	}
	spec := kube.NodeShellSpec(m.nodeName, image)
	return tea.ExecProcess(spec, func(err error) tea.Msg {
		return nodeShellResultMsg{err: err}
	})
}

// openSelectedYAML pushes 8a for the node itself.
func (m Model) openSelectedYAML() (tea.Model, tea.Cmd, bool) {
	if m.openYAML == nil || m.node == nil {
		return nil, nil, false
	}
	task, cmd := m.openYAML(kube.KindNode, "", m.nodeName, m.width, m.height)
	return task, cmd, task != nil
}

// openSelectedEvents pushes 9b object-scoped for the node itself
// (docs/design README.md §11b: "e node events").
func (m Model) openSelectedEvents() (tea.Model, tea.Cmd, bool) {
	if m.openEvents == nil || m.node == nil {
		return nil, nil, false
	}
	task, cmd := m.openEvents(kube.KindNode, "", m.nodeName, m.width, m.height)
	return task, cmd, task != nil
}

// openSelectedTimeline pushes 16b object-scoped for the node itself
// (docs/design README.md §16b) — same shape as openSelectedEvents.
func (m Model) openSelectedTimeline() (tea.Model, tea.Cmd, bool) {
	if m.openTimeline == nil || m.node == nil {
		return nil, nil, false
	}
	task, cmd := m.openTimeline(kube.KindNode, "", m.nodeName, m.width, m.height)
	return task, cmd, task != nil
}

// beginCordon toggles the node's schedulable state — TierNone (verbs.Cordon),
// so it executes immediately with no confirmation, mirroring browse's 11a.
func (m *Model) beginCordon() tea.Cmd {
	if m.node == nil {
		return nil
	}
	verb, label := "cordon", fmt.Sprintf("Cordon %s?", m.nodeName)
	if m.node.Spec.Unschedulable {
		verb, label = "uncordon", fmt.Sprintf("Uncordon %s?", m.nodeName)
	}
	return m.actions.Begin(verbs.Cordon.Tier, tui.TaskAction{
		ID:    "node-" + verb + "-" + m.nodeName,
		Label: label,
		Scope: tui.TaskScope{ResourceKind: string(kube.KindNode), ResourceName: m.nodeName, Verb: verb, IsMutating: true},
	})
}

// beginDrain confirms draining the node — TierModal (verbs.Drain) — showing
// how many of its pods (the cache backing the bottom pane) will be evicted.
// Counts allPods, not the (possibly filtered) pods list, since drain evicts
// every pod on the node regardless of what the filter currently hides.
func (m *Model) beginDrain() tea.Cmd {
	if m.node == nil {
		return nil
	}
	return m.actions.Begin(verbs.Drain.Tier, tui.TaskAction{
		ID:    "node-drain-" + m.nodeName,
		Label: fmt.Sprintf("Drain %s? %d pods will be evicted.", m.nodeName, len(m.allPods)),
		Scope: tui.TaskScope{ResourceKind: string(kube.KindNode), ResourceName: m.nodeName, Verb: "drain", IsMutating: true},
	})
}
