// Job- and CronJob-specific browse machinery: the ↵ "open this workload's
// own pods" recipe deployments.go established for Deployment/StatefulSet/
// DaemonSet (and helm.go for HelmRelease), extended here to a different
// resource family — run-to-completion workloads, with neither a
// rollout-restart verb nor any other machinery to share with
// deployments.go, hence their own file per browse's per-concern split
// convention (nodes.go/sort.go/grouping.go/delete.go/helm.go). Also holds
// Job's own two mutating verbs, ctrl-r retry and 's' suspend/resume — kept
// here alongside the navigation above rather than split out, matching
// deployments.go's own precedent of keeping a kind's navigation and its
// mutating verb(s) in one file.
package browse

import (
	"fmt"
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
// "job-suspend"/"job-resume" based on row.Suspended. Always TierInline, same
// no-TierFor reasoning as beginJobRetry above: neither "job-suspend" nor
// "job-resume" is in requiresTypedName, so a PROD-escalated TierModal would
// render identically to TierInline anyway.
func (m *Model) beginJobSuspend(row resources.Row) tea.Cmd {
	verb, label := "job-suspend", fmt.Sprintf("Suspend %s?", row.Name)
	if row.Suspended {
		verb, label = "job-resume", fmt.Sprintf("Resume %s?", row.Name)
	}
	return m.actions.Begin(verbs.JobSuspend.Tier, tui.TaskAction{
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
