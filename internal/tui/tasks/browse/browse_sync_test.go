package browse

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

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
	updated, cmd := m.applyRowsLoaded(rowsLoadedMsg{kind: kube.KindPod, rows: nil})
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
