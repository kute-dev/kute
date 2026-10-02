package objectdetail

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

// TestFailedDeleteShowsErrorOnKeybar pins review 10 H1 for object detail.
func TestFailedDeleteShowsErrorOnKeybar(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		certificateKind(): {certObj("api-tls", map[string]any{"type": "Ready", "status": "True"})},
	}}
	mut := &fakeMutator{err: errors.New(`admission webhook denied the request`)}
	m := New(Config{
		Session: testSession(), Lister: lister, Events: fakeEvents{}, Mutator: mut,
		Kind: certificateKind(), Namespace: "default", Name: "api-tls",
	})
	m.SetSize(120, 36)
	updated, _ := step(t, &m, m.load()())
	got := updated.(*Model)

	// Outside PROD, deleting a CRD object is the inline y/N.
	updated, _ = step(t, got, tea.KeyPressMsg{Text: "D"})
	got = updated.(*Model)
	updated, cmd := step(t, got, tea.KeyPressMsg{Text: "y"})
	if cmd == nil {
		t.Fatal("expected y to run the delete")
	}
	updated, _ = step(t, updated.(*Model), cmd())
	got = updated.(*Model)
	lines := strings.Split(ansi.Strip(got.Render()), "\n")
	if bar := lines[len(lines)-1]; !strings.Contains(bar, "webhook denied") {
		t.Fatalf("expected the delete error on the keybar, got %q", bar)
	}
}
