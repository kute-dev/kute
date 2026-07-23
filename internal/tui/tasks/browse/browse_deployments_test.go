package browse

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
)

func deploymentObj(ns, name string) *appsv1.Deployment {
	replicas := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Generation: 1},
		Spec: appsv1.DeploymentSpec{Replicas: &replicas, Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Image: "app:1.0"}}},
		}},
		Status: appsv1.DeploymentStatus{Replicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1, ObservedGeneration: 1},
	}
}

func TestDeploymentColumnsRenderRolloutAndImage(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {deploymentObj("default", "api")},
	}}
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	view := plain(m.Render())
	for _, want := range []string{"api", "ROLLOUT", "IMAGE", "stable", "app:1.0"} {
		if !strings.Contains(view, want) {
			t.Fatalf("deployment view missing %q:\n%s", want, view)
		}
	}
}

// TestDeploymentsKeybarPillIsShortForm pins 9a: the keybar pill reads
// "DEPLOY", not the plural "DEPLOYMENTS" the generic
// strings.ToUpper(desc.Display) path would otherwise produce.
func TestDeploymentsKeybarPillIsShortForm(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {deploymentObj("default", "api")},
	}}
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if got := m.Keybar().PillText; got != "DEPLOY" {
		t.Fatalf("Keybar().PillText = %q, want %q", got, "DEPLOY")
	}
}

// TestPlainRKeyDoesNotTouchDeploymentRollout pins RolloutRestart's move off
// bare 'r' (verbs.go's own doc comment on the move): 'r' is retry/re-probe
// vocabulary everywhere else in the app, so a Deployment row must ignore it
// entirely now — only ctrl-r reaches beginRolloutRestart.
func TestPlainRKeyDoesNotTouchDeploymentRollout(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {deploymentObj("default", "api")},
	}}
	mut := &fakeMutator{}
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "r"})
	if m.actions.Active() {
		t.Fatal("plain 'r' should not open a confirm on a Deployment row")
	}
	if len(mut.restarted) != 0 {
		t.Fatalf("plain 'r' unexpectedly restarted the rollout: %v", mut.restarted)
	}
}

// TestCtrlRShowsConfirmThenRestartsRolloutOnY pins RolloutRestart's new
// TierInline confirm (moved off TierNone's unconfirmed immediate fire):
// ctrl-r shows the will-run line and waits for 'y', mirroring
// TestCtrlDNonProdShowsInlinePromptAndDeletesOnY's shape for delete.
func TestCtrlRShowsConfirmThenRestartsRolloutOnY(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {deploymentObj("default", "api")},
	}}
	mut := &fakeMutator{}
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+r"})
	if !m.actions.Active() || m.actions.Tier() != actions.TierInline {
		t.Fatalf("expected ctrl+r to open the inline prompt, tier=%v", m.actions.Tier())
	}
	kb := m.Keybar()
	if !strings.Contains(kb.RightNote, "kubectl rollout restart deploy/api -n default") {
		t.Fatalf("expected the will-run line in the confirm, got %q", kb.RightNote)
	}
	if len(mut.restarted) != 0 {
		t.Fatalf("expected no restart before 'y', got %v", mut.restarted)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if len(mut.restarted) != 1 || mut.restarted[0] != "api" {
		t.Fatalf("restarted = %v, want [api]", mut.restarted)
	}
}

func TestEnterOnDeploymentSwitchesToPodsFilteredByName(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {deploymentObj("default", "api")},
		kube.KindPod: {
			pod("default", "api-abc123"),
			pod("default", "worker-0"),
		},
	}}
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	next := *updated.(*Model)
	if cmd != nil {
		next = step(t, next, cmd())
	}
	if next.kind != kube.KindPod {
		t.Fatalf("expected kind switched to Pod, got %s", next.kind)
	}
	if next.filterQuery != "api" {
		t.Fatalf("filterQuery = %q, want %q", next.filterQuery, "api")
	}
	view := plain(next.Render())
	if !strings.Contains(view, "api-abc123") {
		t.Fatalf("expected the owned pod to remain visible:\n%s", view)
	}
	if strings.Contains(view, "worker-0") {
		t.Fatalf("expected the unrelated pod to be filtered out:\n%s", view)
	}
}

func TestEscFromDeploymentPodsReturnsToDeploymentAndSelectsRow(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {
			deploymentObj("default", "api"),
			deploymentObj("default", "worker"),
		},
		kube.KindPod: {
			pod("default", "api-abc123"),
			pod("default", "worker-0"),
		},
	}}
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	// Select the second row ("worker") so esc-back is proven to restore the
	// specific origin row, not just any Deployments row.
	m.moveSelection(1)
	row, ok := m.selectedRow()
	if !ok || row.Name != "worker" {
		t.Fatalf("expected worker selected before opening its pods, got %+v (ok=%v)", row, ok)
	}

	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	if m.kind != kube.KindPod || m.originName != "worker" {
		t.Fatalf("expected Pods filtered by worker, got kind=%s originName=%q", m.kind, m.originName)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "esc"})
	if m.kind != kube.KindDeployment {
		t.Fatalf("expected esc to switch back to Deployments, got %s", m.kind)
	}
	if m.originName != "" {
		t.Fatalf("expected originName cleared after esc-back, got %q", m.originName)
	}
	selected, ok := m.selectedRow()
	if !ok || selected.Name != "worker" {
		t.Fatalf("expected worker re-selected on Deployments, got %+v (ok=%v)", selected, ok)
	}
}

func TestDeploymentOriginClearsOnUnrelatedNavigation(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {deploymentObj("default", "api")},
		kube.KindPod: {
			pod("default", "api-abc123"),
			pod("staging", "api-xyz789"),
		},
		kube.KindNamespace: {namespace("default"), namespace("staging")},
	}}

	newAtDeploymentPods := func(t *testing.T) Model {
		t.Helper()
		session := newSession()
		session.Location.Kind = kube.KindDeployment
		m := New(Config{Session: session, Lister: lister})
		m.SetSize(120, 36)
		m = step(t, m, m.Init()())
		m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
		if m.originName != "api" {
			t.Fatalf("setup: expected originName set, got %q", m.originName)
		}
		return m
	}

	t.Run("switchKind", func(t *testing.T) {
		m := newAtDeploymentPods(t)
		m = step(t, m, tui.GotoKindMsg{Kind: kube.KindNamespace})
		if m.originName != "" {
			t.Fatalf("expected originName cleared after switching kind, got %q", m.originName)
		}
	})

	t.Run("switchNamespace", func(t *testing.T) {
		m := newAtDeploymentPods(t)
		m = step(t, m, tui.SwitchNamespaceMsg{Namespace: "staging"})
		if m.originName != "" {
			t.Fatalf("expected originName cleared after switching namespace, got %q", m.originName)
		}
	})

	t.Run("freshGoToResource", func(t *testing.T) {
		m := newAtDeploymentPods(t)
		m = step(t, m, tui.GotoResourceMsg{Kind: kube.KindPod, Name: "api-abc123"})
		if m.originName != "" {
			t.Fatalf("expected originName cleared after a fresh goto, got %q", m.originName)
		}
	})

	t.Run("filterCleared", func(t *testing.T) {
		m := newAtDeploymentPods(t)
		m.filterActive = true
		m = step(t, m, tea.KeyPressMsg{Text: "esc"})
		if m.originName != "" {
			t.Fatalf("expected originName cleared after clearing the filter, got %q", m.originName)
		}
	})

	t.Run("filterEdited", func(t *testing.T) {
		m := newAtDeploymentPods(t)
		m.filterActive = true
		m = step(t, m, tea.KeyPressMsg{Text: "x"})
		if m.originName != "" {
			t.Fatalf("expected originName cleared after editing the filter, got %q", m.originName)
		}
	})
}

func TestBreadcrumbShowsOriginDeploymentName(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {deploymentObj("default", "api")},
		kube.KindPod:        {pod("default", "api-abc123")},
	}}
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	header := m.Header()
	before := crumbText(header)
	if strings.Contains(before, "api › Pods") {
		t.Fatalf("expected no deployment name in breadcrumb before opening pods:\n%s", before)
	}

	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	after := crumbText(m.Header())
	if !strings.Contains(after, "api › Pods") {
		t.Fatalf("expected breadcrumb to include %q, got:\n%s", "api › Pods", after)
	}
}

func crumbText(h tui.HeaderState) string {
	var b strings.Builder
	for _, c := range h.Crumbs {
		b.WriteString(c.Text)
	}
	return b.String()
}

func TestEKeyOpensNamespaceScopedEvents(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-1")},
	}}
	var openedNamespace string
	session := newSession()
	m := New(Config{
		Session: session, Lister: lister,
		OpenEvents: func(namespace string, w, h int) (tea.Model, tea.Cmd) {
			openedNamespace = namespace
			return stubTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tea.KeyPressMsg{Text: "e"})
	if openedNamespace != "default" {
		t.Fatalf("expected events opened for namespace default, got %q", openedNamespace)
	}
	if _, ok := updated.(stubTask); !ok {
		t.Fatalf("expected Update to return the pushed stub task, got %T", updated)
	}
}

// TestGotoKindMsgEventPushesEventsTask covers the goto palette's Events
// entry (kind alias "e", internal/tui/goto.go's gotoAliases): unlike
// KindWhoCan/KindOverview, Event has a real resources.Descriptor and could
// list as a stock browse table, but 9b (tasks/events) is the curated
// experience for it, so GotoKindMsg{Kind: KindEvent} must redirect there
// the same way the 'e' key already does (TestEKeyOpensNamespaceScopedEvents
// above), not fall through to switchKind.
func TestGotoKindMsgEventPushesEventsTask(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-1")},
	}}
	var openedNamespace string
	session := newSession()
	m := New(Config{
		Session: session, Lister: lister,
		OpenEvents: func(namespace string, w, h int) (tea.Model, tea.Cmd) {
			openedNamespace = namespace
			return stubTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tui.GotoKindMsg{Kind: kube.KindEvent})
	if openedNamespace != "default" {
		t.Fatalf("expected events opened for namespace default, got %q", openedNamespace)
	}
	if _, ok := updated.(stubTask); !ok {
		t.Fatalf("expected GotoKindMsg{KindEvent} to push tasks/events, got %T", updated)
	}
}
