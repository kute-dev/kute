package poddetail

import (
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
	"github.com/kute-dev/kute/internal/tui/components"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// pasteTarget is the type-the-name confirm buffer while a PROD-tier delete
// modal is up — the same gate updateConfirmKey routes typed keys through. 5a
// has no other text entry.
func (m *Model) pasteTarget() tui.PasteTarget {
	if m.actions.Tier() != actions.TierModal {
		return nil
	}
	return m.actions.PasteTarget()
}

// Reload implements tui.Reloader — see its doc comment: this screen misses
// every kube.ResourceChangedMsg while parked in the stack, so BackMsg
// restoring it asks it to catch up immediately rather than showing stale
// data until an unrelated change happens to land while it's active again.
func (m *Model) Reload() tea.Cmd {
	if m.lister == nil {
		return nil
	}
	return m.load()
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if cmd, ok := tui.RoutePaste(msg, m.pasteTarget()); ok {
		return m, cmd
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
	case kube.ResourceChangedMsg:
		// The pod itself, plus the caches its panels are built from without
		// displaying directly: Events feeds the EVENTS grid; ReplicaSets
		// resolve the owner hop up to a Deployment for the meta grid and
		// RELATED's numbered owner entry; Services/Ingresses resolve
		// RELATED's numbered Service/Ingress entries;
		// PersistentVolumeClaims resolve RELATED's numbered PVC entries
		// (load.go's resolveRelatedItems) — a kind read but not listed here
		// goes stale (CLAUDE.md: a screen reading a kind it doesn't display
		// must still reload on it).
		switch msg.Kind {
		case kube.KindPod, kube.KindEvent, kube.KindReplicaSet, kube.KindService, kube.KindIngress, kube.KindPersistentVolumeClaim:
			if m.lister != nil {
				return m, m.load()
			}
		}
	case kube.ConnStateMsg:
		m.conn = kube.ConnState(msg)
		m.actions.SetOffline(m.conn.Offline())
	case loadedMsg:
		return m.applyLoaded(msg)
	case spinner.TickMsg:
		if m.state != tui.TaskStateLoading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
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
	case editResultMsg:
		if msg.err != nil {
			m.execFeedback = "edit exited: " + msg.err.Error()
		} else {
			m.execFeedback = ""
		}
	case podShellsProbedMsg:
		return m.routePodShellsProbed(msg)
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

// execResultMsg carries a directly-run (single-container, no picker pushed)
// kubectl exec's exit outcome — same contract as tasks/execpicker's own
// execResultMsg and browse's, duplicated per the repo's
// package-local-seam convention.
type execResultMsg struct{ err error }

// editResultMsg carries a kubectl edit exit outcome (edit.go) — same
// feedback channel as execResultMsg, kept as its own type for the same
// reason.
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
	if !msg.found {
		m.gone = true
		m.state = tui.TaskStateReady
		m.feedback = ""
		return m, nil
	}
	m.pod = msg.pod
	m.found = true
	m.eventRows = msg.events
	m.eventsErr = msg.eventsErr
	m.controller = msg.controller
	m.related = msg.related
	if total := m.totalContainerRows(); m.selectedContainer >= total {
		m.selectedContainer = max(total-1, 0)
	}
	m.state = tui.TaskStateReady
	m.feedback = ""
	return m, nil
}

func (m *Model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.actions.Active() {
		return m.updateConfirmKey(msg)
	}
	if m.pendingEdit != nil {
		return m.updateEditConfirmKey(msg)
	}
	if m.gone {
		// "pod gone ⇒ banner + auto-back after keypress" (docs/design
		// README.md §5a) — every key returns to browse, except q/ctrl+c:
		// quit is a global expectation everywhere else in the app, and this
		// banner shouldn't be the one state where it silently does something
		// else instead.
		switch msg.String() {
		case "ctrl+q", "ctrl+c":
			return m, tea.Quit
		default:
			return m, func() tea.Msg { return tui.BackMsg{} }
		}
	}
	switch msg.String() {
	case "ctrl+q", "ctrl+c":
		return m, tea.Quit
	case "esc", "backspace":
		return m, func() tea.Msg { return tui.BackMsg{} }
	case "]":
		return m, m.moveSibling(1)
	case "[":
		return m, m.moveSibling(-1)
	case "up", "k":
		m.moveContainerSelection(-1)
	case "down", "j":
		m.moveContainerSelection(1)
	case "ctrl+d":
		m.moveContainerHalfPage(1)
	case "ctrl+u":
		m.moveContainerHalfPage(-1)
	case "l":
		if task, cmd, ok := m.openSelectedLogs(); ok {
			return task, cmd
		}
	case "enter":
		// §41e: re-attach to an already-created ephemeral container — a
		// real container in the pod once attached, exec'd into the same
		// way any other container is (kube.ExecSpec), no new kube
		// function needed. No-op anywhere else in this grid; enter has no
		// other meaning on an ordinary CONTAINERS row.
		if eph, ok := m.selectedEphemeralContainer(); ok {
			if verbs.Exec.HiddenWhileOffline(m.conn.Offline()) {
				return m, nil
			}
			return m, execCmd(m.namespace, m.name, eph.Name)
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
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		if cmd, ok := m.openRelated(int(msg.String()[0] - '1')); ok {
			return m, cmd
		}
	case "x":
		// Exec is Mutating: refused while offline (docs/design README.md
		// §52). keys.go has already dropped the hint and switched the keybar
		// to OFFLINE · "mutating actions disabled".
		if verbs.Exec.HiddenWhileOffline(m.conn.Offline()) {
			return m, nil
		}
		if task, cmd, ok := m.beginExecOrDebug(); ok {
			if task != nil {
				return task, cmd
			}
			return m, cmd
		}
	case "w":
		// §41a's RBAC pre-check offers this handoff for a denied debug
		// launch — pre-filled with the exact create/resource/namespace
		// query that came back denied (docs/design v.0.11.0.dc.html §41a:
		// "offers w who-can").
		if m.pendingDebugDenial != nil && m.openWhoCan != nil {
			d := m.pendingDebugDenial
			task, cmd := m.openWhoCan(d.verb, d.resource, d.namespace, m.width, m.height)
			if task != nil {
				m.pendingDebugDenial = nil
				return task, cmd
			}
		}
	case "f":
		if task, cmd, ok := m.openSelectedForward(); ok {
			return task, cmd
		}
	case "E":
		// kubectl edit applies whatever the user saves, so it's gated with
		// the other mutating verbs while offline (docs/design README.md
		// §4a: "delete/exec/edit verbs are disabled while offline").
		if verbs.Edit.HiddenWhileOffline(m.conn.Offline()) {
			return m, nil
		}
		if cmd, ok := m.beginEdit(); ok {
			return m, cmd
		}
	case "D":
		return m, m.beginDelete()
	}
	return m, nil
}

// updateConfirmKey routes keys while a confirmation is showing: TierModal
// (the type-the-name PROD modal) gets its own key handling — typing,
// backspace, ctrl-k force-delete escalation, enter-when-matched — while
// TierInline/TierNone stay the simple y/n/esc prompt (mvp-plan.md §8b), plus
// ctrl-k on the inline delete confirm: rather than jumping to the PROD
// modal, it stages force-delete right inside this same prompt
// (ArmForceDelete) — "y" then runs DeleteResourceForced, "n" backs out of
// just the force sub-state (DisarmForceDelete), "esc" still cancels
// outright.
func (m *Model) updateConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.actions.Tier() == actions.TierModal {
		return m.updateModalConfirmKey(msg)
	}
	switch msg.String() {
	case "y":
		return m, m.actions.Confirm()
	case "C":
		m.actions.ArmForceDelete()
	case "n":
		if m.actions.ForceArmed() {
			m.actions.DisarmForceDelete()
			return m, nil
		}
		m.actions.Cancel()
	case "esc":
		m.actions.Cancel()
	}
	return m, nil
}

// updateModalConfirmKey drives the 8b type-the-name modal: enter executes
// only once Controller.NameMatches (a no-op otherwise, "↵ stays dead until
// the typed name matches"), backspace/typing edit the buffer, ctrl-k
// escalates a pending Pod delete to force-delete, esc cancels.
func (m *Model) updateModalConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.actions.Cancel()
	case "enter":
		return m, m.actions.Confirm()
	case "C":
		m.actions.Escalate()
	default:
		return m, m.actions.HandleTypeKey(msg)
	}
	return m, nil
}

// isProd reports whether the active session's current context is tagged
// prod in ~/.config/kute/config.yaml — the same source 7a's context
// palette PROD tag reads (internal/tui/context.go).
func (m Model) isProd() bool {
	if m.session == nil {
		return false
	}
	return m.session.Config.IsProd(m.session.Location.Context)
}

// moveSibling shifts to the next/prev pod in browse's ordered list without
// leaving detail (docs/design README.md §5a: "[/] moves through the table's
// pod list without leaving detail view") — a no-op at either end, and a
// no-op when there's no sibling list at all (e.g. reached from nodedetail's
// single-pod handoff).
func (m *Model) moveSibling(delta int) tea.Cmd {
	if len(m.siblings) == 0 {
		return nil
	}
	next := m.siblingIndex + delta
	if next < 0 || next >= len(m.siblings) {
		return nil
	}
	m.siblingIndex = next
	m.name = m.siblings[next]
	m.gone = false
	m.found = false
	m.pod = kube.Pod{}
	m.eventRows = nil
	m.eventsErr = nil
	m.related = nil
	m.selectedContainer = 0
	m.state = tui.TaskStateLoading
	m.feedback = "Loading " + m.name + "..."
	if m.lister == nil {
		m.state = tui.TaskStateError
		m.feedback = "no cluster connection"
		return nil
	}
	return tea.Batch(m.load(), m.spinner.Tick)
}

// totalContainerRows is the combined CONTAINERS + EPHEMERAL selection space
// (§41e) — ephemeral rows come after the ordinary ones, so index arithmetic
// stays a single offset rather than two independent cursors.
func (m Model) totalContainerRows() int {
	return len(m.pod.ContainerInfos) + len(m.pod.EphemeralContainerInfos)
}

// selectedEphemeralContainer resolves m.selectedContainer to an
// EphemeralContainerInfo when the cursor is on the EPHEMERAL group, ok
// false otherwise.
func (m Model) selectedEphemeralContainer() (kube.EphemeralContainerInfo, bool) {
	i := m.selectedContainer - len(m.pod.ContainerInfos)
	if i < 0 || i >= len(m.pod.EphemeralContainerInfos) {
		return kube.EphemeralContainerInfo{}, false
	}
	return m.pod.EphemeralContainerInfos[i], true
}

// moveContainerSelection shifts the combined CONTAINERS/EPHEMERAL
// selection by delta, clamped rather than wrapping — same as browse's own
// moveSelection (j/k ≡ ↑↓ everywhere, CLAUDE.md convention).
func (m *Model) moveContainerSelection(delta int) {
	n := m.totalContainerRows()
	if n == 0 {
		return
	}
	next := m.selectedContainer + delta
	if next < 0 {
		next = 0
	}
	if next >= n {
		next = n - 1
	}
	m.selectedContainer = next
}

func (m *Model) moveContainerHalfPage(direction int) {
	n := m.totalContainerRows()
	position := components.MoveHalfPage(m.selectedContainer, 0, max(n, 1), n, direction)
	m.selectedContainer = position.Selected
}

// openSelectedLogs pushes the log-stream screen for the loaded pod — same
// contract as browse.openSelectedLogs (ok is false when logs aren't wired
// or nothing's loaded yet, so 'l' stays a no-op rather than pushing a
// broken screen). Opens on whichever container the CONTAINERS grid has
// selected, not always index 0 — the only screen among openLogs' three
// callers with a per-container selection to hand off.
func (m Model) openSelectedLogs() (tea.Model, tea.Cmd, bool) {
	if m.openLogs == nil || !m.found {
		return nil, nil, false
	}
	container := ""
	if m.selectedContainer < len(m.pod.ContainerInfos) {
		container = m.pod.ContainerInfos[m.selectedContainer].Name
	} else if eph, ok := m.selectedEphemeralContainer(); ok {
		container = eph.Name
	}
	task, cmd := m.openLogs(m.pod, container, m.width, m.height)
	return task, cmd, task != nil
}

// openSelectedYAML pushes 8a for the loaded pod.
func (m Model) openSelectedYAML() (tea.Model, tea.Cmd, bool) {
	if m.openYAML == nil || !m.found {
		return nil, nil, false
	}
	task, cmd := m.openYAML(kube.KindPod, m.namespace, m.name, m.width, m.height)
	return task, cmd, task != nil
}

// openSelectedEvents pushes 9b object-scoped for the loaded pod.
func (m Model) openSelectedEvents() (tea.Model, tea.Cmd, bool) {
	if m.openEvents == nil || !m.found {
		return nil, nil, false
	}
	task, cmd := m.openEvents(kube.KindPod, m.namespace, m.name, m.width, m.height)
	return task, cmd, task != nil
}

// openSelectedTimeline pushes 16b object-scoped for the loaded pod
// (docs/design README.md §16b: "object-scoped from detail").
func (m Model) openSelectedTimeline() (tea.Model, tea.Cmd, bool) {
	if m.openTimeline == nil || !m.found {
		return nil, nil, false
	}
	task, cmd := m.openTimeline(kube.KindPod, m.namespace, m.name, m.width, m.height)
	return task, cmd, task != nil
}

// openRelated jumps to the RELATED sidebar's (idx+1)-th entry — the digit
// keys' replacement for the old 'o'/'i' shortcuts. tui.GotoResource fires
// the same navigation the 'g' goto palette's own resource picks do,
// pre-filled: model.go's routeGoto pushes a fresh browse view retargeted at
// the destination and keeps poddetail one esc-back away, rather than
// discarding it. ok is false when idx is out of range, so a digit with
// nothing behind it stays a no-op. The targets themselves are resolved once
// in load() (load.go's resolveRelatedItems), not here — render/key-handling
// code stays free of the synchronous lookups that used to live in this
// file's own resolveOwnerWorkload/resolveIngress.
func (m Model) openRelated(idx int) (tea.Cmd, bool) {
	if idx < 0 || idx >= len(m.related) {
		return nil, false
	}
	item := m.related[idx]
	return tui.GotoResource(m.session, item.Kind, m.namespace, item.Name), true
}

// openSelectedForward resolves 'f' for the loaded pod (docs/design
// README.md §304, §308: "on any object row") by pushing tasks/forwardpicker
// (13a) — mirrors browse.openSelectedForward's contract.
func (m Model) openSelectedForward() (tea.Model, tea.Cmd, bool) {
	if !m.found || m.openForward == nil {
		return nil, nil, false
	}
	target := kube.ForwardTarget{Kind: kube.KindPod, Namespace: m.namespace, Name: m.name}
	task, cmd := m.openForward(target, m.width, m.height)
	return task, cmd, task != nil
}

// openSelectedExec resolves 'x' for the loaded pod (docs/design README.md
// §10a): a single container execs immediately via kube.ExecSpec — task is
// nil and cmd is the tea.ExecProcess Cmd, so poddetail stays the active
// task and handles its own execResultMsg — while more than one container
// pushes tasks/execpicker instead. ok is false when nothing's loaded or no
// containers are known, so 'x' stays a no-op.
func (m Model) openSelectedExec() (tea.Model, tea.Cmd, bool) {
	if !m.found || len(m.pod.ContainerInfos) == 0 {
		return nil, nil, false
	}
	if len(m.pod.ContainerInfos) == 1 {
		return nil, execCmd(m.namespace, m.name, m.pod.ContainerInfos[0].Name), true
	}
	if m.openExec == nil {
		return nil, nil, false
	}
	task, cmd := m.openExec(m.namespace, m.name, m.pod.ContainerInfos, m.width, m.height)
	return task, cmd, task != nil
}

// execCmd suspends the program and hands the tty to kubectl for container
// (tea.ExecProcess over kube.ExecSpec) — shared shape with browse's own
// execCmd and tasks/execpicker's execSelected, duplicated per the repo's
// package-local-seam convention.
func execCmd(namespace, pod, container string) tea.Cmd {
	spec := kube.ExecSpec(namespace, pod, container, "")
	return tea.ExecProcess(spec, func(err error) tea.Msg {
		return execResultMsg{err: err}
	})
}

// beginDelete confirms deleting the pod — inline y/N in non-prod contexts,
// the full type-the-name modal in PROD (mvp-plan.md §8b, verbs.TierFor).
// Owner rides along for the modal's "will be recreated" line when known.
func (m *Model) beginDelete() tea.Cmd {
	if !m.found {
		return nil
	}
	return m.actions.Begin(verbs.TierFor(verbs.Delete, m.isProd()), tui.TaskAction{
		ID:    "pod-delete-" + m.namespace + "/" + m.name,
		Label: "Delete pod " + m.name + "?",
		Owner: m.pod.Owner,
		Scope: tui.TaskScope{
			ResourceKind: string(kube.KindPod),
			ResourceName: m.name,
			Namespace:    m.namespace,
			Verb:         "delete",
			IsMutating:   true,
		},
	})
}
