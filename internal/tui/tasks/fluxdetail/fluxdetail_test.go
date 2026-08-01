package fluxdetail

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/kube/fake"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
)

func demoModel(t *testing.T, name string) Model {
	t.Helper()
	c := fake.NewDemo()
	reg, groups := resources.BuildDiscoveredRegistry(c.DiscoveredKinds(), c)
	sess := &tui.Session{Theme: tui.Dark(), Registry: reg, Groups: groups,
		Location: tui.Location{Context: "microk8s-cluster", Namespace: "flux-system"}}
	m := New(Config{Session: sess, Lister: c, Mutator: c,
		Kind: "Kustomization", Namespace: "flux-system", Name: name})
	m.SetSize(120, 30)
	upd, _ := m.Update(m.load()())
	return *upd.(*Model)
}

func plain(s string) string {
	var b strings.Builder
	skip := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			skip = true
		case skip && r == 'm':
			skip = false
		case !skip:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// TestFailureCardPromotesTheReasonAndTheCulprit is §31a's headline: the
// condition message verbatim, plus the object the health check was actually
// waiting on, without the user digging for it.
func TestFailureCardPromotesTheReasonAndTheCulprit(t *testing.T) {
	view := plain(demoModel(t, "nebula-workers").Render())

	if !strings.Contains(view, "RECONCILE FAILED") {
		t.Errorf("expected the failure card:\n%s", view)
	}
	if !strings.Contains(view, "health check failed after 2m0s") {
		t.Errorf("the condition message must render verbatim:\n%s", view)
	}
	if !strings.Contains(view, "Deployment/nebula-worker") {
		t.Errorf("expected the drill-through to name the failing workload:\n%s", view)
	}
	// The count is the fact an operator acts on, resolved live from the
	// workload rather than restated from the Flux message.
	if !strings.Contains(view, "3/4") {
		t.Errorf("expected the workload's live replica count:\n%s", view)
	}
}

// TestAppliedButUnhealthyIsDistinctFromDrift pins the one line §31a says
// kills the most common Flux misdiagnosis.
func TestAppliedButUnhealthyIsDistinctFromDrift(t *testing.T) {
	failing := plain(demoModel(t, "nebula-workers").Render())
	if !strings.Contains(failing, "in sync") || !strings.Contains(failing, "applied, failing health checks") {
		t.Errorf("a failing-but-applied reconciler must say so:\n%s", failing)
	}

	// A suspended one that has genuinely drifted reports the other family.
	drifted := plain(demoModel(t, "nebula-infra").Render())
	if !strings.Contains(drifted, "source ahead") {
		t.Errorf("a drifted reconciler must report drift, not health:\n%s", drifted)
	}
	if strings.Contains(drifted, "applied, failing health checks") {
		t.Errorf("a drifted reconciler must not claim a health failure:\n%s", drifted)
	}
}

// TestSuspendedShowsPausedNotFailed guards §30a's rule carried into detail:
// a paused reconciler is not a broken one.
func TestSuspendedShowsPausedNotFailed(t *testing.T) {
	view := plain(demoModel(t, "nebula-infra").Render())
	if !strings.Contains(view, "SUSPENDED") {
		t.Errorf("expected the suspended card:\n%s", view)
	}
	if strings.Contains(view, "RECONCILE FAILED") {
		t.Errorf("a suspended reconciler must not be reported as failed:\n%s", view)
	}
	if !strings.Contains(view, "resume") {
		t.Errorf("the keybar should offer resume on a suspended object:\n%s", view)
	}
}

// TestHealthyReconcilerHasNoCard: zero chrome when there is nothing wrong.
func TestHealthyReconcilerHasNoCard(t *testing.T) {
	view := plain(demoModel(t, "flux-system").Render())
	if strings.Contains(view, "RECONCILE FAILED") || strings.Contains(view, "SUSPENDED") {
		t.Errorf("a healthy reconciler should carry no card:\n%s", view)
	}
	if !strings.Contains(view, "in sync") {
		t.Errorf("expected the in-sync marker:\n%s", view)
	}
}

// TestInventoryFoldsHealthyTailAndOpensTheCulprit covers the third band:
// trouble first, the rest folded, and ↵ landing on the object under the
// cursor — which after the sort is the failing one.
func TestInventoryFoldsHealthyTailAndOpensTheCulprit(t *testing.T) {
	m := demoModel(t, "nebula-workers")
	view := plain(m.Render())
	if !strings.Contains(view, "1 NOT READY") {
		t.Errorf("expected the not-ready count in the inventory header:\n%s", view)
	}
	if !strings.Contains(view, "+ 2 ready") {
		t.Errorf("expected the healthy tail folded:\n%s", view)
	}

	upd, cmd := m.Update(tea.KeyPressMsg{Text: "enter"})
	if cmd == nil {
		t.Fatal("expected ↵ to emit a goto cmd")
	}
	msg, ok := cmd().(tui.GotoResourceMsg)
	if !ok {
		t.Fatalf("expected GotoResourceMsg, got %T", cmd())
	}
	if msg.Kind != kube.KindDeployment || msg.Name != "nebula-worker" {
		t.Errorf("↵ should open the failing workload, got %+v", msg)
	}

	// tab reveals the rest.
	m2 := *upd.(*Model)
	m3, _ := m2.Update(tea.KeyPressMsg{Text: "tab"})
	if !strings.Contains(plain(m3.(*Model).Render()), "nebula-worker-config") {
		t.Errorf("tab should expand the healthy tail")
	}
}

// TestHelmReleaseSaysWhyItHasNoInventory — a HelmRelease delegates to
// Helm's own storage, and saying so beats an empty list that reads as
// "this manages nothing".
func TestHelmReleaseSaysWhyItHasNoInventory(t *testing.T) {
	c := fake.NewDemo()
	reg, groups := resources.BuildDiscoveredRegistry(c.DiscoveredKinds(), c)
	sess := &tui.Session{Theme: tui.Dark(), Registry: reg, Groups: groups,
		Location: tui.Location{Context: "microk8s-cluster", Namespace: "flux-system"}}
	m := New(Config{Session: sess, Lister: c, Mutator: c,
		Kind: kube.KindFluxHelmRelease, Namespace: "flux-system", Name: "nebula-redis"})
	m.SetSize(120, 30)
	upd, _ := m.Update(m.load()())
	view := plain(upd.(*Model).Render())
	if !strings.Contains(view, "inventory not published by this kind") {
		t.Errorf("expected the explicit no-inventory note:\n%s", view)
	}
}
