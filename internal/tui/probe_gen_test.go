package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
)

// probeGenTestTask is a minimal Task for driving Model.Update directly in
// this package's internal tests (the tui_test package's screenTask isn't
// reachable from here).
type probeGenTestTask struct{}

func (probeGenTestTask) Init() tea.Cmd                       { return nil }
func (probeGenTestTask) SetSize(int, int)                    {}
func (probeGenTestTask) View() tea.View                      { return tea.NewView("") }
func (probeGenTestTask) Update(tea.Msg) (tea.Model, tea.Cmd) { return probeGenTestTask{}, nil }

// TestContextProbeMsgStaleGenNotAppliedButStillDrained pins the fix for a
// real race: reopening/re-probing the 7a context palette while a previous
// kube.ProbeContexts run is still streaming results must not let the stale
// run's data land in m.probes, but its channel still needs draining to
// completion (contextProbeMsg carries its own channel — see context.go —
// so the stale drain loop never has to consult m.probeGen to know what to
// read next).
func TestContextProbeMsgStaleGenNotAppliedButStillDrained(t *testing.T) {
	t.Parallel()

	m := NewWithSession(probeGenTestTask{}, &Session{Theme: Dark()})
	// m.probeGen's zero value (0) is "current" here — no startContextProbe
	// call needed for this test, which only exercises the gen guard itself.
	updated, _ := m.Update(contextProbeMsg{gen: 0, res: kube.ProbeResult{Name: "dev", Latency: 5}})
	m2 := updated.(Model)
	if got, ok := m2.probes["dev"]; !ok || got.Latency != 5 {
		t.Fatalf("expected the current-gen result applied to probes, got %+v ok=%v", got, ok)
	}

	updated, cmd := m2.Update(contextProbeMsg{gen: 1, res: kube.ProbeResult{Name: "stale-ctx", Latency: 99}})
	m3 := updated.(Model)
	if _, ok := m3.probes["stale-ctx"]; ok {
		t.Fatalf("expected a stale-gen (1 != current 0) result NOT applied to probes")
	}
	if cmd == nil {
		t.Fatalf("expected the stale-gen drain loop to keep reading its own channel to completion")
	}
}
