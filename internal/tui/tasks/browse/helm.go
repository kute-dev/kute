// Helm-release-specific browse machinery for 18a (docs/design README.md):
// the ↵ "open this release's objects" shortcut (9a's recipe), 'v' values
// (pushes the read-only YAML viewer) and 'h' history (pushes
// tasks/helmhistory's revision rail), and the 'R' rollback verb (shells out
// to the real helm binary, 8b-style friction). Kept in its own file,
// browse's per-concern split convention (like nodes.go/deployments.go/
// forwards.go).
package browse

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// openReleaseObjects switches kind to Pods with row's release name
// pre-applied as the filter query — the same "owner match via the existing
// fuzzy filter" recipe openDeploymentPods (deployments.go) already
// established for 9a, reused verbatim for 18a's "↵ = objects in the release
// (filtered tables, 9a's recipe)": Helm-managed pod names conventionally
// carry the release name too (`<release>-<chart>-...`).
func (m *Model) openReleaseObjects(row resources.Row) tea.Cmd {
	cmd := m.switchKind(kube.KindPod)
	m.setFilter(row.Name)
	m.originKind, m.originName = kube.KindHelmRelease, row.Name
	return cmd
}

// openSelectedHelmValues pushes 18a's 'v' — the selected release's decoded
// values in the read-only YAML viewer. ok is false when values aren't
// wired, nothing's selected, or the release's full data hasn't loaded yet,
// so 'v' stays a no-op rather than pushing a broken screen.
func (m Model) openSelectedHelmValues() (tea.Model, tea.Cmd, bool) {
	if m.openHelmValues == nil || m.kind != kube.KindHelmRelease {
		return nil, nil, false
	}
	row, ok := m.selectedRow()
	if !ok {
		return nil, nil, false
	}
	release, ok := m.helmReleases[row.Name]
	if !ok {
		return nil, nil, false
	}
	task, cmd := m.openHelmValues(release, m.width, m.height)
	return task, cmd, task != nil
}

// openSelectedHelmHistory pushes 18a's 'h' — the selected release's full
// revision rail.
func (m Model) openSelectedHelmHistory() (tea.Model, tea.Cmd, bool) {
	if m.openHelmHistory == nil || m.kind != kube.KindHelmRelease {
		return nil, nil, false
	}
	row, ok := m.selectedRow()
	if !ok {
		return nil, nil, false
	}
	task, cmd := m.openHelmHistory(row.Namespace, row.Name, m.width, m.height)
	return task, cmd, task != nil
}

// beginRollback confirms rolling row back to its previous revision —
// verbs.Rollback's Tier (TierInline, escalated to TierModal in PROD by
// verbs.TierFor — "Rollback inherits 8b friction"). Executes through
// kube.Mutator.HelmRollback like every other write verb.
func (m *Model) beginRollback(row resources.Row) tea.Cmd {
	release := m.helmReleases[row.Name]
	toRevision := release.Revision - 1 // 0 once Revision is 1, which HelmRollback/HelmRollbackCommandString both read as "Helm's own default: the previous revision"
	tier := verbs.TierFor(verbs.Rollback, m.isProd())
	return m.actions.Begin(tier, tui.TaskAction{
		ID:    "rollback-" + row.Namespace + "/" + row.Name,
		Label: fmt.Sprintf("Rollback %s to revision %d?", row.Name, toRevision),
		Scope: tui.TaskScope{
			ResourceKind: string(kube.KindHelmRelease),
			ResourceName: row.Name,
			Namespace:    row.Namespace,
			Verb:         "rollback",
			IsMutating:   true,
			Revision:     toRevision,
		},
	})
}

// rollbackPrompt renders 18a's inline (non-PROD) confirm line — the
// generic actions.Controller.Prompt() plus the exact `helm rollback` command
// that will run (18a: "shell out to helm with a will run line").
func rollbackPrompt(scope tui.TaskScope) string {
	// Kept short deliberately: insetChromeLine (tui/chrome.go) drops the
	// whole RightNote rather than truncating it when the keybar line
	// overflows width, so this stays terser than Controller.Prompt()'s own
	// default "Rollback HelmRelease ns/name? (y) confirm (n) cancel" despite
	// carrying more information (the actual command).
	return kube.HelmRollbackCommandString(scope.Namespace, scope.ResourceName, scope.Revision)
}

// rollbackDetail renders the PROD modal's detail line (nodes.go's
// confirmBody) — the same "will run" command string, shown alongside the
// confirm card instead of the keybar's single line.
func rollbackDetail(scope tui.TaskScope) string {
	return "will run: " + kube.HelmRollbackCommandString(scope.Namespace, scope.ResourceName, scope.Revision)
}
