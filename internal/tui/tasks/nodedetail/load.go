package nodedetail

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
)

// errNodeNotFound is findNode's sentinel for "this node isn't in the cache
// right now" — deliberately not enough on its own to conclude the node is
// gone. applyLoaded checks the Node cache's own sync/error state before
// believing it: a cache that hasn't finished its initial fill, or one
// that's Forbidden, looks exactly like a missing node to a scan for one
// name.
var errNodeNotFound = errors.New("node not found")

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
// match) and builds both halves of the facts panel:
//
//   - REQUESTED / ALLOCATABLE — the pods' effective requests against what
//     the scheduler may hand out. Requests, not usage, so this half still
//     renders on a cluster with no metrics-server.
//   - USED / CAPACITY — this node's live metrics-server usage against the
//     whole machine. Best-effort: a failed read leaves usedOK false and the
//     block says so.
//
// It also best-effort enriches the pod rows with live usage for their own
// MEM/CPU columns.
func (m Model) load() tea.Cmd {
	lister := m.lister
	metrics := m.metrics
	nodeMetrics := m.nodeMetrics
	nodeName := m.nodeName
	timeout := m.timeout
	parent := m.session.ClusterContext()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, timeout)
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
		used, usedOK := readNodeUsage(ctx, nodeMetrics, nodeName)

		podDesc, _ := resources.DefaultRegistry().Descriptor(kube.KindPod)

		var allocated allocation
		rows := make([]nodePodRow, 0, len(podObjs))
		for _, obj := range podObjs {
			p, ok := obj.(*corev1.Pod)
			if !ok || p.Spec.NodeName != nodeName || nodeDetailTerminalPod(p) {
				continue
			}
			pod := kube.PodFromObject(p)
			// The pod's *effective* request, not the bare sum over
			// spec.containers that kube.Pod carries: native sidecars, a
			// heavy init container and spec.overhead all reserve room on
			// the node too, and kubectl describe node counts them.
			cpuReq, memReq := kube.PodEffectiveRequests(p)
			allocated.cpuMilli += cpuReq
			allocated.memBytes += memReq

			if pm, found := podMetrics[kube.PodKey(pod.Namespace, pod.Name)]; found {
				pod.CPU, pod.MEM = pm.CPU, pm.MEM
				pod.CPUMilli, pod.MEMBytes = pm.CPUMilli, pm.MemBytes
			}
			rows = append(rows, nodePodRow{pod: pod, row: podDesc.Project(p)})
		}
		slices.SortStableFunc(rows, func(a, b nodePodRow) int {
			return cmp.Or(
				cmp.Compare(healthRank(a.row.Status), healthRank(b.row.Status)),
				cmp.Compare(strings.ToLower(a.pod.Name), strings.ToLower(b.pod.Name)),
			)
		})

		return loadedMsg{
			node:        node,
			allocated:   allocated,
			allocatable: nodeAllocatable(node),
			used:        used,
			capacity:    nodeCapacity(node),
			usedOK:      usedOK,
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
	return nil, fmt.Errorf("node %q not found: %w", name, errNodeNotFound)
}

func nodeAllocatable(n *corev1.Node) allocation {
	return fromNodeResources(kube.NodeAllocatable(n))
}

// nodeCapacity is the USED bar's denominator — the whole machine, since
// node usage is measured across the whole machine (kube.NodeCapacity).
func nodeCapacity(n *corev1.Node) allocation {
	return fromNodeResources(kube.NodeCapacity(n))
}

func fromNodeResources(r kube.NodeResources) allocation {
	return allocation{cpuMilli: r.CPUMilli, memBytes: r.MemBytes, pods: r.Pods}
}

// readNodeUsage pulls this node's live usage out of one NodeMetrics poll.
// ok is false for every way the reading can be absent — no seam wired, the
// poll failed (no metrics-server), the node isn't in the map, or it reports
// kube.NodeMetric's own ""/"n/a" sentinel — so the caller has a single flag
// to decide between a real bar and the "no metrics-server" note. This is
// the same sentinel check overview's loadOverview applies.
func readNodeUsage(ctx context.Context, src NodeMetricsReader, nodeName string) (allocation, bool) {
	if src == nil {
		return allocation{}, false
	}
	metrics, err := src.NodeMetrics(ctx)
	if err != nil {
		return allocation{}, false
	}
	nm, found := metrics[nodeName]
	if !found || nm.CPU == "" || nm.CPU == "n/a" {
		return allocation{}, false
	}
	return allocation{cpuMilli: nm.CPUMilli, memBytes: nm.MemBytes}, true
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

// pollInterval is how often the USED / CAPACITY block and the pod rows'
// own CPU/MEM cells refresh — the same cadence browse polls at, so walking
// from the nodes list into a node doesn't change how fresh the number is.
const pollInterval = 2 * time.Second

// scheduleMetricsTick arranges the next tick of an existing chain.
func (m Model) scheduleMetricsTick(epoch int) tea.Cmd {
	if !m.pollsMetrics() {
		return nil
	}
	return tea.Tick(pollInterval, func(time.Time) tea.Msg {
		return metricsTickMsg{epoch: epoch}
	})
}

// armMetricsTick starts a *new* poll chain, retiring any chain already
// running. Called when the screen first appears and whenever it is restored
// from the stack, where the previous chain's tick was delivered to whatever
// screen was active instead and so never came back.
func (m *Model) armMetricsTick() tea.Cmd {
	m.metricsEpoch++
	return m.scheduleMetricsTick(m.metricsEpoch)
}

// pollsMetrics reports whether a usage poll is worth making: there has to
// be a seam to read, and the cluster has to be reachable — polling a
// cluster we know is offline just burns a timeout per tick, the same gate
// browse.pollsMetrics applies.
func (m Model) pollsMetrics() bool {
	if m.metrics == nil && m.nodeMetrics == nil {
		return false
	}
	return !m.conn.Offline()
}

// loadMetrics is the poll itself: usage only, deliberately not a full
// load(). A node's pod set changes on a watch event, which already
// triggers a reload — re-listing every pod in the cluster every two
// seconds to refresh a number would make this screen more expensive than
// the list it was opened from.
func (m Model) loadMetrics() tea.Cmd {
	metrics := m.metrics
	nodeMetrics := m.nodeMetrics
	nodeName := m.nodeName
	timeout := m.timeout
	epoch := m.metricsEpoch
	parent := m.session.ClusterContext()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, timeout)
		defer cancel()

		msg := metricsLoadedMsg{epoch: epoch}
		msg.used, msg.usedOK = readNodeUsage(ctx, nodeMetrics, nodeName)
		if metrics != nil {
			msg.podMetrics, _ = metrics.PodMetricsByNamespace(ctx, "")
		}
		return msg
	}
}
