// Deployment-specific browse machinery for 9a (docs/design README.md §9a):
// the R rollout-restart verb (verbs.RolloutRestart) and the ↵ "open this
// deployment's pods" shortcut, plus StatefulSet's and DaemonSet's own ↵
// "open this workload's pods" (same recipe, no rollout-restart verb —
// neither StatefulSets nor DaemonSets have one). Kept in its own file,
// browse's per-concern split convention (like nodes.go/sort.go/grouping.go/
// delete.go) rather than sprinkled through model.go/view.go/update.go.
package browse

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// beginRolloutRestart restarts row's rollout — TierInline
// (verbs.RolloutRestart), so actions.Controller.Begin moves to the
// confirming state (inline y/N non-prod, escalated to the type-the-name
// modal in PROD, verbs.Resolve) rather than executing immediately.
func (m *Model) beginRolloutRestart(row resources.Row) tea.Cmd {
	return m.actions.Begin(actions.Request{Verb: verbs.RolloutRestart, Prod: m.isProd()}, tui.TaskAction{
		ID:    "rollout-restart-" + row.Namespace + "/" + row.Name,
		Label: fmt.Sprintf("Restart rollout for %s?", row.Name),
		Scope: tui.TaskScope{
			ResourceKind: string(kube.KindDeployment),
			ResourceName: row.Name,
			Namespace:    row.Namespace,
			Verb:         "rollout-restart",
			IsMutating:   true,
		},
	})
}

// rolloutRestartWillRunLine is the confirm's "will run: kubectl rollout
// restart ..." documentation line — same idiom as deleteWillRunLine/
// setImageWillRunLine.
func rolloutRestartWillRunLine(scope tui.TaskScope) string {
	return "will run: " + kube.RolloutRestartCommandString(kube.ResourceKind(scope.ResourceKind), scope.Namespace, scope.ResourceName)
}

// openDeploymentPods switches kind to Pods narrowed to the Deployment's own
// Pods (docs/design README.md §9a: "↵ = the deployment's pods ... not a new
// screen"), resolved through owner references (Deployment → ReplicaSet →
// Pod) rather than by name. It used to pre-fill the fuzzy filter with the
// Deployment's name, which also matched every sibling whose name contains
// the same letters in order — aim-bp-api's pods listed aim-bp-integration-
// api's too.
func (m *Model) openDeploymentPods(row resources.Row) tea.Cmd {
	return m.openWorkloadPods(kube.KindDeployment, row)
}

// openStatefulSetPods is openDeploymentPods's StatefulSet twin: the
// StatefulSet owns its Pods directly.
func (m *Model) openStatefulSetPods(row resources.Row) tea.Cmd {
	return m.openWorkloadPods(kube.KindStatefulSet, row)
}

// openDaemonSetPods is openDeploymentPods's DaemonSet twin: the DaemonSet
// owns its Pods directly, no intermediate ReplicaSet.
func (m *Model) openDaemonSetPods(row resources.Row) tea.Cmd {
	return m.openWorkloadPods(kube.KindDaemonSet, row)
}

func (m *Model) openWorkloadPods(kind kube.ResourceKind, row resources.Row) tea.Cmd {
	return m.openOwnedPods(kind, row, []kube.HelmReleasePodSource{{Kind: kind, Namespace: row.Namespace, Name: row.Name}})
}

// openOwnedPods switches to Pods narrowed to the Pods sources' controller
// chains own (kube.HelmReleasePodKeys, resolved from the informer caches in
// load()) — the one owner-scoped child view, shared by a Helm release's ↵
// and a workload's. The association is kept apart from the user's fuzzy
// filter, so '/' still narrows within it and the query box stays empty.
func (m *Model) openOwnedPods(originKind kube.ResourceKind, row resources.Row, sources []kube.HelmReleasePodSource) tea.Cmd {
	_ = m.switchKind(kube.KindPod)
	m.releasePodSources = sources
	m.releasePodView = true
	// resetAndLoad may have restored an ordinary Pods cached view before the
	// owner association was installed. It is not a valid preview of this
	// narrower view.
	m.rows, m.visible, m.display = nil, nil, nil
	m.cachedView = false
	// switchKind's resetAndLoad clears the origin, so it's set here, after.
	m.originKind, m.originName, m.originRowNamespace = originKind, row.Name, row.Namespace
	// switchKind created its load command before the association was
	// installed. Supersede it so the first Pods read cannot briefly use the
	// ordinary unfiltered path.
	m.reloadEpoch++
	m.metricsEpoch++
	cmds := []tea.Cmd{m.load(), m.spinner.Tick}
	if m.pollsMetrics() {
		cmds = append(cmds, m.loadMetricsCmd(m.metricsEpoch), m.scheduleMetricsTick(m.metricsEpoch))
	}
	return tea.Batch(cmds...)
}

// backToOrigin reverses openDeploymentPods/openStatefulSetPods/
// openDaemonSetPods/openReleaseObjects/openJobPods/openCronJobPods (jobs.go):
// switches back to the origin kind and selects the row esc came from, via
// the same pendingSelect mechanism goToResource uses for a cross-kind jump.
func (m *Model) backToOrigin() tea.Cmd {
	m.pendingSelect = rowRef{namespace: m.originRowNamespace, name: m.originName}
	if m.originWidened {
		m.namespace = m.originNamespace
		if m.session != nil {
			m.session.Location.Namespace = m.originNamespace
		}
	}
	return m.switchKind(m.originKind)
}
