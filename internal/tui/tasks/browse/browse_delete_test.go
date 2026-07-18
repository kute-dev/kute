package browse

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/config"
	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui/actions"
)

// TestCtrlDNonProdShowsInlinePromptAndDeletesOnY exercises 8b's non-prod
// path from browse: the table stays visible (no modal body override), the
// keybar becomes the y/N prompt, and 'y' deletes.
func TestCtrlDNonProdShowsInlinePromptAndDeletesOnY(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-0")},
	}}
	mut := &fakeMutator{}
	m := New(Config{Session: newSession(), Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+d"})
	if !m.actions.Active() || m.actions.Tier() != actions.TierInline {
		t.Fatalf("expected ctrl+d in a non-prod context to open the inline prompt, tier=%v", m.actions.Tier())
	}
	// The table itself must still be visible — TierInline never overrides
	// Body().
	if !strings.Contains(plain(m.Render()), "api-0") {
		t.Fatalf("expected the table to stay visible under an inline confirm:\n%s", plain(m.Render()))
	}
	kb := m.Keybar()
	if kb.RightNote == "" || !strings.Contains(kb.RightNote, "api-0") {
		t.Fatalf("expected the keybar prompt to name the target, got %q", kb.RightNote)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if len(mut.deleted) != 1 || mut.deleted[0] != "api-0" {
		t.Fatalf("deleted = %v, want [api-0]", mut.deleted)
	}
}

// TestCtrlDProdOpensTypeNameModal exercises 8b's PROD escalation: the modal
// covers the body, enter no-ops until the name is typed, esc cancels.
func TestCtrlDProdOpensTypeNameModal(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-0")},
	}}
	mut := &fakeMutator{}
	sess := newSession()
	sess.Config = config.Config{ProdContexts: []string{sess.Location.Context}}
	m := New(Config{Session: sess, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+d"})
	if !m.actions.Active() || m.actions.Tier() != actions.TierModal {
		t.Fatalf("expected ctrl+d in a prod context to open the type-the-name modal, tier=%v", m.actions.Tier())
	}
	view := plain(m.Render())
	if !strings.Contains(view, "PROD CONTEXT") {
		t.Fatalf("expected the PROD CONTEXT tag in the modal:\n%s", view)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "enter"})
	if len(mut.deleted) != 0 {
		t.Fatalf("expected enter to no-op before the name matches: %v", mut.deleted)
	}
	for _, r := range "api-0" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Text: "enter"})
	if len(mut.deleted) != 1 || mut.deleted[0] != "api-0" {
		t.Fatalf("deleted = %v, want [api-0]", mut.deleted)
	}
}

// TestCtrlKEscalatesToForceDelete confirms the ctrl-k chord inside an
// active Pod-delete modal calls DeleteResourceForced, not DeleteResource.
func TestCtrlKEscalatesToForceDelete(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-0")},
	}}
	mut := &fakeMutator{}
	sess := newSession()
	sess.Config = config.Config{ProdContexts: []string{sess.Location.Context}}
	m := New(Config{Session: sess, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+d"})
	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+k"})
	for _, r := range "api-0" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Text: "enter"})
	if len(mut.forceDeleted) != 1 || mut.forceDeleted[0] != "api-0" {
		t.Fatalf("forceDeleted = %v, want [api-0]", mut.forceDeleted)
	}
	if len(mut.deleted) != 0 {
		t.Fatalf("expected the plain delete path untouched, got %v", mut.deleted)
	}
}
