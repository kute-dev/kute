package resources

import (
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kute-dev/kute/internal/kube"
)

func argoApplicationDiscoveredKind() kube.DiscoveredKind {
	return kube.DiscoveredKind{
		Kind: "Application", Plural: "applications", Group: kube.ArgoGroup,
		GVR:         schema.GroupVersionResource{Group: kube.ArgoGroup, Version: "v1alpha1", Resource: "applications"},
		Versions:    []kube.CRDVersion{{Name: "v1alpha1", Served: true, Storage: true}},
		Established: true,
		CRDName:     "applications." + kube.ArgoGroup,
	}
}

func argoAppProjectDiscoveredKind() kube.DiscoveredKind {
	return kube.DiscoveredKind{
		Kind: "AppProject", Plural: "appprojects", Group: kube.ArgoGroup,
		GVR:         schema.GroupVersionResource{Group: kube.ArgoGroup, Version: "v1alpha1", Resource: "appprojects"},
		Versions:    []kube.CRDVersion{{Name: "v1alpha1", Served: true, Storage: true}},
		Established: true,
		CRDName:     "appprojects." + kube.ArgoGroup,
	}
}

// argoObj builds a minimal Application object: sync/health as plain
// strings (no conditions array, unlike Flux), optional per-resource health
// entries for argoSubLine.
func argoObj(name, targetRevision, revision, syncStatus, healthStatus string, resources ...map[string]any) *unstructured.Unstructured {
	obj := map[string]any{
		"apiVersion": kube.ArgoGroup + "/v1alpha1",
		"kind":       "Application",
		"metadata": map[string]any{
			"name": name, "namespace": "argocd",
			"creationTimestamp": time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339),
		},
		"spec": map[string]any{
			"source": map[string]any{"targetRevision": targetRevision},
		},
	}
	status := map[string]any{
		"sync":   map[string]any{"status": syncStatus, "revision": revision},
		"health": map[string]any{"status": healthStatus},
	}
	if len(resources) > 0 {
		items := make([]any, len(resources))
		for i, r := range resources {
			items[i] = r
		}
		status["resources"] = items
	}
	obj["status"] = status
	return &unstructured.Unstructured{Object: obj}
}

func argoResourceHealth(kind, name, health, message string) map[string]any {
	return map[string]any{
		"kind": kind, "name": name,
		"health": map[string]any{"status": health, "message": message},
	}
}

// TestArgoApplicationDoesNotReplaceAppProject pins the group-plus-Kind
// gate: argoproj.io also serves AppProject, which has no sync/health status
// and must stay on the generic CustomDescriptor path (crd.go's
// BuildDiscoveredRegistry switch), never argoDescriptor.
func TestArgoApplicationDoesNotReplaceAppProject(t *testing.T) {
	t.Parallel()
	reg, _ := BuildDiscoveredRegistry([]kube.DiscoveredKind{argoApplicationDiscoveredKind(), argoAppProjectDiscoveredKind()}, nil)

	app, ok := reg.Descriptor(kube.ResourceKind("Application"))
	if !ok {
		t.Fatal("Application descriptor missing")
	}
	if !app.Argo {
		t.Error("Application should be curated onto §33a's descriptor (Argo: true)")
	}

	project, ok := reg.Descriptor(kube.ResourceKind("AppProject"))
	if !ok {
		t.Fatal("AppProject descriptor missing")
	}
	if project.Argo {
		t.Error("AppProject has no sync/health status and must stay on the generic path (Argo: false)")
	}
	if project.Group != GroupCustomResources {
		t.Errorf("AppProject.Group = %v, want GroupCustomResources", project.Group)
	}
}

// TestArgoKindAppearsInExactlyOneGroup mirrors
// TestFluxKindsAppearInExactlyOneGroup: Application belongs to GroupArgo
// and must not also be listed under Custom Resources.
func TestArgoKindAppearsInExactlyOneGroup(t *testing.T) {
	t.Parallel()
	discovered := []kube.DiscoveredKind{
		argoApplicationDiscoveredKind(),
		argoAppProjectDiscoveredKind(),
		certificateDiscoveredKind(),
	}
	_, groups := BuildDiscoveredRegistry(discovered, nil)

	var argo, custom []kube.ResourceKind
	for _, g := range groups {
		switch g.ID {
		case GroupArgo:
			argo = g.Kinds
		case GroupCustomResources:
			custom = g.Kinds
		}
	}
	if !slices.Contains(argo, kube.ResourceKind("Application")) {
		t.Errorf("Application missing from the Argo CD group, got %v", argo)
	}
	if slices.Contains(argo, kube.ResourceKind("AppProject")) {
		t.Errorf("AppProject should not be in the Argo CD group, got %v", argo)
	}
	for _, k := range argo {
		if slices.Contains(custom, k) {
			t.Errorf("%s is in both the Argo CD and Custom Resources groups", k)
		}
	}
	if !slices.Contains(custom, kube.ResourceKind("AppProject")) {
		t.Errorf("AppProject should stay in Custom Resources, got %v", custom)
	}
}

// TestArgoOnlyClusterGetsNoEmptyCustomGroup mirrors
// TestFluxOnlyClusterGetsNoEmptyCustomGroup.
func TestArgoOnlyClusterGetsNoEmptyCustomGroup(t *testing.T) {
	t.Parallel()
	_, groups := BuildDiscoveredRegistry([]kube.DiscoveredKind{argoApplicationDiscoveredKind()}, nil)
	for _, g := range groups {
		if g.ID == GroupCustomResources {
			t.Fatalf("expected no Custom Resources group on an Argo-only cluster, got %v", g.Kinds)
		}
	}
}

// TestNoArgoKindsOmitsTheArgoGroup mirrors TestNoFluxKindsOmitsTheFluxGroup:
// zero chrome on a cluster that doesn't run Argo CD.
func TestNoArgoKindsOmitsTheArgoGroup(t *testing.T) {
	t.Parallel()
	_, groups := BuildDiscoveredRegistry([]kube.DiscoveredKind{certificateDiscoveredKind()}, nil)
	for _, g := range groups {
		if g.ID == GroupArgo {
			t.Fatal("a non-Argo cluster must not get an Argo CD group")
		}
	}
}

// TestProjectArgoResourceStatus is §33a's precedence table: Degraded/
// Missing health outranks everything, OutOfSync is next regardless of
// health, Progressing is normal activity, Synced+Healthy is the quiet
// state.
func TestProjectArgoResourceStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		sync      string
		health    string
		wantGlyph string
		wantClass StatusClass
	}{
		{"degraded outranks everything", "Synced", "Degraded", "✕", StatusFail},
		{"missing is also terminal", "Synced", "Missing", "✕", StatusFail},
		{"degraded outranks out of sync too", "OutOfSync", "Degraded", "✕", StatusFail},
		{"out of sync, health fine", "OutOfSync", "Healthy", "▲", StatusWarn},
		{"progressing is normal activity", "Synced", "Progressing", "◌", StatusNeutral},
		{"synced and healthy is the quiet state", "Synced", "Healthy", "●", StatusOK},
		{"unknown surfaces rather than folding", "Unknown", "Unknown", "▲", StatusWarn},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			glyph, class := argoStatus(tt.sync, tt.health)
			if glyph != tt.wantGlyph {
				t.Errorf("glyph = %q, want %q", glyph, tt.wantGlyph)
			}
			if class != tt.wantClass {
				t.Errorf("class = %v, want %v", class, tt.wantClass)
			}
		})
	}
}

// TestArgoSubLineCarriesTheResourceHealthMessageVerbatim pins §33a's
// sub-line rule: the sickest managed resource's own message, unparaphrased.
func TestArgoSubLineCarriesTheResourceHealthMessageVerbatim(t *testing.T) {
	t.Parallel()
	obj := argoObj("billing", "main", "e41b90c", "Synced", "Degraded",
		argoResourceHealth("Deployment", "billing-api", "Degraded", `container "api" is in CrashLoopBackOff — exit 1, 2m ago`))
	row := projectArgoResource(obj)
	want := `Deployment/billing-api: container "api" is in CrashLoopBackOff — exit 1, 2m ago`
	if row.SubLine != want {
		t.Errorf("SubLine = %q, want %q", row.SubLine, want)
	}
}

// TestArgoSubLineEmptyWhenHealthy pins the other half: no sub-line on a
// Healthy/OutOfSync/Progressing row, even one that happens to carry a
// status.resources entry.
func TestArgoSubLineEmptyWhenHealthy(t *testing.T) {
	t.Parallel()
	for _, health := range []string{"Healthy", "Progressing"} {
		obj := argoObj("api", "main", "e41b90c", "Synced", health)
		row := projectArgoResource(obj)
		if row.SubLine != "" {
			t.Errorf("health=%s: SubLine = %q, want empty", health, row.SubLine)
		}
	}
}

// TestProjectArgoResourceRevisionCell pins argoRevisionCell's short form
// and its "HEAD" fallback for an unset targetRevision.
func TestProjectArgoResourceRevisionCell(t *testing.T) {
	t.Parallel()
	got := projectArgoResource(argoObj("api", "main", "e41b90c1f2a3b4c5d6e7f8091a2b3c4d5e6f7081", "Synced", "Healthy"))
	if got.Cells[3] != "main@e41b90c" {
		t.Errorf("REVISION cell = %q, want %q", got.Cells[3], "main@e41b90c")
	}
	headFallback := projectArgoResource(argoObj("api", "", "", "Synced", "Healthy"))
	if headFallback.Cells[3] != "HEAD" {
		t.Errorf("REVISION cell = %q, want %q", headFallback.Cells[3], "HEAD")
	}
}

// TestProjectArgoResourceMetaOf smoke-tests the plumbing outside status
// derivation: namespace/name/age land on the Row the same way every other
// projection's does.
func TestProjectArgoResourceMetaOf(t *testing.T) {
	t.Parallel()
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": kube.ArgoGroup + "/v1alpha1",
		"kind":       "Application",
		"metadata": map[string]any{
			"name": "api", "namespace": "argocd",
			"creationTimestamp": metav1.Now().UTC().Format(time.RFC3339),
		},
		"status": map[string]any{
			"sync":   map[string]any{"status": "Synced"},
			"health": map[string]any{"status": "Healthy"},
		},
	}}
	row := projectArgoResource(obj)
	if row.Namespace != "argocd" || row.Name != "api" {
		t.Errorf("Namespace/Name = %q/%q, want argocd/api", row.Namespace, row.Name)
	}
}
