package browse

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/config"
	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui/actions"
)

func certModelWithMutator(t *testing.T, mut *fakeMutator, prod bool, objs ...runtime.Object) Model {
	t.Helper()
	reg, groups := resources.BuildDiscoveredRegistry([]kube.DiscoveredKind{discoveredCertificateDK()}, nil)
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.ResourceKind("Certificate"): objs,
	}}
	session := newSession()
	session.Registry, session.Groups = reg, groups
	session.Location.Kind = kube.ResourceKind("Certificate")
	session.Location.Namespace = "default"
	if prod {
		session.Config = config.Config{ProdContexts: []string{session.Location.Context}}
	}
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	return step(t, m, m.Init()())
}

// TestCertRenewShowsConfirmThenRenewsOnY covers §35c's 'r' — TierInline, so
// it waits for 'y' like every other single-target mutation.
func TestCertRenewShowsConfirmThenRenewsOnY(t *testing.T) {
	mut := &fakeMutator{}
	m := certModelWithMutator(t, mut, false, certificateInstance("admin-tls", "default"))

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
	if len(mut.certRenews) != 0 {
		t.Fatalf("expected no renew before 'y', got %v", mut.certRenews)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if len(mut.certRenews) != 1 || mut.certRenews[0] != "default/admin-tls" {
		t.Fatalf("certRenews = %v, want [default/admin-tls]", mut.certRenews)
	}
}

// TestCertRenewCancelsOnN covers the 'n' escape hatch every TierInline
// confirm gets — nothing runs, the model returns to the plain list.
func TestCertRenewCancelsOnN(t *testing.T) {
	mut := &fakeMutator{}
	m := certModelWithMutator(t, mut, false, certificateInstance("admin-tls", "default"))

	m = step(t, m, tea.KeyPressMsg{Text: "r"})
	m = step(t, m, tea.KeyPressMsg{Text: "n"})
	if m.actions.Active() {
		t.Fatal("expected 'n' to close the confirm")
	}
	if len(mut.certRenews) != 0 {
		t.Fatalf("expected no renew after 'n', got %v", mut.certRenews)
	}
}

// TestCertRenewNeverEscalatesInProd pins §35c's own explicit carve-out:
// unlike ArgoSync/RolloutRestart, renew stays the plain inline y/N even on a
// PROD context — it's delete that escalates, not reissue.
func TestCertRenewNeverEscalatesInProd(t *testing.T) {
	mut := &fakeMutator{}
	m := certModelWithMutator(t, mut, true, certificateInstance("admin-tls", "default"))

	m = step(t, m, tea.KeyPressMsg{Text: "r"})
	if m.actions.Tier() != actions.TierInline {
		t.Fatalf("expected renew to stay TierInline in PROD, got %v", m.actions.Tier())
	}
	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if len(mut.certRenews) != 1 {
		t.Fatalf("expected the plain y/N to still execute the renew, got %v", mut.certRenews)
	}
}

// TestCertRenewKeybarHint covers §35c's keybar group: 'r' renew is offered
// whenever the current kind is Certificate with a wired mutator.
func TestCertRenewKeybarHint(t *testing.T) {
	mut := &fakeMutator{}
	m := certModelWithMutator(t, mut, false, certificateInstance("admin-tls", "default"))
	kb := m.Keybar()
	var found bool
	for _, group := range kb.Groups {
		for _, hint := range group {
			if hint.Key == "r" && hint.Label == "renew" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected an 'r renew' keybar hint, got %+v", kb.Groups)
	}
}
