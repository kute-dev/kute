// Job- and CronJob-specific browse machinery: the ↵ "open this workload's
// own pods" recipe deployments.go established for Deployment/StatefulSet/
// DaemonSet (and helm.go for HelmRelease), extended here to a different
// resource family — run-to-completion workloads, with neither a
// rollout-restart verb nor any other machinery to share with
// deployments.go, hence their own file per browse's per-concern split
// convention (nodes.go/sort.go/grouping.go/delete.go/helm.go).
package browse

import (
	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
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
