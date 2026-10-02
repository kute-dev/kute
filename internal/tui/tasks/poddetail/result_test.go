package poddetail

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

// TestFailedDeleteShowsErrorOnKeybar pins review 10 H1 for pod detail: a
// failed delete must not close its confirm as silently as a success.
func TestFailedDeleteShowsErrorOnKeybar(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {runningPod("api-0", "default", "node-a")},
	}}
	mut := &fakeMutator{err: errors.New(`pods "api-0" is forbidden`)}
	m := New(Config{Session: newSession(), Lister: lister, Mutator: mut, Namespace: "default", Name: "api-0"})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "D"})
	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	lines := strings.Split(plain(m.Render()), "\n")
	if bar := lines[len(lines)-1]; !strings.Contains(bar, "forbidden") {
		t.Fatalf("expected the delete error on the keybar, got %q", bar)
	}
}
