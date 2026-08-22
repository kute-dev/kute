package certchain

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/kube/fake"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
)

// demoModelWithMutator is demoModel's own twin, wired with a mutator (the
// same fake.Cluster instance, which implements kube.Mutator in full) —
// §35c's 'r' is the only verb this screen has that needs one.
func demoModelWithMutator(t *testing.T, namespace, name string) (Model, *fake.Cluster) {
	t.Helper()
	c := fake.NewDemo()
	sess := &tui.Session{Theme: tui.Dark(), Location: tui.Location{Context: "microk8s-cluster", Namespace: namespace}}
	m := New(Config{Session: sess, Lister: c, Mutator: c, Namespace: namespace, Name: name})
	m.SetSize(120, 30)
	return step(t, m, m.load()()), c
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

// TestCertRenewShowsConfirmThenRenewsOnY covers §35c's 'r' from the chain
// screen: TierInline, the exact literal-kubectl-patch will-run line (never
// `cmctl renew` — the will-run contract), and the row flips in place once
// the confirm lands. Works "identically from the list and the chain view"
// per §35c's own note — this pins the chain-view half.
func TestCertRenewShowsConfirmThenRenewsOnY(t *testing.T) {
	m, _ := demoModelWithMutator(t, "default", "admin-tls")

	m = step(t, m, tea.KeyPressMsg{Text: "r"})
	if !m.actions.Active() || m.actions.Tier() != actions.TierInline {
		t.Fatalf("expected 'r' to open the inline prompt, tier=%v", m.actions.Tier())
	}
	kb := m.Keybar()
	if !strings.Contains(kb.RightNote, "will run: kubectl patch certificate/admin-tls") {
		t.Fatalf("expected the literal kubectl patch will-run line in the confirm, got %q", kb.RightNote)
	}
	if !strings.Contains(kb.RightNote, "--subresource=status") {
		t.Fatalf("expected the status-subresource flag in the will-run line, got %q", kb.RightNote)
	}
	if strings.Contains(kb.RightNote, "cmctl") {
		t.Fatalf("will-run line must not name the cmctl binary: %q", kb.RightNote)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	view := plain(m.Render())
	if !strings.Contains(view, "Ready=False · Issuing") {
		t.Fatalf("expected the chain row to flip to Ready=False · Issuing after renew — no toast, no navigation:\n%s", view)
	}
}

// TestCertRenewCancelsOnN covers the 'n' escape hatch every TierInline
// confirm gets — nothing runs, the screen returns to plain navigation.
func TestCertRenewCancelsOnN(t *testing.T) {
	m, c := demoModelWithMutator(t, "default", "admin-tls")

	m = step(t, m, tea.KeyPressMsg{Text: "r"})
	m = step(t, m, tea.KeyPressMsg{Text: "n"})
	if m.actions.Active() {
		t.Fatal("expected 'n' to close the confirm")
	}
	if status := certReadyStatus(t, c, "default", "admin-tls"); status != "True" {
		t.Fatalf("expected Ready to stay True after 'n', got %q", status)
	}
}

// certReadyStatus reads back name's Ready condition status directly from the
// fake cluster's cache — the same "prove the write landed" idiom the kube
// package's own dynamic-client tests use.
func certReadyStatus(t *testing.T, c *fake.Cluster, namespace, name string) string {
	t.Helper()
	objs, err := c.ListRaw(t.Context(), kube.KindCertificate, namespace)
	if err != nil {
		t.Fatalf("ListRaw: %v", err)
	}
	for _, obj := range objs {
		u, ok := obj.(*unstructured.Unstructured)
		if !ok || u.GetName() != name {
			continue
		}
		conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
		for _, raw := range conds {
			cond, ok := raw.(map[string]any)
			if !ok || cond["type"] != "Ready" {
				continue
			}
			status, _ := cond["status"].(string)
			return status
		}
	}
	t.Fatalf("%s/%s not found or has no Ready condition", namespace, name)
	return ""
}

// TestCertRenewNeverEscalatesInProd pins §35c's own explicit carve-out:
// unlike a PROD-tiered verb, renew stays the plain inline y/N even on a PROD
// context — it's delete that escalates, not reissue.
func TestCertRenewNeverEscalatesInProd(t *testing.T) {
	m, _ := demoModelWithMutator(t, "default", "admin-tls")
	m.session.Config.ProdContexts = []string{m.session.Location.Context}

	m = step(t, m, tea.KeyPressMsg{Text: "r"})
	if m.actions.Tier() != actions.TierInline {
		t.Fatalf("expected renew to stay TierInline in PROD, got %v", m.actions.Tier())
	}
}

// TestCertRenewHiddenOffline covers the 4a offline treatment: the renew
// hint disappears from the keybar while the connection is down, the same
// way a mutating verb always does.
func TestCertRenewHiddenOffline(t *testing.T) {
	m, _ := demoModelWithMutator(t, "default", "admin-tls")
	m.conn.Phase = kube.ConnReconnecting
	kb := m.Keybar()
	for _, group := range kb.Groups {
		for _, hint := range group {
			if hint.Label == "renew" {
				t.Fatalf("expected no renew hint while offline, got %+v", kb.Groups)
			}
		}
	}
}
