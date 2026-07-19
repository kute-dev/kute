package kube

import (
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Pod struct {
	Context     string
	Namespace   string
	Name        string
	Ready       string
	Status      string
	Reason      string
	Restarts    int32
	Age         string
	AgeDuration time.Duration
	Node        string
	Owner       string
	Containers  []string
	Unready     []string
	CPU         string
	MEM         string
	// Exact usage from the metrics API (0 until metrics are merged in).
	CPUMilli int64
	MEMBytes int64
	// Summed container requests/limits from the pod spec (0 when unset).
	CPURequestMilli int64
	CPULimitMilli   int64
	MEMRequestBytes int64
	MEMLimitBytes   int64

	// Detail fields (5a pod detail), populated by PodFromObject; browse's
	// list rows leave these at their zero value rather than paying to
	// compute them for every row.
	IP              string
	QoSClass        string
	Labels          map[string]string
	Tolerations     []string // formatted "key=value:Effect" / "key (exists):Effect"
	ContainerInfos  []ContainerInfo
	LastTermination *LastTermination // nil when no container has ever terminated
	// GracePeriodSeconds is Spec.TerminationGracePeriodSeconds, or the
	// cluster default (30) when unset — 8b's delete confirm shows this
	// concrete figure instead of a generic "default grace period applies"
	// (docs/design README.md §8b: "30s").
	GracePeriodSeconds int64
}

// ContainerInfo is one row of the 5a CONTAINERS grid (and 10a's exec
// picker).
type ContainerInfo struct {
	Name     string
	Image    string
	State    string // "Running", "Waiting", "Terminated"
	Reason   string // e.g. "CrashLoopBackOff", "Completed"
	Restarts int32
	Ready    bool
	// IsSidecar is true for a native sidecar container (KEP-753: an
	// initContainer with restartPolicy: Always) — a real API-level signal,
	// not a name heuristic (docs/design README.md §10a: "sidecars labeled
	// sidecar").
	IsSidecar bool
}

// LastTermination is the 5a last-termination banner: the most recent
// container termination across the pod, promoted to the top of detail so
// "why is it broken?" is answered first.
type LastTermination struct {
	Container  string
	ExitCode   int32
	Reason     string
	Age        time.Duration
	FinishedAt time.Time
	// RestartCount is the container's current restart count, used by
	// NextBackoff to estimate kubelet's CrashLoopBackOff delay.
	RestartCount int32
}

// NextBackoff estimates kubelet's CrashLoopBackOff delay before the next
// restart attempt (docs/design README.md §5a: body line names "the memory
// limit + next backoff"): 10s, doubling per restart, capped at 5 minutes —
// the same base/cap kubelet's own SyncTerminatedPod backoff uses. This is an
// estimate, not a scheduled instant read from the API (Kubernetes doesn't
// expose one).
func (lt LastTermination) NextBackoff() time.Duration {
	const (
		base   = 10 * time.Second
		capDur = 5 * time.Minute
	)
	restarts := lt.RestartCount
	if restarts < 1 {
		restarts = 1
	}
	d := base
	for i := int32(1); i < restarts; i++ {
		d *= 2
		if d >= capDur {
			return capDur
		}
	}
	return d
}

// PodFromObject projects a *corev1.Pod into the domain Pod struct, list
// fields and detail fields alike, so the pod list and pod detail (5a)
// screens share one projection instead of drifting apart.
func PodFromObject(pod *corev1.Pod) Pod {
	ready, restarts, reason, unready := containerStatusSummary(pod.Status.ContainerStatuses)

	containers := make([]string, 0, len(pod.Spec.Containers))
	var cpuReq, cpuLim, memReq, memLim int64
	for _, container := range pod.Spec.Containers {
		containers = append(containers, container.Name)
		cpuReq += container.Resources.Requests.Cpu().MilliValue()
		cpuLim += container.Resources.Limits.Cpu().MilliValue()
		memReq += container.Resources.Requests.Memory().Value()
		memLim += container.Resources.Limits.Memory().Value()
	}
	if reason == "" {
		reason = string(pod.Status.Phase)
	}

	age := metav1.Now().Sub(pod.CreationTimestamp.Time).Round(0)
	return Pod{
		Namespace:   pod.Namespace,
		Name:        pod.Name,
		Ready:       formatReady(ready, int32(len(pod.Spec.Containers))),
		Status:      string(pod.Status.Phase),
		Reason:      reason,
		Restarts:    restarts,
		Age:         age.String(),
		AgeDuration: age,
		Node:        pod.Spec.NodeName,
		Owner:       ownerRef(pod.OwnerReferences),
		Containers:  containers,
		Unready:     unready,
		CPU:         "n/a",
		MEM:         "n/a",

		CPURequestMilli: cpuReq,
		CPULimitMilli:   cpuLim,
		MEMRequestBytes: memReq,
		MEMLimitBytes:   memLim,

		IP:              pod.Status.PodIP,
		QoSClass:        string(pod.Status.QOSClass),
		Labels:          pod.Labels,
		Tolerations:     formatTolerations(pod.Spec.Tolerations),
		ContainerInfos:  buildContainerInfos(pod),
		LastTermination: findLastTermination(pod.Status.ContainerStatuses),
		GracePeriodSeconds: func() int64 {
			if pod.Spec.TerminationGracePeriodSeconds != nil {
				return *pod.Spec.TerminationGracePeriodSeconds
			}
			return 30 // corev1's own default when the field is unset
		}(),
	}
}

// formatTolerations renders each toleration as "key=value:Effect" (or
// "key (exists):Effect" for the Exists operator) for the 5a sidebar.
func formatTolerations(tolerations []corev1.Toleration) []string {
	out := make([]string, 0, len(tolerations))
	for _, t := range tolerations {
		key := t.Key
		if key == "" {
			key = "*"
		}
		cond := key + "=" + t.Value
		if t.Operator == corev1.TolerationOpExists {
			cond = key + " (exists)"
		}
		effect := string(t.Effect)
		if effect == "" {
			effect = "All"
		}
		out = append(out, cond+":"+effect)
	}
	return out
}

// buildContainerInfos merges spec (name/image) with status (state/ready/
// restarts) for the 5a CONTAINERS grid and 10a's exec picker. A container
// with no status yet (still being created) gets a zero-value State. Native
// sidecars (initContainers with restartPolicy: Always) are appended after
// the regular containers, flagged IsSidecar, matched against
// InitContainerStatuses rather than ContainerStatuses.
func buildContainerInfos(pod *corev1.Pod) []ContainerInfo {
	byName := make(map[string]corev1.ContainerStatus, len(pod.Status.ContainerStatuses))
	for _, s := range pod.Status.ContainerStatuses {
		byName[s.Name] = s
	}
	out := make([]ContainerInfo, 0, len(pod.Spec.Containers))
	for _, c := range pod.Spec.Containers {
		info := ContainerInfo{Name: c.Name, Image: c.Image}
		if s, ok := byName[c.Name]; ok {
			applyContainerStatus(&info, s)
		}
		out = append(out, info)
	}

	initByName := make(map[string]corev1.ContainerStatus, len(pod.Status.InitContainerStatuses))
	for _, s := range pod.Status.InitContainerStatuses {
		initByName[s.Name] = s
	}
	for _, c := range pod.Spec.InitContainers {
		if c.RestartPolicy == nil || *c.RestartPolicy != corev1.ContainerRestartPolicyAlways {
			continue // a regular (non-sidecar) init container, not part of the running-containers grid
		}
		info := ContainerInfo{Name: c.Name, Image: c.Image, IsSidecar: true}
		if s, ok := initByName[c.Name]; ok {
			applyContainerStatus(&info, s)
		}
		out = append(out, info)
	}
	return out
}

func applyContainerStatus(info *ContainerInfo, s corev1.ContainerStatus) {
	info.Ready = s.Ready
	info.Restarts = s.RestartCount
	switch {
	case s.State.Running != nil:
		info.State = "Running"
	case s.State.Terminated != nil:
		info.State = "Terminated"
		info.Reason = s.State.Terminated.Reason
	case s.State.Waiting != nil:
		info.State = "Waiting"
		info.Reason = s.State.Waiting.Reason
	}
}

// findLastTermination scans both current and last-known termination states
// across every container and returns the most recent one (by FinishedAt),
// for the 5a last-termination banner. Returns nil when no container has
// ever terminated.
func findLastTermination(statuses []corev1.ContainerStatus) *LastTermination {
	var best *corev1.ContainerStateTerminated
	var bestName string
	var bestRestarts int32
	consider := func(name string, t *corev1.ContainerStateTerminated, restarts int32) {
		if t == nil {
			return
		}
		if best == nil || t.FinishedAt.After(best.FinishedAt.Time) {
			best, bestName, bestRestarts = t, name, restarts
		}
	}
	for _, s := range statuses {
		consider(s.Name, s.State.Terminated, s.RestartCount)
		consider(s.Name, s.LastTerminationState.Terminated, s.RestartCount)
	}
	if best == nil {
		return nil
	}
	return &LastTermination{
		Container:    bestName,
		ExitCode:     best.ExitCode,
		Reason:       best.Reason,
		Age:          metav1.Now().Sub(best.FinishedAt.Time).Round(0),
		FinishedAt:   best.FinishedAt.Time,
		RestartCount: bestRestarts,
	}
}

func formatReady(ready, total int32) string {
	return strconv.FormatInt(int64(ready), 10) + "/" + strconv.FormatInt(int64(total), 10)
}

func containerStatusSummary(statuses []corev1.ContainerStatus) (int32, int32, string, []string) {
	ready := int32(0)
	restarts := int32(0)
	reason := ""
	unready := make([]string, 0)
	for _, status := range statuses {
		if status.Ready {
			ready++
		} else {
			unready = append(unready, status.Name)
		}
		restarts += status.RestartCount
		if reason == "" && status.State.Waiting != nil && status.State.Waiting.Reason != "" {
			reason = status.State.Waiting.Reason
		}
		if reason == "" && status.State.Terminated != nil && status.State.Terminated.Reason != "" {
			reason = status.State.Terminated.Reason
		}
	}
	return ready, restarts, reason, unready
}

func ownerRef(refs []metav1.OwnerReference) string {
	if len(refs) == 0 {
		return ""
	}
	return refs[0].Kind + "/" + refs[0].Name
}
