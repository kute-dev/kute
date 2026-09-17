package objectdetail

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/testutil/goldentest"
)

// labeledCertObj is certObj with a label set, so 26a's panel has a grid row
// to render — 14d shows metadata nowhere else, so the panel is the only
// surface these labels appear on at all.
func labeledCertObj(name string) runtime.Object {
	obj := certObj(name, map[string]any{"type": "Ready", "status": "True"})
	obj.SetLabels(map[string]string{"app": "shop"})
	return obj
}

// The panel's own behavior is tested in internal/tui/metapanel — what's
// covered here is objectdetail's hosting glue: 'm' opens it on the loaded
// discovered object, it captures keys ('y' stops meaning YAML, esc closes
// the panel rather than popping the screen), and the keybar swaps to META.
func TestMetaKeyOpensPanelOnDiscoveredObject(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		certificateKind(): {labeledCertObj("api-tls")},
	}}
	m := New(Config{
		Session: testSession(), Lister: lister, Events: fakeEvents{}, Mutator: &fakeMutator{},
		Kind: certificateKind(), Namespace: "default", Name: "api-tls",
	})
	m.SetSize(120, 36)
	updated, _ := step(t, &m, m.load()())
	got := updated.(*Model)

	updated, _ = step(t, got, tea.KeyPressMsg{Text: "m"})
	got = updated.(*Model)
	if got.meta == nil {
		t.Fatal("'m' should open the labels/annotations panel")
	}
	if !got.CapturingInput() {
		t.Error("the open panel should capture input (no global g/n/c routing)")
	}
	if kb := got.Keybar(); kb.PillText != "META" {
		t.Errorf("keybar pill = %q, want META", kb.PillText)
	}
	body := goldentest.Plain(got.Body(120, 30))
	if !strings.Contains(body, "LABELS · 1") || !strings.Contains(body, "app=") {
		t.Fatalf("panel body missing the labels grid:\n%s", body)
	}
	// The title line stays frozen above the panel (the detail-screen
	// analogue of §26a's selected-row framing).
	if !strings.Contains(body, "api-tls") {
		t.Error("panel body should keep the object's own title line above the grid")
	}

	// While open, 'y' belongs to the panel (copy key=value), never YAML.
	updated, cmd := got.updateKey(tea.KeyPressMsg{Text: "y"})
	got = updated.(*Model)
	if cmd == nil {
		t.Error("'y' with the panel open should return the panel's clipboard cmd")
	}

	// esc closes the panel and stays on the detail screen — it must not
	// bubble into objectdetail's own esc-goes-back binding.
	updated, _ = step(t, got, tea.KeyPressMsg{Text: "esc"})
	got = updated.(*Model)
	if got.meta != nil {
		t.Fatal("esc should close the panel")
	}
	if !strings.Contains(goldentest.Plain(got.Body(120, 30)), "CONDITIONS") {
		t.Error("closing the panel should land back on the detail body")
	}
}

func TestMetaKeyIsNoOpWithoutMutator(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		certificateKind(): {labeledCertObj("api-tls")},
	}}
	m := New(Config{
		Session: testSession(), Lister: lister, Events: fakeEvents{},
		Kind: certificateKind(), Namespace: "default", Name: "api-tls",
	})
	m.SetSize(120, 36)
	updated, _ := step(t, &m, m.load()())
	got := updated.(*Model)

	updated, _ = step(t, got, tea.KeyPressMsg{Text: "m"})
	if updated.(*Model).meta != nil {
		t.Error("'m' without a mutator should stay a no-op (read-only session)")
	}
}
