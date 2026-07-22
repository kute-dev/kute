package nodedetail

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
)

// reloadDebounce is how long a still-syncing retry waits before re-running
// load() — same value as browse's own reloadDebounce, duplicated per the
// repo's package-local-seam convention.
const reloadDebounce = 250 * time.Millisecond

// reloadDueMsg fires scheduleReload's retry — epoch guards a stale reply
// (from an earlier retry, or a since-superseded node) against re-triggering
// a load() that's no longer wanted, mirroring browse's own reloadDueMsg.
type reloadDueMsg struct{ epoch int }

// scheduleReload arranges one reloadDueMsg reloadDebounce from now —
// mirrors browse's own scheduleReload.
func (m Model) scheduleReload(epoch int) tea.Cmd {
	return tea.Tick(reloadDebounce, func(time.Time) tea.Msg {
		return reloadDueMsg{epoch: epoch}
	})
}

// load fetches the node itself and its non-terminal pods (spec.nodeName
// match), sums their CPU/MEM requests against the node's Allocatable
// capacity (ALLOCATED/ALLOCATABLE, docs/design README.md §11b — request
// sums, not live usage, so this works even without metrics-server), and
// best-effort enriches the pods with live usage for the MEM/CPU columns.
func (m Model) load() tea.Cmd {
	lister := m.lister
	metrics := m.metrics
	nodeName := m.nodeName
	timeout := m.timeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		node, err := findNode(ctx, lister, nodeName)
		if err != nil {
			return loadedMsg{err: err}
		}

		podObjs, err := lister.ListRaw(ctx, kube.KindPod, "")
		if err != nil {
			return loadedMsg{err: err}
		}

		var podMetrics map[string]kube.PodMetrics
		if metrics != nil {
			podMetrics, _ = metrics.PodMetricsByNamespace(ctx, "")
		}

		podDesc, _ := resources.DefaultRegistry().Descriptor(kube.KindPod)

		var allocated allocation
		rows := make([]nodePodRow, 0, len(podObjs))
		for _, obj := range podObjs {
			p, ok := obj.(*corev1.Pod)
			if !ok || p.Spec.NodeName != nodeName || nodeDetailTerminalPod(p) {
				continue
			}
			pod := kube.PodFromObject(p)
			allocated.cpuMilli += pod.CPURequestMilli
			allocated.memBytes += pod.MEMRequestBytes

			if pm, found := podMetrics[pod.Name]; found {
				pod.CPU, pod.MEM = pm.CPU, pm.MEM
				pod.CPUMilli, pod.MEMBytes = pm.CPUMilli, pm.MemBytes
			}
			rows = append(rows, nodePodRow{pod: pod, row: podDesc.Project(p)})
		}
		sort.SliceStable(rows, func(i, j int) bool {
			ri, rj := healthRank(rows[i].row.Status), healthRank(rows[j].row.Status)
			if ri != rj {
				return ri < rj
			}
			return strings.Compare(strings.ToLower(rows[i].pod.Name), strings.ToLower(rows[j].pod.Name)) < 0
		})

		return loadedMsg{
			node:        node,
			allocated:   allocated,
			allocatable: nodeAllocatable(node),
			pods:        rows,
		}
	}
}

func findNode(ctx context.Context, lister resources.RawLister, name string) (*corev1.Node, error) {
	objs, err := lister.ListRaw(ctx, kube.KindNode, "")
	if err != nil {
		return nil, err
	}
	for _, obj := range objs {
		if n, ok := obj.(*corev1.Node); ok && n.Name == name {
			return n, nil
		}
	}
	return nil, fmt.Errorf("node %q not found", name)
}

func nodeAllocatable(n *corev1.Node) allocation {
	a := allocation{}
	if q, ok := n.Status.Allocatable[corev1.ResourceCPU]; ok {
		a.cpuMilli = q.MilliValue()
	}
	if q, ok := n.Status.Allocatable[corev1.ResourceMemory]; ok {
		a.memBytes = q.Value()
	}
	if q, ok := n.Status.Allocatable[corev1.ResourcePods]; ok {
		a.pods = q.Value()
	}
	return a
}

func nodeDetailTerminalPod(p *corev1.Pod) bool {
	return p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed
}

// healthRank orders StatusClass worst-first — failing/warning rows sort to
// the top, neutral rows sink to the bottom — duplicated from browse's own
// healthRank per the repo's package-local-seam convention, so this screen's
// pods sort exactly like 2a's own Pods list (unhealthy-first, then name).
func healthRank(class resources.StatusClass) int {
	switch class {
	case resources.StatusFail:
		return 0
	case resources.StatusWarn:
		return 1
	case resources.StatusOK:
		return 2
	default:
		return 3
	}
}
