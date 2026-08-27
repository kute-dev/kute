// debug.go routes 'x' on nodedetail's selected Pod row. Unlike browse and
// poddetail, this screen keeps its existing direct exec/container-picker
// behavior for Running pods; the state-first branch prevents kubectl exec
// from being launched against Pending or terminal pods and opens §41c's
// copy-mode debug panel instead.
package nodedetail

import (
	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
)

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
	task, cmd := m.openDebug(row.pod.Namespace, row.pod.Name, row.pod.ContainerInfos, row.pod.Status, waiting, m.width, m.height)
	return task, cmd, true
}
