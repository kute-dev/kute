package resources

import (
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kute-dev/kute/internal/kube"
)

func fluxKustomizationDiscoveredKind() kube.DiscoveredKind {
	return kube.DiscoveredKind{
		Kind: "Kustomization", Plural: "kustomizations", Group: kube.FluxGroupKustomize,
		GVR:         schema.GroupVersionResource{Group: kube.FluxGroupKustomize, Version: "v1", Resource: "kustomizations"},
		Versions:    []kube.CRDVersion{{Name: "v1", Served: true, Storage: true}},
		Established: true,
		CRDName:     "kustomizations." + kube.FluxGroupKustomize,
	}
}

func fluxHelmReleaseDiscoveredKind() kube.DiscoveredKind {
	return kube.DiscoveredKind{
		Kind: "HelmRelease", Plural: "helmreleases", Group: kube.FluxGroupHelm,
		GVR:         schema.GroupVersionResource{Group: kube.FluxGroupHelm, Version: "v2", Resource: "helmreleases"},
		Versions:    []kube.CRDVersion{{Name: "v2", Served: true, Storage: true}},
		Established: true,
		CRDName:     "helmreleases." + kube.FluxGroupHelm,
	}
}

// TestFluxHelmReleaseDoesNotReplaceHelmReleasesList is the regression this
// whole design exists for. Registry.Register is last-write-wins on the bare
// Kind, so before the substitution a Flux cluster's discovered HelmRelease
// silently overwrote 18a's Helm-3 release descriptor: the Helm Releases
// list rendered NAME+AGE only (its projection type-asserts a decoded
// release and missed), and Flux's own HelmReleases never appeared at all.
func TestFluxHelmReleaseDoesNotReplaceHelmReleasesList(t *testing.T) {
	t.Parallel()
	reg, _ := BuildDiscoveredRegistry([]kube.DiscoveredKind{fluxHelmReleaseDiscoveredKind()}, nil)

	helm, ok := reg.Descriptor(kube.KindHelmRelease)
	if !ok {
		t.Fatal("18a's Helm Releases descriptor is gone entirely")
	}
	if helm.Display != "Helm Releases" {
		t.Errorf("18a's descriptor was overwritten: Display = %q, want %q", helm.Display, "Helm Releases")
	}
	if helm.Custom {
		t.Error("18a's descriptor was overwritten by a discovered one (Custom = true)")
	}
	if helm.Flux {
		t.Error("18a's Helm-3 release kind must not be marked Flux")
	}

	fluxHR, ok := reg.Descriptor(kube.KindFluxHelmRelease)
	if !ok {
		t.Fatal("Flux's own HelmRelease was not registered under its substituted key")
	}
	if !fluxHR.Flux || !fluxHR.Custom {
		t.Errorf("Flux HelmRelease descriptor flags: Flux=%v Custom=%v, want both true", fluxHR.Flux, fluxHR.Custom)
	}
	if fluxHR.APIGroup != kube.FluxGroupHelm {
		t.Errorf("APIGroup = %q, want %q", fluxHR.APIGroup, kube.FluxGroupHelm)
	}
}

// TestFluxKindsAppearInExactlyOneGroup pins the mutual exclusion: a Flux
// kind belongs to GroupFlux and must not also be listed under Custom
// Resources, or the goto corpus offers it twice.
func TestFluxKindsAppearInExactlyOneGroup(t *testing.T) {
	t.Parallel()
	discovered := []kube.DiscoveredKind{
		fluxKustomizationDiscoveredKind(),
		fluxHelmReleaseDiscoveredKind(),
		certificateDiscoveredKind(),
	}
	_, groups := BuildDiscoveredRegistry(discovered, nil)

	var flux, custom []kube.ResourceKind
	for _, g := range groups {
		switch g.ID {
		case GroupFlux:
			flux = g.Kinds
		case GroupCustomResources:
			custom = g.Kinds
		}
	}
	if !slices.Contains(flux, kube.ResourceKind("Kustomization")) {
		t.Errorf("Kustomization missing from the Flux group, got %v", flux)
	}
	if !slices.Contains(flux, kube.KindFluxHelmRelease) {
		t.Errorf("Flux HelmRelease missing from the Flux group, got %v", flux)
	}
	for _, k := range flux {
		if slices.Contains(custom, k) {
			t.Errorf("%s is in both the Flux and Custom Resources groups", k)
		}
	}
	if !slices.Contains(custom, kube.ResourceKind("Certificate")) {
		t.Errorf("a non-Flux CRD should stay in Custom Resources, got %v", custom)
	}
}

// TestFluxOnlyClusterGetsNoEmptyCustomGroup guards the gating change: the
// Custom Resources group keys off its own kinds now, not off len(discovered).
func TestFluxOnlyClusterGetsNoEmptyCustomGroup(t *testing.T) {
	t.Parallel()
	_, groups := BuildDiscoveredRegistry([]kube.DiscoveredKind{fluxKustomizationDiscoveredKind()}, nil)
	for _, g := range groups {
		if g.ID == GroupCustomResources {
			t.Fatalf("expected no Custom Resources group on a Flux-only cluster, got %v", g.Kinds)
		}
	}
}

// TestNoFluxKindsOmitsTheFluxGroup is the other half: zero chrome on a
// cluster that doesn't run Flux.
func TestNoFluxKindsOmitsTheFluxGroup(t *testing.T) {
	t.Parallel()
	_, groups := BuildDiscoveredRegistry([]kube.DiscoveredKind{certificateDiscoveredKind()}, nil)
	for _, g := range groups {
		if g.ID == GroupFlux {
			t.Fatal("a non-Flux cluster must not get a Flux group")
		}
	}
}
