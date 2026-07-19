package poddetail

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

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
		if msg.Kind == kube.KindPod && m.lister != nil {
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
	if m.selectedContainer >= len(m.pod.ContainerInfos) {
		m.selectedContainer = max(len(m.pod.ContainerInfos)-1, 0)
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
		// README.md §5a) — every key returns to browse.
		return m, func() tea.Msg { return tui.BackMsg{} }
	}
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc", "backspace":
		return m, func() tea.Msg { return tui.BackMsg{} }
	case "j":
		return m, m.moveSibling(1)
	case "k":
		return m, m.moveSibling(-1)
	case "tab":
		m.cycleContainer()
	case "l":
		if task, cmd, ok := m.openSelectedLogs(); ok {
			return task, cmd
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
	case "alt+o":
		if cmd, ok := m.openOwnerWorkload(); ok {
			return m, cmd
		}
	case "alt+i":
		if cmd, ok := m.openIngress(); ok {
			return m, cmd
		}
	case "x":
		if task, cmd, ok := m.openSelectedExec(); ok {
			if task != nil {
				return task, cmd
			}
			return m, cmd
		}
	case "f":
		if task, cmd, ok := m.openSelectedForward(); ok {
			return task, cmd
		}
	case "E":
		if cmd, ok := m.beginEdit(); ok {
			return m, cmd
		}
	case "ctrl+d":
		return m, m.beginDelete()
	}
	return m, nil
}

// updateConfirmKey routes keys while a confirmation is showing: TierModal
// (the type-the-name PROD modal) gets its own key handling — typing,
// backspace, ctrl-k force-delete escalation, enter-when-matched — while
// TierInline/TierNone stay the simple y/n/esc prompt (mvp-plan.md §8b).
func (m *Model) updateConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.actions.Tier() == actions.TierModal {
		return m.updateModalConfirmKey(msg)
	}
	switch msg.String() {
	case "y":
		return m, m.actions.Confirm()
	case "n", "esc":
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
	case "backspace":
		m.actions.Backspace()
	case "ctrl+k":
		m.actions.Escalate()
	default:
		if msg.Text != "" {
			m.actions.TypeRune(msg.Text)
		}
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
// leaving detail (docs/design README.md §5a: "j/k moves through the table's
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
	m.selectedContainer = 0
	m.state = tui.TaskStateLoading
	m.feedback = "Loading " + m.name + "..."
	if m.lister == nil {
		m.state = tui.TaskStateError
		m.feedback = "no cluster connection"
		return nil
	}
	return tea.Batch(m.load(), components.SpinnerTick())
}

func (m *Model) cycleContainer() {
	n := len(m.pod.ContainerInfos)
	if n == 0 {
		return
	}
	m.selectedContainer = (m.selectedContainer + 1) % n
}

// openSelectedLogs pushes the log-stream screen for the loaded pod — same
// contract as browse.openSelectedLogs (ok is false when logs aren't wired
// or nothing's loaded yet, so 'l' stays a no-op rather than pushing a
// broken screen).
func (m Model) openSelectedLogs() (tea.Model, tea.Cmd, bool) {
	if m.openLogs == nil || !m.found {
		return nil, nil, false
	}
	task, cmd := m.openLogs(m.pod, m.width, m.height)
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

// openOwnerWorkload is alt+o: "go to the owning Deployment/StatefulSet".
// tea.Sequence(BackMsg, GotoResourceMsg) is the same pair events'
// openSelectedObject uses (poddetail's own doc comment flagged this exact
// shape as the eventual follow-up once a key existed to hang it on — this
// is that key) — BackMsg pops to whatever pushed poddetail (browse, in the
// common case), then GotoResourceMsg asks it to jump. ok is false when the
// owner can't be resolved to a Deployment or StatefulSet (e.g. a
// DaemonSet/Job-owned pod, or no owner at all), so the key stays a no-op.
func (m Model) openOwnerWorkload() (tea.Cmd, bool) {
	kind, name, ok := m.resolveOwnerWorkload()
	if !ok {
		return nil, false
	}
	ns := m.namespace
	return tea.Sequence(
		func() tea.Msg { return tui.BackMsg{} },
		func() tea.Msg { return tui.GotoResourceMsg{Kind: kind, Namespace: ns, Name: name} },
	), true
}

// resolveOwnerWorkload resolves the pod's owning Deployment or StatefulSet.
// A StatefulSet owns its pods directly (m.pod.Owner is already
// "StatefulSet/name"), but a Deployment never appears as a pod's direct
// owner — the pod only points at its ReplicaSet — so reaching the
// Deployment costs one extra ReplicaSet lookup against the cached informer
// to read *its* OwnerReference in turn.
func (m Model) resolveOwnerWorkload() (kube.ResourceKind, string, bool) {
	if !m.found || m.lister == nil {
		return "", "", false
	}
	kind, name, ok := splitOwner(m.pod.Owner)
	if !ok {
		return "", "", false
	}
	switch kind {
	case kube.KindStatefulSet:
		return kube.KindStatefulSet, name, true
	case kube.KindReplicaSet:
		ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
		defer cancel()
		objs, err := m.lister.ListRaw(ctx, kube.KindReplicaSet, m.namespace)
		if err != nil {
			return "", "", false
		}
		for _, obj := range objs {
			rs, ok := obj.(*appsv1.ReplicaSet)
			if !ok || rs.Name != name {
				continue
			}
			if depKind, depName, ok := splitOwner(ownerRef(rs.OwnerReferences)); ok && depKind == kube.KindDeployment {
				return kube.KindDeployment, depName, true
			}
		}
	}
	return "", "", false
}

// splitOwner parses an "Owner/name" string (kube.PodFromObject's ownerRef
// shape, e.g. "ReplicaSet/nva-worker-abc123") into its kind/name.
func splitOwner(owner string) (kube.ResourceKind, string, bool) {
	kind, name, found := strings.Cut(owner, "/")
	if !found || kind == "" || name == "" {
		return "", "", false
	}
	return kube.ResourceKind(kind), name, true
}

// ownerRef mirrors kube.PodFromObject's own unexported helper of the same
// name (pods.go), duplicated here since a ReplicaSet's OwnerReferences need
// the identical "Kind/Name" projection and that helper isn't exported.
func ownerRef(refs []metav1.OwnerReference) string {
	if len(refs) == 0 {
		return ""
	}
	return refs[0].Kind + "/" + refs[0].Name
}

// openIngress is alt+i: "go to the Ingress that routes to this pod" —
// resolve the Services whose label selector matches the pod, then the
// Ingress whose rules/default backend name one of those Services, and jump
// to the first match via the same BackMsg/GotoResourceMsg pair
// openOwnerWorkload uses. ok is false when no Ingress can be resolved (no
// matching Service, no Ingress referencing it, or several matches — this
// keeps the single-key jump unambiguous rather than guessing).
func (m Model) openIngress() (tea.Cmd, bool) {
	name, ok := m.resolveIngress()
	if !ok {
		return nil, false
	}
	ns := m.namespace
	return tea.Sequence(
		func() tea.Msg { return tui.BackMsg{} },
		func() tea.Msg { return tui.GotoResourceMsg{Kind: kube.KindIngress, Namespace: ns, Name: name} },
	), true
}

func (m Model) resolveIngress() (string, bool) {
	if !m.found || m.lister == nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()

	svcObjs, err := m.lister.ListRaw(ctx, kube.KindService, m.namespace)
	if err != nil {
		return "", false
	}
	podLabels := labels.Set(m.pod.Labels)
	matched := map[string]bool{}
	for _, obj := range svcObjs {
		svc, ok := obj.(*corev1.Service)
		if !ok || len(svc.Spec.Selector) == 0 {
			continue
		}
		if labels.SelectorFromSet(svc.Spec.Selector).Matches(podLabels) {
			matched[svc.Name] = true
		}
	}
	if len(matched) == 0 {
		return "", false
	}

	ingObjs, err := m.lister.ListRaw(ctx, kube.KindIngress, m.namespace)
	if err != nil {
		return "", false
	}
	name := ""
	for _, obj := range ingObjs {
		ing, ok := obj.(*networkingv1.Ingress)
		if !ok || !ingressReferencesServices(ing, matched) {
			continue
		}
		if name != "" && name != ing.Name {
			return "", false // ambiguous — more than one Ingress fronts this pod
		}
		name = ing.Name
	}
	return name, name != ""
}

// ingressReferencesServices reports whether ing's default backend or any
// rule's paths name one of services.
func ingressReferencesServices(ing *networkingv1.Ingress, services map[string]bool) bool {
	if b := ing.Spec.DefaultBackend; b != nil && b.Service != nil && services[b.Service.Name] {
		return true
	}
	for _, rule := range ing.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		for _, path := range rule.HTTP.Paths {
			if path.Backend.Service != nil && services[path.Backend.Service.Name] {
				return true
			}
		}
	}
	return false
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
