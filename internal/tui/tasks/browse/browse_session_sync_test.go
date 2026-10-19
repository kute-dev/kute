package browse

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/state"
	"github.com/kute-dev/kute/internal/tui"
)

// TestAllNamespacesHotkeySyncsSessionLocation confirms 'a' (browse's
// in-place all-namespaces toggle) updates Session.Location.Namespace, not
// just m.namespace — the goto palette (tui/goto.go's gotoNamespace) reads
// Session.Location.Namespace directly, so a stale copy there left every
// kind's live palette count scoped to whatever namespace browse was last
// actually at, even once the header already said "∗ all namespaces".
func TestAllNamespacesHotkeySyncsSessionLocation(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("default", "api-0")},
	}}
	session := newSession()
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "a"})
	if m.namespace != "" {
		t.Fatalf("expected browse's own namespace to be all-namespaces, got %q", m.namespace)
	}
	if session.Location.Namespace != "" {
		t.Fatalf("expected Session.Location.Namespace synced to all-namespaces, got %q", session.Location.Namespace)
	}
}

// TestOpenDeploymentPodsSyncsSessionLocationKind confirms '↵' on a
// Deployment row (openDeploymentPods, 9a's recipe) updates
// Session.Location.Kind — the same class of staleness
// TestAllNamespacesHotkeySyncsSessionLocation covers for Namespace, just
// for Kind: gotoResourceKindOrder (tui/goto.go) reads Session.Location.Kind
// to rank the "current kind"'s own resources first in the fuzzy corpus.
func TestOpenDeploymentPodsSyncsSessionLocationKind(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {deploymentObj("default", "api")},
		kube.KindPod:        {pod("default", "api-abc123")},
	}}
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	if m.kind != kube.KindPod {
		t.Fatalf("expected browse's own kind to be Pods, got %s", m.kind)
	}
	if session.Location.Kind != kube.KindPod {
		t.Fatalf("expected Session.Location.Kind synced to Pods, got %s", session.Location.Kind)
	}
}

// TestOpenStatefulSetPodsSyncsSessionLocationKind is
// TestOpenDeploymentPodsSyncsSessionLocationKind's StatefulSet counterpart
// (openStatefulSetPods, deployments.go).
func TestOpenStatefulSetPodsSyncsSessionLocationKind(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindStatefulSet: {statefulSetObj("default", "db", 2, 2)},
		kube.KindPod:         {pod("default", "db-0")},
	}}
	session := newSession()
	session.Location.Kind = kube.KindStatefulSet
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	if m.kind != kube.KindPod {
		t.Fatalf("expected browse's own kind to be Pods, got %s", m.kind)
	}
	if session.Location.Kind != kube.KindPod {
		t.Fatalf("expected Session.Location.Kind synced to Pods, got %s", session.Location.Kind)
	}
}

// TestOpenDaemonSetPodsSyncsSessionLocationKind is
// TestOpenDeploymentPodsSyncsSessionLocationKind's DaemonSet counterpart
// (openDaemonSetPods, deployments.go).
func TestOpenDaemonSetPodsSyncsSessionLocationKind(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDaemonSet: {daemonSetObj("default", "fluentd", 3, 3)},
		kube.KindPod:       {pod("default", "fluentd-abc12")},
	}}
	session := newSession()
	session.Location.Kind = kube.KindDaemonSet
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	if m.kind != kube.KindPod {
		t.Fatalf("expected browse's own kind to be Pods, got %s", m.kind)
	}
	if session.Location.Kind != kube.KindPod {
		t.Fatalf("expected Session.Location.Kind synced to Pods, got %s", session.Location.Kind)
	}
}

// TestNamespaceSwitchDropsOwnerFilter pins that leaving a Deployment's
// filtered pods view for another namespace drops the owner's pre-filled
// query along with its breadcrumb segment. The query lives behind a closed
// filter strip, so carrying it over left all namespaces silently showing
// only that one Deployment's pods.
func TestNamespaceSwitchDropsOwnerFilter(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {deploymentObj("default", "api")},
		kube.KindPod:        {pod("default", "api-abc123"), pod("kube-system", "coredns-xyz12")},
	}}
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	if got := m.filterInput.Value(); got != "api" {
		t.Fatalf("expected the deployment's pods filtered by its name, got query %q", got)
	}

	m = step(t, m, tui.SwitchNamespaceMsg{Namespace: ""})
	if got := m.filterInput.Value(); got != "" {
		t.Fatalf("expected the owner filter dropped on namespace switch, got query %q", got)
	}
	if session.Location.Filter != "" {
		t.Fatalf("expected Session.Location.Filter cleared, got %q", session.Location.Filter)
	}
	if len(m.visible) != 2 {
		t.Fatalf("expected both namespaces' pods visible, got %d", len(m.visible))
	}
}

// TestOpenNamespacePodsSyncsSessionLocation confirms '↵' on a Namespaces row
// (openSelectedNamespacePods) makes the row's namespace the active one —
// the same effect selecting it in the "n" namespace palette has — and
// switches kind to Pods in the same step, mirroring
// TestOpenDeploymentPodsSyncsSessionLocationKind's Session.Location
// coverage for the namespace+kind combination.
func TestOpenNamespacePodsSyncsSessionLocation(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNamespace: {namespace("default"), namespace("prod")},
		kube.KindPod:       {pod("prod", "api-abc123")},
	}}
	session := newSession()
	session.Location.Kind = kube.KindNamespace
	session.State.PerContext = map[string]state.PerContext{}
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "down"}) // "default" -> "prod"
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})

	if m.kind != kube.KindPod {
		t.Fatalf("expected browse's own kind to be Pods, got %s", m.kind)
	}
	if m.namespace != "prod" {
		t.Fatalf("expected browse's own namespace to be prod, got %q", m.namespace)
	}
	if session.Location.Kind != kube.KindPod {
		t.Fatalf("expected Session.Location.Kind synced to Pods, got %s", session.Location.Kind)
	}
	if session.Location.Namespace != "prod" {
		t.Fatalf("expected Session.Location.Namespace synced to prod, got %q", session.Location.Namespace)
	}
	if got := session.State.PerContext[session.Location.Context].RecentNamespaces; len(got) == 0 || got[0] != "prod" {
		t.Fatalf("expected prod recorded as the most recent namespace, got %v", got)
	}
}

// TestOpenReleaseObjectsSyncsSessionLocationKind is
// TestOpenDeploymentPodsSyncsSessionLocationKind's Helm-release counterpart
// (18a's p = release pods shortcut, browse/helm.go).
func TestOpenReleaseObjectsSyncsSessionLocationKind(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindHelmRelease: {helmRelease("default", "postgresql", "postgresql", "12.1.9", "15.4.0", "deployed", 3)},
		kube.KindPod:         {pod("default", "postgresql-0")},
	}}
	session := newSession()
	session.Location.Kind = kube.KindHelmRelease
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "p"})
	if session.Location.Kind != kube.KindPod {
		t.Fatalf("expected Session.Location.Kind synced to Pods, got %s", session.Location.Kind)
	}
}
