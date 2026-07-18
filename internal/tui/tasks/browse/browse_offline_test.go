package browse

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// TestOfflineRendersBannerStaleStripAndOfflineKeybar pins 4a (mvp-plan.md
// Phase 4): once a Reconnecting kube.ConnStateMsg lands, the health strip is
// replaced by the reconnect banner + stale-snapshot strip, the keybar swaps
// to the OFFLINE pill, and the table itself keeps rendering (browsing the
// snapshot still works) rather than blanking.
func TestOfflineRendersBannerStaleStripAndOfflineKeybar(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-1")},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	if m.state != tui.TaskStateReady {
		t.Fatalf("state = %s, want ready", m.state)
	}

	m = step(t, m, kube.ConnStateMsg{
		Phase:       kube.ConnReconnecting,
		Err:         "dial tcp 10.0.0.5:16443: i/o timeout",
		Attempt:     3,
		NextRetryAt: time.Now().Add(4 * time.Second),
	})

	if !m.offline() {
		t.Fatalf("offline() = false after a Reconnecting ConnStateMsg")
	}
	if m.state != tui.TaskStateReady {
		t.Fatalf("state = %s, want ready (browsing the stale snapshot still works)", m.state)
	}

	strips := m.Strips(120)
	if len(strips) != 2 {
		t.Fatalf("Strips() = %d lines while offline, want 2 (banner + stale)", len(strips))
	}
	banner := plain(strips[0])
	if !strings.Contains(banner, "dial tcp 10.0.0.5:16443: i/o timeout") {
		t.Errorf("banner strip = %q, want the verbatim conn error", banner)
	}
	if !strings.Contains(banner, "retry 3") {
		t.Errorf("banner strip = %q, want the attempt count", banner)
	}
	stale := plain(strips[1])
	if !strings.Contains(stale, "showing snapshot from") {
		t.Errorf("stale strip = %q, want the snapshot-age note", stale)
	}

	kb := m.Keybar()
	if kb.Pill != tui.ModeOffline || kb.PillText != "OFFLINE" {
		t.Errorf("Keybar Pill/PillText = %v/%q, want ModeOffline/OFFLINE", kb.Pill, kb.PillText)
	}
	if kb.RightNote != "mutating actions disabled" {
		t.Errorf("Keybar RightNote = %q, want the mutating-disabled note", kb.RightNote)
	}

	body := plain(m.tableBody(120, 30))
	if !strings.Contains(body, "api-1") {
		t.Errorf("table body dropped the row while offline: %q", body)
	}

	view := ansi.Strip(m.Render())
	if !strings.Contains(view, "disconnected") {
		t.Errorf("header badge should read disconnected while offline:\n%s", view)
	}
}

// fakeRetrier records RetryNow calls, standing in for *kube.Cluster/
// *fake.Cluster's real RetryNow in tests.
type fakeRetrier struct{ calls int }

func (f *fakeRetrier) RetryNow() { f.calls++ }

func TestOfflineRetryKeyCallsRetrier(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindPod: {pod("default", "a")}}}
	retrier := &fakeRetrier{}
	m := New(Config{Session: newSession(), Lister: lister, Retrier: retrier})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnReconnecting, Err: "boom"})

	updated, _ := m.updateKey(tea.KeyPressMsg{Text: "r"})
	m = *updated.(*Model)

	if retrier.calls != 1 {
		t.Fatalf("RetryNow calls = %d, want 1", retrier.calls)
	}
}

// TestConnectedClearsOfflineTreatment pins 4a's "on reconnect: silently
// return to live" — once a Connected ConnStateMsg lands, offline() must go
// back to false and the health strip returns.
func TestConnectedClearsOfflineTreatment(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindPod: {pod("default", "a")}}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnReconnecting, Err: "boom"})
	if !m.offline() {
		t.Fatalf("expected offline after Reconnecting")
	}

	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnConnected, Latency: 5 * time.Millisecond})
	if m.offline() {
		t.Fatalf("expected offline() = false after Connected")
	}
	if len(m.Strips(120)) != 1 {
		t.Fatalf("Strips() = %d lines once reconnected, want 1 (plain health strip)", len(m.Strips(120)))
	}
}

// forbiddenLister always errors with a typed apierrors.Forbidden for the
// given kind, everything else falls through to a normal fakeLister.
type forbiddenLister struct {
	fakeLister
	kind kube.ResourceKind
	err  error
}

func (f forbiddenLister) ListRaw(ctx context.Context, kind kube.ResourceKind, namespace string) ([]runtime.Object, error) {
	if kind == f.kind {
		return nil, f.err
	}
	return f.fakeLister.ListRaw(ctx, kind, namespace)
}

// TestPermissionDeniedRendersCardAndCopiesError pins 4b (mvp-plan.md Phase
// 4): a Forbidden list error puts browse into TaskStatePermissionDenied
// with a 403 card body (verbatim message, quoted entities highlighted) and
// wires 'y' to copy it via the clipboard, 'r' to retry the load.
func TestPermissionDeniedRendersCardAndCopiesError(t *testing.T) {
	msg := `User "dev-readonly" cannot list resource "secrets" in namespace "nva-stage"`
	lister := forbiddenLister{
		kind: kube.KindSecret,
		err:  apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "", errors.New(msg)),
	}
	sess := newSession()
	sess.Location.Kind = kube.KindSecret
	m := New(Config{Session: sess, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStatePermissionDenied {
		t.Fatalf("state = %s, want permission-denied", m.state)
	}

	body := plain(m.permissionDeniedBody(120, 30))
	if !strings.Contains(body, "403 Forbidden") {
		t.Errorf("body missing 403 Forbidden title:\n%s", body)
	}
	// apierrors.NewForbidden prefixes the cause with "<resource> is
	// forbidden: "; the card also word-wraps the line — check the quoted
	// entities individually rather than one continuous substring.
	for _, want := range []string{`"dev-readonly"`, `"secrets"`, `"nva-stage"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s from the verbatim RBAC message:\n%s", want, body)
		}
	}

	highlighted := highlightQuoted(msg, lipgloss.NewStyle(), lipgloss.NewStyle())
	if ansi.Strip(highlighted) != msg {
		t.Errorf("highlightQuoted changed the text: got %q want %q", ansi.Strip(highlighted), msg)
	}

	kb := m.Keybar()
	found := false
	for _, group := range kb.Groups {
		for _, h := range group {
			if h.Label == "copy error" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("Keybar missing the copy-error hint: %+v", kb.Groups)
	}

	updated, cmd := m.updateKey(tea.KeyPressMsg{Text: "y"})
	if cmd == nil {
		t.Fatalf("'y' should return a SetClipboard cmd")
	}
	_ = updated
}
