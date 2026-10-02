package nodedetail

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

func nodeResultModel(t *testing.T, mut *fakeMutator) Model {
	t.Helper()
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {testNode("node-a")},
		kube.KindPod:  {schedPod("default", "big", "node-a", "1Gi")},
	}}
	m := New(Config{Session: newSession(), Lister: lister, NodeName: "node-a", Mutator: mut})
	m.SetSize(160, 36)
	return step(t, m, m.Init()())
}

func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	return lines[len(lines)-1]
}

// TestFailedDrainShowsErrorOnKeybar pins review 10 H1 for node detail: a
// PDB-blocked drain leaves the node cordoned and half-drained, and used to
// say nothing at all.
func TestFailedDrainShowsErrorOnKeybar(t *testing.T) {
	m := nodeResultModel(t, &fakeMutator{err: errors.New("cannot evict pod as it would violate the pod's disruption budget")})
	m = step(t, m, tea.KeyPressMsg{Text: verbs.Drain.Key})
	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if bar := lastLine(plain(m.Render())); !strings.Contains(bar, "disruption budget") {
		t.Fatalf("expected the drain error on the keybar, got %q", bar)
	}
}

// TestFailedCordonShowsErrorOnKeybar: cordon is TierNone (no confirm at
// all), so without the result line a Forbidden cordon was invisible.
func TestFailedCordonShowsErrorOnKeybar(t *testing.T) {
	m := nodeResultModel(t, &fakeMutator{err: errors.New(`nodes "node-a" is forbidden`)})
	m = step(t, m, tea.KeyPressMsg{Text: verbs.Cordon.Key})
	if bar := lastLine(plain(m.Render())); !strings.Contains(bar, "forbidden") {
		t.Fatalf("expected the cordon error on the keybar, got %q", bar)
	}
}
