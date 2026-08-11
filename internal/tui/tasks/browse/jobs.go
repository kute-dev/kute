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
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
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

// openCronJobPods is openJobPods's CronJob twin, skipping an intermediate
// Jobs-list step: a CronJob spawns a Job named <cronjob>-<unixtime>, whose
// own pods are named <cronjob>-<unixtime>-<random> — still prefixed by the
// CronJob's own name, so the same name-prefix fuzzy filter works directly
// from CronJob → Pods without ever listing the intermediate Job.
func (m *Model) openCronJobPods(row resources.Row) tea.Cmd {
	cmd := m.switchKind(kube.KindPod)
	m.setFilter(row.Name)
	m.originKind, m.originName = kube.KindCronJob, row.Name
	return cmd
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

// beginCronJobRunNow starts verbs.CronJobRunNow (ctrl-r): triggers a new,
// standalone Job named "<name>-manual-<unix-timestamp>" from the CronJob's
// own jobTemplate, computed once here so the will-run line
// (cronJobRunNowWillRunLine) and the actual TriggerCronJob call agree —
// exactly beginJobRetry's shape above. Deliberately always TierInline with
// no TierFor/isProd escalation, for the identical reason beginJobRetry
// gives: this is a clone into a new object (the CronJob itself, its
// schedule, its own Job history are all untouched), not a delete+recreate,
// so it doesn't belong in components.TypeNameModal.
func (m *Model) beginCronJobRunNow(row resources.Row) tea.Cmd {
	newName := fmt.Sprintf("%s-manual-%d", row.Name, time.Now().Unix())
	return m.actions.Begin(verbs.CronJobRunNow.Tier, tui.TaskAction{
		ID:    "cronjob-run-now-" + row.Namespace + "/" + row.Name,
		Label: fmt.Sprintf("Run %s now?", row.Name),
		Scope: tui.TaskScope{
			ResourceKind: string(kube.KindCronJob), ResourceName: row.Name,
			Namespace: row.Namespace, Verb: "cronjob-run-now", IsMutating: true,
			NewName: newName,
		},
	})
}

// cronJobRunNowWillRunLine is the confirm's "will run: ..." line — same
// idiom as jobRetryWillRunLine above.
func cronJobRunNowWillRunLine(scope tui.TaskScope) string {
	return "will run: " + kube.CronJobTriggerCommandString(scope.Namespace, scope.ResourceName, scope.NewName)
}

// beginCronJobSuspend starts verbs.CronJobSuspend ('s'): one verb, two
// directions, but — unlike beginFluxSuspend/beginCordon — the two directions
// now resolve to different tiers (0.8.0 plan §36c/§3 task 2): resume passes
// verbs.TierForCronJobSuspend(false, …) straight through, always TierNone,
// so it executes immediately exactly like beginFluxSuspend/beginCordon do;
// suspend passes verbs.TierForCronJobSuspend(true, m.isProd()) instead,
// which is TierInline outside PROD and TierModal inside it — the ordinary
// Controller-driven inline y/N / type-the-name modal every other tiered verb
// already gets, rendered generically (keys.go's "cronjob-suspend" case,
// delete.go's typeNameConfirmModal). m.execFeedback is set only for the
// TierNone (resume) path — a TierInline/TierModal confirm renders its own
// will-run line (cronJobSuspendWillRunLine) instead, the same split
// beginJobSuspend's TierInline-only shape doesn't need to make.
//
// TODO(0.8.0 Phase 4/5): CronJobResourceVersion/CronJobGeneration/StagedAt
// stay zero-valued here — resources.Row carries no resourceVersion/
// generation yet (that's Phase 4's cache-local CronJob+Job aggregation).
// SetCronJobSuspend's precondition and expected-generation stamp are both
// no-ops until that data reaches this Scope.
func (m *Model) beginCronJobSuspend(row resources.Row) tea.Cmd {
	verb, label := "cronjob-suspend", fmt.Sprintf("Suspend %s?", row.Name)
	suspending := !row.Suspended
	if row.Suspended {
		verb, label = "cronjob-resume", fmt.Sprintf("Resume %s?", row.Name)
	}
	tier := verbs.TierForCronJobSuspend(suspending, m.isProd())
	if tier == actions.TierNone {
		m.execFeedback = kube.CronJobSuspendCommandString(row.Namespace, row.Name, suspending)
	}
	return m.actions.Begin(tier, tui.TaskAction{
		ID:    verb + "-" + row.Name,
		Label: label,
		Scope: tui.TaskScope{
			ResourceKind: string(kube.KindCronJob), ResourceName: row.Name,
			Namespace: row.Namespace, Verb: verb, IsMutating: true,
		},
	})
}

// cronJobSuspendWillRunLine is beginCronJobSuspend's confirm "will run: ..."
// line for the TierInline/TierModal (suspend) path — same idiom as
// jobSuspendWillRunLine.
func cronJobSuspendWillRunLine(scope tui.TaskScope) string {
	return "will run: " + kube.CronJobSuspendCommandString(scope.Namespace, scope.ResourceName, scope.Verb == "cronjob-suspend")
}

// cronJobKeybarGroup mirrors jobKeybarGroup/fluxKeybarGroup: copies the
// registry Verb, flips CronJobSuspend's label to "resume" when the selected
// row is already suspended.
func (m Model) cronJobKeybarGroup() []tui.KeyHint {
	suspend := verbs.CronJobSuspend
	if row, ok := m.selectedRow(); ok && row.Suspended {
		suspend.Label = "resume"
	}
	return []tui.KeyHint{
		{Key: verbs.CronJobRunNow.Key, Label: verbs.CronJobRunNow.Label},
		{Key: suspend.Key, Label: suspend.Label},
		{Key: verbs.CronJobSetSchedule.Key, Label: verbs.CronJobSetSchedule.Label},
	}
}
