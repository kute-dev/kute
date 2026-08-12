package poddetail

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/config"
	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui/actions"
)

// TestPasteIntoProdDeleteConfirm: 5a's one text buffer is the PROD
// type-the-name modal, and it accepts a pasted name — the gate is "you
// produced the exact name", not "you typed it by hand".
func TestPasteIntoProdDeleteConfirm(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {runningPod("api-0", "default", "node-a")},
	}}
	mut := &fakeMutator{}
	sess := newSession()
	sess.Config = config.Config{ProdContexts: []string{"test-cluster"}}
	m := New(Config{Session: sess, Lister: lister, Mutator: mut, Namespace: "default", Name: "api-0"})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "D"})
	if m.actions.Tier() != actions.TierModal {
		t.Fatalf("expected the type-the-name modal, tier=%v", m.actions.Tier())
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'v'}); cmd == nil {
		t.Fatal("ctrl+v in the modal returned no cmd, want the clipboard read")
	}

	m = step(t, m, tea.PasteMsg{Content: "api-0"})
	if got := m.actions.TypedName(); got != "api-0" {
		t.Fatalf("typed name = %q, want %q", got, "api-0")
	}
	m = step(t, m, tea.KeyPressMsg{Text: "enter"})
	if len(mut.deleted) != 1 {
		t.Fatalf("deleted = %v, want the delete to run once the pasted name matched", mut.deleted)
	}
}

// TestPasteWithNoConfirmIsIgnored: 5a's resting keys are bare letters, so a
// stray paste must not collect anywhere.
func TestPasteWithNoConfirmIsIgnored(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {runningPod("api-0", "default", "node-a")},
	}}
	m := New(Config{Session: newSession(), Lister: lister, Mutator: &fakeMutator{}, Namespace: "default", Name: "api-0"})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.PasteMsg{Content: "api-0"})
	if got := m.actions.TypedName(); got != "" {
		t.Fatalf("typed-name buffer collected %q with no confirm open", got)
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'v'}); cmd != nil {
		t.Fatal("ctrl+v with no buffer open should not read the clipboard")
	}
}

// TestPasteIntoInlineConfirmIsIgnored: a non-prod delete is a y/N prompt with
// no text field.
func TestPasteIntoInlineConfirmIsIgnored(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {runningPod("api-0", "default", "node-a")},
	}}
	mut := &fakeMutator{}
	m := New(Config{Session: newSession(), Lister: lister, Mutator: mut, Namespace: "default", Name: "api-0"})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "D"})
	if m.actions.Tier() != actions.TierInline {
		t.Fatalf("expected the inline y/N confirm, tier=%v", m.actions.Tier())
	}
	m = step(t, m, tea.PasteMsg{Content: "api-0"})
	if got := m.actions.TypedName(); got != "" {
		t.Fatalf("inline confirm collected %q; it has no text field", got)
	}
	if len(mut.deleted) != 0 {
		t.Fatalf("nothing should have been deleted: %v", mut.deleted)
	}
}
