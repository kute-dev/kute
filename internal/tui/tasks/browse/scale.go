// 17b's +/− inline scale prompt (docs/design README.md §17b): reversible,
// so it never goes through actions.Controller's confirm machinery — instead
// a bespoke gate (pendingScale, like edit.go's pendingEdit) gathers a target
// replica count via a numeric type-ahead buffer, then commits through
// actions.Controller once ↵ applies, reusing its execute()/ResultMsg/
// HandleResult plumbing the same way beginRolloutRestart/beginCordon do for
// their own TierNone verbs. Kept in its own file, browse's per-concern split
// convention (like deployments.go/nodes.go/forwards.go).
package browse

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// scaleTarget is the state pendingScale gates on while 17b's prompt is
// showing.
type scaleTarget struct {
	kind      kube.ResourceKind
	namespace string
	name      string
	// value is the typed-ahead replica count, pre-filled to current±1 by
	// beginScale.
	value string
	// typed is true once a digit or backspace has touched value, so the
	// next digit appends instead of replacing the prefill (docs/design
	// README.md §17b: "typing a number replaces it").
	typed bool
}

// scalable reports whether kind takes 17b's scale prompt — Deployments and
// StatefulSets, the only two kinds browse projects a spec.replicas-derived
// READY column for.
func scalable(kind kube.ResourceKind) bool {
	return kind == kube.KindDeployment || kind == kube.KindStatefulSet
}

// currentReplicas reads a Deployment/StatefulSet row's desired replica
// count back out of its own READY cell ("3/3" — resources/projections.go's
// readyRatio puts the ready count and spec.replicas at Cells[1] for both
// kinds) rather than a second raw fetch.
func currentReplicas(row resources.Row) int32 {
	if len(row.Cells) < 2 {
		return 0
	}
	_, want, ok := strings.Cut(row.Cells[1], "/")
	if !ok {
		return 0
	}
	return scaleValue(want)
}

// scaleValue parses s as a non-negative replica count, defaulting to 0 for
// an empty or invalid buffer (docs/design README.md §17b: "0 = deliberate
// scale-to-zero").
func scaleValue(s string) int32 {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return int32(n)
}

// beginScale opens 17b's prompt for the selected row, pre-filled to
// current+delta clamped at 0 (delta is +1 for '+' and -1 for '−'). Returns
// false (no-op) when nothing applies — wrong kind, no mutator, not ready, or
// no row selected — mirroring openSelectedForward's ok-bool contract.
func (m *Model) beginScale(delta int32) bool {
	if !scalable(m.kind) || m.mutator == nil || m.state != tui.TaskStateReady {
		return false
	}
	row, ok := m.selectedRow()
	if !ok {
		return false
	}
	value := max(currentReplicas(row)+delta, 0)
	m.pendingScale = &scaleTarget{
		kind: m.kind, namespace: row.Namespace, name: row.Name,
		value: strconv.Itoa(int(value)),
	}
	return true
}

// updateScaleKey routes keys while pendingScale's prompt is showing.
func (m *Model) updateScaleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	t := m.pendingScale
	switch msg.String() {
	case "esc":
		m.pendingScale = nil
	case "enter":
		m.pendingScale = nil
		return m, m.commitScale(*t)
	case "backspace":
		if n := len(t.value); n > 0 {
			t.value = t.value[:n-1]
		}
		t.typed = true
	case "+":
		t.value = strconv.Itoa(int(scaleValue(t.value) + 1))
		t.typed = true
	case "-":
		t.value = strconv.Itoa(int(max(scaleValue(t.value)-1, 0)))
		t.typed = true
	default:
		if len(msg.Text) == 1 && msg.Text[0] >= '0' && msg.Text[0] <= '9' {
			if t.typed {
				t.value += msg.Text
			} else {
				t.value = msg.Text
				t.typed = true
			}
		}
	}
	return m, nil
}

// commitScale executes t through actions.Controller — verbs.Scale is
// TierNone, so Begin runs it immediately with no separate confirm, the same
// "reversible, no confirm" path RolloutRestart/Cordon already take.
func (m *Model) commitScale(t scaleTarget) tea.Cmd {
	replicas := scaleValue(t.value)
	return m.actions.Begin(verbs.Scale.Tier, tui.TaskAction{
		ID:    "scale-" + t.namespace + "/" + t.name,
		Label: fmt.Sprintf("Scale %s to %d", t.name, replicas),
		Scope: tui.TaskScope{
			ResourceKind: string(t.kind),
			ResourceName: t.name,
			Namespace:    t.namespace,
			Verb:         "scale",
			IsMutating:   true,
			Replicas:     replicas,
		},
	})
}

// scaleWillRunLine is pendingScale's keybar RightNote: the exact kubectl
// invocation Scale is equivalent to (docs/design README.md §17b: "same
// copyable-documentation idiom as 10a/13a"), updating live as the typed
// value changes.
func (m Model) scaleWillRunLine() string {
	t := m.pendingScale
	return "will run: " + kube.ScaleCommandString(t.kind, t.namespace, t.name, scaleValue(t.value))
}
