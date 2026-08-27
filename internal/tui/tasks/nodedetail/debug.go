// debug.go routes 'x' on nodedetail's selected Pod row. Unlike browse and
// poddetail, this screen keeps its existing direct exec/container-picker
// behavior for Running pods; the state-first branch prevents kubectl exec
// from being launched against Pending or terminal pods and opens §41c's
// copy-mode debug panel instead.
package nodedetail

import (
	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

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

// debugCapabilityDenied mirrors browse/poddetail's fail-open RBAC check.
// An unsynced or errored authorization cache cannot make a denial claim;
// the API server remains the backstop when the result is not trustworthy.
func (m Model) debugCapabilityDenied(namespace, podPhase string, waiting bool) *debugDenial {
	if m.rbac == nil {
		return nil
	}
	resource := kube.DebugAttachResource
	if kube.PodWontStayRunning(podPhase, waiting) {
		resource = kube.DebugCopyResource
	}
	const verb = "create"
	result, err := m.rbac.WhoCan(m.session.ClusterContext(), kube.WhoCanQuery{Verb: verb, Resource: resource, Namespace: namespace})
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

func podWaiting(containers []kube.ContainerInfo) bool {
	for _, c := range containers {
		if c.State == "Waiting" {
			return true
		}
	}
	return false
}

func (m *Model) openSelectedExecOrDebug() (tea.Model, tea.Cmd, bool) {
	row, ok := m.selectedPod()
	if !ok || len(row.pod.ContainerInfos) == 0 {
		return nil, nil, false
	}
	waiting := podWaiting(row.pod.ContainerInfos)
	if !kube.PodWontStayRunning(row.pod.Status, waiting) {
		return m.openSelectedExec()
	}
	if m.openDebug == nil {
		m.execFeedback = row.pod.Name + ": debug is unavailable"
		return nil, nil, true
	}
	if denial := m.debugCapabilityDenied(row.pod.Namespace, row.pod.Status, waiting); denial != nil {
		m.pendingDebugDenial = denial
		m.execFeedback = denial.feedback()
		return nil, nil, true
	}
	m.pendingDebugDenial = nil
	task, cmd := m.openDebug(row.pod.Namespace, row.pod.Name, row.pod.ContainerInfos, row.pod.Status, waiting, m.width, m.height)
	return task, cmd, true
}
