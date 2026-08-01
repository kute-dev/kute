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
)

func kustomizationDK() kube.DiscoveredKind {
	return kube.DiscoveredKind{
		Kind: "Kustomization", Plural: "kustomizations", Group: kube.FluxGroupKustomize,
		GVR:         schema.GroupVersionResource{Group: kube.FluxGroupKustomize, Version: "v1", Resource: "kustomizations"},
		Versions:    []kube.CRDVersion{{Name: "v1", Served: true, Storage: true}},
		Established: true,
		CRDName:     "kustomizations." + kube.FluxGroupKustomize,
	}
}

// kustomization builds one Flux object. ready "" means no conditions at all.
func kustomization(name string, suspend bool, ready, message string) *unstructured.Unstructured {
	obj := map[string]any{
		"apiVersion": kube.FluxGroupKustomize + "/v1",
		"kind":       "Kustomization",
		"metadata":   map[string]any{"name": name, "namespace": "flux-system"},
		"spec": map[string]any{
			"sourceRef": map[string]any{"kind": "GitRepository", "name": "flux-system"},
		},
	}
	if suspend {
		obj["spec"].(map[string]any)["suspend"] = true
	}
	status := map[string]any{"lastAppliedRevision": "master@sha1:efd398bed98a38348c7702355ecd98fc11ac2bef"}
	if ready != "" {
		status["conditions"] = []any{map[string]any{
			"type": "Ready", "status": ready, "message": message,
			"lastTransitionTime": "2026-08-01T09:00:00Z",
		}}
	}
	obj["status"] = status
	return &unstructured.Unstructured{Object: obj}
}

func fluxModel(t *testing.T, objs ...runtime.Object) Model {
	t.Helper()
	reg, groups := resources.BuildDiscoveredRegistry([]kube.DiscoveredKind{kustomizationDK()}, nil)
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.ResourceKind("Kustomization"): objs,
	}}
	session := newSession()
	session.Registry, session.Groups = reg, groups
	session.Location.Kind = kube.ResourceKind("Kustomization")
	session.Location.Namespace = "flux-system"
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	return step(t, m, m.Init()())
}

// TestFluxSubLineRendersTheConditionMessageVerbatim is §30a's core claim:
// the failure reason is on the row, not one screen away.
func TestFluxSubLineRendersTheConditionMessageVerbatim(t *testing.T) {
	msg := "health check failed after 2m0s: Deployment/aim-stage/aim-worker status: 'InProgress'"
	m := fluxModel(t, kustomization("aim-workers", false, "False", msg))

	view := plain(m.Render())
	if !strings.Contains(view, "health check failed after 2m0s") {
		t.Fatalf("expected the condition message on the row, got:\n%s", view)
	}
	// Verbatim: the object name from inside the message survives too, which
	// is what makes the message a diagnosis rather than a category.
	if !strings.Contains(view, "aim-worker") {
		t.Errorf("the message must render verbatim, not paraphrased:\n%s", view)
	}
}

// TestFluxSubLineIsNotSelectable pins that the cursor skips a continuation
// line. Stopping on one would make every row-scoped verb ambiguous, since
// a sub-line has no object behind it.
func TestFluxSubLineIsNotSelectable(t *testing.T) {
	m := fluxModel(t,
		kustomization("aim-apps", false, "False", "reconciliation failed"),
		kustomization("aim-infra", false, "False", "another failure"),
	)

	// Both rows failed, so both carry sub-lines: display is row, sub, row, sub.
	if len(m.display) != 4 {
		t.Fatalf("expected 4 display lines (2 rows + 2 sub-lines), got %d", len(m.display))
	}
	seen := map[string]bool{}
	for i := 0; i < 6; i++ {
		if row, ok := m.selectedRow(); ok {
			seen[row.Name] = true
		}
		if m.display[m.selected].kind == rowKindSubLine {
			t.Fatalf("cursor landed on a sub-line at display index %d", m.selected)
		}
		m.moveSelection(1)
	}
	if len(seen) != 2 {
		t.Errorf("expected to reach both data rows, saw %v", seen)
	}
}

// TestFluxHealthyRowsFoldAwayBehindTheUnhealthy is §30a's "unhealthy first"
// triage shape in a single-namespace list, where browse previously had no
// fold at all.
func TestFluxHealthyRowsFoldAwayBehindTheUnhealthy(t *testing.T) {
	m := fluxModel(t,
		kustomization("aim-apps", false, "True", "Applied revision: master@sha1:efd398b"),
		kustomization("aim-workers", false, "False", "health check failed"),
		kustomization("flux-system", false, "True", "Applied revision: master@sha1:efd398b"),
		kustomization("observability", false, "True", "Applied revision: master@sha1:efd398b"),
	)

	view := plain(m.Render())
	if !strings.Contains(view, "aim-workers") {
		t.Errorf("the failing row must stay visible:\n%s", view)
	}
	if !strings.Contains(view, "+ 3 ready") {
		t.Errorf("expected a '+ 3 ready' fold line for the healthy tail:\n%s", view)
	}
	if strings.Contains(view, "observability") {
		t.Errorf("healthy rows should be folded away, but one rendered:\n%s", view)
	}

	// ↹ expands the fold, and every row comes back.
	m = step(t, m, tea.KeyPressMsg{Text: "tab"})
	if !strings.Contains(plain(m.Render()), "observability") {
		t.Errorf("tab should expand the fold and reveal the healthy tail")
	}
}

// TestFluxAllSuspendedListKeepsItsStatusSemantics guards the StatusSemantics
// gate. Every row suspended means every row is Neutral, which for a plain
// CRD kind is 14a's "no status semantics" case — but a Flux list emphatically
// has them, and saying otherwise would be a lie about the one kind whose
// status derivation is the point of the feature.
func TestFluxAllSuspendedListKeepsItsStatusSemantics(t *testing.T) {
	m := fluxModel(t,
		kustomization("aim-infra", true, "True", "Applied revision: master@sha1:efd398b"),
		kustomization("aim-apps", true, "True", "Applied revision: master@sha1:efd398b"),
	)

	view := plain(m.Render())
	if strings.Contains(view, "no status semantics") {
		t.Errorf("a Flux list must never claim it has no status semantics:\n%s", view)
	}
	if !strings.Contains(view, "suspended") {
		t.Errorf("expected the strip to count suspended rows:\n%s", view)
	}
}

// TestFluxSuspendedRowStaysVisibleAndSortsAboveHealthy is §30a's "suspended
// is a state, not a footnote". Suspended is StatusNeutral, which for every
// other kind means finished-and-uninteresting and sinks to the bottom of a
// list — folding a paused Kustomization away would hide exactly the object
// that is silently drifting from git.
func TestFluxSuspendedRowStaysVisibleAndSortsAboveHealthy(t *testing.T) {
	m := fluxModel(t,
		kustomization("aim-apps", false, "True", "ok"),
		kustomization("aim-infra", true, "True", "Applied revision: master@sha1:efd398b"),
		kustomization("flux-system", false, "True", "ok"),
		kustomization("observability", false, "True", "ok"),
	)

	view := plain(m.Render())
	if !strings.Contains(view, "aim-infra") {
		t.Errorf("the suspended row must survive the healthy-tail fold:\n%s", view)
	}
	if !strings.Contains(view, "+ 3 ready") {
		t.Errorf("expected the three healthy rows to fold, leaving the suspended one:\n%s", view)
	}
	if got := m.rows[0].Name; got != "aim-infra" {
		t.Errorf("suspended row should sort first among otherwise-healthy rows, got %q", got)
	}
	// It must not be double-reported: the READY cell already says the word.
	if strings.Count(view, "suspended") != 2 { // strip segment + READY cell
		t.Errorf("expected 'suspended' exactly twice (strip + cell), got %d:\n%s",
			strings.Count(view, "suspended"), view)
	}
}
