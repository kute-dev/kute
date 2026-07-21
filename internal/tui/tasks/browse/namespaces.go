// Namespaces-kind browse machinery: the ↵ "select this namespace and open
// its pods" shortcut. Kept in its own file, browse's per-concern split
// convention (like deployments.go/nodes.go) rather than sprinkled through
// model.go/update.go.
package browse

import (
	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/state"
)

// openSelectedNamespacePods handles ↵ on a Namespaces-kind row: makes the
// selected row's namespace the active one — the same effect ↵ has in the
// "n" namespace palette (tui/namespace.go's namespaceDispatch), including
// recording it in that context's namespace recents — and switches kind to
// Pods in the same step, since remaining on the (cluster-scoped)
// Namespaces list after switching would show nothing useful. Mirrors
// goToResource's combined kind+namespace change (one resetAndLoad, not
// two) rather than chaining switchNamespace then switchKind.
func (m *Model) openSelectedNamespacePods() (tea.Cmd, bool) {
	if m.kind != kube.KindNamespace || m.session == nil {
		return nil, false
	}
	row, ok := m.selectedRow()
	if !ok {
		return nil, false
	}
	desc, ok := m.session.Registry.Descriptor(kube.KindPod)
	if !ok {
		return nil, false
	}
	m.pushRecentNamespace(row.Name)
	m.clearOrigin()
	m.kind = kube.KindPod
	m.desc = desc
	m.session.Location.Kind = kube.KindPod
	m.namespace = row.Name
	m.session.Location.Namespace = row.Name
	return m.resetAndLoad(), true
}

// pushRecentNamespace records namespace as the active context's most recent
// namespace (state.PerContext[ctx].RecentNamespaces) — mirrors tui/
// namespace.go's own unexported pushRecentNamespace, duplicated here since
// it's unexported in a different package.
func (m *Model) pushRecentNamespace(namespace string) {
	if m.session == nil || namespace == "" {
		return
	}
	ctx := m.session.Location.Context
	pc := m.session.State.PerContext[ctx]
	pc.RecentNamespaces = state.PushRecent(pc.RecentNamespaces, namespace)
	m.session.State.PerContext[ctx] = pc
}
