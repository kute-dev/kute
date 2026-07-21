package nodedetail

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
)

// load fetches the node itself and its non-terminal pods (spec.nodeName
// match), sums their CPU/MEM requests against the node's Allocatable
// capacity (ALLOCATED/ALLOCATABLE, docs/design README.md §11b — request
// sums, not live usage, so this works even without metrics-server), and
// best-effort enriches the pods with live usage for the MEM/CPU columns and
// the memory-desc sort.
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

			row := nodePodRow{pod: pod}
			row.glyph, row.glyphBad = podGlyph(pod)
			if pm, found := podMetrics[pod.Name]; found {
				pod.CPU, pod.MEM = pm.CPU, pm.MEM
				pod.CPUMilli, pod.MEMBytes = pm.CPUMilli, pm.MemBytes
			}
			row.pod = pod
			row.cpuText, row.memText = usageText(pod.CPU), usageText(pod.MEM)
			rows = append(rows, row)
		}
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].pod.MEMBytes != rows[j].pod.MEMBytes {
				return rows[i].pod.MEMBytes > rows[j].pod.MEMBytes
			}
			return strings.Compare(rows[i].pod.Name, rows[j].pod.Name) < 0
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

// podGlyph classifies a pod for the bottom pane's leading status glyph —
// the same reasoning resources.projectPod applies, kept local since
// nodedetail works from kube.Pod (PodFromObject), not a runtime.Object.
func podGlyph(p kube.Pod) (glyph string, bad bool) {
	switch {
	case p.Deleting:
		return "◌", false
	case strings.Contains(p.Reason, "CrashLoop"):
		return "✕", true
	case p.Status == string(corev1.PodFailed):
		return "✕", true
	case p.Status == string(corev1.PodSucceeded):
		return "○", false
	case p.Status == string(corev1.PodPending):
		return "◐", false
	default:
		return "●", false
	}
}

func usageText(v string) string {
	if v == "" || v == "n/a" {
		return "–"
	}
	return v
}
