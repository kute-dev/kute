package overview

import (
	"context"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
)

// load fetches every source 19a's panels need — Node/Pod projections (via
// the registry, so status/glyph derivation never drifts from 11a/2a's own),
// raw Node capacity, a node-usage poll, and the ReplicaSet-derived rollout
// feed windowed to the last 30m — in one pass.
func (m Model) load() tea.Cmd {
	epoch := m.reloadEpoch
	lister := m.lister
	nodeMetricsSrc := m.nodeMetrics
	registry := resources.Registry{}
	if m.session != nil {
		registry = m.session.Registry
	}
	timeout := m.timeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		data, err := loadOverview(ctx, lister, nodeMetricsSrc, registry)
		return loadedMsg{epoch: epoch, data: data, err: err}
	}
}

func loadOverview(ctx context.Context, lister resources.RawLister, nodeMetricsSrc NodeMetricsReader, registry resources.Registry) (loadedData, error) {
	var data loadedData

	nodeObjs, err := lister.ListRaw(ctx, kube.KindNode, "")
	if err != nil {
		return data, err
	}
	nodeDesc, _ := registry.Descriptor(kube.KindNode)
	versions := map[string]int{}
	data.nodeCount = len(nodeObjs)
	for _, obj := range nodeObjs {
		if nodeDesc.Project != nil {
			row := nodeDesc.Project(obj)
			if row.Status != resources.StatusOK || row.Cordoned {
				data.nodeTrouble = append(data.nodeTrouble, row)
			}
		}
		if n, ok := obj.(*corev1.Node); ok {
			data.capCPUTotal += allocatable(n, corev1.ResourceCPU)
			data.capMemTotal += allocatable(n, corev1.ResourceMemory)
			data.capPodsTotal += allocatable(n, corev1.ResourcePods)
			if v := n.Status.NodeInfo.KubeletVersion; v != "" {
				versions[v]++
			}
		}
	}
	data.version = majorityVersion(versions)
	data.nodeHealthy = data.nodeCount - len(data.nodeTrouble)
	sortTrouble(data.nodeTrouble)

	if nodeMetricsSrc != nil {
		if metrics, mErr := nodeMetricsSrc.NodeMetrics(ctx); mErr == nil {
			for _, nm := range metrics {
				// "n/a"/"" is kube.NodeMetric's own no-metrics-server sentinel
				// (browse/nodes.go's nodeMetricCell checks the same thing per
				// row) — a node reporting it contributes nothing, and if every
				// node does (demo mode always does), metricsAvailable must
				// stay false so capacityLines shows the "no metrics-server"
				// note instead of a misleadingly flatlined 0-used bar.
				if nm.CPU == "" || nm.CPU == "n/a" {
					continue
				}
				data.metricsAvailable = true
				data.capCPUUsed += nm.CPUMilli
				data.capMemUsed += nm.MemBytes
			}
		}
	}

	podObjs, err := lister.ListRaw(ctx, kube.KindPod, "")
	if err != nil {
		return data, err
	}
	podDesc, _ := registry.Descriptor(kube.KindPod)
	data.podCount = len(podObjs)
	for _, obj := range podObjs {
		if podDesc.Project == nil {
			continue
		}
		row := podDesc.Project(obj)
		if row.Status == resources.StatusWarn || row.Status == resources.StatusFail {
			data.podTrouble = append(data.podTrouble, row)
		}
		if p, ok := obj.(*corev1.Pod); ok && !podTerminal(p) {
			data.capPodsUsed++
		}
	}
	data.podHealthy = data.podCount - len(data.podTrouble)
	sortTrouble(data.podTrouble)

	if nsObjs, nsErr := lister.ListRaw(ctx, kube.KindNamespace, ""); nsErr == nil {
		data.nsCount = len(nsObjs)
	}

	if rsObjs, rsErr := lister.ListRaw(ctx, kube.KindReplicaSet, ""); rsErr == nil {
		cutoff := time.Now().Add(-changesWindow)
		for _, e := range kube.TimelineFromRollouts(rsObjs) {
			if e.Time.After(cutoff) {
				data.changes = append(data.changes, e)
			}
		}
		sort.Slice(data.changes, func(i, j int) bool { return data.changes[i].Time.After(data.changes[j].Time) })
	}

	return data, nil
}

// sortTrouble orders unhealthy rows Fail-then-Warn(-then-cordoned Neutral),
// namespace/name within each — "unhealthy-first" (docs/design README.md
// §19a) applied to the aggregated trouble list itself, not just relative to
// a healthy tail already dropped.
func sortTrouble(rows []resources.Row) {
	rank := func(s resources.StatusClass) int {
		switch s {
		case resources.StatusFail:
			return 0
		case resources.StatusWarn:
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if ri, rj := rank(rows[i].Status), rank(rows[j].Status); ri != rj {
			return ri < rj
		}
		return false
	})
}

func podTerminal(p *corev1.Pod) bool {
	return p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed
}

func allocatable(n *corev1.Node, name corev1.ResourceName) int64 {
	q, ok := n.Status.Allocatable[name]
	if !ok {
		return 0
	}
	if name == corev1.ResourceCPU {
		return q.MilliValue()
	}
	return q.Value()
}

func majorityVersion(counts map[string]int) string {
	best, bestN := "", 0
	for v, n := range counts {
		if n > bestN {
			best, bestN = v, n
		}
	}
	return best
}

// splitObject splits a kube.TimelineEntry.Object string ("Deployment/nva-
// worker") into its Kind and Name — mirrors tasks/timeline/tasks/events' own
// helper of the same name (duplicated per the repo's package-local-seam
// convention).
func splitObject(object string) (kube.ResourceKind, string) {
	kind, name, ok := strings.Cut(object, "/")
	if !ok {
		return "", ""
	}
	return kube.ResourceKind(kind), name
}
