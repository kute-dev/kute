// §36a's inline edit-schedule panel ('S' on a CronJob row): a bespoke gate
// (pendingCronSchedule) like scale.go's pendingScale, gathering a typed
// schedule string with client-side robfig/cron/v3 validation before there's
// an action to Begin. Unlike scale.go/setimage.go, this follows the
// keep-open contract (CLAUDE.md: "confirm → execute → refresh → show
// result → remain on screen is the decided contract for every
// context-preserving inspector/editor... don't copy SetImage/SetResources's
// close-on-commit code as precedent for new work") — meta.go is the
// template for that half of this file, scale.go for the buffer-gathering
// half.
package browse

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/robfig/cron/v3"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// cronScheduleTarget is the state pendingCronSchedule gates on while the
// panel is showing.
type cronScheduleTarget struct {
	namespace, name string
	// current is the schedule the row carried when the panel opened —
	// updated optimistically to the just-committed value on a successful
	// apply (see handleCronScheduleResult), since a single scalar field has
	// no join/immutable-key complexity worth a full object refetch the way
	// meta.go's multi-row grid needs.
	current string
	input   textinput.Model
	// parseErr is set inline when the typed value fails cron.ParseStandard —
	// cleared on the next edit or commit attempt. Client-side only; there is
	// no dry-run round trip the way setresources.go needs one, since a cron
	// expression's validity doesn't depend on cluster state.
	parseErr error
	// pendingCommit is the schedule value an in-flight Begin is attempting —
	// nil once HandleResult lands, the same "what was this commit trying to
	// do" bookkeeping meta.go's metaPendingCommit provides for its own
	// richer case.
	pendingCommit *string
	// resultNote/resultErr are handleCronScheduleResult's inline feedback —
	// exactly one of the two is non-empty right after a commit lands, both
	// cleared on the next edit.
	resultNote string
	resultErr  string
}

// beginCronJobSetSchedule opens the panel for the selected row, pre-filled
// with its current schedule. ok-bool contract like beginScale: false when
// nothing applies (wrong kind, no mutator, not ready, no row selected).
func (m *Model) beginCronJobSetSchedule() bool {
	if m.kind != kube.KindCronJob || m.mutator == nil || m.state != tui.TaskStateReady {
		return false
	}
	row, ok := m.selectedRow()
	if !ok {
		return false
	}
	current := ""
	if len(row.Cells) > 1 {
		current = row.Cells[1] // Columns: Name, Schedule, Next Run, Suspend, Active, Age
	}
	input := textinput.New()
	input.SetStyles(tui.TextInputStyles(m.Theme()))
	input.Prompt = ""
	input.SetValue(current)
	input.CursorEnd()
	input.Focus()
	m.pendingCronSchedule = &cronScheduleTarget{
		namespace: row.Namespace, name: row.Name, current: current, input: input,
	}
	return true
}

// cronSchedulePasteTarget is the schedule buffer — no digit-only gate
// (scalePasteTarget's own), since a cron expression carries `*`, `/`, `-`,
// `,` and digits alike.
func (m *Model) cronSchedulePasteTarget() tui.PasteTarget {
	t := m.pendingCronSchedule
	insert := tui.PasteInto(&t.input)
	return func(s string) {
		insert(s)
		t.parseErr = nil
	}
}

// updateCronScheduleKey routes keys while pendingCronSchedule's panel is
// showing. Never nulls m.pendingCronSchedule on enter — see
// commitCronSchedule/handleCronScheduleResult's keep-open contract.
func (m *Model) updateCronScheduleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	t := m.pendingCronSchedule
	switch msg.String() {
	case "esc":
		m.pendingCronSchedule = nil
	case "enter":
		return m, m.commitCronSchedule()
	default:
		var cmd tea.Cmd
		t.input, cmd = t.input.Update(msg)
		t.parseErr = nil
		return m, cmd
	}
	return m, nil
}

// commitCronSchedule validates the typed schedule client-side
// (cron.ParseStandard — the same parser robfig/cron/v3 gives the real
// CronJob controller server-side) before ever calling Begin. A parse
// failure keeps the panel open with t.parseErr set inline — no dry-run
// round trip, unlike setresources.go's own inline-error idiom, since a cron
// expression's validity is decidable without touching the cluster.
func (m *Model) commitCronSchedule() tea.Cmd {
	t := m.pendingCronSchedule
	value := strings.TrimSpace(t.input.Value())
	if _, err := cron.ParseStandard(value); err != nil {
		t.parseErr = err
		return nil
	}
	t.parseErr = nil
	t.resultErr = ""
	t.resultNote = ""
	pc := value
	t.pendingCommit = &pc
	return m.actions.Begin(verbs.TierForCronJobSetSchedule(m.isProd()), tui.TaskAction{
		ID:    "cronjob-set-schedule-" + t.namespace + "/" + t.name,
		Label: fmt.Sprintf("Set %s's schedule to %q?", t.name, value),
		Scope: tui.TaskScope{
			ResourceKind: string(kube.KindCronJob), ResourceName: t.name,
			Namespace: t.namespace, Verb: "cronjob-set-schedule", IsMutating: true,
			Schedule: value,
		},
	})
}

// isCronScheduleActionID reports whether id names a §36a "cronjob-set-
// schedule" commit (commitCronSchedule's own ID scheme) — used by
// update.go's actions.ResultMsg handler, the same way isMetaActionID routes
// meta.go's own commits.
func isCronScheduleActionID(id string) bool {
	return strings.HasPrefix(id, "cronjob-set-schedule-")
}

// handleCronScheduleResult applies a cronjob-set-schedule commit's outcome
// to the still-open panel — update.go's actions.ResultMsg case calls this
// instead of ever nulling m.pendingCronSchedule itself, the same "confirm →
// execute → refresh → show result → remain on screen" contract
// handleMetaResult follows (CLAUDE.md). On success the schedule is applied
// optimistically to t.current/resultNote rather than refetched through the
// lister: unlike meta.go's grid (which can carry join/immutable-key
// consequences a removal or another key can change), there is exactly one
// scalar field here and the server has just accepted the identical merge
// patch this panel sent — nothing about the commit is partial or
// server-computed the way a Service-selector join can make a label edit's
// real outcome. Only esc ever closes the panel from here.
func (m *Model) handleCronScheduleResult(msg actions.ResultMsg) tea.Cmd {
	t := m.pendingCronSchedule
	pc := t.pendingCommit
	t.pendingCommit = nil
	if msg.Err != nil {
		t.resultErr = msg.Err.Error()
		t.resultNote = ""
		return nil
	}
	t.resultErr = ""
	if pc != nil {
		t.current = *pc
		t.resultNote = "updated schedule to " + *pc
	}
	return nil
}

// cronScheduleWillRunLine is pendingCronSchedule's keybar RightNote while
// typing (pre-confirm) — the exact kubectl patch CronJobSetSchedule is
// equivalent to, updating live as the typed value changes, same idiom as
// scale.go's scaleWillRunLine.
func (m Model) cronScheduleWillRunLine() string {
	t := m.pendingCronSchedule
	return "will run: " + kube.CronJobSetScheduleCommandString(t.namespace, t.name, t.input.Value())
}

// cronJobSetScheduleWillRunLine is the confirm's "will run: ..." line (only
// reached when TierForCronJobSetSchedule escalates to TierInline in PROD) —
// same idiom as jobSuspendWillRunLine (jobs.go).
func cronJobSetScheduleWillRunLine(scope tui.TaskScope) string {
	return "will run: " + kube.CronJobSetScheduleCommandString(scope.Namespace, scope.ResourceName, scope.Schedule)
}
