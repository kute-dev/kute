package helmhistory

import (
	"context"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

func plain(s string) string { return ansi.Strip(s) }

type fakeLister struct {
	objs map[kube.ResourceKind][]runtime.Object
}

func (f fakeLister) ListRaw(_ context.Context, kind kube.ResourceKind, _ string) ([]runtime.Object, error) {
	return f.objs[kind], nil
}

type fakeMutator struct {
	namespace, name string
	revision        int
	err             error
}

func (f *fakeMutator) DeleteResource(context.Context, kube.ResourceKind, string, string) error {
	return nil
}
func (f *fakeMutator) DeleteResourceForced(context.Context, kube.ResourceKind, string, string) error {
	return nil
}
func (f *fakeMutator) RolloutRestart(context.Context, kube.ResourceKind, string, string) error {
	return nil
}
func (f *fakeMutator) Cordon(context.Context, string, bool) error { return nil }
func (f *fakeMutator) Drain(context.Context, string) (int, error) { return 0, nil }
func (f *fakeMutator) Scale(context.Context, kube.ResourceKind, string, string, int32) error {
	return nil
}
func (f *fakeMutator) SetImage(context.Context, kube.ResourceKind, string, string, string, string) error {
	return nil
}
func (f *fakeMutator) SetResources(context.Context, kube.ResourceKind, string, string, string, kube.ResourceEdits, bool) error {
	return nil
}
func (f *fakeMutator) PatchMeta(context.Context, kube.ResourceKind, string, string, bool, string, string, bool) error {
	return nil
}
func (f *fakeMutator) PatchSecretData(context.Context, string, string, string, string, bool) error {
	return nil
}
func (f *fakeMutator) PatchConfigMapData(context.Context, string, string, string, string, bool) error {
	return nil
}
func (f *fakeMutator) HelmRollback(_ context.Context, namespace, name string, revision int) error {
	f.namespace, f.name, f.revision = namespace, name, revision
	return f.err
}
func (f *fakeMutator) RolloutUndo(context.Context, string, string, int) error { return nil }

func newSession() *tui.Session {
	return &tui.Session{Theme: tui.Dark(), Location: tui.Location{Context: "test-cluster"}}
}

func step(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c != nil {
				m = step(t, m, c())
			}
		}
		return m
	}
	updated, cmd := m.Update(msg)
	next := *updated.(*Model)
	if cmd != nil {
		return step(t, next, cmd())
	}
	return next
}

func revisionSecret(namespace, name, status string, revision int) *corev1.Secret {
	return kube.EncodeHelmReleaseSecret(kube.HelmRelease{
		Namespace: namespace, Name: name, Chart: "postgresql", ChartVersion: "12.1.9",
		Revision: revision, Status: status,
	})
}

func TestLoadSortsRevisionsNewestFirst(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindSecret: {
			revisionSecret("production", "postgresql", "superseded", 1),
			revisionSecret("production", "postgresql", "deployed", 3),
			revisionSecret("production", "postgresql", "superseded", 2),
			revisionSecret("production", "other-release", "deployed", 1),
		},
	}}
	m := New(Config{Session: newSession(), Lister: lister, Namespace: "production", Name: "postgresql"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateReady {
		t.Fatalf("expected ready state, got %s (feedback=%q)", m.state, m.feedback)
	}
	if len(m.revisions) != 3 {
		t.Fatalf("expected 3 revisions for postgresql, got %d", len(m.revisions))
	}
	for i, want := range []int{3, 2, 1} {
		if m.revisions[i].Revision != want {
			t.Fatalf("revisions[%d].Revision = %d, want %d", i, m.revisions[i].Revision, want)
		}
	}
	body := plain(m.railBody(m.Theme(), 120, 20))
	if !strings.Contains(body, "(current)") {
		t.Fatalf("expected the current revision marked, got:\n%s", body)
	}
}

func TestRollbackToSelectedRevisionConfirmsAndExecutes(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindSecret: {
			revisionSecret("production", "postgresql", "superseded", 1),
			revisionSecret("production", "postgresql", "deployed", 2),
		},
	}}
	mut := &fakeMutator{}
	m := New(Config{Session: newSession(), Lister: lister, Mutator: mut, Namespace: "production", Name: "postgresql"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m.moveSelection(1) // select revision 1 (the older one)
	if rev, ok := m.selectedRevision(); !ok || rev.Revision != 1 {
		t.Fatalf("expected revision 1 selected, got %+v (ok=%v)", rev, ok)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "R"})
	if !m.actions.Active() {
		t.Fatal("expected a pending rollback confirm after 'R'")
	}
	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if mut.namespace != "production" || mut.name != "postgresql" || mut.revision != 1 {
		t.Fatalf("HelmRollback called with ns=%q name=%q rev=%d, want production/postgresql/1", mut.namespace, mut.name, mut.revision)
	}
}

// TestKeybarGoesOfflineAndHidesRollback pins the cross-cutting 4a fix
// (docs/design README.md §52, §301): helmhistory must show the OFFLINE pill
// and drop rollback from the keybar while disconnected, not just browse.
func TestKeybarGoesOfflineAndHidesRollback(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindSecret: {
			revisionSecret("production", "postgresql", "superseded", 1),
			revisionSecret("production", "postgresql", "deployed", 2),
		},
	}}
	mut := &fakeMutator{}
	m := New(Config{Session: newSession(), Lister: lister, Mutator: mut, Namespace: "production", Name: "postgresql"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnReconnecting, Err: "dial timeout"})
	kb := m.Keybar()
	if kb.Pill != tui.ModeOffline || kb.PillText != "OFFLINE" {
		t.Fatalf("Pill/PillText = %v/%q while offline, want ModeOffline/OFFLINE", kb.Pill, kb.PillText)
	}
	for _, g := range kb.Groups {
		for _, h := range g {
			if h.Key == verbs.Rollback.Key {
				t.Fatalf("expected rollback hint hidden while offline, got groups %+v", kb.Groups)
			}
		}
	}

	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnConnected})
	kb = m.Keybar()
	if kb.PillText != "HELM" {
		t.Fatalf("PillText = %q after reconnect, want HELM", kb.PillText)
	}
}

func TestEscReturnsToPreviousTask(t *testing.T) {
	m := New(Config{Session: newSession(), Lister: fakeLister{}, Namespace: "production", Name: "postgresql"})
	m.SetSize(120, 36)

	_, cmd := m.Update(tea.KeyPressMsg{Text: "esc"})
	if cmd == nil {
		t.Fatal("expected a Cmd from esc")
	}
	if _, ok := cmd().(tui.BackMsg); !ok {
		t.Fatalf("expected esc to produce tui.BackMsg, got %T", cmd())
	}
}

// TestMoveSelectionScrollsOffset guards against the rail's Offset staying
// pinned at 0 while Selected scrolls off the bottom of the rendered
// viewport — a real gap this screen had (unlike nodedetail/routetable,
// which always tracked m.offset alongside m.selected).
func TestMoveSelectionScrollsOffset(t *testing.T) {
	objs := make([]runtime.Object, 40)
	for i := range objs {
		objs[i] = revisionSecret("production", "postgresql", "superseded", i+1)
	}
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindSecret: objs}}
	m := New(Config{Session: newSession(), Lister: lister, Namespace: "production", Name: "postgresql"})
	m.SetSize(120, 24)
	m = step(t, m, m.Init()())

	if m.offset != 0 {
		t.Fatalf("initial offset = %d, want 0", m.offset)
	}
	rows := m.tableDataRows()
	if rows >= len(m.revisions) {
		t.Fatalf("tableDataRows() = %d, want fewer than %d revisions so scrolling is exercised", rows, len(m.revisions))
	}

	for i := 0; i < len(m.revisions)-1; i++ {
		m.moveSelection(1)
	}
	if m.selected != len(m.revisions)-1 {
		t.Fatalf("selected = %d, want %d", m.selected, len(m.revisions)-1)
	}
	if m.offset == 0 {
		t.Fatalf("offset stayed 0 after scrolling selection to the last row (selected=%d, revisions=%d) — the viewport never followed the cursor", m.selected, len(m.revisions))
	}
	if m.selected < m.offset || m.selected >= m.offset+rows {
		t.Fatalf("selected %d not within rendered viewport [%d, %d)", m.selected, m.offset, m.offset+rows)
	}
}

// unsyncedLister is the shape a lazily-started cache presents on first read:
// an empty answer that means nothing yet, until synced flips.
type unsyncedLister struct {
	inner  fakeLister
	synced *bool
}

func (l *unsyncedLister) ListRaw(ctx context.Context, kind kube.ResourceKind, ns string) ([]runtime.Object, error) {
	if !*l.synced {
		return nil, nil
	}
	return l.inner.ListRaw(ctx, kind, ns)
}

func (l *unsyncedLister) KindSynced(kube.ResourceKind) bool { return *l.synced }

// TestNoEmptyStateWhileTheReleaseCacheFills is the regression test for 18a
// announcing "no revisions found — the release secrets may have been
// deleted" for a couple of seconds after opening, before the data arrived.
// Release Secrets live in a cache that starts on first read, so that empty
// first answer said nothing about the cluster — and what it displayed was
// alarming rather than merely wrong.
func TestNoEmptyStateWhileTheReleaseCacheFills(t *testing.T) {
	synced := false
	lister := &unsyncedLister{synced: &synced, inner: fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindSecret: {revisionSecret("default", "web", "deployed", 1)},
	}}}
	m := New(Config{Session: newSession(), Lister: lister, Namespace: "default", Name: "web"})
	m.SetSize(120, 36)

	// Deliberately not step(): it drains commands recursively, and the retry
	// this schedules re-arms for as long as the cache stays unsynced — which
	// here is forever, by design.
	updated, _ := m.Update(m.load()())
	m = *updated.(*Model)

	if m.state != tui.TaskStateLoading {
		t.Fatalf("state = %s, want loading while the release cache is still filling", m.state)
	}
	if got := plain(m.Render()); strings.Contains(got, "no revisions found") {
		t.Fatalf("claimed the release's secrets were deleted while its cache was still filling:\n%s", got)
	}
}

// TestEmptyStateOnceTheCacheIsSettled: the message is right when it's true,
// so a settled-and-genuinely-empty cache must still reach it.
func TestEmptyStateOnceTheCacheIsSettled(t *testing.T) {
	synced := true
	lister := &unsyncedLister{synced: &synced, inner: fakeLister{objs: map[kube.ResourceKind][]runtime.Object{}}}
	m := New(Config{Session: newSession(), Lister: lister, Namespace: "default", Name: "web"})
	m.SetSize(120, 36)

	updated, _ := m.Update(m.load()())
	m = *updated.(*Model)

	if m.state != tui.TaskStateEmpty {
		t.Fatalf("state = %s, want empty for a settled cache with no revisions", m.state)
	}
}

// TestRetryResolvesOnceTheCacheFills drives it through: the scheduled retry
// re-reads and settles on the real revisions.
func TestRetryResolvesOnceTheCacheFills(t *testing.T) {
	synced := false
	lister := &unsyncedLister{synced: &synced, inner: fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindSecret: {revisionSecret("default", "web", "deployed", 1)},
	}}}
	m := New(Config{Session: newSession(), Lister: lister, Namespace: "default", Name: "web"})
	m.SetSize(120, 36)
	updated, _ := m.Update(m.load()())
	m = *updated.(*Model)

	synced = true // the cache fills while the retry is pending
	updated, cmd := m.Update(tui.CacheSyncRetryMsg{Gen: m.reloadEpoch})
	m = *updated.(*Model)
	if cmd == nil {
		t.Fatal("expected the retry to re-issue a load")
	}
	updated, _ = m.Update(cmd())
	m = *updated.(*Model)

	if m.state != tui.TaskStateReady {
		t.Fatalf("state = %s, want ready once the cache filled (feedback %q)", m.state, m.feedback)
	}
	if len(m.revisions) != 1 {
		t.Fatalf("got %d revisions, want 1", len(m.revisions))
	}
}

// TestLoadingStateRendersTheFullShell pins 15a's claim for 18a's history:
// the shell — breadcrumb, strip, the rail's own column headers, the keybar's
// live esc — paints in the first frame, and only the rows are replaced by
// placeholders. It used to be a bare centered spinner over an empty body.
func TestLoadingStateRendersTheFullShell(t *testing.T) {
	m := New(Config{Session: newSession(), Lister: fakeLister{}, Namespace: "production", Name: "postgresql"})
	m.SetSize(120, 36)
	view := plain(m.Render())

	for _, want := range []string{
		"production", "postgresql", "History", // shell breadcrumb
		"loading postgresql history",                                 // header timer
		"reading postgresql revisions", "decoded from the release's", // strip
		"REV", "STATUS", "CHART", "UPDATED", // the rail's real column headers
		"– of –",   // placeholder footer
		"esc",      // back is live from the first frame
		"rollback", // ...as the disabled-verb note
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("loading view missing %q:\n%s", want, view)
		}
	}
}

// TestLoadingHeaderTimerAdvances checks the 15a header's "· 0.4s" counter
// ticks off the spinner's own TickMsg rather than staying frozen at 0s, and
// that it does so without Render reading the clock (the purity invariant).
func TestLoadingHeaderTimerAdvances(t *testing.T) {
	m := New(Config{Session: newSession(), Lister: fakeLister{}, Namespace: "production", Name: "postgresql"})
	m.SetSize(120, 36)
	m.loadStartedAt = m.loadStartedAt.Add(-2 * time.Second)

	updated, _ := m.Update(spinner.TickMsg{Time: time.Now()})
	view := plain(updated.(*Model).Render())
	if !strings.Contains(view, "history · 2.") {
		t.Fatalf("expected the header timer to show ~2s elapsed:\n%s", view)
	}
}

// TestLoadingAndReadyStripsAreTheSameHeight is what makes the revisions
// landing a fill-in rather than a relayout: the strip and its divider rule
// come out of tui.FrameBodyHeight, so a strip that only appeared once the
// data arrived would shove the rail down two rows at that exact moment.
func TestLoadingAndReadyStripsAreTheSameHeight(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindSecret: {revisionSecret("production", "postgresql", "deployed", 1)},
	}}
	m := New(Config{Session: newSession(), Lister: lister, Namespace: "production", Name: "postgresql"})
	m.SetSize(120, 36)
	loading := m.stripLineCount()

	m = step(t, m, m.Init()())
	if m.state != tui.TaskStateReady {
		t.Fatalf("expected ready state, got %s", m.state)
	}
	if ready := m.stripLineCount(); ready != loading {
		t.Fatalf("strip is %d lines while loading and %d when ready — the rail relayouts on arrival", loading, ready)
	}
}
