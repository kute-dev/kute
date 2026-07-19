package browse

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

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

// certificateInstanceNoCondition mirrors certificateInstance but carries no
// status.conditions at all — 14a's "never fake health" fallback path.
func certificateInstanceNoCondition(name, ns string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec":       map[string]any{"secretName": name + "-secret"},
	}}
}

func discoveredCertificateDK() kube.DiscoveredKind {
	return kube.DiscoveredKind{
		Kind: "Certificate", Plural: "certificates", Group: "cert-manager.io",
		GVR:           schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"},
		Versions:      []kube.CRDVersion{{Name: "v1", Served: true, Storage: true}},
		ClusterScoped: false, Established: true,
	}
}

// TestCRDBreadcrumbShowsCapitalizedNameAndAPIVersionTag pins 14a: the
// breadcrumb kind segment reads "Certificates" (capitalized, not the CRD's
// raw lowercase plural) with a dim "cert-manager.io/v1" API-version tag.
func TestCRDBreadcrumbShowsCapitalizedNameAndAPIVersionTag(t *testing.T) {
	dk := discoveredCertificateDK()
	reg, _ := resources.BuildDiscoveredRegistry([]kube.DiscoveredKind{dk}, nil)
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.ResourceKind("Certificate"): {certificateInstance("api-tls", "default")},
	}}
	session := newSession()
	session.Registry = reg
	session.Location.Kind = kube.ResourceKind("Certificate")
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	view := plain(m.Render())
	if !strings.Contains(view, "Certificates") {
		t.Fatalf("expected the capitalized 'Certificates' breadcrumb segment:\n%s", view)
	}
	if strings.Contains(view, "› certificates ") {
		t.Fatalf("expected no raw lowercase 'certificates' breadcrumb segment:\n%s", view)
	}
	if !strings.Contains(view, "cert-manager.io/v1") {
		t.Fatalf("expected the API-version tag:\n%s", view)
	}
}

// TestCRDNoStatusSemanticsStripDropsCounts pins 14a's fallback: when every
// visible row has no Ready/Available condition at all, the health strip
// drops the per-status counts and says "no status semantics · NAME + AGE
// only" instead of faking a neutral-status tally.
func TestCRDNoStatusSemanticsStripDropsCounts(t *testing.T) {
	dk := discoveredCertificateDK()
	reg, _ := resources.BuildDiscoveredRegistry([]kube.DiscoveredKind{dk}, nil)
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.ResourceKind("Certificate"): {certificateInstanceNoCondition("bare", "default")},
	}}
	session := newSession()
	session.Registry = reg
	session.Location.Kind = kube.ResourceKind("Certificate")
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	view := plain(m.Render())
	if !strings.Contains(view, "no status semantics · NAME + AGE only") {
		t.Fatalf("expected the 'no status semantics' strip note:\n%s", view)
	}
}

// installingCRDRow is a freshly-applied CRD the API hasn't started serving
// yet (no Established condition at all) — 14b's ◐ "installing" state.
func installingCRDRow(name, group string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"group":    group,
			"names":    map[string]any{"kind": "Widget", "plural": "widgets"},
			"scope":    "Namespaced",
			"versions": []any{map[string]any{"name": "v1", "served": true, "storage": true}},
		},
	}}
}

// TestCRDsListStripShowsEstablishedInstallingAndGroupCount pins 14b (docs/
// design README.md:208): the strip must read "N established · M installing"
// (not the generic "N ok · M warn") and "N definitions · M API groups ·
// sorted by group" (not "N customresourcedefinitions") on the right.
func TestCRDsListStripShowsEstablishedInstallingAndGroupCount(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindCustomResourceDefinition: {
			certificateCRDRow(),                            // established, cert-manager.io
			installingCRDRow("widgets.acme.io", "acme.io"), // installing, a different group
		},
	}}
	session := newSession()
	session.Location.Kind = kube.KindCustomResourceDefinition
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	view := plain(m.Render())
	for _, want := range []string{"1 established", "1 installing", "2 definitions · 2 API groups · sorted by group"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in the CRD list strip:\n%s", want, view)
		}
	}
	if strings.Contains(view, "customresourcedefinitions") {
		t.Fatalf("expected the generic '<N> customresourcedefinitions' wording to be replaced:\n%s", view)
	}
}

// TestCRDsListKeybarPillIsShortForm pins 14b: the keybar pill reads "CRDS",
// not the full "CUSTOMRESOURCEDEFINITIONS" the generic
// strings.ToUpper(desc.Display) path would otherwise produce.
func TestCRDsListKeybarPillIsShortForm(t *testing.T) {
	session := newSession()
	session.Location.Kind = kube.KindCustomResourceDefinition
	m := New(Config{Session: session, Lister: fakeLister{}})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if got := m.Keybar().PillText; got != "CRDS" {
		t.Fatalf("Keybar().PillText = %q, want %q", got, "CRDS")
	}
}

// TestCRDsListSortsByGroup pins 14b's "sorted by group" requirement: rows
// from the same API group must cluster together rather than falling back
// to plain alphabetical-by-name order.
func TestCRDsListSortsByGroup(t *testing.T) {
	// Deliberately chosen so plain alphabetical-by-name order (aaa-cert,
	// bbb-issuer, zzz-widget) differs from group-clustered order
	// (zzz-widget's aaa.io group sorts first, then bbb.io's two rows) — a
	// test built from already-group-ordered input couldn't tell "sorted by
	// group" apart from "no-op, already alphabetical".
	rows := []resources.Row{
		{Name: "aaa-cert", Cells: []string{"aaa-cert", "bbb.io", "v1", "Namespaced", "0", "1h"}},
		{Name: "bbb-issuer", Cells: []string{"bbb-issuer", "bbb.io", "v1", "Namespaced", "0", "1h"}},
		{Name: "zzz-widget", Cells: []string{"zzz-widget", "aaa.io", "v1", "Namespaced", "0", "1h"}},
	}
	sortForDisplay(kube.KindCustomResourceDefinition, "", rows)
	want := []string{"zzz-widget", "aaa-cert", "bbb-issuer"}
	for i, w := range want {
		if rows[i].Name != w {
			t.Fatalf("rows[%d].Name = %q, want %q (got order %v)", i, rows[i].Name, w, []string{rows[0].Name, rows[1].Name, rows[2].Name})
		}
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
