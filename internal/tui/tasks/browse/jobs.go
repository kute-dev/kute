// Job- and CronJob-specific browse machinery: the ↵ "open this workload's
// own pods" recipe deployments.go established for Deployment/StatefulSet/
// DaemonSet (and helm.go for HelmRelease), extended here to a different
// resource family — run-to-completion workloads, with neither a
// rollout-restart verb nor any other machinery to share with
// deployments.go, hence their own file per browse's per-concern split
// convention (nodes.go/sort.go/grouping.go/delete.go/helm.go). Also holds
// Job's own two mutating verbs (ctrl-r retry, 's' suspend/resume) and
// CronJob's own ctrl-r/'s' siblings (run now, suspend/resume — one level up:
// a CronJob's ctrl-r triggers a Job rather than cloning itself) — kept here
// alongside the navigation above rather than split out, matching
// deployments.go's own precedent of keeping a kind's navigation and its
// mutating verb(s) in one file. CronJob's third verb, 'S' edit-schedule, is
// sizable enough (its own typed-buffer panel, validation, keep-open result
// handling) to live in its own file, cronjobschedule.go.
package browse

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// openJobPods switches kind to Pods with row's name pre-applied as the
// filter query, exactly like openDeploymentPods (deployments.go): a Job's
// own pods are named <job>-<random> (assigned directly by the Job
// controller, no intermediate ReplicaSet), so they too start with the
// owning Job's name and the existing fuzzy filter reads as an owner match.
func (m *Model) openJobPods(row resources.Row) tea.Cmd {
	cmd := m.switchKind(kube.KindPod)
	m.setFilter(row.Name)
	// switchKind's resetAndLoad clears originKind/originName along with
	// filterQuery, so they're set here for the same reason filterQuery is:
	// still in place once the loaded rows reach recomputeVisible.
	m.originKind, m.originName = kube.KindJob, row.Name
	return cmd
}

// openSelectedCronJobDetail pushes §36e (tasks/cronjobdetail, wired once
// Phase 7/8 land) for the selected CronJob row — 0.8.0 plan Phase 4 task
// 14's replacement for the old openCronJobPods jump-straight-to-Pods
// shortcut. siblings are every currently visible row's namespace-qualified
// ref, in order, the same "current list + this row's index" shape
// openSelectedPodDetail/openSelectedObjectDetail already use for their own
// `[`/`]` sibling movement — except namespace-qualified, since all-
// namespaces mode can list two different CronJobs sharing a name (Phase 4
// test 11). ok is false — and `enter` a no-op — until app.go wires
// OpenCronJobDetail; nothing regresses, there is simply nowhere to push
// yet.
func (m Model) openSelectedCronJobDetail() (tea.Model, tea.Cmd, bool) {
	if m.openCronJobDetail == nil || m.kind != kube.KindCronJob {
		return nil, nil, false
	}
	row, ok := m.selectedRow()
	if !ok {
		return nil, nil, false
	}
	siblings := make([]CronJobSiblingRef, len(m.visible))
	index := 0
	for i, fm := range m.visible {
		siblings[i] = CronJobSiblingRef{Namespace: fm.row.Namespace, Name: fm.row.Name}
		if fm.row.Namespace == row.Namespace && fm.row.Name == row.Name {
			index = i
		}
	}
	task, cmd := m.openCronJobDetail(row.Namespace, row.Name, siblings, index, m.width, m.height)
	return task, cmd, task != nil
}

// openSelectedCronJobSchedule pushes §36d (tasks/cronjobschedule) for the
// selected CronJob row's own schedule editor — Phase 6's replacement for
// the pre-Phase-6 inline beginCronJobSetSchedule buffer (the deleted
// cronjobschedule.go). Same "ok is false until app.go wires the opener"
// contract openSelectedCronJobDetail's own doc comment describes.
func (m Model) openSelectedCronJobSchedule() (tea.Model, tea.Cmd, bool) {
	if m.openCronJobSchedule == nil || m.kind != kube.KindCronJob {
		return nil, nil, false
	}
	row, ok := m.selectedRow()
	if !ok {
		return nil, nil, false
	}
	task, cmd := m.openCronJobSchedule(row.Namespace, row.Name, m.width, m.height)
	return task, cmd, task != nil
}

// selectedCronJobSummary returns the full aggregated resources.CronJobSummary
// behind the selected row — view.go's behavior strip and cronJobLogsTarget
// below both need spec/Job-history fields Row's own display Cells don't
// carry.
func (m Model) selectedCronJobSummary() (resources.CronJobSummary, bool) {
	row, ok := m.selectedRow()
	if !ok {
		return resources.CronJobSummary{}, false
	}
	return m.cronJobSummaryFor(row.Namespace, row.Name)
}

// cronJobSummaryFor resolves any row's full aggregated
// resources.CronJobSummary by namespace/name against m.cronJobSummaries —
// selectedCronJobSummary's own lookup, generalized for cronjob_actions.go's
// preflights, which (for a marked-set bulk action) need every marked row's
// summary, not just the selected one. Resolved by namespace/name rather than
// assumed position-aligned with m.visible/m.display: sort/filter never
// reorder m.rows for this kind (sort.go), but a defensive lookup costs
// nothing at this list's realistic size and stays correct regardless.
func (m Model) cronJobSummaryFor(namespace, name string) (resources.CronJobSummary, bool) {
	for i := range m.cronJobSummaries {
		obj := m.cronJobSummaries[i].Object
		if obj != nil && obj.Namespace == namespace && obj.Name == name {
			return m.cronJobSummaries[i], true
		}
	}
	return resources.CronJobSummary{}, false
}

// cronJobLogsTarget resolves §36a's 'l' (update.go's own case, since it
// needs a pointer receiver to set m.execFeedback on the "unavailable"
// path): the newest useful Pod of the selected CronJob's most recent
// associated Job, active or terminal, succeeded or failed — Runs is
// newest-first by runTimestamp and already includes every run regardless of
// state (resources.CronJobSummary's own doc comment), so index 0 is exactly
// "the latest job run" with no success/failure filtering (verbs.Logs' doc
// comment). reason names why no pod is available when ok is false; both
// empty means nothing is selected at all, a silent no-op like every other
// row-scoped verb.
func (m Model) cronJobLogsTarget() (pod kube.Pod, reason string, ok bool) {
	row, rowOK := m.selectedRow()
	if !rowOK {
		return kube.Pod{}, "", false
	}
	summary, sumOK := m.selectedCronJobSummary()
	if !sumOK {
		return kube.Pod{}, "", false
	}
	if len(summary.Runs) == 0 {
		return kube.Pod{}, row.Name + " has no runs to show logs for", false
	}
	run := summary.Runs[0]
	if run.PodName == "" {
		return kube.Pod{}, row.Name + ": no pod collected for this run yet", false
	}
	pod, ok = m.pods[run.PodName]
	if !ok {
		pod = kube.Pod{Namespace: row.Namespace, Name: run.PodName}
	}
	return pod, "", true
}

// beginJobRetry starts verbs.JobRetry (ctrl-r): clones row's spec into a new
// Job named "<name>-retry-<unix-timestamp>", computed once here so the
// will-run line (jobRetryWillRunLine) and the actual RetryJob call use the
// identical name. Deliberately always TierInline, with no TierFor/isProd
// PROD escalation unlike Delete/RolloutUndo: components.TypeNameModal (the
// PROD escalation surface those verbs share) is documented as "the app's
// other red-bordered surface" — reserved for destructive confirms — and
// Retry is explicitly non-destructive (confirmed with the user: it clones
// into a new object, the source Job is never touched). Escalating to
// TierModal without also opting into the typed-name gate would render
// identically to TierInline anyway (Controller.Confirm/view.go's dispatch
// both key off requiresTypedName, not the tier alone), so skipping the
// escalation entirely is the honest choice, not just the simpler one.
func (m *Model) beginJobRetry(row resources.Row) tea.Cmd {
	newName := fmt.Sprintf("%s-retry-%d", row.Name, time.Now().Unix())
	return m.actions.Begin(verbs.JobRetry.Tier, tui.TaskAction{
		ID:    "job-retry-" + row.Namespace + "/" + row.Name,
		Label: fmt.Sprintf("Retry %s?", row.Name),
		Scope: tui.TaskScope{
			ResourceKind: string(kube.KindJob), ResourceName: row.Name,
			Namespace: row.Namespace, Verb: "job-retry", IsMutating: true,
			NewName: newName,
		},
	})
}

// jobRetryWillRunLine is the confirm's "will run: ..." line — same idiom as
// rolloutRestartWillRunLine (deployments.go).
func jobRetryWillRunLine(scope tui.TaskScope) string {
	return "will run: " + kube.JobRetryCommandString(scope.Namespace, scope.ResourceName, scope.NewName)
}

// beginJobSuspend starts verbs.JobSuspend ('s'): one verb, two directions,
// exactly beginFluxSuspend's (flux.go) shape — Scope.Verb flips between
// "job-suspend"/"job-resume" based on row.Suspended. Unlike beginJobRetry's
// deliberate skip of TierFor, this one is routed through it: JobSuspend's own
// verbs.go doc comment is explicit that suspending a Job tears down its
// active pods immediately, a real destructive side effect Retry's clone-only
// semantics don't have, so PROD escalating TierInline to TierModal is not a
// no-op here — TierModal replaces the table with confirmBody's full
// ConfirmCard overlay, TierInline stays an inline keybar y/N row with the
// table still visible. Neither "job-suspend" nor "job-resume" is in
// requiresTypeNameConfirm/requiresTypedName, so the escalated PROD confirm
// still renders the plain ConfirmCard, not the typed-name modal — the same
// treatment Drain and Rollback already get.
func (m *Model) beginJobSuspend(row resources.Row) tea.Cmd {
	verb, label := "job-suspend", fmt.Sprintf("Suspend %s?", row.Name)
	if row.Suspended {
		verb, label = "job-resume", fmt.Sprintf("Resume %s?", row.Name)
	}
	tier := verbs.TierFor(verbs.JobSuspend, m.isProd())
	return m.actions.Begin(tier, tui.TaskAction{
		ID:    verb + "-" + row.Name,
		Label: label,
		Scope: tui.TaskScope{
			ResourceKind: string(kube.KindJob), ResourceName: row.Name,
			Namespace: row.Namespace, Verb: verb, IsMutating: true,
		},
	})
}

// jobSuspendWillRunLine is beginJobSuspend's confirm "will run: ..." line.
func jobSuspendWillRunLine(scope tui.TaskScope) string {
	return "will run: " + kube.JobSuspendCommandString(scope.Namespace, scope.ResourceName, scope.Verb == "job-suspend")
}

// jobKeybarGroup mirrors fluxKeybarGroup (flux.go): copies the registry
// Verb, flips its Label to "resume" when the selected row is already
// suspended, so the keybar always shows the direction pressing 's' would
// actually take.
func (m Model) jobKeybarGroup() []tui.KeyHint {
	suspend := verbs.JobSuspend
	if row, ok := m.selectedRow(); ok && row.Suspended {
		suspend.Label = "resume"
	}
	return []tui.KeyHint{
		{Key: verbs.JobRetry.Key, Label: verbs.JobRetry.Label},
		{Key: suspend.Key, Label: suspend.Label},
	}
}

// CronJobRunNow (ctrl-r) and CronJobSuspend's resume direction both stage a
// screen-owned preview before ever calling actions.Begin(TierNone, …) —
// 0.8.0 plan §36b/§36c/Phase 5. Their staging/commit/render logic lives in
// cronjob_actions.go, big enough on its own (overlap detection, missed-run
// math, bulk marked-set support) to outgrow this file's "navigation plus a
// kind's mutating verbs" scope. beginCronJobSuspend below now only ever
// handles the *suspend* direction — the dangerous half that still goes
// through actions.Controller's ordinary TierInline/TierModal confirm — with
// direction/marked-set routing itself living in
// cronjob_actions.go's beginCronJobSuspendOrResume, update.go's 's' dispatch
// target.

// beginCronJobSuspend starts verbs.CronJobSuspend's suspend direction only:
// TierInline outside PROD, TierModal (the type-the-name modal,
// actions.RequiresTypedName's "cronjob-suspend" entry) inside one — the
// ordinary Controller-driven confirm every other tiered verb gets, rendered
// generically (keys.go's "cronjob-suspend" case, delete.go's
// typeNameConfirmModal). CronJobResourceVersion/CronJobGeneration are the
// precondition/expected-generation values SetCronJobSuspend needs (§3.3) —
// resolved from the cache-local summary now that Phase 4's aggregation
// exists, closing out the TODO this function used to carry.
func (m *Model) beginCronJobSuspend(row resources.Row) tea.Cmd {
	summary, _ := m.cronJobSummaryFor(row.Namespace, row.Name)
	var resourceVersion string
	var generation int64
	if summary.Object != nil {
		resourceVersion = summary.Object.ResourceVersion
		generation = summary.Object.Generation
	}
	tier := verbs.TierForCronJobSuspend(true, m.isProd())
	return m.actions.Begin(tier, tui.TaskAction{
		ID:    "cronjob-suspend-" + row.Namespace + "/" + row.Name,
		Label: fmt.Sprintf("Suspend %s?", row.Name),
		Scope: tui.TaskScope{
			ResourceKind: string(kube.KindCronJob), ResourceName: row.Name,
			Namespace: row.Namespace, Verb: "cronjob-suspend", IsMutating: true,
			CronJobResourceVersion: resourceVersion,
			CronJobGeneration:      generation,
			StagedAt:               m.now,
		},
	})
}

// cronJobSuspendWillRunLine is beginCronJobSuspend's confirm "will run: ..."
// line — same idiom as jobSuspendWillRunLine.
func cronJobSuspendWillRunLine(scope tui.TaskScope) string {
	return "will run: " + kube.CronJobSuspendCommandString(scope.Namespace, scope.ResourceName, scope.Verb == "cronjob-suspend")
}

// cronJobSuspendDangerNote supplements beginCronJobSuspend's confirm (docs/
// design README.md §36c: "the card names any currently-active Jobs and
// states plainly that they are unaffected: suspend only stops *future*
// scheduling"). "" when summary is unavailable (falls back to the plain
// will-run line with no supplement). now drives the same NextRunLabel
// computation §36a's own NEXT column uses, so the "stops happening" claim
// names the actual next occurrence rather than a vague "future runs".
func cronJobSuspendDangerNote(summary resources.CronJobSummary, now time.Time) string {
	if summary.Object == nil {
		return ""
	}
	next := resources.NextRunLabel(summary.Object.Spec, now)
	consequence := "future scheduled runs stop"
	if next != "—" && next != "controller local" {
		consequence = fmt.Sprintf("the next scheduled run (%s) stops happening", next)
	}
	if len(summary.ActiveRuns) == 0 {
		return "no active jobs right now · " + consequence
	}
	names := make([]string, len(summary.ActiveRuns))
	for i, r := range summary.ActiveRuns {
		names[i] = r.Name
	}
	return "running jobs unaffected · " + strings.Join(names, ", ") + " · " + consequence
}

// cronJobKeybarGroup mirrors jobKeybarGroup/fluxKeybarGroup: copies the
// registry Verb, flips CronJobSuspend's label to "resume" when the selected
// row is already suspended.
func (m Model) cronJobKeybarGroup() []tui.KeyHint {
	suspend := verbs.CronJobSuspend
	if row, ok := m.selectedRow(); ok && row.Suspended {
		suspend.Label = "resume"
	}
	hints := []tui.KeyHint{
		{Key: verbs.CronJobRunNow.Key, Label: verbs.CronJobRunNow.Label},
		{Key: suspend.Key, Label: suspend.Label},
		{Key: verbs.CronJobSetSchedule.Key, Label: verbs.CronJobSetSchedule.Label},
	}
	if m.openLogs != nil {
		// §36a's keybar: "l logs" — verbs.Logs' Kinds already includes
		// CronJob (verbs.go), same conditional-on-wiring guard Pod's own
		// keybar group uses for the identical hint.
		hints = append(hints, verbs.Logs.Hint())
	}
	return hints
}
