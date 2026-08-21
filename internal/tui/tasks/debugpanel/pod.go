package debugpanel

import "github.com/kute-dev/kute/internal/kube"

// attachTargetContainer is the container §41b's ephemeral container will
// share the process namespace with — the 't' cycle's current selection.
func (m Model) attachTargetContainer() kube.ContainerInfo {
	if m.attachTargetIdx < 0 || m.attachTargetIdx >= len(m.containers) {
		return kube.ContainerInfo{}
	}
	return m.containers[m.attachTargetIdx]
}

// copyContainer is §41c's "container" field — which container's image the
// copy pod's --container flag names.
func (m Model) copyContainer() kube.ContainerInfo {
	if m.copyContainerIdx < 0 || m.copyContainerIdx >= len(m.containers) {
		return kube.ContainerInfo{}
	}
	return m.containers[m.copyContainerIdx]
}

func cycleIndex(i, n int) int {
	if n == 0 {
		return 0
	}
	return (i + 1) % n
}

func (m *Model) cycleAttachTarget() {
	m.attachTargetIdx = cycleIndex(m.attachTargetIdx, len(m.containers))
}

func (m *Model) cycleCopyContainer() {
	m.copyContainerIdx = cycleIndex(m.copyContainerIdx, len(m.containers))
}

// cycleMode toggles between modeAttach/modeCopy — 'm' on a pod target.
// Attach is hard-disabled while the pod won't stay running (the confirmed
// §41c decision: kubectl's own --target on a non-running pod just fails,
// so a cursor landing there has nothing productive to do), so cycling away
// from copy in that state is a no-op rather than a switch.
func (m *Model) cycleMode() {
	if m.mode == modeCopy && m.podWontStayRunning() {
		return
	}
	if m.mode == modeAttach {
		m.mode = modeCopy
	} else {
		m.mode = modeAttach
	}
}
