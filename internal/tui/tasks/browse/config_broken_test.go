package browse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/config"
	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/testutil/testenv"
	"github.com/kute-dev/kute/internal/tui/actions"
)

// loadBrokenConfig writes body to config.Path() under a fresh HOME and loads
// it — the real Load path, since a broken file's fail-closed state isn't
// constructible from outside the config package. Not t.Parallel-safe.
func loadBrokenConfig(t *testing.T, body string) config.Config {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	if err := os.MkdirAll(filepath.Dir(config.Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.Path(), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return config.Load()
}

// TestBrokenConfigFailsClosedAndSaysSo is the regression for a hand-broken
// config.yaml silently un-PRODding every context: the header must say the
// file is unreadable, and ctrl+d must escalate to the type-the-name modal
// rather than the non-prod inline y/N.
func TestBrokenConfigFailsClosedAndSaysSo(t *testing.T) {
	sess := newSession()
	sess.Config = loadBrokenConfig(t, "prodContexts: [unterminated")
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-0")},
	}}
	m := New(Config{Session: sess, Lister: lister, Mutator: &fakeMutator{}})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if view := plain(m.Render()); !strings.Contains(view, "config.yaml unreadable · all PROD") {
		t.Fatalf("expected the broken-config chip in the header:\n%s", view)
	}
	m = step(t, m, tea.KeyPressMsg{Text: "D"})
	if !m.actions.Active() || m.actions.Tier() != actions.TierModal {
		t.Fatalf("a broken config must fail closed to the PROD delete modal, tier=%v", m.actions.Tier())
	}
}

// TestPartlyBrokenConfigKeepsProdContexts: a mistyped unrelated key is
// reported, but must not take the valid prodContexts list down with it.
func TestPartlyBrokenConfigKeepsProdContexts(t *testing.T) {
	sess := newSession()
	sess.Config = loadBrokenConfig(t, "prodContexts: [other]\nnodeShellImage: [x]\n")
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-0")},
	}}
	m := New(Config{Session: sess, Lister: lister, Mutator: &fakeMutator{}})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	view := plain(m.Render())
	if !strings.Contains(view, "config.yaml has errors") || strings.Contains(view, "all PROD") {
		t.Fatalf("expected the yellow has-errors chip, not all-PROD:\n%s", view)
	}
	m = step(t, m, tea.KeyPressMsg{Text: "D"})
	if m.actions.Tier() != actions.TierInline {
		t.Fatalf("a non-prod context must keep the inline confirm, tier=%v", m.actions.Tier())
	}
}
