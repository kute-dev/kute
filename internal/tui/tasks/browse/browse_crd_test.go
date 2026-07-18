package browse

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
)

func certificateCRDRow() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]any{"name": "certificates.cert-manager.io"},
		"spec": map[string]any{
			"group":    "cert-manager.io",
			"names":    map[string]any{"kind": "Certificate", "plural": "certificates"},
			"scope":    "Namespaced",
			"versions": []any{map[string]any{"name": "v1", "served": true, "storage": true}},
		},
		"status": map[string]any{
			"conditions": []any{map[string]any{"type": "Established", "status": "True"}},
		},
	}}
}

func certificateInstance(name, ns string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec":       map[string]any{"secretName": name + "-secret"},
		"status": map[string]any{
			"conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
		},
	}}
}

// TestEnterOnCRDRowJumpsToInstanceKind exercises 14b's "CRDs are a routing
// layer": ↵ on a CustomResourceDefinitions row switches kind to the
// discovered instance kind via GotoKindMsg, reading the target off the
// row's Key (projectCRD's doc comment) — the same message the goto
// palette's own kind-switch dispatch emits.
func TestEnterOnCRDRowJumpsToInstanceKind(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindCustomResourceDefinition: {certificateCRDRow()},
	}}
	session := newSession()
	session.Location.Kind = kube.KindCustomResourceDefinition
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	_, cmd := m.Update(tea.KeyPressMsg{Text: "enter"})
	if cmd == nil {
		t.Fatalf("expected a GotoKindMsg cmd from ↵ on a CRD row")
	}
	msg := cmd()
	goMsg, ok := msg.(tui.GotoKindMsg)
	if !ok || goMsg.Kind != kube.ResourceKind("Certificate") {
		t.Fatalf("expected GotoKindMsg{Kind: Certificate}, got %+v (ok=%v)", msg, ok)
	}
}

// TestEnterOnCustomRowOpensObjectDetail exercises 14d's generic routing:
// any discovered (Descriptor.Custom) kind's ↵ pushes objectdetail via
// OpenObjectDetail — the one branch that scales to every discovered kind
// without per-kind code.
func TestEnterOnCustomRowOpensObjectDetail(t *testing.T) {
	dk := kube.DiscoveredKind{
		Kind: "Certificate", Plural: "certificates", Group: "cert-manager.io",
		Versions:      []kube.CRDVersion{{Name: "v1", Served: true, Storage: true}},
		ClusterScoped: false, Established: true,
	}
	reg, _ := resources.BuildDiscoveredRegistry([]kube.DiscoveredKind{dk}, nil)
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.ResourceKind("Certificate"): {certificateInstance("api-tls", "default")},
	}}
	session := newSession()
	session.Registry = reg
	session.Location.Kind = kube.ResourceKind("Certificate")

	var openedKind kube.ResourceKind
	var openedNS, openedName string
	m := New(Config{
		Session: session, Lister: lister,
		OpenObjectDetail: func(kind kube.ResourceKind, ns, name string, siblings []string, index, w, h int) (tea.Model, tea.Cmd) {
			openedKind, openedNS, openedName = kind, ns, name
			return stubTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tea.KeyPressMsg{Text: "enter"})
	if openedKind != kube.ResourceKind("Certificate") || openedNS != "default" || openedName != "api-tls" {
		t.Fatalf("expected Certificate default/api-tls opened, got kind=%s ns=%s name=%s", openedKind, openedNS, openedName)
	}
	if _, ok := updated.(stubTask); !ok {
		t.Fatalf("expected Update to return the pushed stub task, got %T", updated)
	}
}

// TestCRDAndCustomKeybarsShowOpenHint mirrors
// TestPodKeybarShowsOpenAndLogsWhenWired for the two new routing kinds:
// both the CustomResourceDefinitions list and any discovered Custom kind
// need "↵ open" in their keybar (keys.go), matching the fact that ↵
// actually does something on both (crddetail.go).
func TestCRDAndCustomKeybarsShowOpenHint(t *testing.T) {
	hasOpenHint := func(kb tui.Keybar) bool {
		for _, g := range kb.Groups {
			for _, h := range g {
				if h.Key == "↵" {
					return true
				}
			}
		}
		return false
	}

	t.Run("CRD list", func(t *testing.T) {
		lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
			kube.KindCustomResourceDefinition: {certificateCRDRow()},
		}}
		session := newSession()
		session.Location.Kind = kube.KindCustomResourceDefinition
		m := New(Config{Session: session, Lister: lister})
		m.SetSize(120, 36)
		m = step(t, m, m.Init()())
		if !hasOpenHint(m.Keybar()) {
			t.Fatalf("expected ↵ open in the CustomResourceDefinitions keybar, got %+v", m.Keybar().Groups)
		}
	})

	t.Run("Custom kind", func(t *testing.T) {
		dk := kube.DiscoveredKind{Kind: "Certificate", Plural: "certificates", Group: "cert-manager.io", Established: true}
		reg, _ := resources.BuildDiscoveredRegistry([]kube.DiscoveredKind{dk}, nil)
		lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
			kube.ResourceKind("Certificate"): {certificateInstance("api-tls", "default")},
		}}
		session := newSession()
		session.Registry = reg
		session.Location.Kind = kube.ResourceKind("Certificate")
		m := New(Config{
			Session: session, Lister: lister,
			OpenObjectDetail: func(kube.ResourceKind, string, string, []string, int, int, int) (tea.Model, tea.Cmd) {
				return stubTask{}, nil
			},
		})
		m.SetSize(120, 36)
		m = step(t, m, m.Init()())
		if !hasOpenHint(m.Keybar()) {
			t.Fatalf("expected ↵ open in a Custom kind's keybar, got %+v", m.Keybar().Groups)
		}
	})
}

// TestCRDDeleteAlwaysForcesModalTier confirms 14b's "deleting a CRD always
// gets the type-the-name modal, even outside PROD" — beginDelete forces
// actions.TierModal for KindCustomResourceDefinition regardless of the
// active context's PROD tag.
func TestCRDDeleteAlwaysForcesModalTier(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindCustomResourceDefinition: {certificateCRDRow()},
	}}
	session := newSession() // not PROD
	session.Location.Kind = kube.KindCustomResourceDefinition
	m := New(Config{Session: session, Lister: lister, Mutator: &fakeMutator{}})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+d"})
	if !m.actions.Active() {
		t.Fatalf("expected ctrl+d to begin a delete confirmation")
	}
	if m.actions.Tier() != actions.TierModal {
		t.Fatalf("expected TierModal for a CRD delete even outside PROD, got %v", m.actions.Tier())
	}
}
