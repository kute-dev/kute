package cronjobdetail

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
)

// reloadDebounce coalesces bursts of watch events into one reload — same
// value as browse/nodedetail's own reloadDebounce, duplicated per the
// repo's package-local-seam convention.
const reloadDebounce = 250 * time.Millisecond

// reloadDueMsg fires scheduleReload's retry — epoch guards a stale reply
// against re-triggering a load() that's no longer wanted, mirroring browse/
// nodedetail's own reloadDueMsg.
type reloadDueMsg struct{ epoch int }

// scheduleReload arranges one reloadDueMsg reloadDebounce from now.
func (m Model) scheduleReload(epoch int) tea.Cmd {
	return tea.Tick(reloadDebounce, func(time.Time) tea.Msg {
		return reloadDueMsg{epoch: epoch}
	})
}

// load lists CronJob/Job (namespace-scoped) and best-effort Pod once each,
// then aggregates client-side via resources.BuildCronJobSummaries — the same
// recipe browse's loadCronJobRows follows for the list (Phase 4 task 2),
// applied here to a single named CronJob. A CronJob list failure fails the
// whole load; a Job list failure does not (§4.4 point 5: the facts grid and
// schedule stay visible, with the Jobs table/failure band marked
// unavailable instead of falsely empty — see applyLoaded's own handling of
// jobsErr).
func (m Model) load() tea.Cmd {
	epoch := m.reloadEpoch
	lister := m.lister
	namespace := m.namespace
	name := m.name
	timeout := m.timeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		cronJobObjs, err := lister.ListRaw(ctx, kube.KindCronJob, namespace)
		if err != nil {
			return loadedMsg{epoch: epoch, err: err}
		}
		jobObjs, jobsErr := lister.ListRaw(ctx, kube.KindJob, namespace)
		// Pods are best-effort (§4.4) — a failed Pod read must never erase a
		// valid Job-level failure (buildJobSummary already tolerates a nil/
		// partial pods slice), so its own error is simply discarded.
		podObjs, _ := lister.ListRaw(ctx, kube.KindPod, namespace)

		summaries := resources.BuildCronJobSummaries(cronJobObjs, jobObjs, podObjs)
		summary, found := findCronJobSummary(summaries, name)
		if !found {
			return loadedMsg{epoch: epoch, found: false, jobsErr: jobsErr}
		}

		controller := resolveCronJobController(summary.Object)
		return loadedMsg{
			epoch: epoch, found: true, summary: summary, jobsErr: jobsErr,
			pods:       podsByName(podObjs),
			controller: controller,
		}
	}
}

// findCronJobSummary resolves name against summaries (a
// BuildCronJobSummaries reply already scoped to this screen's namespace).
func findCronJobSummary(summaries []resources.CronJobSummary, name string) (resources.CronJobSummary, bool) {
	for _, s := range summaries {
		if s.Object != nil && s.Object.Name == name {
			return s, true
		}
	}
	return resources.CronJobSummary{}, false
}

// podsByName indexes podObjs by name for the Jobs table's own 'l' logs
// target lookup — mirrors browse's own podsByName, scoped to an
// already-fetched slice rather than issuing its own ListRaw (this screen
// already read Pods once for BuildCronJobSummaries and reuses that read
// rather than fetching them twice).
func podsByName(podObjs []runtime.Object) map[string]kube.Pod {
	out := make(map[string]kube.Pod, len(podObjs))
	for _, obj := range podObjs {
		if p, ok := obj.(*corev1.Pod); ok {
			out[p.Name] = kube.PodFromObject(p)
		}
	}
	return out
}

// resolveCronJobController resolves 36e's best-effort CONTROLLER field
// (docs/design README.md §36e: "a best-effort controller/manager link"): a
// controller owner reference formatted "Kind/name" when one exists, else a
// Helm-managed CronJob's own release annotation ("helm/<release>") as a
// fallback — Helm doesn't set ownerReferences on the resources it installs,
// only these two well-known annotations, so a Helm-deployed CronJob would
// otherwise show no controller at all. "" when neither is present; the view
// renders "–" for that case, exactly like poddetail's own controller field.
func resolveCronJobController(cj *batchv1.CronJob) string {
	if cj == nil {
		return ""
	}
	for _, ref := range cj.OwnerReferences {
		if ref.Controller != nil && *ref.Controller {
			return ref.Kind + "/" + ref.Name
		}
	}
	if release := cj.Annotations["meta.helm.sh/release-name"]; release != "" {
		return "helm/" + release
	}
	return ""
}

// lastScheduleTime reads cj.Status.LastScheduleTime as a plain time.Time,
// zero when unset — a small accessor kept here (not view.go) so the
// facts-grid renderer stays a pure formatter over already-extracted values.
func lastScheduleTime(cj *batchv1.CronJob) time.Time {
	if cj == nil || cj.Status.LastScheduleTime == nil {
		return time.Time{}
	}
	return cj.Status.LastScheduleTime.Time
}
