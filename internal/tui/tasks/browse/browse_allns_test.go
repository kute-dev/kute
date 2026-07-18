package browse

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// TestAllNamespacesGroupsRowsByNamespaceUnhealthyFirst covers 6b's core
// triage layout (docs/design README.md §6b): rows group by namespace, and
// each group sorts unhealthy-first internally rather than one global
// health sort mixing namespaces together.
func TestAllNamespacesGroupsRowsByNamespaceUnhealthyFirst(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {
			pod("prod", "api-1"),
			crashPod("prod", "api-2"),
			pod("staging", "worker-1"),
		},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m = step(t, m, tui.SwitchNamespaceMsg{Namespace: ""})

	if m.namespace != "" || !m.grouped() {
		t.Fatalf("expected all-namespaces (grouped) mode, namespace=%q grouped=%v", m.namespace, m.grouped())
	}
	if len(m.visible) != 3 {
		t.Fatalf("visible = %d, want 3", len(m.visible))
	}
	// prod sorts before staging (namespace order), and within prod the
	// crashlooping pod sorts before the healthy one.
	if got := []string{m.visible[0].row.Name, m.visible[1].row.Name, m.visible[2].row.Name}; got[0] != "api-2" || got[1] != "api-1" || got[2] != "worker-1" {
		t.Fatalf("visible order = %v, want [api-2 api-1 worker-1]", got)
	}

	view := plain(m.Render())
	for _, want := range []string{"prod", "staging", "all namespaces"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in the grouped view:\n%s", want, view)
		}
	}
}

// TestAllNamespacesHealthyGroupsSortLast covers 6b's namespace-group order:
// fully-healthy namespaces sort after every namespace with trouble,
// regardless of alphabetical order — "aaa-healthy" would sort first
// alphabetically, but must land after "zzz-trouble" here since it has
// nothing to triage.
func TestAllNamespacesHealthyGroupsSortLast(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {
			pod("aaa-healthy", "api-1"),
			crashPod("zzz-trouble", "api-2"),
		},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m = step(t, m, tea.KeyPressMsg{Text: "a"})

	if len(m.display) < 2 {
		t.Fatalf("expected at least 2 display entries, got %d", len(m.display))
	}
	if m.display[0].namespace != "zzz-trouble" {
		t.Fatalf("expected the troubled namespace first, got display[0].namespace = %q", m.display[0].namespace)
	}
	last := m.display[len(m.display)-1]
	if last.namespace != "aaa-healthy" || last.kind != rowKindCollapsedSummary {
		t.Fatalf("expected the healthy namespace last as a collapsed summary, got %+v", last)
	}
}

// TestNKeyJumpsFromCollapsedSummaryLine covers "N" working from a
// fully-collapsed group's own summary line (unlike row-scoped verbs, which
// stay gated on selectedRow — a namespace jump only ever needs the
// namespace name, which every displayRow kind carries).
func TestNKeyJumpsFromCollapsedSummaryLine(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("staging", "worker-1")},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m = step(t, m, tea.KeyPressMsg{Text: "a"})

	if _, ok := m.selectedRow(); ok {
		t.Fatalf("expected the collapsed summary line selected, not a data row")
	}
	m = step(t, m, tea.KeyPressMsg{Text: "N"})

	if m.grouped() {
		t.Fatalf("expected 'N' to leave all-namespaces mode even from the collapsed summary line")
	}
	if m.namespace != "staging" {
		t.Fatalf("namespace = %q, want %q", m.namespace, "staging")
	}
}

// TestAKeyEntersAllNamespacesMode covers the 'a' shortcut (verbs.AllNamespaces).
func TestAKeyEntersAllNamespacesMode(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-1"), pod("prod", "api-2")},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "a"})

	if !m.grouped() {
		t.Fatalf("expected 'a' to enter all-namespaces mode")
	}
	if len(m.visible) != 2 {
		t.Fatalf("visible = %d, want 2 (both namespaces)", len(m.visible))
	}
}

// TestNKeyJumpsIntoSelectedPodNamespace covers 6b's "N" — jump into the
// selected row's namespace without leaving through the palette. Both pods
// are healthy, so their namespace groups render collapsed by default (no
// directly selectable row) — "tab" expands the first group so there's an
// actual row for "N" to act on, matching the rule every row-scoped verb
// follows post-collapse: only a directly displayed row is selectable.
func TestNKeyJumpsIntoSelectedPodNamespace(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-1"), pod("prod", "api-2")},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m = step(t, m, tea.KeyPressMsg{Text: "a"})
	m = step(t, m, tea.KeyPressMsg{Text: "tab"})

	row, ok := m.selectedRow()
	if !ok {
		t.Fatalf("expected a selected row in all-namespaces mode")
	}
	target := row.Namespace

	m = step(t, m, tea.KeyPressMsg{Text: "N"})

	if m.grouped() {
		t.Fatalf("expected 'N' to leave all-namespaces mode for the selected pod's namespace")
	}
	if m.namespace != target {
		t.Fatalf("namespace = %q, want %q", m.namespace, target)
	}
}

// TestClusterScopedKindNeverGroups covers the guard excluding cluster-scoped
// kinds: switching to Nodes while namespace == "" (carried over from
// all-namespaces mode on the prior kind — switchKind never touches
// namespace) must not render grouped, since a cluster-scoped kind has no
// namespace to group by.
func TestClusterScopedKindNeverGroups(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod:  {pod("default", "api-1")},
		kube.KindNode: {},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m = step(t, m, tui.SwitchNamespaceMsg{Namespace: ""})
	m = step(t, m, tui.GotoKindMsg{Kind: kube.KindNode})

	if m.namespace != "" {
		t.Fatalf("expected namespace to stay \"\" across the kind switch, got %q", m.namespace)
	}
	if m.grouped() {
		t.Fatalf("cluster-scoped kinds must never render grouped, even with namespace == %q", m.namespace)
	}
}

// TestAllNamespacesSelectionScrollsIntoView is TestSelectionScrollsIntoView's
// grouped-mode counterpart: header/fold lines consume viewport slots too, so
// clampOffset must account for them or the selected line can render
// off-screen despite browse believing it's within view. Each namespace gets
// one crashlooping pod plus three healthy ones so every group renders
// collapsed-but-partial (header + the crashloop row + a fold line) —
// exercising the same interleaving the old all-rows-shown version of this
// test covered, now through 6b's default-collapsed rendering.
func TestAllNamespacesSelectionScrollsIntoView(t *testing.T) {
	var objs []runtime.Object
	for _, ns := range []string{"ns-a", "ns-b", "ns-c"} {
		objs = append(objs, crashPod(ns, ns+"-pod-1"))
		for _, name := range []string{"pod-2", "pod-3", "pod-4"} {
			objs = append(objs, pod(ns, ns+"-"+name))
		}
	}
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindPod: objs}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 11) // body 5 → table header + footer + 3 data-row slots
	m = step(t, m, m.Init()())
	m = step(t, m, tea.KeyPressMsg{Text: "a"})

	for range 11 {
		m = step(t, m, tea.KeyPressMsg{Text: "j"})
	}

	var selectedLine string
	for line := range strings.SplitSeq(plain(m.Render()), "\n") {
		if strings.HasPrefix(line, "▎") {
			selectedLine = line
		}
	}
	if selectedLine == "" {
		t.Fatalf("expected a selected (▎-prefixed) line on screen after scrolling:\n%s", plain(m.Render()))
	}
	if want := m.selectedName(); want != "" && !strings.Contains(selectedLine, want) {
		t.Fatalf("selected row %q must be inside the rendered viewport, got selection line %q in:\n%s", want, selectedLine, plain(m.Render()))
	}
}

// TestAllNamespacesCollapsedByDefault covers 6b's triage-default folding: a
// fully-healthy namespace collapses to one grayed-out summary line with none
// of its pods individually shown, while a namespace with a crashlooping pod
// keeps that pod visible and folds only the healthy remainder into a
// "+N running · ↹ expand" tail.
func TestAllNamespacesCollapsedByDefault(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {
			pod("prod", "api-1"),
			crashPod("prod", "api-2"),
			pod("staging", "worker-1"),
		},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m = step(t, m, tea.KeyPressMsg{Text: "a"})

	view := plain(m.Render())
	if !strings.Contains(view, "▸ staging · 1 pods · all running") {
		t.Fatalf("expected staging to collapse to a single all-running summary line:\n%s", view)
	}
	if strings.Contains(view, "worker-1") {
		t.Fatalf("expected staging's pod to stay folded away, got it rendered:\n%s", view)
	}
	if !strings.Contains(view, "api-2") {
		t.Fatalf("expected prod's crashlooping pod to stay visible:\n%s", view)
	}
	if strings.Contains(view, "api-1") {
		t.Fatalf("expected prod's healthy pod to fold away by default, got it rendered:\n%s", view)
	}
	if !strings.Contains(view, "+ 1 running") || !strings.Contains(view, "expand") {
		t.Fatalf("expected a '+1 running · ↹ expand' fold line for prod's healthy remainder:\n%s", view)
	}
}

// TestToggleGroupExpandsAndCollapses covers 6b's "tab": expanding a
// collapsed group reveals every one of its rows, and tab again puts it back
// to the triage default.
func TestToggleGroupExpandsAndCollapses(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {
			pod("prod", "api-1"),
			crashPod("prod", "api-2"),
		},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m = step(t, m, tea.KeyPressMsg{Text: "a"})

	if strings.Contains(plain(m.Render()), "api-1") {
		t.Fatalf("expected api-1 folded away before expanding:\n%s", plain(m.Render()))
	}

	m = step(t, m, tea.KeyPressMsg{Text: "tab"})
	view := plain(m.Render())
	if !strings.Contains(view, "api-1") || !strings.Contains(view, "api-2") {
		t.Fatalf("expected both pods visible once the group is expanded:\n%s", view)
	}
	if strings.Contains(view, tui.GlyphTab) {
		// The fold line embeds the ↹ glyph inline; the keybar's own
		// ToggleGroup hint renders the literal word "tab" instead, so this
		// only matches an actual (unwanted, post-expand) fold line.
		t.Fatalf("expected no fold line once the group is fully expanded:\n%s", view)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "tab"})
	view = plain(m.Render())
	if strings.Contains(view, "api-1") {
		t.Fatalf("expected api-1 to fold away again after collapsing back:\n%s", view)
	}
}

// TestFullyCollapsedGroupSelectableAndExpandable covers the case a fully
// folded group (no rowKindData entries at all) still needs: the summary
// line itself must be a reachable stop for j/k, and tab there must expand
// it — otherwise a fully-healthy namespace would be permanently stuck
// collapsed with no way to see its pods.
func TestFullyCollapsedGroupSelectableAndExpandable(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-1")},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m = step(t, m, tea.KeyPressMsg{Text: "a"})

	if _, ok := m.selectedRow(); ok {
		t.Fatalf("expected no selectable data row while the only group is fully collapsed")
	}
	var selectedLine string
	for line := range strings.SplitSeq(plain(m.Render()), "\n") {
		if strings.HasPrefix(line, "▎") {
			selectedLine = line
		}
	}
	if !strings.Contains(selectedLine, "all running") {
		t.Fatalf("expected the collapsed summary line itself to be the selected line, got %q", selectedLine)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "tab"})
	if !strings.Contains(plain(m.Render()), "api-1") {
		t.Fatalf("expected tab on the summary line to expand and reveal api-1:\n%s", plain(m.Render()))
	}
}

// TestFoldLineVerbNoOp covers the flip side of gating every row-scoped verb
// on selectedRow(): resting on a fold/collapsed-summary line must not let
// ctrl-d act on some arbitrary underlying pod the user never saw named.
func TestFoldLineVerbNoOp(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-1")},
	}}
	mut := &fakeMutator{}
	m := New(Config{Session: newSession(), Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m = step(t, m, tea.KeyPressMsg{Text: "a"})

	if _, ok := m.selectedRow(); ok {
		t.Fatalf("expected the collapsed summary line selected, not a data row")
	}
	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+d"})
	if m.actions.Active() {
		t.Fatalf("expected ctrl-d to no-op while a fold/summary line is selected")
	}
}
