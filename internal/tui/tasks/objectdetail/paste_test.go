package objectdetail

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/config"
	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui/actions"
)

// TestPasteIntoProdDeleteConfirm: deleting a discovered CRD object always goes
// through the type-the-name modal in a prod context (CLAUDE.md's CRD rule),
// and that buffer accepts a pasted name. 14d has no other text entry.
func TestPasteIntoProdDeleteConfirm(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		certificateKind(): {certObj("api-tls", map[string]any{"type": "Ready", "status": "True"})},
	}}
	mut := &fakeMutator{}
	sess := testSession()
	sess.Config = config.Config{ProdContexts: []string{"test-cluster"}}
	m := New(Config{
		Session: sess, Lister: lister, Events: fakeEvents{}, Mutator: mut,
		Kind: certificateKind(), Namespace: "default", Name: "api-tls",
	})
	updated, _ := step(t, &m, m.load()())
	got := updated.(*Model)

	updated, _ = step(t, got, tea.KeyPressMsg{Text: "ctrl+d"})
	got = updated.(*Model)
	if got.actions.Tier() != actions.TierModal {
		t.Fatalf("expected the type-the-name modal, tier=%v", got.actions.Tier())
	}

	updated, _ = step(t, got, tea.PasteMsg{Content: "api-tls"})
	got = updated.(*Model)
	if name := got.actions.TypedName(); name != "api-tls" {
		t.Fatalf("typed name = %q, want %q", name, "api-tls")
	}
	// step() hands back the Cmd rather than running it, so the delete only
	// happens once Confirm's Cmd is executed.
	updated, cmd := step(t, got, tea.KeyPressMsg{Text: "enter"})
	if cmd == nil {
		t.Fatal("expected enter to return the delete cmd once the name matched")
	}
	step(t, updated.(*Model), cmd())
	if len(mut.deleted) != 1 || mut.deleted[0] != "api-tls" {
		t.Fatalf("deleted = %v, want [api-tls] once the pasted name matched", mut.deleted)
	}
}

// TestPasteWithNoConfirmIsIgnored: the resting screen has no buffer, so a
// stray paste must collect nowhere.
func TestPasteWithNoConfirmIsIgnored(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		certificateKind(): {certObj("api-tls", map[string]any{"type": "Ready", "status": "True"})},
	}}
	m := New(Config{
		Session: testSession(), Lister: lister, Events: fakeEvents{}, Mutator: &fakeMutator{},
		Kind: certificateKind(), Namespace: "default", Name: "api-tls",
	})
	updated, _ := step(t, &m, m.load()())
	got := updated.(*Model)

	updated, _ = step(t, got, tea.PasteMsg{Content: "api-tls"})
	if name := updated.(*Model).actions.TypedName(); name != "" {
		t.Fatalf("typed-name buffer collected %q with no confirm open", name)
	}
}
