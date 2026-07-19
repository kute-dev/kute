package browse

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// TestGotoKindMsgSwitchesKindKeepingNamespace covers the jump palette's
// kind-result Enter (mvp-plan.md Phase 2): kind changes in place, namespace
// doesn't, and the new kind's rows load.
func TestGotoKindMsgSwitchesKindKeepingNamespace(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod:       {pod("default", "api-1")},
		kube.KindConfigMap: {configMap("default", "app-config")},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	if m.kind != kube.KindPod {
		t.Fatalf("kind = %s, want Pod before switching", m.kind)
	}

	m = step(t, m, tui.GotoKindMsg{Kind: kube.KindConfigMap})

	if m.kind != kube.KindConfigMap {
		t.Fatalf("kind = %s, want ConfigMap after GotoKindMsg", m.kind)
	}
	if m.namespace != "default" {
		t.Fatalf("namespace = %q, want unchanged %q", m.namespace, "default")
	}
	if m.state != tui.TaskStateReady {
		t.Fatalf("state = %s, want ready", m.state)
	}
	row, ok := m.selectedRow()
	if !ok || row.Name != "app-config" {
		t.Fatalf("selectedRow() = %+v, %v, want app-config", row, ok)
	}
}

// TestGotoKindMsgSameKindIsNoop mirrors browse's own guard: re-selecting the
// kind already showing shouldn't blow away the loaded rows/selection.
func TestGotoKindMsgSameKindIsNoop(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-1"), pod("default", "api-2")},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	rowsBefore := len(m.rows)

	m = step(t, m, tui.GotoKindMsg{Kind: kube.KindPod})

	if m.state != tui.TaskStateReady || len(m.rows) != rowsBefore {
		t.Fatalf("re-selecting the active kind should be a no-op, got state=%s rows=%d", m.state, len(m.rows))
	}
}

// TestGotoKindMsgWithFilterAppliesQuery pins 23b's routetable→browse jump
// (docs/design README.md:292: "↵ on a listener filters to attached
// routes"): a GotoKindMsg with a non-empty Filter must switch kind and
// apply that filter query — switchKind's own resetAndLoad would otherwise
// clear it, which is right for a bare kind switch but wrong for this one.
func TestGotoKindMsgWithFilterAppliesQuery(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod:       {pod("default", "api-1")},
		kube.KindConfigMap: {configMap("default", "app-config"), configMap("default", "other-config")},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tui.GotoKindMsg{Kind: kube.KindConfigMap, Filter: "app-"})

	if m.kind != kube.KindConfigMap {
		t.Fatalf("kind = %s, want ConfigMap", m.kind)
	}
	if !m.filterActive || m.filterQuery != "app-" {
		t.Fatalf("filterActive=%v filterQuery=%q, want true/\"app-\"", m.filterActive, m.filterQuery)
	}
	view := plain(m.Render())
	if !strings.Contains(view, "app-config") {
		t.Fatalf("expected the filtered-in row in view:\n%s", view)
	}
	if strings.Contains(view, "other-config") {
		t.Fatalf("expected the filtered-out row absent from view:\n%s", view)
	}
}

// TestSwitchNamespaceMsgChangesNamespaceKeepsKind covers the jump palette's
// namespace-result Enter.
func TestSwitchNamespaceMsgChangesNamespaceKeepsKind(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {
			pod("default", "api-1"),
			pod("nva-stage", "worker-1"),
		},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tui.SwitchNamespaceMsg{Namespace: "nva-stage"})

	if m.namespace != "nva-stage" {
		t.Fatalf("namespace = %q, want nva-stage", m.namespace)
	}
	if m.kind != kube.KindPod {
		t.Fatalf("kind = %s, want unchanged Pod", m.kind)
	}
	row, ok := m.selectedRow()
	if !ok || row.Name != "worker-1" {
		t.Fatalf("selectedRow() = %+v, %v, want worker-1", row, ok)
	}
}

// TestSwitchNamespaceMsgPreservesFilter pins the fix for "filter doesn't
// survive a namespace switch" (mvp-tasks.md's known gap, docs/design
// README.md §6a: "switching keeps kind + filter"): resetAndLoad normally
// clears the filter along with everything else per-view, but switchNamespace
// snapshots it first and restores it after, so an active "/" query keeps
// narrowing the list in the new namespace too.
func TestSwitchNamespaceMsgPreservesFilter(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {
			pod("default", "api-1"),
			pod("default", "worker-1"),
			pod("nva-stage", "api-2"),
			pod("nva-stage", "worker-2"),
		},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
	m = step(t, m, tea.KeyPressMsg{Code: 'a', Text: "api"})
	if !m.filterActive || m.filterQuery != "api" {
		t.Fatalf("filterActive=%v filterQuery=%q, want active with 'api'", m.filterActive, m.filterQuery)
	}

	m = step(t, m, tui.SwitchNamespaceMsg{Namespace: "nva-stage"})

	if m.namespace != "nva-stage" {
		t.Fatalf("namespace = %q, want nva-stage", m.namespace)
	}
	if !m.filterActive || m.filterQuery != "api" {
		t.Fatalf("filter not preserved across namespace switch: filterActive=%v filterQuery=%q, want active with 'api'", m.filterActive, m.filterQuery)
	}
	if len(m.visible) != 1 || m.visible[0].row.Name != "api-2" {
		t.Fatalf("visible = %+v, want just api-2 (nva-stage's row matching 'api')", m.visible)
	}
}

// TestSwitchContextMsgRestoresMsgFilter pins the other half of "filter
// doesn't survive a switch" — 7a's context palette (docs/design README.md:
// "each context remembers its own namespace + kind + filter; switching
// restores them"). Unlike a namespace switch, a context switch doesn't
// preserve the outgoing filter verbatim: it applies whatever filter
// context.go's switchContextCmd resolved for the *target* context from
// state.PerContext and put on SwitchContextMsg.Filter — empty here, so an
// active filter from the old context must NOT leak into the new one.
func TestSwitchContextMsgRestoresMsgFilter(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-1")},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
	m = step(t, m, tea.KeyPressMsg{Code: 'a', Text: "api"})
	if !m.filterActive || m.filterQuery != "api" {
		t.Fatalf("filterActive=%v filterQuery=%q, want active with 'api'", m.filterActive, m.filterQuery)
	}

	m = step(t, m, tui.SwitchContextMsg{Context: "other-cluster", Namespace: "default", Kind: kube.KindPod})

	if m.filterActive || m.filterQuery != "" {
		t.Fatalf("filterActive=%v filterQuery=%q, want cleared (target context has no remembered filter)", m.filterActive, m.filterQuery)
	}
}

// TestSwitchContextMsgAppliesRestoredFilter is the positive case: a target
// context with a non-empty remembered filter (SwitchContextMsg.Filter,
// resolved from state.PerContext by switchContextCmd) comes back active and
// applied against the newly loaded rows.
func TestSwitchContextMsgAppliesRestoredFilter(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-1"), pod("default", "worker-1")},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tui.SwitchContextMsg{Context: "other-cluster", Namespace: "default", Kind: kube.KindPod, Filter: "api"})

	if !m.filterActive || m.filterQuery != "api" {
		t.Fatalf("filterActive=%v filterQuery=%q, want active with the restored 'api' filter", m.filterActive, m.filterQuery)
	}
	if len(m.visible) != 1 || m.visible[0].row.Name != "api-1" {
		t.Fatalf("visible = %+v, want just api-1", m.visible)
	}
}

// TestGotoResourceMsgSameKindSelectsWithoutReload covers jumping straight to
// a resource of the kind already showing — no reload needed, just a
// selection change.
func TestGotoResourceMsgSameKindSelectsWithoutReload(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {
			pod("default", "api-1"),
			pod("default", "api-2"),
		},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, cmd := m.Update(tui.GotoResourceMsg{Kind: kube.KindPod, Namespace: "default", Name: "api-2"})
	m = *updated.(*Model)
	if cmd != nil {
		t.Fatalf("expected no reload command for a same-kind resource jump")
	}
	row, ok := m.selectedRow()
	if !ok || row.Name != "api-2" {
		t.Fatalf("selectedRow() = %+v, %v, want api-2", row, ok)
	}
}

// TestGotoResourceMsgDifferentKindSwitchesThenSelects covers jumping to a
// resource of a kind other than the one showing: switch kind, then select
// the target once its rows land.
func TestGotoResourceMsgDifferentKindSwitchesThenSelects(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-1")},
		kube.KindConfigMap: {
			configMap("default", "app-config"),
			configMap("default", "other-config"),
		},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tui.GotoResourceMsg{Kind: kube.KindConfigMap, Namespace: "default", Name: "other-config"})

	if m.kind != kube.KindConfigMap {
		t.Fatalf("kind = %s, want ConfigMap", m.kind)
	}
	row, ok := m.selectedRow()
	if !ok || row.Name != "other-config" {
		t.Fatalf("selectedRow() = %+v, %v, want other-config", row, ok)
	}
}

// TestGotoResourceMsgDifferentNamespaceSwitchesThenSelects covers a
// cluster-wide jump (tasks/overview's TROUBLE/RECENT CHANGES rows, 19a) that
// lands on a resource of the kind already showing but in a different
// namespace than the one active — the namespace must switch too, or the
// target simply never appears in the freshly (still old-namespace-scoped)
// loaded rows.
func TestGotoResourceMsgDifferentNamespaceSwitchesThenSelects(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {
			pod("default", "api-1"),
			pod("other-ns", "worker-0"),
		},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tui.GotoResourceMsg{Kind: kube.KindPod, Namespace: "other-ns", Name: "worker-0"})

	if m.namespace != "other-ns" {
		t.Fatalf("namespace = %q, want other-ns", m.namespace)
	}
	if m.session.Location.Namespace != "other-ns" {
		t.Fatalf("Session.Location.Namespace = %q, want other-ns", m.session.Location.Namespace)
	}
	row, ok := m.selectedRow()
	if !ok || row.Name != "worker-0" {
		t.Fatalf("selectedRow() = %+v, %v, want worker-0", row, ok)
	}
}

// TestGotoResourceMsgDifferentKindAndNamespaceSwitchesBoth covers the same
// cluster-wide jump when the target is also a different kind (e.g. 19a's
// RECENT CHANGES rows land on a Deployment while browse shows Pods).
func TestGotoResourceMsgDifferentKindAndNamespaceSwitchesBoth(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-1")},
		kube.KindConfigMap: {
			configMap("other-ns", "app-config"),
		},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tui.GotoResourceMsg{Kind: kube.KindConfigMap, Namespace: "other-ns", Name: "app-config"})

	if m.kind != kube.KindConfigMap {
		t.Fatalf("kind = %s, want ConfigMap", m.kind)
	}
	if m.namespace != "other-ns" {
		t.Fatalf("namespace = %q, want other-ns", m.namespace)
	}
	row, ok := m.selectedRow()
	if !ok || row.Name != "app-config" {
		t.Fatalf("selectedRow() = %+v, %v, want app-config", row, ok)
	}
}

// TestGotoResourceMsgClusterScopedKindIgnoresNamespace covers a jump to a
// cluster-scoped kind (Nodes) — msg.Namespace is meaningless there and must
// never be applied, unlike the namespaced-kind cases above.
func TestGotoResourceMsgClusterScopedKindIgnoresNamespace(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod:  {pod("default", "api-1")},
		kube.KindNode: {nodeObj("node-a", true, false)},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tui.GotoResourceMsg{Kind: kube.KindNode, Namespace: "some-namespace-string", Name: "node-a"})

	if m.kind != kube.KindNode {
		t.Fatalf("kind = %s, want Node", m.kind)
	}
	if m.namespace != "default" {
		t.Fatalf("namespace = %q, want unchanged (default) for a cluster-scoped kind jump", m.namespace)
	}
}
