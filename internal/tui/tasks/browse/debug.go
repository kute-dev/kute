// debug.go wires §41a/§41b/§41c/§41d (docs/design v.0.11.0.dc.html): the
// shell pre-check that decides whether 'x' on a Pod row execs, opens
// tasks/execpicker, or opens tasks/debugpanel, plus 'x' on a Node row
// (openSelectedNodeDebug, update.go).
package browse

import (
	"context"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// ShellDetector answers which shells a container actually has — declared
// here per the repo's consuming-interface convention (execpicker's own
// ShellDetector is the model this mirrors); kube.DetectShells is the real
// implementation, kube/fake supplies the demo one.
type ShellDetector interface {
	DetectShells(ctx context.Context, namespace, pod, container string) ([]string, error)
}

// RBACChecker answers §41a's "capability is checked before ↵" pre-check —
// same shape as tasks/whocan's own WhoCanReader, duplicated per the repo's
// consuming-interface convention (browse doesn't import tasks/whocan). A nil
// value disables the check entirely, the same way a nil Shells disables the
// shell pre-check.
type RBACChecker interface {
	WhoCan(ctx context.Context, query kube.WhoCanQuery) (kube.WhoCanResult, error)
}

// debugDenial carries §41a's RBAC pre-check verdict for a debug-panel open
// that came back denied — enough for the 'w' key to open tasks/whocan
// pre-filled with the exact query that was denied (docs/design
// v.0.11.0.dc.html §41a: "offers w who-can").
type debugDenial struct {
	verb, resource, namespace, reason string
}

// feedback renders the panel's explain-block line: the verbatim reason
// kube.WhoCanResult.CurrentUserVia already carries (the same "closest
// miss" wording tasks/whocan pins to the current user's own row), plus the
// 'w' handoff — "Same discipline as helm-missing-from-PATH (18a)".
func (d debugDenial) feedback() string {
	reason := d.reason
	if reason == "" {
		reason = d.verb + " " + d.resource + " is denied"
	}
	return reason + " — w who-can"
}

// debugCapabilityDenied is §41a's RBAC pre-check, run right before the
// debug panel would open for a shell-less container: "capability is
// checked before ↵, never after" — a permission denial otherwise only
// surfaced as raw kubectl stderr after the tty suspend/handoff, a worse
// failure mode than the exec-picker's own explain block. namespace/
// podPhase/waiting are the same inputs debugpanel.New itself uses to pick
// attach vs copy mode (kube.PodWontStayRunning) — the two modes exercise
// different API resources, so the check has to agree with the mode the
// panel is actually about to open in.
//
// Returns nil when rbac isn't wired, the check comes back granted, or the
// RBAC caches needed to trust the answer aren't confirmed synced yet — a
// cold cache must never make the security claim that a write is denied
// (docs/lazy-informers.md §5.6, the same discipline tasks/whocan's own
// load() applies before trusting a WhoCanResult). kute fails open in every
// one of those cases and lets the real API server's own authorization stay
// the backstop, same as before this check existed.
func (m Model) debugCapabilityDenied(namespace, podPhase string, waiting bool) *debugDenial {
	if m.rbac == nil {
		return nil
	}
	resource := kube.DebugAttachResource
	if kube.PodWontStayRunning(podPhase, waiting) {
		resource = kube.DebugCopyResource
	}
	const verb = "create"
	result, err := m.rbac.WhoCan(context.Background(), kube.WhoCanQuery{Verb: verb, Resource: resource, Namespace: namespace})
	if err != nil || result.CurrentUser == "" || result.CurrentUserGranted {
		return nil
	}
	if !tui.KindsSynced(m.rbac, namespace, kube.KindRole, kube.KindRoleBinding) ||
		!tui.KindsSynced(m.rbac, "", kube.KindClusterRole, kube.KindClusterRoleBinding) {
		return nil
	}
	if tui.KindsError(m.rbac, namespace, kube.KindRole, kube.KindRoleBinding) != nil ||
		tui.KindsError(m.rbac, "", kube.KindClusterRole, kube.KindClusterRoleBinding) != nil {
		return nil
	}
	return &debugDenial{verb: verb, resource: resource, namespace: namespace, reason: result.CurrentUserVia}
}

// shellProbeTimeout bounds one container's probe — same bound execpicker's
// own detectShellsCmd uses.
const shellProbeTimeout = 5 * time.Second

// podShellDetection is one container's probe result.
type podShellDetection struct {
	container string
	shells    []string
	err       error
}

// podShellsProbedMsg carries every container's shell-detection outcome for
// one 'x' press (D5's async pre-check) — unlike execpicker's own per-row
// message, browse needs every result at once to decide whether to exec
// directly, push execpicker, or push the debug panel.
type podShellsProbedMsg struct {
	namespace, podName string
	containers         []kube.ContainerInfo
	results            []podShellDetection
}

// allShellless reports whether every container's probe came back with a
// real (non-nil-err) empty result — the distroless case §41a/§41b route to
// the debug panel for. A probe that couldn't run at all (non-nil err) never
// counts as "no shell", matching execpicker's own shellsText contract.
func (msg podShellsProbedMsg) allShellless() bool {
	if len(msg.results) == 0 {
		return false
	}
	for _, r := range msg.results {
		if r.err != nil || len(r.shells) != 0 {
			return false
		}
	}
	return true
}

// detectPodShellsCmd probes every container in parallel, returning one
// aggregated message once all probes finish — one short-lived kubectl exec
// per container, the same cost tasks/execpicker's own Init already pays,
// now paid on every 'x' press (not just the multi-container ones) so a
// distroless single-container pod routes to the debug panel instead of a
// dead exec attempt.
func detectPodShellsCmd(namespace, podName string, containers []kube.ContainerInfo, detector ShellDetector) tea.Cmd {
	return func() tea.Msg {
		results := make([]podShellDetection, len(containers))
		var wg sync.WaitGroup
		for i, c := range containers {
			wg.Add(1)
			go func(i int, name string) {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), shellProbeTimeout)
				defer cancel()
				shells, err := detector.DetectShells(ctx, namespace, podName, name)
				results[i] = podShellDetection{container: name, shells: shells, err: err}
			}(i, c.Name)
		}
		wg.Wait()
		return podShellsProbedMsg{namespace: namespace, podName: podName, containers: containers, results: results}
	}
}

// beginExecOrDebug resolves 'x' for the selected Pod row: fires the async
// shell pre-check above when Shells is wired (the actual push happens once
// podShellsProbedMsg arrives, in routePodShellsProbed, since the decision
// needs every container's result at once) — falls back to
// openSelectedExec's old synchronous single-vs-multi-container routing when
// Shells isn't wired, so 'x' keeps working without §41a's distroless fork.
func (m Model) beginExecOrDebug() (tea.Model, tea.Cmd, bool) {
	if m.kind != kube.KindPod {
		return nil, nil, false
	}
	if m.shells == nil {
		return m.openSelectedExec()
	}
	row, ok := m.selectedRow()
	if !ok {
		return nil, nil, false
	}
	pod, ok := m.pods[row.Name]
	if !ok || len(pod.ContainerInfos) == 0 {
		return nil, nil, false
	}
	return nil, detectPodShellsCmd(pod.Namespace, pod.Name, pod.ContainerInfos, m.shells), true
}

// routePodShellsProbed decides, now that every container's probe result is
// in, which of exec/execpicker/debugpanel 'x' actually meant (docs/design
// v.0.11.0.dc.html §41a): every container shell-less skips the picker
// entirely and opens the debug panel directly (modeCopy if the pod won't
// stay running, else modeAttach — decided inside debugpanel.New from
// podPhase/waiting); a single container with a shell (or an unknown result)
// execs immediately, matching today's fast path; anything else pushes
// execpicker as before.
func (m *Model) routePodShellsProbed(msg podShellsProbedMsg) (tea.Model, tea.Cmd) {
	if msg.allShellless() {
		if m.openDebug == nil {
			return m, nil
		}
		podPhase, waiting := m.podPhaseAndWaiting(msg.podName)
		if denial := m.debugCapabilityDenied(msg.namespace, podPhase, waiting); denial != nil {
			m.pendingDebugDenial = denial
			m.execFeedback = denial.feedback()
			return m, nil
		}
		m.pendingDebugDenial = nil
		task, cmd := m.openDebug(msg.namespace, msg.podName, msg.containers, podPhase, waiting, m.width, m.height)
		if task != nil {
			return task, cmd
		}
		return m, cmd
	}
	if len(msg.containers) == 1 {
		return m, execCmd(msg.namespace, msg.podName, msg.containers[0].Name)
	}
	if m.openExec == nil {
		return m, nil
	}
	task, cmd := m.openExec(msg.namespace, msg.podName, msg.containers, m.width, m.height)
	if task != nil {
		return task, cmd
	}
	return m, cmd
}

// decorateDebugCopies appends §41c's "⚑ debug copy" NameSuffix tag to any
// Pod row this session recognizes (Session.DebugCopies) as a copy-mode pod
// kute itself created. This is a client-side fact that never lands on the
// object, so — unlike §41e's ephemeral-container tag — it can't be applied
// at projection time (projectPod's own doc comment) and instead rides in
// here as a post-projection decoration, once per fresh Pod list load
// (applyRowsLoaded).
func (m *Model) decorateDebugCopies() {
	if m.kind != kube.KindPod || m.session == nil || m.session.DebugCopies == nil {
		return
	}
	for i := range m.rows {
		tag := " ⚑ debug copy"
		if m.rows[i].NameSuffix != "" {
			// Already carrying §41e's ephemeral-container ⚑ — don't repeat
			// the glyph, just add the second fact.
			tag = " · debug copy"
		}
		if m.session.DebugCopies.Contains(m.rows[i].Namespace, m.rows[i].Name) {
			m.rows[i].NameSuffix += tag
		}
	}
}

// podPhaseAndWaiting reads the loaded pod's own Reason (e.g.
// "CrashLoopBackOff") and whether any container currently sits Waiting
// (e.g. ImagePullBackOff) — debugpanel's own default-mode gate for §41c.
func (m Model) podPhaseAndWaiting(podName string) (string, bool) {
	pod, ok := m.pods[podName]
	if !ok {
		return "", false
	}
	for _, c := range pod.ContainerInfos {
		if c.State == "Waiting" {
			return pod.Reason, true
		}
	}
	return pod.Reason, false
}
