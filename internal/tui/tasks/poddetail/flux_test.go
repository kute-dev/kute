package poddetail

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/kube/fake"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
)

// fluxSession is a session whose registry serves the Flux kinds, as a
// connected Flux cluster's would.
func fluxSession() *tui.Session {
	c := fake.NewDemo()
	reg, groups := resources.BuildDiscoveredRegistry(c.DiscoveredKinds(), c)
	return &tui.Session{Theme: tui.Dark(), Registry: reg, Groups: groups, Location: tui.Location{Context: "test-cluster"}}
}

func fluxManagedLister(depLabels map[string]string) fakeLister {
	return fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {crashLoopPod("worker-0", "gitlab", "node-a")},
		kube.KindReplicaSet: {&appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
			Name: "worker-abc123", Namespace: "gitlab",
			OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "worker"}},
		}}},
		kube.KindDeployment: {&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
			Name: "worker", Namespace: "gitlab", Labels: depLabels,
		}}},
	}}
}

// TestRelatedNamesTheFluxReconcilerThatAppliedTheWorkload: Flux labels the
// Deployment it applied, not the pods the Deployment later creates, so the
// link has to be read one hop up — and it lands on 31a's inventory, in the
// reconciler's own namespace.
func TestRelatedNamesTheFluxReconcilerThatAppliedTheWorkload(t *testing.T) {
	lister := fluxManagedLister(map[string]string{
		kube.FluxGroupKustomize + "/name":      "flux-system",
		kube.FluxGroupKustomize + "/namespace": "flux-system",
	})
	var opened [3]string
	m := New(Config{Session: fluxSession(), Lister: lister, Namespace: "gitlab", Name: "worker-0",
		OpenFluxDetail: func(kind kube.ResourceKind, ns, name string, _, _ int) (tea.Model, tea.Cmd) {
			opened = [3]string{string(kind), ns, name}
			return &Model{}, nil
		}})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	if len(m.related) < 2 {
		t.Fatalf("related = %+v, want the Deployment then its Kustomization", m.related)
	}
	got := m.related[1]
	if got.Kind != "Kustomization" || got.Namespace != "flux-system" || got.Name != "flux-system" {
		t.Fatalf("related[1] = %+v, want Kustomization flux-system/flux-system", got)
	}
	if got.Label != "Kustomization/flux-system in flux-system" {
		t.Errorf("label = %q", got.Label)
	}

	if task, _ := m.Update(tea.KeyPressMsg{Text: "2"}); task == nil {
		t.Fatal("'2' pushed nothing")
	}
	if opened != [3]string{"Kustomization", "flux-system", "flux-system"} {
		t.Errorf("opened %v, want 31a for Kustomization flux-system/flux-system", opened)
	}
}

// TestRelatedFluxHelmReleaseUsesItsRegistryKind: a HelmRelease link must
// carry the substituted registry kind, or it opens §18a's Helm list instead.
func TestRelatedFluxHelmReleaseUsesItsRegistryKind(t *testing.T) {
	lister := fluxManagedLister(map[string]string{
		kube.FluxGroupHelm + "/name":      "gitlab",
		kube.FluxGroupHelm + "/namespace": "gitlab",
	})
	m := New(Config{Session: fluxSession(), Lister: lister, Namespace: "gitlab", Name: "worker-0"})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	if len(m.related) < 2 || m.related[1].Kind != kube.KindFluxHelmRelease {
		t.Fatalf("related = %+v, want a FluxHelmRelease entry after the Deployment", m.related)
	}
	if m.related[1].Label != "HelmRelease/gitlab" {
		t.Errorf("label = %q, want no namespace suffix for a same-namespace release", m.related[1].Label)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Text: "2"})
	msg, ok := cmd().(tui.GotoResourceMsg)
	if !ok || msg.Kind != kube.KindFluxHelmRelease || msg.Namespace != "gitlab" {
		t.Errorf("'2' without OpenFluxDetail = %#v, want the goto fallback", msg)
	}
}

// TestRelatedHasNoFluxEntryWithoutFlux: labels alone are not enough — a
// cluster that doesn't serve the reconciler's kind gets no link to a kind
// kute can't open.
func TestRelatedHasNoFluxEntryWithoutFlux(t *testing.T) {
	lister := fluxManagedLister(map[string]string{kube.FluxGroupKustomize + "/name": "flux-system"})
	m := New(Config{Session: newSession(), Lister: lister, Namespace: "gitlab", Name: "worker-0"})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())
	for _, it := range m.related {
		if it.Kind == "Kustomization" {
			t.Fatalf("related = %+v, want no Flux entry on a non-Flux registry", m.related)
		}
	}
}
