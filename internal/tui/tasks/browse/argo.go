// Argo CD-specific browse machinery for §33a: the refresh, sync and
// dashboard-url verbs. Kept in its own file per browse's per-concern split
// (flux.go, deployments.go, nodes.go, helm.go, routes.go).
//
// There is deliberately no ↵ carve-out here, same reasoning as flux.go's
// own doc comment: an Argo descriptor keeps Custom: true, so
// crddetail.go's openSelectedObjectDetail already routes it to 14d's
// generic detail — §33a has no bespoke detail screen the way Flux's 31a
// does.
package browse

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// argoVerbsApply reports whether §33a's verbs are live on the current view:
// an Argo Application kind, a wired mutator, and a settled list. Keyed on
// the descriptor's own Argo flag, never a kind-name check — same shape as
// fluxVerbsApply.
func (m Model) argoVerbsApply() bool {
	return m.desc.Argo && m.mutator != nil && m.state == tui.TaskStateReady
}

// beginArgoRefresh stamps the refresh annotation on row. TierNone, so it
// executes immediately; the will-run line is set first so the command is
// on screen the moment the key lands, same as beginFluxReconcile.
func (m *Model) beginArgoRefresh(row resources.Row) tea.Cmd {
	m.execFeedback = kube.ArgoRefreshCommandString(m.kind, row.Namespace, row.Name)
	return m.actions.Begin(verbs.ArgoRefresh.Tier, tui.TaskAction{
		ID:    "argo-refresh-" + row.Name,
		Label: fmt.Sprintf("Refresh %s?", row.Name),
		Scope: tui.TaskScope{
			ResourceKind: string(m.kind), ResourceName: row.Name,
			Namespace: row.Namespace, Verb: "argo-refresh", IsMutating: true,
		},
	})
}

// beginArgoSync patches operation.sync.revision on row to its own
// already-configured target revision (argoSyncRevisionFromRow re-expands
// the row's own rendered REVISION cell, the same "read the cell instead of
// re-fetching the object" idiom fluxSourceRef uses) — a re-apply of what
// git already says to run, never a move to a different ref. TierInline:
// PROD escalates to the modal automatically via verbs.TierFor, same as
// beginRolloutRestart.
func (m *Model) beginArgoSync(row resources.Row) tea.Cmd {
	revision := argoSyncRevisionFromRow(row)
	tier := verbs.TierFor(verbs.ArgoSync, m.isProd())
	return m.actions.Begin(tier, tui.TaskAction{
		ID:    "argo-sync-" + row.Name,
		Label: fmt.Sprintf("Sync %s?", row.Name),
		Scope: tui.TaskScope{
			ResourceKind: string(m.kind), ResourceName: row.Name,
			Namespace: row.Namespace, Verb: "argo-sync", IsMutating: true,
			ArgoSyncRevision: revision,
		},
	})
}

// argoSyncRevisionFromRow re-expands a projected REVISION cell ("main@
// e41b90c", or bare "HEAD" before the first sync) back into the target ref
// argoRevisionCell built it from — the part before "@", or the whole cell
// when there's no SHA yet.
func argoSyncRevisionFromRow(row resources.Row) string {
	if len(row.Cells) < 4 {
		return "HEAD"
	}
	ref, _, _ := strings.Cut(row.Cells[3], "@")
	return ref
}

// argoSyncWillRunLine is beginArgoSync's confirm "will run: ..." line —
// same idiom as rolloutRestartWillRunLine (deployments.go).
func argoSyncWillRunLine(scope tui.TaskScope) string {
	return "will run: " + kube.ArgoSyncCommandString(
		kube.ResourceKind(scope.ResourceKind), scope.Namespace, scope.ResourceName, scope.ArgoSyncRevision)
}

// copySelectedArgoDashboardURL builds §33a's 'u' deep link from argocd-cm's
// own configured base URL (the same ConfigMap argocd-server itself reads
// its "url" key from) plus the row's namespace/name, and copies it to the
// clipboard — a local write, not a cluster call the mutator seam needs to
// know about, same shape as copySelectedForwardURL. Sets execFeedback
// instead of copying when argocd-cm or its url key isn't found, so the key
// still answers rather than silently doing nothing.
func (m *Model) copySelectedArgoDashboardURL() tea.Cmd {
	row, ok := m.selectedRow()
	if !ok || m.lister == nil {
		return nil
	}
	base, ok := argoDashboardBaseURL(m.session.ClusterContext(), m.lister, row.Namespace)
	if !ok {
		m.execFeedback = "dashboard url not configured — argocd-cm has no url key in " + row.Namespace
		return nil
	}
	url := strings.TrimRight(base, "/") + "/applications/" + row.Namespace + "/" + row.Name
	return tea.SetClipboard(url)
}

// argoDashboardBaseURL reads argocd-cm's own "url" data key from namespace
// — Argo CD's control-plane ConfigMaps conventionally live in the same
// namespace as the Applications they manage, the same assumption the demo
// fixtures and §33a's own mockup make.
func argoDashboardBaseURL(ctx context.Context, lister resources.RawLister, namespace string) (string, bool) {
	objs, err := lister.ListRaw(ctx, kube.KindConfigMap, namespace)
	if err != nil {
		return "", false
	}
	for _, obj := range objs {
		cm, ok := obj.(*corev1.ConfigMap)
		if !ok || cm.Name != "argocd-cm" {
			continue
		}
		if url := cm.Data["url"]; url != "" {
			return url, true
		}
		return "", false
	}
	return "", false
}

// argoKeybarGroup is §33a's keybar block.
func (m Model) argoKeybarGroup() []tui.KeyHint {
	hints := []tui.KeyHint{
		{Key: verbs.ArgoRefresh.Key, Label: verbs.ArgoRefresh.Label},
		{Key: verbs.ArgoSync.Key, Label: verbs.ArgoSync.Label},
	}
	if m.lister != nil {
		hints = append(hints, tui.KeyHint{Key: verbs.ArgoURL.Key, Label: verbs.ArgoURL.Label})
	}
	return hints
}
