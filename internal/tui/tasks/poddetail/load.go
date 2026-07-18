package poddetail

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

// load fetches the pod itself (found=false, not an error, when it's no
// longer in the cache — a watch delete), merges best-effort live metrics for
// the CPU/MEM bars, and best-effort fetches its events. A metrics or events
// failure never fails the whole load — the pod's own fields are what
// matters; bars/events just degrade to "–"/empty.
func (m Model) load() tea.Cmd {
	lister := m.lister
	metrics := m.metrics
	events := m.events
	namespace := m.namespace
	name := m.name
	timeout := m.timeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		objs, err := lister.ListRaw(ctx, kube.KindPod, namespace)
		if err != nil {
			return loadedMsg{err: err}
		}
		obj := findPod(objs, name)
		if obj == nil {
			return loadedMsg{found: false}
		}
		pod := kube.PodFromObject(obj)

		if metrics != nil {
			if podMetrics, err := metrics.PodMetricsByNamespace(ctx, namespace); err == nil {
				if pm, ok := podMetrics[name]; ok {
					pod.CPU, pod.MEM = pm.CPU, pm.MEM
					pod.CPUMilli, pod.MEMBytes = pm.CPUMilli, pm.MemBytes
				}
			}
		}

		var eventRows []kube.Event
		var eventsErr error
		if events != nil {
			eventRows, eventsErr = events.ObjectEvents(ctx, namespace, kube.KindPod, name)
		}

		return loadedMsg{pod: pod, found: true, events: eventRows, eventsErr: eventsErr}
	}
}

func findPod(objs []runtime.Object, name string) *corev1.Pod {
	for _, obj := range objs {
		if p, ok := obj.(*corev1.Pod); ok && p.Name == name {
			return p
		}
	}
	return nil
}

// statusClass mirrors resources.projectPod's classification (docs/design
// README.md §2a/§5a), but works from the already-projected kube.Pod (whose
// Reason field, from kube.PodFromObject, already carries the waiting/
// terminated reason) instead of a raw runtime.Object — the same reasoning
// nodedetail.podGlyph documents for working off kube.Pod.
func statusClass(p kube.Pod) (glyph, class, text string) {
	switch {
	case p.Status == string(corev1.PodSucceeded):
		return "○", "neutral", "Completed"
	case p.Status == string(corev1.PodFailed):
		return "✕", "fail", p.Reason
	case strings.Contains(p.Reason, "CrashLoop"):
		return "✕", "fail", p.Reason
	case p.Status == string(corev1.PodPending):
		return "◐", "warn", p.Reason
	default:
		return "●", "ok", p.Reason
	}
}

func usageText(v string) string {
	if v == "" || v == "n/a" {
		return "–"
	}
	return v
}
