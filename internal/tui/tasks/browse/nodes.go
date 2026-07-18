// Node-specific browse machinery for 11a (docs/design README.md §11a):
// live cluster/node CPU/MEM bars, PODS counts, version-skew flagging, the
// cordon/drain verbs, and 11b's node-detail push. Kept in its own file
// (browse's per-concern split, like sort.go/grouping.go/metrics.go) rather
// than sprinkled through model.go/view.go/update.go.
package browse

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	corev1 "k8s.io/api/core/v1"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/components"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// NodeMetricsReader is the live node-usage seam browse needs for 11a's
// CPU/MEM mini-bars and the health strip's "cluster cpu/mem %" — satisfied
// by *kube.Cluster and *fake.Cluster.
type NodeMetricsReader interface {
	NodeMetrics(ctx context.Context) (map[string]kube.NodeMetric, error)
}

// nodeCapacity is one node's schedulable capacity (Status.Allocatable) —
// the PODS/CPU/MEM bar denominators. resources.Row only carries display
// Cells, so this rides alongside rowsLoadedMsg the same way podMetrics does
// for Pods.
type nodeCapacity struct {
	cpuMilli int64
	memBytes int64
	pods     int64
}

// nodeMetricsLoadedMsg carries one Nodes-kind metrics poll's result. epoch
// guards it the same way podMetricsLoadedMsg guards the Pods poll.
type nodeMetricsLoadedMsg struct {
	epoch   int
	metrics map[string]kube.NodeMetric
	err     error
}

// loadMetricsCmd dispatches Init/resetAndLoad/metricsTickMsg's poll to
// whichever kind's loader applies — Pods' PodMetricsByNamespace or Nodes'
// NodeMetrics — keeping the epoch/tick scaffolding (metrics.go) shared
// across both.
func (m Model) loadMetricsCmd(epoch int) tea.Cmd {
	if m.kind == kube.KindNode {
		return m.loadNodeMetrics(epoch)
	}
	return m.loadMetrics(epoch)
}

func (m Model) loadNodeMetrics(epoch int) tea.Cmd {
	reader := m.nodeMetricsSrc
	timeout := m.timeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		result, err := reader.NodeMetrics(ctx)
		return nodeMetricsLoadedMsg{epoch: epoch, metrics: result, err: err}
	}
}

// loadNodeExtras re-lists raw Node/Pod objects for data resources.Row can't
// carry: each node's Allocatable capacity, how many non-terminal pods are
// currently scheduled on it (kubectl describe node's convention), and the
// cluster-wide non-terminal pod total for the health strip's right side.
func loadNodeExtras(ctx context.Context, lister resources.RawLister) (map[string]nodeCapacity, map[string]int, int) {
	capacity := map[string]nodeCapacity{}
	if objs, err := lister.ListRaw(ctx, kube.KindNode, ""); err == nil {
		for _, obj := range objs {
			n, ok := obj.(*corev1.Node)
			if !ok {
				continue
			}
			cap := nodeCapacity{}
			if q, found := n.Status.Allocatable[corev1.ResourceCPU]; found {
				cap.cpuMilli = q.MilliValue()
			}
			if q, found := n.Status.Allocatable[corev1.ResourceMemory]; found {
				cap.memBytes = q.Value()
			}
			if q, found := n.Status.Allocatable[corev1.ResourcePods]; found {
				cap.pods = q.Value()
			}
			capacity[n.Name] = cap
		}
	}

	podCount := map[string]int{}
	total := 0
	if objs, err := lister.ListRaw(ctx, kube.KindPod, ""); err == nil {
		for _, obj := range objs {
			p, ok := obj.(*corev1.Pod)
			if !ok || p.Spec.NodeName == "" || nodeTerminalPod(p) {
				continue
			}
			podCount[p.Spec.NodeName]++
			total++
		}
	}
	return capacity, podCount, total
}

func nodeTerminalPod(p *corev1.Pod) bool {
	return p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed
}

// nodeMetricCell renders one Node row's CPU/MEM cell: a MiniBar against the
// node's real Allocatable capacity (unlike Pod's busiest-visible-pod
// relative bar, this is a true request/limit-style zone bar — Warn ≥70%,
// Bad at/over capacity), then the compact usage value — "–" for both while
// metrics haven't loaded.
func (m Model) nodeMetricCell(name string, cpu bool, st rowCellStyles) components.Cell {
	const barWidth = 6
	valWidth := resources.MetricColumnWidth - barWidth - 1

	cap := m.nodeCapacity[name]
	denom := cap.memBytes
	if cpu {
		denom = cap.cpuMilli
	}

	nm, ok := m.nodeMetrics[name]
	value, used := "–", int64(0)
	if ok {
		if cpu {
			value, used = nm.CPU, nm.CPUMilli
		} else {
			value, used = nm.MEM, nm.MemBytes
		}
		if value == "" || value == "n/a" {
			value = "–"
		}
	} else {
		denom = 0 // metrics not loaded yet: MiniBar renders "–" for denom<=0
	}
	valText := st.dim.Render(" " + components.Truncate(value, valWidth))
	return components.Cell{Text: components.MiniBar(used, denom, barWidth, st.bars) + valText}
}

// nodePodsCell renders the PODS column: "62/110" against the node's
// Allocatable pod capacity, or a bare count when capacity is unknown.
func (m Model) nodePodsCell(name string) string {
	cap, ok := m.nodeCapacity[name]
	if !ok || cap.pods == 0 {
		return fmt.Sprintf("%d", m.podCountByNode[name])
	}
	return fmt.Sprintf("%d/%d", m.podCountByNode[name], cap.pods)
}

// nodeVersionColumn finds Version's position within a Node row's Cells, so
// version-skew flagging doesn't hardcode the column order.
func nodeVersionColumn(desc resources.Descriptor) int {
	for i, title := range desc.Columns {
		if title == "Version" {
			return i
		}
	}
	return -1
}

// nodeMajorityVersion is the most common KubeletVersion across the loaded
// rows — 11a's "quiet yellow ▲" flags any node whose version differs from
// it. Empty when Version isn't a column or no rows carry one.
func (m Model) nodeMajorityVersion() string {
	idx := nodeVersionColumn(m.desc)
	if idx < 0 {
		return ""
	}
	counts := map[string]int{}
	for _, r := range m.rows {
		if idx < len(r.Cells) && r.Cells[idx] != "" {
			counts[r.Cells[idx]]++
		}
	}
	best, bestN := "", 0
	for v, n := range counts {
		if n > bestN {
			best, bestN = v, n
		}
	}
	return best
}

// nodeSummaryText is 11a's health-strip right side: "5 nodes · 125 pods ·
// cluster cpu 46% · mem 71%" — the cluster cpu/mem clause is omitted
// entirely until the first metrics poll lands (no metrics-server installed
// degrades to just the node/pod counts).
func (m Model) nodeSummaryText() string {
	text := fmt.Sprintf("%d nodes · %d pods", len(m.rows), m.clusterPodTotal)
	if cpuPct, memPct, ok := m.clusterUsagePct(); ok {
		text += fmt.Sprintf(" · cluster cpu %d%% · mem %d%%", cpuPct, memPct)
	}
	return text
}

func (m Model) clusterUsagePct() (cpuPct, memPct int, ok bool) {
	if len(m.nodeMetrics) == 0 {
		return 0, 0, false
	}
	var usedCPU, usedMem, capCPU, capMem int64
	for name, cap := range m.nodeCapacity {
		capCPU += cap.cpuMilli
		capMem += cap.memBytes
		if nm, found := m.nodeMetrics[name]; found {
			usedCPU += nm.CPUMilli
			usedMem += nm.MemBytes
		}
	}
	if capCPU == 0 || capMem == 0 {
		return 0, 0, false
	}
	return int(usedCPU * 100 / capCPU), int(usedMem * 100 / capMem), true
}

// beginCordon toggles row's schedulable state — TierNone (verbs.Cordon), so
// actions.Controller.Begin executes immediately with no confirmation.
func (m *Model) beginCordon(row resources.Row) tea.Cmd {
	verb, label := "cordon", fmt.Sprintf("Cordon %s?", row.Name)
	if row.Cordoned {
		verb, label = "uncordon", fmt.Sprintf("Uncordon %s?", row.Name)
	}
	return m.actions.Begin(verbs.Cordon.Tier, tui.TaskAction{
		ID:    "node-" + verb + "-" + row.Name,
		Label: label,
		Scope: tui.TaskScope{ResourceKind: string(kube.KindNode), ResourceName: row.Name, Verb: verb, IsMutating: true},
	})
}

// beginDrain confirms draining row — TierModal (verbs.Drain) — showing how
// many pods the cache says will be evicted.
func (m *Model) beginDrain(row resources.Row) tea.Cmd {
	count := m.podCountByNode[row.Name]
	return m.actions.Begin(verbs.Drain.Tier, tui.TaskAction{
		ID:    "node-drain-" + row.Name,
		Label: fmt.Sprintf("Drain %s? %d pods will be evicted.", row.Name, count),
		Scope: tui.TaskScope{ResourceKind: string(kube.KindNode), ResourceName: row.Name, Verb: "drain", IsMutating: true},
	})
}

// confirmBody renders the drain (or any future TierModal verb) confirmation
// card in place of the normal table body while m.actions is Active — the
// minimal inline shape ahead of 8b's full type-the-name modal
// (components.ConfirmCard's doc comment).
func (m Model) confirmBody(width, height int) string {
	theme := m.Theme()
	title := "Confirm"
	detail := ""
	if pending := m.actions.Pending(); pending != nil {
		title = pending.Label
		if pending.Scope.Verb == "rollback" {
			// 18a: "shell out to helm with a will run line" — shown in the
			// PROD modal's detail slot alongside the confirm card.
			detail = rollbackDetail(pending.Scope)
		}
	}
	styles := components.ConfirmStyles{
		Border: lipgloss.NewStyle().Foreground(theme.ConfirmBorder).Background(theme.ConfirmHeaderBg),
		Title:  lipgloss.NewStyle().Foreground(theme.Text).Bold(true).Background(theme.ConfirmHeaderBg),
		Detail: lipgloss.NewStyle().Foreground(theme.TextSecondary).Background(theme.ConfirmHeaderBg),
		Rule:   lipgloss.NewStyle().Foreground(theme.TextGhost).Background(theme.ConfirmHeaderBg),
		Key:    lipgloss.NewStyle().Foreground(theme.Accent).Background(theme.ConfirmHeaderBg),
		Label:  lipgloss.NewStyle().Foreground(theme.TextDim).Background(theme.ConfirmHeaderBg),
	}
	return components.ConfirmCard(title, detail, styles, width, height)
}

// openSelectedNodeDetail pushes 11b for the selected Node row. ok is false
// when nodedetail isn't wired or nothing's selected, mirroring
// openSelectedLogs' contract so 'enter' stays a no-op rather than pushing a
// broken screen.
func (m Model) openSelectedNodeDetail() (tea.Model, tea.Cmd, bool) {
	if m.openNodeDetail == nil || m.kind != kube.KindNode {
		return nil, nil, false
	}
	row, ok := m.selectedRow()
	if !ok {
		return nil, nil, false
	}
	task, cmd := m.openNodeDetail(row.Name, m.width, m.height)
	return task, cmd, task != nil
}
