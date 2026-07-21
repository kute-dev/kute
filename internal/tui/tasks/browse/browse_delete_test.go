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

// TestCtrlKArmsForceDeleteInsideInlineConfirm covers the non-prod path:
// ctrl-k stages force-delete inside the same inline y/N confirm rather than
// jumping to the PROD type-the-name modal — a bare ctrl-k must run nothing,
// the keybar must flip to the destructive FORCE DELETE treatment with the
// exact --grace-period=0 --force command synced on the right, and only a
// second "y" actually executes DeleteResourceForced.
func TestCtrlKArmsForceDeleteInsideInlineConfirm(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-0")},
	}}
	mut := &fakeMutator{}
	m := New(Config{Session: newSession(), Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+d"})
	kb := m.Keybar()
	if !strings.Contains(kb.RightNote, "kubectl delete pod api-0") || strings.Contains(kb.RightNote, "--force") {
		t.Fatalf("expected the plain delete will-run line before arming, got %q", kb.RightNote)
	}
	found := false
	for _, h := range kb.Groups[0] {
		if h.Key == "ctrl-k" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a discoverable ctrl-k hint in the inline confirm's Groups, got %+v", kb.Groups)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+k"})
	if m.actions.Tier() != actions.TierInline {
		t.Fatalf("expected force-delete to stay staged at TierInline, not jump to the PROD modal, got %v", m.actions.Tier())
	}
	if !m.actions.ForceArmed() {
		t.Fatal("expected ctrl+k to arm force-delete")
	}
	if len(mut.forceDeleted) != 0 || len(mut.deleted) != 0 {
		t.Fatalf("expected ctrl+k alone to run nothing, deleted=%v forceDeleted=%v", mut.deleted, mut.forceDeleted)
	}
	kb = m.Keybar()
	if kb.PillText != "FORCE DELETE" {
		t.Fatalf("expected the FORCE DELETE pill once armed, got %q", kb.PillText)
	}
	if kb.RightNote != "kubectl delete pod api-0 -n default --grace-period=0 --force" {
		t.Fatalf("expected the synced force-delete will-run line, got %q", kb.RightNote)
	}

	// "n" while armed backs out to the plain prompt instead of cancelling.
	m = step(t, m, tea.KeyPressMsg{Text: "n"})
	if !m.actions.Active() || m.actions.ForceArmed() {
		t.Fatalf("expected n to disarm back to the plain prompt, not cancel: active=%v armed=%v", m.actions.Active(), m.actions.ForceArmed())
	}
	kb = m.Keybar()
	if kb.PillText != "CONFIRM" {
		t.Fatalf("expected the plain CONFIRM pill after disarming, got %q", kb.PillText)
	}

	// Re-arm and confirm for real.
	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+k"})
	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if len(mut.forceDeleted) != 1 || mut.forceDeleted[0] != "api-0" {
		t.Fatalf("forceDeleted = %v, want [api-0]", mut.forceDeleted)
	}
	if len(mut.deleted) != 0 {
		t.Fatalf("expected the plain delete path untouched, got %v", mut.deleted)
	}
}

// TestEscArmedForceDeleteCancelsOutright confirms esc still ends the whole
// confirm even while force-armed, unlike n's disarm-only behavior.
func TestEscArmedForceDeleteCancelsOutright(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-0")},
	}}
	mut := &fakeMutator{}
	m := New(Config{Session: newSession(), Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+d"})
	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+k"})
	m = step(t, m, tea.KeyPressMsg{Text: "esc"})
	if m.actions.Active() {
		t.Fatal("expected esc to cancel the confirm outright, even while force-armed")
	}
	if len(mut.deleted) != 0 || len(mut.forceDeleted) != 0 {
		t.Fatalf("expected nothing to execute after esc, deleted=%v forceDeleted=%v", mut.deleted, mut.forceDeleted)
	}
}
