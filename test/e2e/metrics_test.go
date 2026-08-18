//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// noMetrics is what kute renders for a usage reading it does not have.
const noMetrics = "– –"

// TestNoMetricsServerRendersUnknown: kind ships no metrics-server, which
// makes it free coverage for the rule "CPU/MEM render –, never a lie or a
// crash". Every surface that shows usage has to say it does not know, on a
// cluster where nothing can tell it.
//
// The failure this guards against is not a crash — it is a zero. A missing
// metrics client that yields an empty PodMetrics reads as 0m/0Mi, and a pod
// reported as using no CPU is a worse answer than no answer.
func TestNoMetricsServerRendersUnknown(t *testing.T) {
	a := Launch(t)
	a.WaitFor("api-", Connect)

	t.Run("browse columns", func(t *testing.T) {
		frame := a.WaitFor(noMetrics, Settle)
		// Not just somewhere on screen: on every pod row. One row rendering
		// the unknown marker while the rest render zeroes would pass a bare
		// Contains.
		for _, line := range strings.Split(frame, "\n") {
			if !strings.Contains(line, "api-") && !strings.Contains(line, "worker-") {
				continue
			}
			if !strings.Contains(line, noMetrics) {
				t.Errorf("a pod row has a usage reading with no metrics-server to produce it:\n%s", line)
			}
		}
	})

	t.Run("pod detail bars", func(t *testing.T) {
		a.filterTo(t, "api-")
		a.Enter()
		a.WaitLoaded(Settle)
		// §5a's CPU/MEM bars: "usage / limit", both unknown.
		a.WaitForAll(Settle, "CPU", "MEM", noMetrics)
		a.Esc()
		a.WaitFor("api-", Settle)
		a.Esc()
	})

	t.Run("node detail", func(t *testing.T) {
		a.gotoKind(t, "nodes", "Nodes")
		// gotoKind only waits for the breadcrumb, which is on screen before
		// the rows are. The 140-column E2E viewport keeps the deterministic
		// control-plane name intact, so use the complete row name rather than
		// a prefix that can also occur in the breadcrumb.
		a.WaitFor(NodeNamePrefix(t)+"-control-plane", Settle)
		a.Enter()
		a.WaitLoaded(Settle)
		// The allocated/allocatable bars are computed from pod *requests*,
		// which need no metrics server, so those stay real numbers — it is
		// the per-pod usage columns that must read unknown.
		a.WaitForAll(Settle, "ALLOCATED / ALLOCATABLE", noMetrics)
		a.Esc()
		a.WaitFor("Nodes", Settle)
	})

	t.Run("overview capacity", func(t *testing.T) {
		a.gotoPalette(t, "overview", "cluster · routing", "Cluster Overview")
		a.WaitLoaded(Settle)
		// 19a says so outright rather than drawing an empty bar, which is
		// the clearest form of "no answer" on this screen.
		a.WaitFor("no metrics-server installed", Settle)
		frame := a.Frame()
		if strings.Contains(frame, "cpu ▮") || strings.Contains(frame, "mem ▮") {
			t.Errorf("the overview drew a cpu/mem capacity bar with no metrics-server behind it:\n%s", frame)
		}
	})

	// Still running and still usable after all of it — the "never a crash"
	// half of the row. The overview is a routing layer rather than a resting
	// screen, so getting back to a list means jumping to one.
	a.Esc()
	a.gotoKind(t, "pods", "Pods")
	a.WaitFor("api-", Settle)
}
