package browse

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// firstRowsLoaded picks the rowsLoadedMsg out of a Cmd that may be a bare
// message or (as Init's now is, since it also batches the spinner's tick
// alongside the row load) a tea.BatchMsg — this test only cares about the
// row-load leg of that batch, not the spinner's independent tick chain.
func firstRowsLoaded(t *testing.T, cmd tea.Cmd) rowsLoadedMsg {
	t.Helper()
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c == nil {
				continue
			}
			if rl, ok := c().(rowsLoadedMsg); ok {
				return rl
			}
		}
		t.Fatal("no rowsLoadedMsg found in batch")
	}
	rl, ok := msg.(rowsLoadedMsg)
	if !ok {
		t.Fatalf("expected rowsLoadedMsg, got %T", msg)
	}
	return rl
}

// emptyRowsFor builds the empty reply load() would have produced for kind
// against the descriptor m currently holds. The columns stamp is part of that
// reply: applyRowsLoaded discards one projected against a different
// descriptor shape, the way it discards one for a superseded kind.
func emptyRowsFor(m Model, kind kube.ResourceKind) rowsLoadedMsg {
	return rowsLoadedMsg{kind: kind, columns: len(m.desc.Columns), rows: nil}
}

// notYetSyncedLister simulates *kube.Cluster's CacheSyncChecker: ListRaw
// reads a cache that's still filling (empty, no error — the same
// "truthful-looking but wrong" shape the real informer cache returns before
// WaitForCacheSync completes) until synced flips true, external to any
// individual ListRaw call (mirroring the real Cluster, where a single
// load() cycle's several ListRaw calls all observe the same synced bool).
type notYetSyncedLister struct {
	lister fakeLister
	synced *bool
}

func (l *notYetSyncedLister) ListRaw(ctx context.Context, kind kube.ResourceKind, namespace string) ([]runtime.Object, error) {
	if !*l.synced {
		return nil, nil
	}
	return l.lister.ListRaw(ctx, kind, namespace)
}

func (l *notYetSyncedLister) Synced() bool { return *l.synced }

// perKindSyncedLister reports sync state per kind, the way *kube.Cluster
// does once each informer is started independently. Only the kinds named in
// unsynced are still filling; every other kind is trustworthy.
type perKindSyncedLister struct {
	lister   fakeLister
	unsynced map[kube.ResourceKind]bool
}

func (l *perKindSyncedLister) ListRaw(ctx context.Context, kind kube.ResourceKind, namespace string) ([]runtime.Object, error) {
	if l.unsynced[kind] {
		return nil, nil
	}
	return l.lister.ListRaw(ctx, kind, namespace)
}

func (l *perKindSyncedLister) KindSynced(kind kube.ResourceKind) bool { return !l.unsynced[kind] }

// TestListerSyncedIsPerKind is the point of the whole per-kind seam: one
// kind's cache still filling must not make another kind's empty result look
// untrustworthy, and vice versa. Under the old cluster-wide flag both of
// these answered the same way.
func TestListerSyncedIsPerKind(t *testing.T) {
	lister := &perKindSyncedLister{
		unsynced: map[kube.ResourceKind]bool{kube.KindDeployment: true},
		lister:   fakeLister{objs: map[kube.ResourceKind][]runtime.Object{}},
	}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)

	m.kind = kube.KindPod
	if !m.listerSynced() {
		t.Error("Pods should read as synced — only Deployments is still filling")
	}
	m.kind = kube.KindDeployment
	if m.listerSynced() {
		t.Error("Deployments should read as unsynced while its informer is still filling")
	}
}

// TestEmptyKindRendersEmptyWhileAnotherKindSyncs: a genuinely empty Pods
// list must reach the empty state immediately even though some unrelated
// kind hasn't synced. The cluster-wide flag used to hold this in a loading
// spinner until every informer in the process had finished.
func TestEmptyKindRendersEmptyWhileAnotherKindSyncs(t *testing.T) {
	lister := &perKindSyncedLister{
		unsynced: map[kube.ResourceKind]bool{kube.KindHorizontalPodAutoscaler: true},
		lister:   fakeLister{objs: map[kube.ResourceKind][]runtime.Object{}},
	}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)

	updated, _ := m.applyRowsLoaded(emptyRowsFor(m, kube.KindPod))
	m = *updated.(*Model)

	if m.state != tui.TaskStateEmpty {
		t.Fatalf("state = %s, want empty — Pods synced, so this empty answer is trustworthy", m.state)
	}
}

// TestUnsyncedKindStaysLoadingViaKindChecker is the mirror image: the kind
// on screen is the one still filling, so its empty result is not
// trustworthy and browse must keep spinning.
func TestUnsyncedKindStaysLoadingViaKindChecker(t *testing.T) {
	lister := &perKindSyncedLister{
		unsynced: map[kube.ResourceKind]bool{kube.KindSecret: true},
		lister:   fakeLister{objs: map[kube.ResourceKind][]runtime.Object{}},
	}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m.kind = kube.KindSecret

	updated, cmd := m.applyRowsLoaded(emptyRowsFor(m, kube.KindSecret))
	m = *updated.(*Model)

	if m.state != tui.TaskStateLoading {
		t.Fatalf("state = %s, want loading while the Secret informer is still filling", m.state)
	}
	if cmd == nil {
		t.Fatal("expected a retry to be scheduled")
	}
}

// stalledLister is a cache that has given up: settled, because nothing more
// is coming, but with a reason rather than a genuinely empty cluster behind
// it. *kube.Cluster answers this way for an initial LIST that keeps failing.
type stalledLister struct {
	lister fakeLister
	kind   kube.ResourceKind
	err    error
}

func (l *stalledLister) ListRaw(ctx context.Context, kind kube.ResourceKind, namespace string) ([]runtime.Object, error) {
	if kind == l.kind {
		return nil, nil
	}
	return l.lister.ListRaw(ctx, kind, namespace)
}

func (l *stalledLister) KindSynced(kube.ResourceKind) bool { return true }

func (l *stalledLister) KindError(kind kube.ResourceKind) error {
	if kind == l.kind {
		return l.err
	}
	return nil
}

// TestStalledCacheRendersTheErrorNotAnEmptyCluster: the failure this pairs
// with is a Helm Releases screen that loaded for over 2000 seconds against a
// cluster whose release LIST could not finish inside the API server's window.
// Reporting the cache as settled is what gets the screen off the spinner —
// but settled-and-empty would then assert there are no releases, which is a
// claim about the cluster this screen has no evidence for.
func TestStalledCacheRendersTheErrorNotAnEmptyCluster(t *testing.T) {
	lister := &stalledLister{
		kind:   kube.KindHelmRelease,
		err:    errors.New("stream error when reading response body"),
		lister: fakeLister{objs: map[kube.ResourceKind][]runtime.Object{}},
	}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m.kind = kube.KindHelmRelease
	m.desc, _ = m.session.Registry.Descriptor(kube.KindHelmRelease)

	updated, cmd := m.applyRowsLoaded(emptyRowsFor(m, kube.KindHelmRelease))
	m = *updated.(*Model)

	if m.state != tui.TaskStateError {
		t.Fatalf("state = %s, want error — the cache stopped filling, it is not empty", m.state)
	}
	if !strings.Contains(m.feedback, "stream error") {
		t.Errorf("feedback = %q, want the read failure that actually happened", m.feedback)
	}
	if cmd != nil {
		t.Error("no retry should be scheduled here; the informer retries underneath and its change event reloads this list")
	}
}

// TestStalledUnrelatedKindStillRendersEmpty: the stall belongs to the kind
// that failed. A working list with genuinely nothing in it must still say so.
func TestStalledUnrelatedKindStillRendersEmpty(t *testing.T) {
	lister := &stalledLister{
		kind:   kube.KindHelmRelease,
		err:    errors.New("stream error when reading response body"),
		lister: fakeLister{objs: map[kube.ResourceKind][]runtime.Object{}},
	}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m.kind = kube.KindPod

	updated, _ := m.applyRowsLoaded(emptyRowsFor(m, kube.KindPod))
	m = *updated.(*Model)

	if m.state != tui.TaskStateEmpty {
		t.Fatalf("state = %s, want empty — the Pod cache is fine and there are no pods", m.state)
	}
}

// TestApplyRowsLoadedStaysLoadingWhileCacheSyncing is the regression test for
// launch showing "no pods in <namespace>" instead of a loading indicator:
// an empty result from a not-yet-synced lister must not flip browse to
// TaskStateEmpty.
func TestApplyRowsLoadedStaysLoadingWhileCacheSyncing(t *testing.T) {
	synced := false
	lister := &notYetSyncedLister{synced: &synced, lister: fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-0")},
	}}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)

	before := m.reloadEpoch
	updated, cmd := m.applyRowsLoaded(emptyRowsFor(m, kube.KindPod))
	m = *updated.(*Model)

	if m.state != tui.TaskStateLoading {
		t.Fatalf("state = %s, want loading (cache not yet synced)", m.state)
	}
	if cmd == nil {
		t.Fatal("expected a retry command to be scheduled while the cache is still syncing")
	}
	if m.reloadEpoch == before {
		t.Fatal("expected reloadEpoch to advance so the scheduled retry is distinguishable from a stale one")
	}
}

// TestLaunchStaysLoadingUntilCacheSynced drives the retry through to
// completion: once the lister reports synced, browse settles at Ready with
// the real rows rather than getting stuck showing an empty namespace. The
// synced flag flips between the first (empty) load and the scheduled retry,
// same as the real Cluster's WaitForCacheSync completing in the background
// while browse is already showing its loading state.
func TestLaunchStaysLoadingUntilCacheSynced(t *testing.T) {
	synced := false
	lister := &notYetSyncedLister{synced: &synced, lister: fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-0")},
	}}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)

	updated, cmd := m.Update(firstRowsLoaded(t, m.Init()))
	m = *updated.(*Model)
	if m.state != tui.TaskStateLoading {
		t.Fatalf("state = %s, want loading right after the first (unsynced) empty load", m.state)
	}
	if cmd == nil {
		t.Fatal("expected a retry command to be scheduled")
	}

	synced = true // the cache finishes syncing while the retry is pending
	updated, cmd = m.Update(cmd())
	m = *updated.(*Model)
	if cmd == nil {
		t.Fatal("expected the retry to re-issue a load")
	}

	updated, _ = m.Update(cmd())
	m = *updated.(*Model)

	if m.state != tui.TaskStateReady {
		t.Fatalf("state = %s, want ready once the cache reports synced (feedback=%q)", m.state, m.feedback)
	}
	if !strings.Contains(plain(m.Render()), "api-0") {
		t.Fatalf("expected the table to show the pod once synced:\n%s", plain(m.Render()))
	}
}

// silentlyForbiddenLister is *kube.Cluster under an identity that may not
// list one kind. Unlike browse_offline_test.go's forbiddenLister — a
// one-shot read that returns the 403 — this is the informer-backed shape,
// where the read *succeeds* with nothing in it and the cache reports settled
// because it is never going to fill. Nothing about the read says anything
// went wrong; only KindForbidden knows.
type silentlyForbiddenLister struct {
	kind   kube.ResourceKind
	err    error
	lister fakeLister
}

func (l *silentlyForbiddenLister) ListRaw(ctx context.Context, kind kube.ResourceKind, namespace string) ([]runtime.Object, error) {
	if kind == l.kind {
		return nil, nil
	}
	return l.lister.ListRaw(ctx, kind, namespace)
}

func (l *silentlyForbiddenLister) KindSynced(kube.ResourceKind) bool { return true }

func (l *silentlyForbiddenLister) KindForbidden(kind kube.ResourceKind) error {
	if kind == l.kind {
		return l.err
	}
	return nil
}

// TestForbiddenKindRendersThePermissionCard is the regression the end-to-end
// suite found: under an identity whose reads are Forbidden, browse rendered
// its empty state — "no pods in <ns> · the namespace exists and you can read
// it — there's just nothing here" — under a green connected header. Every
// input to that sentence was technically true (settled cache, no error, zero
// rows) and the sentence itself was false.
func TestForbiddenKindRendersThePermissionCard(t *testing.T) {
	lister := &silentlyForbiddenLister{
		kind:   kube.KindPod,
		err:    apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("nope")),
		lister: fakeLister{objs: map[kube.ResourceKind][]runtime.Object{}},
	}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m.kind = kube.KindPod

	updated, cmd := m.applyRowsLoaded(emptyRowsFor(m, kube.KindPod))
	m = *updated.(*Model)

	if m.state != tui.TaskStatePermissionDenied {
		t.Fatalf("state = %s, want permission-denied — every read was refused", m.state)
	}
	if view := plain(m.View().Content); strings.Contains(view, "no pods in") {
		t.Fatalf("the empty state's claim about the cluster survived a denial:\n%s", view)
	}
	// Permanent for the session, so there is no retry to promise and none
	// to schedule.
	if cmd != nil {
		t.Error("a retry was scheduled for a denial; RBAC will not change while the process runs")
	}
}

// TestForbiddenUnrelatedKindStillRendersEmpty: the denial belongs to the kind
// that was refused. A readable list with genuinely nothing in it must still
// say so — otherwise this fix trades one false claim for a different one.
func TestForbiddenUnrelatedKindStillRendersEmpty(t *testing.T) {
	lister := &silentlyForbiddenLister{
		kind:   kube.KindSecret,
		err:    apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "", errors.New("nope")),
		lister: fakeLister{objs: map[kube.ResourceKind][]runtime.Object{}},
	}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m.kind = kube.KindPod

	updated, _ := m.applyRowsLoaded(emptyRowsFor(m, kube.KindPod))
	m = *updated.(*Model)

	if m.state != tui.TaskStateEmpty {
		t.Fatalf("state = %s, want empty — Pods are readable and there are none", m.state)
	}
}
