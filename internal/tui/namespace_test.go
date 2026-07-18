package tui_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/state"
	"github.com/kute-dev/kute/internal/tui"
)

// fakeMetricsReader is a minimal tui.MetricsReader fixture, keyed by
// namespace like the real *kube.Cluster/*fake.Cluster's
// PodMetricsByNamespace — a missing namespace key errors, matching how a
// metrics-server call would fail rather than returning an empty result.
type fakeMetricsReader struct {
	byNamespace map[string]map[string]kube.PodMetrics
}

func (f fakeMetricsReader) PodMetricsByNamespace(_ context.Context, namespace string) (map[string]kube.PodMetrics, error) {
	m, ok := f.byNamespace[namespace]
	if !ok {
		return nil, fmt.Errorf("no metrics for namespace %q", namespace)
	}
	return m, nil
}

func namespaceObj(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func namespaceTestDeployment(ns, name string) *appsv1.Deployment {
	replicas := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1},
	}
}

// TestRootModelNOpensNamespacePaletteWithLiveCounts drives 'n' end to end
// (docs/design README.md §6a): every namespace lists with a live pod count,
// and "all namespaces" is pinned last with the cluster-wide total.
func TestRootModelNOpensNamespacePaletteWithLiveCounts(t *testing.T) {
	t.Parallel()
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNamespace: {namespaceObj("default"), namespaceObj("prod")},
		kube.KindPod: {
			gotoTestPod("default", "api-1"),
			gotoTestPod("prod", "api-2"),
			gotoTestPod("prod", "api-3"),
		},
	}}
	sess := gotoTestSession(lister)

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "n"})
	m := updated.(tui.Model)
	view := m.View().Content

	for _, want := range []string{"default", "prod", "all namespaces", "current", "PODS", "HEALTH", "CPU"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in the namespace palette:\n%s", want, view)
		}
	}
	if m.Mode() != tui.ModeGoto {
		t.Fatalf("Mode() = %v, want ModeGoto while the namespace palette is open", m.Mode())
	}
}

// TestRootModelNCountsActiveKindNotPods covers browsing a namespaced non-Pod
// kind: pressing 'n' with Deployments open should count/health-tally
// Deployments per namespace, with a "DEPLOYMENTS" header, not Pods.
func TestRootModelNCountsActiveKindNotPods(t *testing.T) {
	t.Parallel()
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNamespace: {namespaceObj("default"), namespaceObj("prod")},
		kube.KindDeployment: {
			namespaceTestDeployment("default", "api"),
			namespaceTestDeployment("prod", "web"),
			namespaceTestDeployment("prod", "worker"),
		},
		// Pods are present too, so the assertion below proves the palette
		// picked Deployments' count (3), not Pods' (1).
		kube.KindPod: {gotoTestPod("default", "stray-pod")},
	}}
	sess := gotoTestSession(lister)
	sess.Location.Kind = kube.KindDeployment

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "n"})
	view := updated.(tui.Model).View().Content

	for _, want := range []string{"DEPLOYMENTS", "default", "prod", "3"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in the namespace palette:\n%s", want, view)
		}
	}
	if strings.Contains(view, "PODS") {
		t.Fatalf("expected the header to follow the active Deployments kind, not stay PODS:\n%s", view)
	}
}

// TestRootModelNFallsBackToPodsForClusterScopedKind covers browsing a
// cluster-scoped kind (Nodes have no meaningful per-namespace count): the
// palette falls back to today's Pods-based counts/header.
func TestRootModelNFallsBackToPodsForClusterScopedKind(t *testing.T) {
	t.Parallel()
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNamespace: {namespaceObj("default")},
		kube.KindPod:       {gotoTestPod("default", "api-1"), gotoTestPod("default", "api-2")},
	}}
	sess := gotoTestSession(lister)
	sess.Location.Kind = kube.KindNode

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "n"})
	view := updated.(tui.Model).View().Content

	for _, want := range []string{"PODS", "default", "2"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected the palette to fall back to Pods counts for a cluster-scoped active kind:\n%s", view)
		}
	}
}

// TestRootModelNFallsBackToPodsBeforeFirstNavigation covers the zero-value
// Location.Kind (before any GotoKindMsg/SwitchContextMsg has set it, i.e.
// right after launch): the palette still falls back to Pods rather than
// resolving an empty kind.
func TestRootModelNFallsBackToPodsBeforeFirstNavigation(t *testing.T) {
	t.Parallel()
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNamespace: {namespaceObj("default")},
		kube.KindPod:       {gotoTestPod("default", "api-1")},
	}}
	sess := gotoTestSession(lister)
	sess.Location.Kind = ""

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "n"})
	view := updated.(tui.Model).View().Content

	if !strings.Contains(view, "PODS") {
		t.Fatalf("expected a PODS header when Location.Kind is unset:\n%s", view)
	}
}

// TestRootModelNOpensNamespacePaletteWithCPUShare pins the fix for "no
// CPU-share column in the namespace palette" (mvp-tasks.md's known gap,
// docs/design README.md §6a: "CPU share right-aligned"): each row's usage,
// as a share of the summed cluster-wide usage across every listed
// namespace, renders alongside the pod count.
func TestRootModelNOpensNamespacePaletteWithCPUShare(t *testing.T) {
	t.Parallel()
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNamespace: {namespaceObj("default"), namespaceObj("prod")},
		kube.KindPod: {
			gotoTestPod("default", "api-1"),
			gotoTestPod("prod", "api-2"),
		},
	}}
	sess := gotoTestSession(lister)
	sess.Metrics = fakeMetricsReader{byNamespace: map[string]map[string]kube.PodMetrics{
		"default": {"api-1": {CPUMilli: 100}},
		"prod":    {"api-2": {CPUMilli: 300}},
	}}

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, cmd := updated.(tui.Model).Update(tea.KeyPressMsg{Text: "n"})
	// CPU shares load in the background (fetchNamespaceCPUSharesCmd) so
	// opening the palette never blocks on the metrics-server round trip —
	// drive the returned cmd's message back through Update, same as a live
	// program would once it resolves.
	if cmd == nil {
		t.Fatalf("expected a CPU-shares fetch cmd from opening the namespace palette")
	}
	updated, _ = updated.(tui.Model).Update(cmd())
	view := updated.(tui.Model).View().Content

	for _, want := range []string{"25%", "75%"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in the namespace palette:\n%s", want, view)
		}
	}
}

// TestRootModelNOpensNamespacePaletteWithoutMetricsOmitsCPUShare covers the
// degrade-gracefully path: no Session.Metrics seam (nil cluster, or --demo
// before one is wired) means the CPU cell stays the ghost dash placeholder,
// not a crash or a misleading "0%" — the PODS/HEALTH columns still render
// live.
func TestRootModelNOpensNamespacePaletteWithoutMetricsOmitsCPUShare(t *testing.T) {
	t.Parallel()
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNamespace: {namespaceObj("default")},
		kube.KindPod:       {gotoTestPod("default", "api-1")},
	}}
	sess := gotoTestSession(lister)

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, cmd := updated.(tui.Model).Update(tea.KeyPressMsg{Text: "n"})
	if cmd != nil {
		updated, _ = updated.(tui.Model).Update(cmd())
	}
	view := updated.(tui.Model).View().Content

	for _, want := range []string{"PODS", "HEALTH", "CPU", "default"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in the namespace palette:\n%s", want, view)
		}
	}
	if strings.Contains(view, "%") {
		t.Fatalf("expected no cpu-share percentage without a Metrics seam:\n%s", view)
	}
}

// notYetSyncedNSLister simulates *kube.Cluster's CacheSyncChecker for the
// namespace-palette path: ListRaw reads an empty cache (no error, the same
// "truthful-looking but wrong" shape the real informer cache returns before
// WaitForCacheSync completes) until synced flips true — mirrors browse's
// notYetSyncedLister fixture.
type notYetSyncedNSLister struct {
	lister gotoFakeLister
	synced *bool
}

func (l *notYetSyncedNSLister) ListRaw(ctx context.Context, kind kube.ResourceKind, namespace string) ([]runtime.Object, error) {
	if !*l.synced {
		return nil, nil
	}
	return l.lister.ListRaw(ctx, kind, namespace)
}

func (l *notYetSyncedNSLister) Synced() bool { return *l.synced }

// TestRootModelNShowsLoadingWhileCacheSyncing is the regression test for
// opening the namespace palette before the informer cache has completed its
// initial sync (just after launch or mid SwitchContext): an empty result
// must not be mistaken for "no matches" — the palette shows a loading line
// instead and settles on the real list once the cache reports synced.
func TestRootModelNShowsLoadingWhileCacheSyncing(t *testing.T) {
	synced := false
	lister := &notYetSyncedNSLister{synced: &synced, lister: gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNamespace: {namespaceObj("default"), namespaceObj("prod")},
	}}}
	sess := gotoTestSession(lister)

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, cmd := updated.(tui.Model).Update(tea.KeyPressMsg{Text: "n"})
	view := updated.(tui.Model).View().Content
	if !strings.Contains(view, "loading") {
		t.Fatalf("expected a loading indicator while the cache is still syncing:\n%s", view)
	}
	if strings.Contains(view, "no matches") {
		t.Fatalf("expected no \"no matches\" while the cache is still syncing:\n%s", view)
	}
	if cmd == nil {
		t.Fatal("expected a retry command to be scheduled while the cache is still syncing")
	}

	synced = true // the cache finishes syncing while the retry is pending
	updated, cmd = updated.(tui.Model).Update(cmd())
	if cmd == nil {
		t.Fatal("expected the settled load to kick off the CPU-shares fetch")
	}
	view = updated.(tui.Model).View().Content

	for _, want := range []string{"default", "prod", "all namespaces"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in the namespace palette once synced:\n%s", want, view)
		}
	}
	if strings.Contains(view, "loading namespaces") {
		t.Fatalf("expected the loading indicator to clear once synced:\n%s", view)
	}
}

// TestRootModelNCapsLongNamespaceListWithTrailer is the regression test for
// a long namespace list overflowing the palette past the screen (the
// palette has no internal scrolling — see maxNamespaceVisible's doc
// comment): the list caps at maxNamespaceVisible rows with a "+ N more ·
// type to narrow" trailer, and the pinned "all namespaces" row still shows
// below it. Typing must still reach a namespace past the cap, proving the
// cap only limits what's displayed, not the corpus fuzzy filtering search.
func TestRootModelNCapsLongNamespaceListWithTrailer(t *testing.T) {
	t.Parallel()
	var nsObjs []runtime.Object
	for i := range 20 {
		nsObjs = append(nsObjs, namespaceObj(fmt.Sprintf("ns-%02d", i)))
	}
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNamespace: nsObjs,
	}}
	sess := gotoTestSession(lister)

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "n"})
	view := updated.(tui.Model).View().Content

	if !strings.Contains(view, "+ 8 more namespaces · type to narrow") {
		t.Fatalf("expected an overflow trailer capping the namespace list:\n%s", view)
	}
	if !strings.Contains(view, "all namespaces") {
		t.Fatalf("expected the pinned all-namespaces row to survive the cap:\n%s", view)
	}

	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "ns-19"})
	view = updated.(tui.Model).View().Content
	if !strings.Contains(view, "ns-19") {
		t.Fatalf("expected filtering to reach a namespace past the visible cap:\n%s", view)
	}
}

// TestRootModelNamespacePaletteOpensWithLastOtherPreselected covers the
// alt-tab grammar that replaced the old "n n" double-tap (docs/design
// README.md §6a): opening the namespace palette pre-selects the
// second-most-recent namespace (recentNamespaces[0] is always current — see
// mostRecentOther), so a bare "n" + enter — the same two keystrokes "n n"
// used — toggles straight to it, now visibly through the palette instead of
// bypassing it.
func TestRootModelNamespacePaletteOpensWithLastOtherPreselected(t *testing.T) {
	t.Parallel()
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNamespace: {namespaceObj("default"), namespaceObj("prod")},
	}}
	sess := gotoTestSession(lister)
	sess.State = state.State{PerContext: map[string]state.PerContext{"microk8s-cluster": {RecentNamespaces: []string{"default", "prod"}}}}

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.KeyPressMsg{Text: "n"})
	updated, cmd := updated.(tui.Model).Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m := updated.(tui.Model)

	if m.PaletteOpen() {
		t.Fatalf("expected enter to close the palette")
	}
	if cmd == nil {
		t.Fatalf("expected a switch-namespace cmd from the pre-selected alt-tab target")
	}
	msg := cmd()
	nsMsg, ok := msg.(tui.SwitchNamespaceMsg)
	if !ok || nsMsg.Namespace != "prod" {
		t.Fatalf("expected SwitchNamespaceMsg{Namespace: prod}, got %#v", msg)
	}
}

// TestRootModelNamespacePreviousRowTaggedWithoutADigit covers the "remove
// current and previous from the recent logic" refinement: the
// mostRecentOther alt-tab target ("prod" here) is tagged "previous" on its
// own row, but — unlike further-back recents — never gets a numbered gutter
// digit, and is absent from the RECENT summary row (both already redundant
// with its own on-row tag).
func TestRootModelNamespacePreviousRowTaggedWithoutADigit(t *testing.T) {
	t.Parallel()
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNamespace: {namespaceObj("default"), namespaceObj("prod"), namespaceObj("stage")},
	}}
	sess := gotoTestSession(lister)
	sess.State = state.State{PerContext: map[string]state.PerContext{"microk8s-cluster": {RecentNamespaces: []string{"default", "prod", "stage"}}}}

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.KeyPressMsg{Text: "n"})
	view := updated.(tui.Model).View().Content

	if !strings.Contains(view, "prod") || !strings.Contains(view, "previous") {
		t.Fatalf("expected prod's row tagged \"previous\":\n%s", view)
	}
	if strings.Contains(view, "1prod") || strings.Contains(view, "1 prod") {
		t.Fatalf("expected prod (previous) to carry no numbered gutter digit:\n%s", view)
	}
	for line := range strings.SplitSeq(view, "\n") {
		if strings.Contains(line, "RECENT") && strings.Contains(line, "prod") {
			t.Fatalf("expected the RECENT summary row to omit prod (previous):\n%s", line)
		}
	}
}

// TestRootModelNamespaceRecentsPromotedToTop covers "current/previous/recent
// namespaces float to the top": with namespaces alphabetically ordered
// aaa-plain, mmm-recent-1, prod (current), stage (previous), zzz-recent-2,
// the palette must render current first, then previous, then the numbered
// recents in digit order, then the untouched plain namespaces — even though
// alphabetically aaa-plain would otherwise lead and zzz-recent-2 would
// trail. The pinned "all namespaces" row must still stay last regardless.
func TestRootModelNamespaceRecentsPromotedToTop(t *testing.T) {
	t.Parallel()
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNamespace: {
			namespaceObj("aaa-plain"),
			namespaceObj("mmm-recent-1"),
			namespaceObj("prod"),
			namespaceObj("stage"),
			namespaceObj("zzz-recent-2"),
		},
	}}
	sess := gotoTestSession(lister)
	sess.State = state.State{
		PerContext: map[string]state.PerContext{
			// prod = current, stage = previous (mostRecentOther), then
			// mmm-recent-1 = digit 1, zzz-recent-2 = digit 2.
			"microk8s-cluster": {RecentNamespaces: []string{"prod", "stage", "mmm-recent-1", "zzz-recent-2"}},
		},
	}
	sess.Location.Namespace = "prod"

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.KeyPressMsg{Text: "n"})
	view := updated.(tui.Model).View().Content

	order := []string{"prod", "stage", "mmm-recent-1", "zzz-recent-2", "aaa-plain", "all namespaces"}
	last := -1
	for _, name := range order {
		i := strings.Index(view, name)
		if i < 0 {
			t.Fatalf("expected %q to appear in the palette:\n%s", name, view)
		}
		if i < last {
			t.Fatalf("expected %q to appear after the earlier entries in %v (got out of order):\n%s", name, order, view)
		}
		last = i
	}
}

// TestRootModelNamespaceDigitNineReachesNinthRecent covers the full 1-9
// range actually being reachable: state.MaxRecent must be large enough to
// hold current + previous + 9 numbered recents (11 total) — with a full
// 11-entry RecentNamespaces list, digit '9' must still resolve to a real
// namespace instead of falling through to plain fuzzy filtering.
func TestRootModelNamespaceDigitNineReachesNinthRecent(t *testing.T) {
	t.Parallel()
	recents := []string{"default", "prod", "r1", "r2", "r3", "r4", "r5", "r6", "r7", "r8", "r9"}
	if len(recents) != state.MaxRecent {
		t.Fatalf("test fixture has %d recents, want state.MaxRecent (%d)", len(recents), state.MaxRecent)
	}

	objs := make([]runtime.Object, len(recents))
	for i, name := range recents {
		objs[i] = namespaceObj(name)
	}
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindNamespace: objs}}
	sess := gotoTestSession(lister)
	sess.State = state.State{PerContext: map[string]state.PerContext{"microk8s-cluster": {RecentNamespaces: recents}}}

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.KeyPressMsg{Text: "n"})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "9"})
	view := updated.(tui.Model).View().Content

	if !strings.Contains(view, "switches to") || !strings.Contains(view, "r9") {
		t.Fatalf("expected digit '9' to pick r9 (the 9th recent after current/previous):\n%s", view)
	}
}

// TestRootModelNamespaceSecondNTypesIntoQuery covers the removal of the old
// "n n" double-tap: typing 'n' twice while the namespace palette is open now
// just filters the query like any other character — even with two recents
// to toggle between — because the alt-tab target is reached by
// pre-selection + enter instead (see
// TestRootModelNamespacePaletteOpensWithLastOtherPreselected).
func TestRootModelNamespaceSecondNTypesIntoQuery(t *testing.T) {
	t.Parallel()
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNamespace: {namespaceObj("default"), namespaceObj("prod")},
	}}
	sess := gotoTestSession(lister)
	sess.State = state.State{PerContext: map[string]state.PerContext{"microk8s-cluster": {RecentNamespaces: []string{"default", "prod"}}}}

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.KeyPressMsg{Text: "n"})
	updated, cmd := updated.(tui.Model).Update(tea.KeyPressMsg{Text: "n"})
	m := updated.(tui.Model)

	if cmd != nil {
		t.Fatalf("expected no toggle cmd — double-tap is gone, the second 'n' just filters")
	}
	if !m.PaletteOpen() {
		t.Fatalf("expected the palette to stay open and the 'n' typed into the query")
	}
}

// TestRootModelNamespaceDigitPicksRecent covers 6a's numbered RECENT-row
// shortcut (digitRecentTarget/recentNumbers): current and the
// immediately-previous namespace are both excluded from the numbering (each
// is already visible tagged "current"/"previous" on its own row — a digit
// for either would be redundant), so with
// RecentNamespaces = [default(current), prod(previous), stage, qa], "stage"
// is digit 1 and "qa" is digit 2. Typing a bare '2' jumps Sel straight to
// "qa" without having to move down and Enter, and the footer names the
// target before commit.
func TestRootModelNamespaceDigitPicksRecent(t *testing.T) {
	t.Parallel()
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNamespace: {namespaceObj("default"), namespaceObj("prod"), namespaceObj("stage"), namespaceObj("qa")},
	}}
	sess := gotoTestSession(lister)
	sess.State = state.State{PerContext: map[string]state.PerContext{"microk8s-cluster": {RecentNamespaces: []string{"default", "prod", "stage", "qa"}}}}

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.KeyPressMsg{Text: "n"})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "2"})
	view := updated.(tui.Model).View().Content
	if !strings.Contains(view, "switches to") || !strings.Contains(view, "qa") {
		t.Fatalf("expected a digit-select footer naming qa:\n%s", view)
	}

	updated, cmd := updated.(tui.Model).Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m := updated.(tui.Model)
	if m.PaletteOpen() {
		t.Fatalf("expected enter to close the palette")
	}
	if cmd == nil {
		t.Fatalf("expected a switch-namespace cmd from the digit-picked recent")
	}
	msg := cmd()
	nsMsg, ok := msg.(tui.SwitchNamespaceMsg)
	if !ok || nsMsg.Namespace != "qa" {
		t.Fatalf("expected SwitchNamespaceMsg{Namespace: qa}, got %#v", msg)
	}
}

// TestRootModelNamespaceDigitThenLetterFiltersNormally covers digitRecentTarget's
// degrade-to-fuzzy rule: once a second character lands in the query, the
// leading digit is just the query's first character, not a recent-picker —
// so the digit-select footer disappears and ordinary fuzzy filtering takes
// over.
func TestRootModelNamespaceDigitThenLetterFiltersNormally(t *testing.T) {
	t.Parallel()
	lister := gotoFakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNamespace: {namespaceObj("default"), namespaceObj("prod"), namespaceObj("stage"), namespaceObj("10-legacy")},
	}}
	sess := gotoTestSession(lister)
	sess.State = state.State{PerContext: map[string]state.PerContext{"microk8s-cluster": {RecentNamespaces: []string{"default", "prod", "stage"}}}}

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.KeyPressMsg{Text: "n"})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "1"})
	view := updated.(tui.Model).View().Content
	if !strings.Contains(view, "switches to") || !strings.Contains(view, "stage") {
		t.Fatalf("expected the bare digit to still pick stage before a second character arrives:\n%s", view)
	}

	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "0"})
	view = updated.(tui.Model).View().Content

	if strings.Contains(view, "switches to") {
		t.Fatalf("expected the digit-select footer to be gone once a second character arrived:\n%s", view)
	}
	if !strings.Contains(view, "10-legacy") {
		t.Fatalf("expected \"10\" to fuzzy-filter to 10-legacy:\n%s", view)
	}
}
