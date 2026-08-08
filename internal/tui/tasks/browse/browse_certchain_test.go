package browse

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
)

// TestEnterOnCertificateRowOpensCertChain exercises §35a's routing carve-out:
// a Certificate row's ↵ pushes tasks/certchain rather than falling through
// to the generic 14d object detail — the same "curated screen wins over the
// generic Custom branch" ordering §31a's Flux detail already takes (see
// browse/update.go's openSelectedEnter comment). Certificate stays a
// Custom/generic-registry kind (per §35a: "no new screens" for the other
// cert-manager kinds), so this is a plain kind-name gate, not a Descriptor
// flag — see certchain.go's isCertificateKind doc comment.
func TestEnterOnCertificateRowOpensCertChain(t *testing.T) {
	dk := kube.DiscoveredKind{
		Kind: "Certificate", Plural: "certificates", Group: "cert-manager.io",
		Versions:      []kube.CRDVersion{{Name: "v1", Served: true, Storage: true}},
		ClusterScoped: false, Established: true,
	}
	reg, _ := resources.BuildDiscoveredRegistry([]kube.DiscoveredKind{dk}, nil)
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.ResourceKind("Certificate"): {certificateInstance("web-tls", "default")},
	}}
	session := newSession()
	session.Registry = reg
	session.Location.Kind = kube.ResourceKind("Certificate")

	var openedNS, openedName string
	var objectDetailCalled bool
	m := New(Config{
		Session: session, Lister: lister,
		OpenCertChain: func(ns, name string, w, h int) (tea.Model, tea.Cmd) {
			openedNS, openedName = ns, name
			return stubTask{}, nil
		},
		OpenObjectDetail: func(kind kube.ResourceKind, ns, name string, siblings []string, index, w, h int) (tea.Model, tea.Cmd) {
			objectDetailCalled = true
			return stubTask{}, nil
		},
	})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	updated, _ := m.Update(tea.KeyPressMsg{Text: "enter"})
	if openedNS != "default" || openedName != "web-tls" {
		t.Fatalf("expected certchain opened for default/web-tls, got ns=%s name=%s", openedNS, openedName)
	}
	if objectDetailCalled {
		t.Fatalf("expected certchain to claim ↵ before the generic 14d branch ran")
	}
	if _, ok := updated.(stubTask); !ok {
		t.Fatalf("expected Update to return the pushed stub task, got %T", updated)
	}
}

// TestGotoResourceOnAFreshInstanceAlwaysLoads guards the bug a certchain
// root row's own ↵ hit: routeGoto (tui/model.go) builds a brand-new browse
// instance via New() — which seeds kind/namespace from Session.Location,
// unchanged since before any screen was pushed on top of browse — and
// routes the GotoResourceMsg straight into goToResource instead of calling
// Init(). When the jump's own target Kind/Namespace happens to already
// match Session.Location (true here: the Certificate the chain screen was
// pushed from never stopped being "current"), the old kindChanged/
// namespaceChanged-only guard saw "nothing to do" and returned a nil cmd —
// leaving a never-loaded instance on its constructor's "Loading
// Certificates..." skeleton forever, since nothing else was ever going to
// ask it to fetch anything.
func TestGotoResourceOnAFreshInstanceAlwaysLoads(t *testing.T) {
	session := newSession()
	session.Location.Kind = kube.ResourceKind("Certificate")
	session.Location.Namespace = "default"
	reg, _ := resources.BuildDiscoveredRegistry([]kube.DiscoveredKind{{
		Kind: "Certificate", Plural: "certificates", Group: "cert-manager.io",
		Versions:      []kube.CRDVersion{{Name: "v1", Served: true, Storage: true}},
		ClusterScoped: false, Established: true,
	}}, nil)
	session.Registry = reg

	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.ResourceKind("Certificate"): {certificateInstance("web-tls", "default")},
	}}
	// A brand-new instance, exactly as routeGoto's m.buildBrowse() produces
	// — never Init()'d, m.rows is still nil.
	m := New(Config{Session: session, Lister: lister})

	cmd := m.goToResource(tui.GotoResourceMsg{
		Kind: kube.ResourceKind("Certificate"), Namespace: "default", Name: "web-tls",
	})
	if cmd == nil {
		t.Fatal("expected goToResource to issue a load cmd for a never-loaded instance, got nil")
	}
}
