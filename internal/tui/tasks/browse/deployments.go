// Deployment-specific browse machinery for 9a (docs/design README.md §9a):
// the R rollout-restart verb and the ↵ "open this deployment's pods"
// shortcut. Kept in its own file, browse's per-concern split convention
// (like nodes.go/sort.go/grouping.go/delete.go) rather than sprinkled
// through model.go/view.go/update.go.
package browse

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// beginRolloutRestart restarts row's rollout — TierNone (verbs.RolloutRestart),
// so actions.Controller.Begin executes immediately with no confirmation
// (docs/design README.md §9a: "R rollout restart (non-destructive, no
// confirm)").
func (m *Model) beginRolloutRestart(row resources.Row) tea.Cmd {
	return m.actions.Begin(verbs.RolloutRestart.Tier, tui.TaskAction{
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

// openDeploymentPods switches kind to Pods with row's name pre-applied as
// the filter query (docs/design README.md §9a: "↵ = the deployment's pods
// ... not a new screen"): switchKind's resetAndLoad clears filterQuery
// synchronously before returning its load Cmd, so setting filterQuery right
// after is safe — it's still in place once the loaded rows reach
// recomputeVisible. Pod names conventionally start with the owning
// deployment's name (deploy-<rs-hash>-<pod-hash>), so the existing fuzzy
// filter already reads as an "owner match" without a second filter
// mechanism.
func (m *Model) openDeploymentPods(row resources.Row) tea.Cmd {
	cmd := m.switchKind(kube.KindPod)
	m.setFilter(row.Name)
	// switchKind's resetAndLoad clears originKind/originName along with
	// filterQuery, so they're set here for the same reason filterQuery is:
	// still in place once the loaded rows reach recomputeVisible.
	m.originKind, m.originName = kube.KindDeployment, row.Name
	return cmd
}

// backToOrigin reverses openDeploymentPods/openReleaseObjects: switches back
// to the origin kind and selects the row esc came from, via the same
// pendingSelect mechanism goToResource uses for a cross-kind jump.
func (m *Model) backToOrigin() tea.Cmd {
	m.pendingSelect = m.originName
	return m.switchKind(m.originKind)
}
