// Ingress/Gateway-API browse machinery for 23a/23b (docs/design
// README.md): ↵ on an Ingress or a discovered Gateway API kind's row pushes
// tasks/routetable instead of falling through to generic object detail.
// Kept in its own file, browse's per-concern split convention (like
// deployments.go/nodes.go/crddetail.go).
package browse

import (
	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
)

// isRouteKind reports whether kind gets the bespoke routing table on ↵
// instead of the generic 14d object detail (or, for Ingress, no ↵ at all
// until now) — Ingress plus every Gateway API kind routetable understands.
func isRouteKind(kind kube.ResourceKind) bool {
	switch kind {
	case kube.KindIngress, kube.KindHTTPRoute, kube.KindGRPCRoute, kube.KindTCPRoute, kube.KindGateway:
		return true
	}
	return false
}

// openSelectedRouteTable pushes tasks/routetable for the selected row of an
// Ingress or discovered Gateway API kind. ok is false when the current kind
// isn't route-shaped, the hook isn't wired, or nothing's selected — the same
// shape as browse's other openSelected* routing helpers.
func (m Model) openSelectedRouteTable() (tea.Model, tea.Cmd, bool) {
	if !isRouteKind(m.kind) || m.openRouteTable == nil {
		return nil, nil, false
	}
	row, ok := m.selectedRow()
	if !ok {
		return nil, nil, false
	}
	task, cmd := m.openRouteTable(m.kind, row.Namespace, row.Name, m.width, m.height)
	return task, cmd, task != nil
}
