package browse

import (
	"strings"
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
	case kube.ResourceChangedMsg:
		if msg.Kind == m.kind {
			m.reloadEpoch++
			return m, m.scheduleReload(m.reloadEpoch)
		}
	case kube.ConnStateMsg:
		m.conn = kube.ConnState(msg)
		m.actions.SetOffline(m.conn.Offline())
		m.now = time.Now()
	case tui.GotoKindMsg:
		if msg.Kind == kube.KindWhoCan {
			// KindWhoCan has no resources.Descriptor to list — like Ingress/
			// Gateway routing to tasks/routetable on row-Enter, this is a
			// kind-name carve-out, just triggered by a kind switch instead
			// of a row Enter (there's no row: 22a is "a query, not a
			// browser"). Defaults to "list" against the currently showing
			// kind's own resource name; 'v'/'K'/'n' change any slot once
			// whocan is open.
			if task, cmd, ok := m.openWhoCanFromCurrentKind(); ok {
				return task, cmd
			}
			return m, nil
		}
		if msg.Kind == kube.KindOverview {
			// KindOverview has no resources.Descriptor to list — 19a is "a
			// routing layer, not a dashboard" — the same kind-name carve-out
			// KindWhoCan takes just above, triggered by a kind switch since
			// there's no row to act on.
			if m.openOverview == nil {
				return m, nil
			}
			task, cmd := m.openOverview(m.width, m.height)
			return task, cmd
		}
		if msg.Kind == kube.KindEvent {
			// Unlike KindWhoCan/KindOverview, Events does have a
			// resources.Descriptor and could list as a stock browse table —
			// but 9b (tasks/events) is the curated experience for this kind
			// (dedup, severity coloring, folded normals), so a goto jump to
			// Events (the "e" alias or the ranked-list row) is redirected
			// here the same way the 'e' key already redirects (update.go's
			// openSelectedEvents, same gating).
			if task, cmd, ok := m.openSelectedEvents(); ok {
				return task, cmd
			}
			return m, nil
		}
		cmd := m.switchKind(msg.Kind)
		if msg.Filter != "" {
			// 23b: "↵ on a listener filters to attached routes" — switchKind
			// (via resetAndLoad) already cleared any prior filter, which is
			// right for a bare kind switch but not for this one, which
			// arrives with its own destination filter already chosen.
			m.filterActive = true
			m.setFilter(msg.Filter)
			m.filterInput.Focus()
		}
		return m, cmd
	case tui.GotoResourceMsg:
		return m, m.goToResource(msg)
	case tui.SwitchNamespaceMsg:
		return m, m.switchNamespace(msg.Namespace)
	case tui.SwitchContextMsg:
		if msg.Err == nil {
			return m, m.switchContext(msg)
		}
	case reloadDueMsg:
		if msg.epoch == m.reloadEpoch {
			return m, m.load()
		}
	case rowsLoadedMsg:
		return m.applyRowsLoaded(msg)
	case components.SpinnerTickMsg:
		if m.state != tui.TaskStateLoading {
			return m, nil
		}
		m.spinner = m.spinner.Advance()
		m.now = time.Now()
		return m, components.SpinnerTick()
	case emptyHintsMsg:
		if msg.kind == m.kind && m.state == tui.TaskStateEmpty {
			m.hints = msg.hints
		}
	case metricsTickMsg:
		if msg.epoch == m.metricsEpoch && m.pollsMetrics() {
			return m, tea.Batch(m.loadMetricsCmd(m.metricsEpoch), m.scheduleMetricsTick(m.metricsEpoch))
		}
	case podMetricsLoadedMsg:
		if msg.epoch == m.metricsEpoch && msg.namespace == m.countNamespace() && msg.err == nil {
			m.podMetrics = msg.metrics
			// A live CPU/MEM sort (sort.go's cellLess) reads straight off
			// this map, so a fresh poll can change the rank ordering even
			// though m.rows itself didn't change — re-sort to keep it live.
			if m.sortColumn > 0 {
				m.applySort()
				m.recomputeVisible()
			}
		}
	case nodeMetricsLoadedMsg:
		if msg.epoch == m.metricsEpoch && msg.err == nil {
			m.nodeMetrics = msg.metrics
			if m.sortColumn > 0 {
				m.applySort()
				m.recomputeVisible()
			}
		}
	case bulkDeleteResultMsg:
		m.pendingBulkDelete = nil
		if msg.err != nil {
			m.execFeedback = "bulk delete: " + msg.err.Error()
		} else {
			m.marks = nil
		}
		return m, m.load()
	case actions.ResultMsg:
		m.actions.HandleResult(msg)
		if isMetaActionID(msg.ActionID) && m.pendingMeta != nil {
			// 26a never closes the panel on a commit's own account, success
			// or failure (docs/design README.md §26a: "confirm → execute →
			// refresh → show result → remain on screen") — handleMetaResult
			// either refreshes the grid from the real object + shows an
			// inline "updated ..."/"removed ..." message, or restores the
			// pre-commit interaction state with the server's error, and only
			// esc/back (updateMetaKey's own "esc" case) ever closes it.
			cmd := m.handleMetaResult(msg)
			if msg.Err == nil {
				return m, tea.Batch(cmd, m.load())
			}
			return m, cmd
		}
		if msg.Err == nil {
			return m, m.load()
		}
		if strings.HasPrefix(msg.ActionID, "rollback-") {
			// 18a: "helm missing from PATH explained inline" — routed
			// through execFeedback (the same transient keybar-note channel
			// exec/node-shell/edit already use), since actions.Controller's
			// own error message has no render path in browse today.
			m.execFeedback = "rollback failed: " + msg.Err.Error()
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
	case dryRunSetResourcesMsg:
		return m.handleDryRunSetResources(msg)
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

// execResultMsg carries a directly-run (single-container, no picker pushed)
// kubectl exec's exit outcome — same contract as tasks/execpicker's own
// execResultMsg, duplicated per the repo's package-local-seam convention.
type execResultMsg struct{ err error }

// nodeShellResultMsg carries a node-shell kubectl debug's exit outcome —
// same feedback channel as execResultMsg, kept as its own type so the
// keybar note can say which of the two exited.
type nodeShellResultMsg struct{ err error }

// editResultMsg carries a kubectl edit exit outcome (edit.go) — same
// feedback channel as execResultMsg/nodeShellResultMsg, kept as its own type
// for the same reason.
type editResultMsg struct{ err error }

// applyRowsLoaded handles a fresh List reply: sorts workload kinds
// unhealthy-first (or the user's own 1-9 column choice, if any —
// applySort/sort.go), recomputes the filtered/visible set (preserving
// selection by name where possible), and picks the resulting task state.
func (m *Model) applyRowsLoaded(msg rowsLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.kind != m.kind {
		return m, nil // stale reply for a kind we've since switched away from
	}
	if msg.err != nil {
		m.state = tui.TaskStateError
		if kube.IsPermissionError(msg.err) {
			m.state = tui.TaskStatePermissionDenied
		}
		m.feedback = msg.err.Error()
		return m, nil
	}

	m.rows = msg.rows
	m.applySort()
	m.pods = msg.pods
	m.helmReleases = msg.helmReleases
	m.nodeCount = msg.nodeCount
	m.nodeCapacity = msg.nodeCapacity
	m.podCountByNode = msg.podCountByNode
	m.clusterPodTotal = msg.clusterPodTotal
	m.nodePodHealth = msg.nodePodHealth
	m.fetchedAt = time.Now()
	// Real data has landed — 15a's cached/dimmed loading view (if any) is
	// superseded either way, whether this resolves to Ready or Empty below.
	m.cachedView = false

	if len(m.rows) == 0 {
		if !m.listerSynced() {
			// The informer cache is still filling (just after launch or mid
			// SwitchContext) — this empty result isn't trustworthy yet.
			// Stay in the loading state and retry shortly rather than
			// flashing "no <kind> in <namespace>".
			m.reloadEpoch++
			return m, m.scheduleReload(m.reloadEpoch)
		}
		m.filterActive = false
		m.setFilter("")
		m.visible = nil
		m.expandedGroups = nil
		m.display = nil
		m.selected, m.offset = 0, 0
		m.state = tui.TaskStateEmpty
		m.hints = emptyHints{}
		return m, m.loadEmptyHints()
	}

	m.recomputeVisible()
	m.state = tui.TaskStateReady
	m.feedback = ""
	m.cacheCurrentRows()
	return m, nil
}

func (m *Model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.actions.Active() {
		return m.updateConfirmKey(msg)
	}
	if m.pendingEdit != nil {
		return m.updateEditConfirmKey(msg)
	}
	if m.pendingStopAllForwards {
		return m.updateStopAllForwardsKey(msg)
	}
	if m.pendingScale != nil {
		return m.updateScaleKey(msg)
	}
	if m.pendingSetImage != nil {
		return m.updateSetImageKey(msg)
	}
	if m.pendingSetResources != nil {
		return m.updateSetResourcesKey(msg)
	}
	if m.pendingMeta != nil {
		return m.updateMetaKey(msg)
	}
	if m.pendingBulkDelete != nil {
		return m.updateBulkDeleteKey(msg)
	}
	if m.filterActive && !m.filterListFocused {
		return m.updateFilterKey(msg)
	}
	switch msg.String() {
	case "ctrl+q", "ctrl+c":
		return m, tea.Quit
	case "esc", "backspace":
		// 20a: "esc clears marks before it walks back a level."
		if m.clearMarks() {
			return m, nil
		}
		if m.filterActive {
			// A committed-but-list-focused filter (filterListFocused) still
			// owns esc, same as while typing — "palette/filter → close"
			// applies to both, so esc clears it rather than walking back.
			m.filterActive = false
			m.filterListFocused = false
			m.setFilter("")
			m.filterInput.Blur()
			m.clearOrigin()
			m.recomputeVisible()
			return m, nil
		}
		if m.originName != "" {
			return m, m.backToOrigin()
		}
		return m, func() tea.Msg { return tui.BackMsg{} }
	case "space":
		if m.state == tui.TaskStateReady {
			m.markCursorAndAdvance()
		}
	case "*":
		if m.state == tui.TaskStateReady {
			m.markAllFiltered()
		}
	case "up", "k":
		m.moveSelection(-1)
	case "down", "j":
		m.moveSelection(1)
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		if m.state == tui.TaskStateReady {
			m.handleSortKey(int(msg.String()[0] - '0'))
		}
	case "/":
		if m.state == tui.TaskStateReady {
			m.filterActive = true
			m.filterListFocused = false
			m.filterInput.Focus()
		}
	case "l":
		if task, cmd, ok := m.openSelectedLogs(); ok {
			return task, cmd
		}
	case "enter":
		if task, cmd, ok := m.openSelectedEnter(); ok {
			return task, cmd
		}
	case "R":
		if resourceEditable(m.kind) && m.mutator != nil {
			m.beginSetResources()
		}
		if m.kind == kube.KindHelmRelease && m.mutator != nil {
			if row, ok := m.selectedRow(); ok {
				return m, m.beginRollback(row)
			}
		}
	case "C":
		if m.kind == kube.KindNode {
			if row, ok := m.selectedRow(); ok {
				return m, m.beginCordon(row)
			}
		}
	case "D":
		if m.kind == kube.KindNode {
			if row, ok := m.selectedRow(); ok {
				return m, m.beginDrain(row)
			}
		}
	case "+":
		m.beginScale(1)
	case "-":
		m.beginScale(-1)
	case "i":
		m.beginSetImage()
	case "m":
		if metaEditable(m.kind) && m.mutator != nil {
			m.beginMeta()
		}
	case "a":
		if !m.desc.ClusterScoped {
			return m, m.switchNamespace("")
		}
	case "N":
		if m.grouped() {
			if ns, ok := m.selectedNamespace(); ok {
				return m, m.switchNamespace(ns)
			}
		}
	case "tab":
		if m.grouped() {
			m.toggleGroup()
		}
	case "ctrl+r":
		if m.kind == kube.KindDeployment && m.state == tui.TaskStateReady && m.mutator != nil {
			if row, ok := m.selectedRow(); ok {
				return m, m.beginRolloutRestart(row)
			}
		}
	case "r":
		switch {
		case m.kind == kube.KindForward && m.state == tui.TaskStateReady:
			return m, m.restartSelectedForward()
		case m.offline() && m.retrier != nil:
			m.retrier.RetryNow()
		case m.state == tui.TaskStatePermissionDenied || m.state == tui.TaskStateError:
			// No auto-retry on 4xx (docs/design README.md §4b) — this is the
			// only retry path for a permission/load error, manual only.
			return m, m.resetAndLoad()
		}
	case "w":
		if m.state == tui.TaskStatePermissionDenied {
			if task, cmd, ok := m.openWhoCanFromCurrentKind(); ok {
				return task, cmd
			}
		}
	case "y":
		if m.state == tui.TaskStatePermissionDenied {
			return m, tea.SetClipboard(m.feedback)
		}
		if m.kind == kube.KindForward {
			return m, m.copySelectedForwardURL()
		}
		if m.kind != kube.KindHelmRelease {
			if task, cmd, ok := m.openSelectedYAML(); ok {
				return task, cmd
			}
		}
	case "e":
		if task, cmd, ok := m.openSelectedEvents(); ok {
			return task, cmd
		}
	case "t":
		if task, cmd, ok := m.openSelectedTimeline(); ok {
			return task, cmd
		}
	case "v":
		if task, cmd, ok := m.openSelectedHelmValues(); ok {
			return task, cmd
		}
	case "h":
		if task, cmd, ok := m.openSelectedHelmHistory(); ok {
			return task, cmd
		}
	case "f":
		if task, cmd, ok := m.openSelectedForward(); ok {
			return task, cmd
		}
	case "x":
		if m.kind == kube.KindForward {
			return m, m.stopSelectedForward()
		}
		if task, cmd, ok := m.openSelectedExec(); ok {
			if task != nil {
				return task, cmd
			}
			return m, cmd
		}
	case "X":
		if m.kind == kube.KindForward && m.state == tui.TaskStateReady {
			m.beginStopAllForwards()
		}
	case "s":
		if cmd, ok := m.selectedNodeShell(); ok {
			return m, cmd
		}
	case "E":
		if cmd, ok := m.beginEdit(); ok {
			return m, cmd
		}
	case "ctrl+d":
		if m.state == tui.TaskStateReady && m.mutator != nil && m.kind != kube.KindForward && m.kind != kube.KindHelmRelease {
			if verbs.Delete.Bulk && len(m.marks) > 0 {
				return m, m.beginBulkDelete()
			}
			if row, ok := m.selectedRow(); ok {
				return m, m.beginDelete(row)
			}
		}
	}
	return m, nil
}

// openSelectedEnter is enter's routing chain for updateKey's plain enter —
// not while filtering, where enter commits the filter instead of opening
// anything (updateFilterKey's "enter" case): node/pod detail, the
// Ingress/Gateway-API routing table (23a/23b — checked ahead of the generic
// Custom-kind branch below since HTTPRoute/GRPCRoute/TCPRoute/Gateway are
// themselves Custom), a Deployment's own pods, a StatefulSet's own pods, a
// DaemonSet's own pods, a CRD's instance list, or a generic Custom kind's
// object detail, in that priority order. ok is false
// when the current kind/selection has no enter destination (e.g. Services,
// Forwards), so the caller leaves the key unhandled.
func (m *Model) openSelectedEnter() (tea.Model, tea.Cmd, bool) {
	if task, cmd, ok := m.openSelectedNodeDetail(); ok {
		return task, cmd, true
	}
	if task, cmd, ok := m.openSelectedPodDetail(); ok {
		return task, cmd, true
	}
	if task, cmd, ok := m.openSelectedRouteTable(); ok {
		return task, cmd, true
	}
	if task, cmd, ok := m.openSelectedSecretData(); ok {
		return task, cmd, true
	}
	if task, cmd, ok := m.openSelectedConfigMapData(); ok {
		return task, cmd, true
	}
	if cmd, ok := m.openSelectedNamespacePods(); ok {
		return m, cmd, true
	}
	if m.kind == kube.KindDeployment {
		if row, ok := m.selectedRow(); ok {
			return m, m.openDeploymentPods(row), true
		}
	}
	if m.kind == kube.KindStatefulSet {
		if row, ok := m.selectedRow(); ok {
			return m, m.openStatefulSetPods(row), true
		}
	}
	if m.kind == kube.KindDaemonSet {
		if row, ok := m.selectedRow(); ok {
			return m, m.openDaemonSetPods(row), true
		}
	}
	if m.kind == kube.KindHelmRelease {
		if row, ok := m.selectedRow(); ok {
			return m, m.openReleaseObjects(row), true
		}
	}
	if m.kind == kube.KindCustomResourceDefinition {
		if cmd, ok := m.openCRDInstances(); ok {
			return m, cmd, true
		}
	}
	if task, cmd, ok := m.openSelectedObjectDetail(); ok {
		return task, cmd, true
	}
	return nil, nil, false
}

// updateConfirmKey routes keys while a mutating action's confirmation is
// showing: a delete/force-delete at TierModal (8b's type-the-name PROD
// modal) gets its own key handling; every other confirming case — TierNone/
// TierInline, and Drain's TierModal (nodes.go's beginDrain, still Phase 9's
// plain ConfirmCard, deliberately not upgraded — see mvp-tasks.md's Phase
// 5/8b exit notes) — stays the simple y/n/esc prompt, plus ctrl-k on a
// pending inline Pod delete: rather than jumping to the PROD type-the-name
// modal, ctrl-k stages force-delete right inside this same inline confirm
// (ArmForceDelete) — "y" then runs DeleteResourceForced, "n" backs out of
// just the force sub-state (DisarmForceDelete) instead of cancelling
// outright, and "esc" still cancels the whole confirm either way. Everything
// else is swallowed so movement/filter can't act underneath.
func (m *Model) updateConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if pending := m.actions.Pending(); m.actions.Tier() == actions.TierModal && pending != nil && isDeleteVerb(pending.Scope.Verb) {
		return m.updateModalConfirmKey(msg)
	}
	switch msg.String() {
	case "y":
		return m, m.actions.Confirm()
	case "ctrl+k":
		m.actions.ArmForceDelete()
	case "n":
		if m.actions.ForceArmed() {
			m.actions.DisarmForceDelete()
			return m, nil
		}
		m.cancelInlineConfirm()
	case "esc":
		m.cancelInlineConfirm()
	}
	return m, nil
}

// cancelInlineConfirm is updateConfirmKey's shared "actually cancel" path —
// esc always takes it; n only when the confirm isn't mid force-delete
// escalation (that case backs out to the plain delete prompt instead, see
// updateConfirmKey's own doc comment).
func (m *Model) cancelInlineConfirm() {
	if m.pendingMeta != nil {
		// The panel stayed open under this confirm (meta.go's doc comment)
		// with the row's edit already applied to its buffer — cancelling
		// must revert that buffer, the same "esc backs out without keeping
		// the typed change" contract editing mode's own esc already has. A
		// no-op for a pending removal, whose buffer never diverged from
		// current in the first place.
		if r := m.pendingMeta.selectedRow(); r != nil {
			r.setBuffer(r.current)
		}
	}
	m.actions.Cancel()
}

// isMetaActionID reports whether id names a 26a set-meta/remove-meta action
// (meta.go's commitMeta/commitMetaRemove ID scheme) — used by the
// actions.ResultMsg handler above to route a failed patch's error message
// through execFeedback.
func isMetaActionID(id string) bool {
	return strings.HasPrefix(id, "set-meta-") || strings.HasPrefix(id, "remove-meta-")
}

// updateModalConfirmKey drives the 8b type-the-name modal: enter executes
// only once Controller.NameMatches ("↵ stays dead until the typed name
// matches"), backspace/typing edit the buffer, ctrl-k escalates a pending
// Pod delete to force-delete, esc cancels.
func (m *Model) updateModalConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.actions.Cancel()
	case "enter":
		return m, m.actions.Confirm()
	case "ctrl+k":
		m.actions.Escalate()
	default:
		return m, m.actions.HandleTypeKey(msg)
	}
	return m, nil
}

func (m *Model) updateFilterKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filterActive = false
		m.filterListFocused = false
		m.setFilter("")
		m.filterInput.Blur()
		m.clearOrigin()
		m.recomputeVisible()
	case "enter":
		// Enter never opens a destination while filtering, even for kinds
		// that have one (Pods, Nodes, Deployments, …) — it always commits
		// the filter instead: query/rows/chrome stay exactly as they are,
		// but keys stop being captured as typing, so j/k, ctrl-d, etc. act
		// on the narrowed rows directly. '/' resumes editing the same
		// query; a second enter (now routed through updateKey, unfiltered)
		// opens the selected row's destination same as it always has.
		m.filterListFocused = true
		m.filterInput.Blur()
	// Only the arrow keys (plus alt+j/alt+k, which never carry Text) move
	// selection while filtering — plain "j"/"k" must stay typeable into the
	// query (mvp-plan.md's "j/k ≡ ↑↓ everywhere" is for browse mode; a live
	// filter input takes every character).
	case "up", "alt+k":
		m.moveSelection(-1)
	case "down", "alt+j":
		m.moveSelection(1)
	case "*":
		// 20a: "filter-then-mark is the bulk grammar" — '*' marks every row
		// the live query currently matches without leaving filter mode.
		// Intercepted here rather than falling to the default typing branch
		// because '*' can never appear in a Kubernetes object name, so this
		// never shadows a character a real filter query would need (unlike
		// 6a's "a", which stays typeable for exactly that reason).
		m.markAllFiltered()
	default:
		var cmd tea.Cmd
		before := m.filterInput.Value()
		m.filterInput, cmd = m.filterInput.Update(msg)
		if after := m.filterInput.Value(); after != before {
			// Sync Session.Location.Filter directly rather than going
			// through setFilter, which forces the cursor to the end —
			// right for a wholesale replace (setFilter's other callers) but
			// wrong here, where the edit that just landed may have been a
			// mid-string insert/delete.
			if m.session != nil {
				m.session.Location.Filter = after
			}
			m.clearOrigin()
			m.recomputeVisible()
		}
		return m, cmd
	}
	return m, nil
}

// openSelectedLogs pushes the log-stream screen for the selected row (Pods
// only). ok is false when logs aren't wired or nothing's selected, so the
// caller leaves 'l' a no-op rather than pushing a broken screen.
func (m Model) openSelectedLogs() (tea.Model, tea.Cmd, bool) {
	if m.openLogs == nil || m.kind != kube.KindPod {
		return nil, nil, false
	}
	row, ok := m.selectedRow()
	if !ok {
		return nil, nil, false
	}
	pod, ok := m.pods[row.Name]
	if !ok {
		pod = kube.Pod{Namespace: m.namespace, Name: row.Name}
	}
	task, cmd := m.openLogs(pod, m.width, m.height)
	return task, cmd, task != nil
}

// openSelectedPodDetail pushes 5a for the selected Pod row, handing over
// the current visible list's ordered names + the selected row's position so
// poddetail's j/k can move to the next/prev pod without leaving detail
// (works the same in 6b's grouped view — m.visible stays name-ordered
// regardless of the interspersed GroupHeader rendering, per grouping.go).
func (m Model) openSelectedPodDetail() (tea.Model, tea.Cmd, bool) {
	if m.openPodDetail == nil || m.kind != kube.KindPod {
		return nil, nil, false
	}
	row, ok := m.selectedRow()
	if !ok {
		return nil, nil, false
	}
	pod, ok := m.pods[row.Name]
	if !ok {
		pod = kube.Pod{Namespace: m.namespace, Name: row.Name}
	}
	siblings := make([]string, len(m.visible))
	index := 0
	for i, fm := range m.visible {
		siblings[i] = fm.row.Name
		if fm.row.Name == row.Name {
			index = i
		}
	}
	task, cmd := m.openPodDetail(pod, siblings, index, m.width, m.height)
	return task, cmd, task != nil
}

// openSelectedSecretData pushes 27b for the selected Secret row's Data view.
// ok is false when the hook isn't wired, the current kind isn't Secret, or
// nothing's selected — so ↵ falls through to whatever the rest of
// openSelectedEnter's chain (or ultimately nothing) resolves to.
func (m Model) openSelectedSecretData() (tea.Model, tea.Cmd, bool) {
	if m.openSecretData == nil || m.kind != kube.KindSecret {
		return nil, nil, false
	}
	row, ok := m.selectedRow()
	if !ok {
		return nil, nil, false
	}
	task, cmd := m.openSecretData(row.Namespace, row.Name, m.width, m.height)
	return task, cmd, task != nil
}

// openSelectedConfigMapData pushes 27a for the selected ConfigMap row's Data
// view. ok is false when the hook isn't wired, the current kind isn't
// ConfigMap, or nothing's selected — so ↵ falls through to whatever the rest
// of openSelectedEnter's chain (or ultimately nothing) resolves to.
func (m Model) openSelectedConfigMapData() (tea.Model, tea.Cmd, bool) {
	if m.openConfigMapData == nil || m.kind != kube.KindConfigMap {
		return nil, nil, false
	}
	row, ok := m.selectedRow()
	if !ok {
		return nil, nil, false
	}
	task, cmd := m.openConfigMapData(row.Namespace, row.Name, m.width, m.height)
	return task, cmd, task != nil
}

// openSelectedYAML pushes 8a for the selected row, any kind (docs/design
// README.md's system-wide interactions: "y opens the YAML view on any
// selected object, any kind" — not gated to Pods, unlike logs/detail).
func (m Model) openSelectedYAML() (tea.Model, tea.Cmd, bool) {
	if m.openYAML == nil || m.state != tui.TaskStateReady {
		return nil, nil, false
	}
	row, ok := m.selectedRow()
	if !ok {
		return nil, nil, false
	}
	task, cmd := m.openYAML(m.kind, row.Namespace, row.Name, m.width, m.height)
	return task, cmd, task != nil
}

// openSelectedEvents pushes 9b namespace-scoped (docs/design README.md
// §9b: "reached by e from browse, namespace-scoped") — m.namespace is
// already "" in 6b's all-namespaces triage, so no separate branch is needed
// for that case. Doesn't need a selected row, unlike YAML/logs/detail —
// which is exactly why TaskStateEmpty (10c: connected, zero rows of *this
// kind* in this namespace) must work too, not just TaskStateReady: the
// namespace can easily have events tied to some other kind (10c's own hint
// line already says as much — "g other kinds — this namespace has N
// pods...") even when the browsed kind has none. Excluding TaskStateEmpty
// silently ate every 'e' press (and 'g'-jump to Events, which redirects
// through this same gate) from an empty-state screen.
func (m Model) openSelectedEvents() (tea.Model, tea.Cmd, bool) {
	if m.openEvents == nil || (m.state != tui.TaskStateReady && m.state != tui.TaskStateEmpty) {
		return nil, nil, false
	}
	task, cmd := m.openEvents(m.namespace, m.width, m.height)
	return task, cmd, task != nil
}

// openSelectedTimeline pushes 16b object-scoped when the cursor sits on a
// Deployment/Pod/Node row (the mockup's own "t on a selected deployment"
// caption, docs/design README.md §16b — previously only reachable via
// poddetail/nodedetail's own 't'), falling back to 16a namespace-scoped
// (§16a) otherwise — same "no selected row needed, namespace already
// carries the all-namespaces case" shape openSelectedEvents uses, including
// the same TaskStateEmpty carve-out.
func (m Model) openSelectedTimeline() (tea.Model, tea.Cmd, bool) {
	if m.state != tui.TaskStateReady && m.state != tui.TaskStateEmpty {
		return nil, nil, false
	}
	if m.openObjectTimeline != nil && isObjectTimelineKind(m.kind) {
		if row, ok := m.selectedRow(); ok {
			task, cmd := m.openObjectTimeline(m.kind, row.Namespace, row.Name, m.width, m.height)
			return task, cmd, task != nil
		}
	}
	if m.openTimeline == nil {
		return nil, nil, false
	}
	task, cmd := m.openTimeline(m.namespace, m.width, m.height)
	return task, cmd, task != nil
}

// isObjectTimelineKind reports whether kind resolves to a real 16b scope —
// the same three kinds tasks/timeline's own load.go (restartsForScope/
// resolveOwningDeployment) knows how to scope a merged feed + revision rail
// to.
func isObjectTimelineKind(kind kube.ResourceKind) bool {
	return kind == kube.KindDeployment || kind == kube.KindPod || kind == kube.KindNode
}

// openSelectedExec resolves 'x' for the selected Pod row (docs/design
// README.md §10a): a single container execs immediately via kube.ExecSpec —
// task is nil and cmd is the tea.ExecProcess Cmd, so browse stays the
// active task and handles its own execResultMsg — while more than one
// container pushes tasks/execpicker instead. ok is false when nothing
// applies (not a Pod, no row selected, or no containers known), so 'x'
// stays a no-op rather than the caller misreading a nil task as failure.
func (m Model) openSelectedExec() (tea.Model, tea.Cmd, bool) {
	if m.kind != kube.KindPod {
		return nil, nil, false
	}
	row, ok := m.selectedRow()
	if !ok {
		return nil, nil, false
	}
	pod, ok := m.pods[row.Name]
	if !ok || len(pod.ContainerInfos) == 0 {
		return nil, nil, false
	}
	if len(pod.ContainerInfos) == 1 {
		return nil, execCmd(pod.Namespace, pod.Name, pod.ContainerInfos[0].Name), true
	}
	if m.openExec == nil {
		return nil, nil, false
	}
	task, cmd := m.openExec(pod.Namespace, pod.Name, pod.ContainerInfos, m.width, m.height)
	return task, cmd, task != nil
}

// execCmd suspends the program and hands the tty to kubectl for container
// (tea.ExecProcess over kube.ExecSpec) — shared shape with
// tasks/execpicker's own execSelected, duplicated per the repo's
// package-local-seam convention.
func execCmd(namespace, pod, container string) tea.Cmd {
	spec := kube.ExecSpec(namespace, pod, container, "")
	return tea.ExecProcess(spec, func(err error) tea.Msg {
		return execResultMsg{err: err}
	})
}

// selectedNodeShell resolves 's' for the selected Nodes row: suspend and
// hand the tty to kubectl debug (kube.NodeShellSpec) — the same direct
// tea.ExecProcess path exec's single-container branch takes, no task
// pushed, so browse stays the active task and handles its own
// nodeShellResultMsg. ok is false when nothing applies (not the Nodes kind,
// no row selected), so 's' stays a no-op.
func (m Model) selectedNodeShell() (tea.Cmd, bool) {
	if m.kind != kube.KindNode || m.state != tui.TaskStateReady {
		return nil, false
	}
	row, ok := m.selectedRow()
	if !ok {
		return nil, false
	}
	image := ""
	if m.session != nil {
		image = m.session.Config.NodeShellImage
	}
	spec := kube.NodeShellSpec(row.Name, image)
	return tea.ExecProcess(spec, func(err error) tea.Msg {
		return nodeShellResultMsg{err: err}
	}), true
}
