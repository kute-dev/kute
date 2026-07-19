package browse

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/config"
	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui/actions"
)

// TestCtrlDShowsConcreteGracePeriod pins 8b's fix: the confirm modal must
// show the pod's actual terminationGracePeriodSeconds (docs/design
// README.md §8b: "30s"), not the generic "default grace period applies" —
// this pod's spec sets a non-default 45s, so a hardcoded "30" would also be
// wrong, proving the value is actually threaded through rather than
// hardcoded.
func TestCtrlDShowsConcreteGracePeriod(t *testing.T) {
	grace := int64(45)
	podWithGrace := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-0", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers:                    []corev1.Container{{Name: "c"}},
			TerminationGracePeriodSeconds: &grace,
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Ready: true}}},
	}
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {podWithGrace},
	}}
	sess := newSession()
	sess.Config = config.Config{ProdContexts: []string{sess.Location.Context}}
	m := New(Config{Session: sess, Lister: lister, Mutator: &fakeMutator{}})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+d"})
	view := plain(m.Render())
	if !strings.Contains(view, "grace period 45s applies") {
		t.Fatalf("expected the concrete grace period in the modal:\n%s", view)
	}
	if strings.Contains(view, "default grace period applies") {
		t.Fatalf("expected the generic fallback text to be gone once a real value is known:\n%s", view)
	}
}

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

// TestCtrlDNoOpsWhileOffline pins the 4a fix: OFFLINE's keybar note ("mutating
// actions disabled") must actually be enforced, not just displayed — ctrl+d
// must neither open the confirm prompt nor reach the mutator while the
// connection is mid-outage.
func TestCtrlDNoOpsWhileOffline(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-0")},
	}}
	mut := &fakeMutator{}
	m := New(Config{Session: newSession(), Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnReconnecting, Err: "boom"})
	if !m.offline() {
		t.Fatal("expected offline() = true after a Reconnecting ConnStateMsg")
	}

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+d"})
	if m.actions.Active() {
		t.Fatal("ctrl+d must not open a confirm prompt while offline")
	}
	if len(mut.deleted) != 0 {
		t.Fatalf("mutator must not run while offline, got %v", mut.deleted)
	}

	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnConnected})
	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+d"})
	if !m.actions.Active() {
		t.Fatal("expected ctrl+d to work again once back online")
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
