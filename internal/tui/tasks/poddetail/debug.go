// debug.go wires §41a/§41b/§41c (docs/design v.0.11.0.dc.html): the shell
// pre-check that decides whether 'x' on the loaded pod execs, opens
// tasks/execpicker, or opens tasks/debugpanel — mirrors browse's own
// debug.go, duplicated per the repo's package-local-seam convention.
package poddetail

import (
	"context"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// shellProbeTimeout bounds one container's probe — same bound execpicker's
// own detectShellsCmd uses.
const shellProbeTimeout = 5 * time.Second

// RBACChecker answers §41a's "capability is checked before ↵" pre-check —
// same shape as browse.RBACChecker, duplicated per the repo's
// consuming-interface convention. A nil value disables the check entirely,
// the same way a nil Shells disables the shell pre-check.
type RBACChecker interface {
	WhoCan(ctx context.Context, query kube.WhoCanQuery) (kube.WhoCanResult, error)
}

// debugDenial carries §41a's RBAC pre-check verdict for a debug-panel open
// that came back denied — see browse's own debugDenial doc comment for the
// full contract; mirrored here per the repo's package-local-seam
// convention.
type debugDenial struct {
	verb, resource, namespace, reason string
}

func (d debugDenial) feedback() string {
	reason := d.reason
	if reason == "" {
		reason = d.verb + " " + d.resource + " is denied"
	}
	return reason + " — w who-can"
}

// debugCapabilityDenied is §41a's RBAC pre-check — see browse's own
// debugCapabilityDenied doc comment for the full contract (nil rbac,
// granted, or an unsynced/errored RBAC cache all fail open).
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

type podShellDetection struct {
	container string
	shells    []string
	err       error
}

// podShellsProbedMsg carries every container's shell-detection outcome for
// one 'x' press — see browse's own podShellsProbedMsg doc comment.
type podShellsProbedMsg struct {
	namespace, podName string
	containers         []kube.ContainerInfo
	results            []podShellDetection
}

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

// detectPodShellsCmd probes every container in parallel — see browse's own
// detectPodShellsCmd doc comment.
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

// beginExecOrDebug resolves 'x' for the loaded pod — see browse's own
// beginExecOrDebug doc comment for the full contract.
func (m Model) beginExecOrDebug() (tea.Model, tea.Cmd, bool) {
	if !m.found || len(m.pod.ContainerInfos) == 0 {
		return nil, nil, false
	}
	if m.shells == nil {
		return m.openSelectedExec()
	}
	return nil, detectPodShellsCmd(m.namespace, m.name, m.pod.ContainerInfos, m.shells), true
}

// routePodShellsProbed decides which of exec/execpicker/debugpanel 'x'
// meant, now that every container's probe result is in — see browse's own
// routePodShellsProbed doc comment.
func (m *Model) routePodShellsProbed(msg podShellsProbedMsg) (tea.Model, tea.Cmd) {
	if msg.allShellless() {
		if m.openDebug == nil {
			return m, nil
		}
		waiting := false
		for _, c := range msg.containers {
			if c.State == "Waiting" {
				waiting = true
				break
			}
		}
		if denial := m.debugCapabilityDenied(msg.namespace, m.pod.Reason, waiting); denial != nil {
			m.pendingDebugDenial = denial
			m.execFeedback = denial.feedback()
			return m, nil
		}
		m.pendingDebugDenial = nil
		task, cmd := m.openDebug(msg.namespace, msg.podName, msg.containers, m.pod.Reason, waiting, m.width, m.height)
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
