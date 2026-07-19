package tui_test

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
)

// gotoFakeLister is a minimal resources.RawLister fixture for the goto
// jump-palette data-building tests below — namespace-aware, since
// gotoCount/gotoResourceItems scope reads by namespace.
type gotoFakeLister struct {
	objs map[kube.ResourceKind][]runtime.Object
}

func (f gotoFakeLister) ListRaw(_ context.Context, kind kube.ResourceKind, namespace string) ([]runtime.Object, error) {
	all := f.objs[kind]
	if namespace == "" {
		return all, nil
	}
	var out []runtime.Object
	for _, o := range all {
		if acc, ok := o.(metav1.Object); ok && acc.GetNamespace() == namespace {
			out = append(out, o)
		}
	}
	return out, nil
}

func gotoTestPod(ns, name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c"}}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Ready: true}}},
	}
}

func gotoTestSession(lister resources.RawLister) *tui.Session {
	return &tui.Session{
		Registry: resources.DefaultRegistry(),
		Groups:   resources.DefaultGroups(),
		Location: tui.Location{Context: "microk8s-cluster", Namespace: "default", Kind: kube.KindPod},
		Theme:    tui.Dark(),
		Lister:   lister,
	}
}

// TestRootModelGBrowseRankedListHasChipsCountsAndFooter drives the whole
// root-shell path ('g' → 12a ranked list) end to end: aliased kinds' labels
// and live per-kind counts render, and the static alias footer shows
// (docs/design README.md §12a).
func TestRootModelGBrowseRankedListHasChipsCountsAndFooter(t *testing.T) {
	t.Parallel()
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {gotoTestPod("default", "api-1"), gotoTestPod("default", "api-2")},
	}}
	sess := gotoTestSession(lister)

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "g"})
	view := updated.(tui.Model).View().Content

	for _, want := range []string{
		"Pods",        // kind row
		"Deployments", // another aliased daily kind
		"alias — colored first letter · typing it pins that kind to rank 1", // 12a's static footer
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in the 12a ranked list:\n%s", want, view)
		}
	}
}

// TestRootModelGShowsGotoKeybarPill pins the cross-cutting fix (docs/design
// README.md §39: "Main keybar while open: GOTO mode pill + one-line
// explanation"): the underlying screen's own keybar pill must be replaced
// by a GOTO pill while the jump palette is open, not just dimmed to gray
// along with the rest of the background.
func TestRootModelGShowsGotoKeybarPill(t *testing.T) {
	t.Parallel()
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {gotoTestPod("default", "api-1")},
	}}
	sess := gotoTestSession(lister)

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "g"})
	view := updated.(tui.Model).View().Content

	if !strings.Contains(view, "GOTO") {
		t.Fatalf("expected the GOTO mode pill in the main keybar:\n%s", view)
	}
	if !strings.Contains(view, "jump to any kind, resource, namespace, or context") {
		t.Fatalf("expected the one-line explanation next to the pill:\n%s", view)
	}
}

// TestRootModelGThenTypeSwitchesToFuzzyResults covers 12a→2b: typing a
// multi-character query after 'g' switches from the ranked list to fuzzy
// results, which include both matching kinds and the current kind's
// resource names.
func TestRootModelGThenTypeSwitchesToFuzzyResults(t *testing.T) {
	t.Parallel()
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {gotoTestPod("default", "api-1")},
	}}
	sess := gotoTestSession(lister)

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "g"})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "a"})
	view := updated.(tui.Model).View().Content

	if !strings.Contains(view, "api-1") {
		t.Fatalf("expected the current kind's resource name in fuzzy results:\n%s", view)
	}
	if strings.Contains(view, "alias — colored first letter") {
		t.Fatalf("expected the 12a footer to be gone once typing starts ('a' isn't an alias):\n%s", view)
	}
}

// TestRootModelGThenTabCompletesToSelectedLabel covers 2b's advertised "tab
// complete" key: pressing tab fills the query in to the highlighted result's
// own label, so the input row itself gains a second, independent occurrence
// of that label (the first comes from the still-matching result row).
func TestRootModelGThenTabCompletesToSelectedLabel(t *testing.T) {
	t.Parallel()
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {},
	}}
	sess := gotoTestSession(lister)

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "g"})
	for _, ch := range []string{"e", "p", "l"} {
		updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: ch})
	}
	before := updated.(tui.Model).View().Content
	beforeCount := strings.Count(before, "Deployments")

	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Code: tea.KeyTab})
	after := updated.(tui.Model).View().Content
	afterCount := strings.Count(after, "Deployments")

	if afterCount <= beforeCount {
		t.Fatalf("expected tab to complete the query to the selected label (Deployments occurrences %d -> %d):\nbefore:\n%s\nafter:\n%s", beforeCount, afterCount, before, after)
	}
}

// TestRootModelGThenAliasLetterPinsMatchAndConfirmsFooter covers 12b: typing
// a single alias character pins the aliased kind to rank 1 (highlighted
// first letter + "alias match" label) and the footer confirms the jump
// destination.
func TestRootModelGThenAliasLetterPinsMatchAndConfirmsFooter(t *testing.T) {
	t.Parallel()
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {},
	}}
	sess := gotoTestSession(lister)

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "g"})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "d"})
	view := updated.(tui.Model).View().Content

	if !strings.Contains(view, "alias match") {
		t.Fatalf("expected the pinned row's 'alias match' label:\n%s", view)
	}
	if !strings.Contains(view, "jumps to Deployments in default") {
		t.Fatalf("expected the 12b destination-confirmation footer:\n%s", view)
	}
}

// TestRootModelGAliasLettersMatchFirstLetterOfNodesAndConfigMaps pins the
// alias rework in docs/design README.md §12a/§12b: every alias is the
// kind's own first letter, so Nodes moved from 'o' to 'n' and ConfigMaps
// from 'm' to 'c' — 'o' and 'm' are plain fuzzy queries now, not aliases.
func TestRootModelGAliasLettersMatchFirstLetterOfNodesAndConfigMaps(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		key  string
		want string
	}{
		{"n", "jumps to Nodes in default"},
		{"c", "jumps to ConfigMaps in default"},
	} {
		sess := gotoTestSession(gotoFakeLister{})

		task := &screenTask{name: "browse"}
		model := tui.NewWithSession(task, sess)
		updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
		updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "g"})
		updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: tc.key})
		view := updated.(tui.Model).View().Content

		if !strings.Contains(view, tc.want) {
			t.Fatalf("key %q: expected %q in the 12b destination footer:\n%s", tc.key, tc.want, view)
		}
	}

	// 'o' and 'm' are no longer aliases — they must not pin anything to rank
	// 1 or show the destination-confirmation footer.
	for _, key := range []string{"o", "m"} {
		sess := gotoTestSession(gotoFakeLister{})
		task := &screenTask{name: "browse"}
		model := tui.NewWithSession(task, sess)
		updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
		updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "g"})
		updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: key})
		view := updated.(tui.Model).View().Content

		if strings.Contains(view, "alias match") {
			t.Fatalf("key %q: did not expect an 'alias match' pin (o/m are retired aliases):\n%s", key, view)
		}
	}
}

// TestRootModelGEnterOnKindSwitchesBrowseKind exercises the full round trip:
// selecting a kind result and pressing enter should switch the underlying
// browse task's kind (verified indirectly through the rendered view, since
// the task is a real browse.Model wired the way app.NewModel wires it).
func TestRootModelGEnterOnKindSwitchesBrowseKind(t *testing.T) {
	t.Parallel()
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod:       {gotoTestPod("default", "api-1")},
		kube.KindConfigMap: {},
	}}
	sess := gotoTestSession(lister)

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m := updated.(tui.Model)
	m2, _ := m.Update(tea.KeyPressMsg{Text: "g"})
	m = m2.(tui.Model)

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(tui.Model)
	if cmd == nil {
		t.Fatalf("expected enter on the pre-selected kind to return a navigation cmd")
	}
	msg := cmd()
	kindMsg, ok := msg.(tui.GotoKindMsg)
	if !ok {
		t.Fatalf("expected a GotoKindMsg, got %T", msg)
	}
	if kindMsg.Kind != kube.KindPod {
		t.Fatalf("expected the first grid entry (Pods) to be pre-selected, got %v", kindMsg.Kind)
	}
	if m.PaletteOpen() {
		t.Fatalf("expected enter to close the palette")
	}
}

// TestRootModelGBrowseOpensWithLastOtherKindPreselected covers the alt-tab
// grammar (docs/design README.md §2b: "g ↵ returns to the last kind"):
// recentKinds[0] is always the current kind (only a completed jump ever
// pushes to it), so the 12a ranked list must pre-select recentKinds[1] — the
// kind you were on before — not the current one, or "g ↵" would be a no-op.
func TestRootModelGBrowseOpensWithLastOtherKindPreselected(t *testing.T) {
	t.Parallel()
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {gotoTestPod("default", "api-1")},
	}}
	sess := gotoTestSession(lister)
	sess.State.RecentKinds = []string{string(kube.KindPod), string(kube.KindDeployment)}

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "g"})
	updated, cmd := updated.(tui.Model).Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m := updated.(tui.Model)

	if m.PaletteOpen() {
		t.Fatalf("expected enter to close the palette")
	}
	if cmd == nil {
		t.Fatalf("expected a goto cmd since Deployments (not the current Pods) should be pre-selected")
	}
	msg := cmd()
	kindMsg, ok := msg.(tui.GotoKindMsg)
	if !ok || kindMsg.Kind != kube.KindDeployment {
		t.Fatalf("expected GotoKindMsg{Kind: Deployment}, got %#v", msg)
	}
}

// pushableScreenTask is a screenTask that transitions to next on the
// synthetic "open" key, letting tests simulate a detail screen (e.g.
// poddetail) pushed on top of browse via the root shell's own
// push-on-different-task-instance mechanism (model.go's sameTask check).
type pushableScreenTask struct {
	screenTask
	next tui.Task
}

func (t *pushableScreenTask) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok && key.String() == "open" && t.next != nil {
		return t.next, nil
	}
	// Must return t itself (not t.screenTask.Update(msg)'s result, which
	// would be &t.screenTask — a different pointer than t, tricking the
	// root shell's sameTask check into treating every message as a push).
	t.screenTask.Update(msg)
	return t, nil
}

// TestRootModelGotoFromPushedScreenReturnsToBrowseAndDispatches pins the fix
// for the goto palette doing nothing from a pushed detail screen (e.g.
// poddetail): GotoKindMsg/GotoResourceMsg are only handled by the browse
// task, so selecting an item while a detail screen is active must first
// unwind the stack back to the root browse task before the message is
// dispatched, or the jump silently no-ops.
func TestRootModelGotoFromPushedScreenReturnsToBrowseAndDispatches(t *testing.T) {
	t.Parallel()
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {gotoTestPod("default", "api-1")},
	}}
	sess := gotoTestSession(lister)

	detail := &screenTask{name: "poddetail"}
	browseTask := &pushableScreenTask{screenTask: screenTask{name: "browse"}, next: detail}

	model := tui.NewWithSession(browseTask, sess)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "open"})
	m := updated.(tui.Model)
	if !strings.Contains(m.View().Content, "poddetail") {
		t.Fatalf("expected poddetail pushed on top of browse:\n%s", m.View().Content)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Text: "g"})
	m = updated.(tui.Model)
	if !m.PaletteOpen() {
		t.Fatalf("expected 'g' to open the goto palette from a pushed screen")
	}

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(tui.Model)
	if cmd == nil {
		t.Fatalf("expected enter on the pre-selected kind to return a navigation cmd")
	}
	if !strings.Contains(m.View().Content, "browse") || strings.Contains(m.View().Content, "poddetail") {
		t.Fatalf("expected the stack unwound back to browse immediately on dispatch:\n%s", m.View().Content)
	}

	msg := cmd()
	if _, ok := msg.(tui.GotoKindMsg); !ok {
		t.Fatalf("expected a GotoKindMsg, got %T", msg)
	}
	updated, _ = m.Update(msg)
	m = updated.(tui.Model)
	if len(browseTask.updates) != 1 {
		t.Fatalf("expected the GotoKindMsg forwarded to the browse task, got %d forwards", len(browseTask.updates))
	}

	// A subsequent back should have nothing left to pop to (the detail
	// screen was discarded, not just hidden behind browse).
	updated, _ = m.Update(tui.BackMsg{})
	m = updated.(tui.Model)
	if !strings.Contains(m.View().Content, "browse") {
		t.Fatalf("expected browse to remain active with an empty stack:\n%s", m.View().Content)
	}
}

// TestRootModelGAltJKNavigateRankedListWithoutTyping covers the alt-modified
// vim keys added alongside the arrow keys: since an alt-modified press never
// carries Key.Text, it can move the 12a ranked list's selection without also
// being typeable into the "type to narrow" query (unlike plain j/k, which
// must stay reserved for typing — see handlePaletteKey's comment in
// model.go). Round-trips alt+j/alt+k down the ranked list (Pods ->
// Deployments -> back to Pods).
func TestRootModelGAltJKNavigateRankedListWithoutTyping(t *testing.T) {
	t.Parallel()
	sess := gotoTestSession(gotoFakeLister{})

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "g"})

	view := updated.(tui.Model).View().Content
	if !isSelectedLine(view, "Pods") {
		t.Fatalf("expected Pods pre-selected after 'g':\n%s", view)
	}

	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModAlt})
	m := updated.(tui.Model)
	view = m.View().Content
	if !isSelectedLine(view, "Deployments") {
		t.Fatalf("expected alt+j to move selection to Deployments:\n%s", view)
	}
	if !m.PaletteOpen() {
		t.Fatalf("palette should still be open — alt+j must not have been typed into the query")
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModAlt})
	m = updated.(tui.Model)
	view = m.View().Content
	if !isSelectedLine(view, "Pods") {
		t.Fatalf("expected alt+k to move selection back to Pods:\n%s", view)
	}
}

// isSelectedLine reports whether view has a line carrying both label and
// the palette's 1-cell selection-bar glyph — i.e. label is the currently
// selected row, not just present somewhere in the panel (the input row's
// block cursor uses the same glyph, so a bare "▎" search isn't enough).
func isSelectedLine(view, label string) bool {
	for l := range strings.SplitSeq(view, "\n") {
		if strings.Contains(l, label) && strings.Contains(l, "▎") {
			return true
		}
	}
	return false
}
