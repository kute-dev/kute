// Certificate-specific browse machinery for §35a (docs/design/
// v.0.7.0.dc.html): ↵ on a Certificate row pushes tasks/certchain instead of
// falling through to generic object detail. Kept in its own file, browse's
// per-concern split convention (like routes.go/flux.go).
package browse

import (
	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
)

// isCertificateKind reports whether kind gets §35a's bespoke chain screen on
// ↵ instead of the generic 14d object detail — a plain kind-name gate, the
// same shape routes.go's isRouteKind already takes for HTTPRoute/Gateway:
// Certificate is a discovered/generic kind with no curated Descriptor (§35a
// leaves EXPIRES/RENEWAL columns to §35b), so there's nothing on Descriptor
// to key off the way Flux's own .Flux flag does.
func isCertificateKind(kind kube.ResourceKind) bool {
	return kind == kube.KindCertificate
}

// openSelectedCertChain pushes tasks/certchain for the selected Certificate
// row. ok is false when the current kind isn't Certificate, the hook isn't
// wired, or nothing's selected — the same shape as browse's other
// openSelected* routing helpers.
func (m Model) openSelectedCertChain() (tea.Model, tea.Cmd, bool) {
	if !isCertificateKind(m.kind) || m.openCertChain == nil {
		return nil, nil, false
	}
	row, ok := m.selectedRow()
	if !ok {
		return nil, nil, false
	}
	task, cmd := m.openCertChain(row.Namespace, row.Name, m.width, m.height)
	return task, cmd, task != nil
}
