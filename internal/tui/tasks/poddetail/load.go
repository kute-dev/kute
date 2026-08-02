package poddetail

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
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

		controller := resolveControllerDisplay(ctx, lister, namespace, pod.Owner)
		related := resolveRelatedItems(ctx, lister, namespace, pod.Labels, controller)

		return loadedMsg{pod: pod, found: true, events: eventRows, eventsErr: eventsErr, controller: controller, related: related}
	}
}

// relatedItem is one RELATED sidebar entry (docs/design README.md §5a) — a
// numbered jump target resolved once here in load(), never in the render
// path (CLAUDE.md: render functions are pure, no I/O), so pressing its
// digit key (update.go's openRelated) can jump without a synchronous
// lookup. Label is the pre-formatted "Kind/name" text sidebarBlock renders.
type relatedItem struct {
	Kind  kube.ResourceKind
	Name  string
	Label string
}

// resolveRelatedItems resolves RELATED's numbered entries: the owning
// Deployment/StatefulSet (reusing controller's already-resolved
// ReplicaSet→Deployment hop — DaemonSet/Job owners and an unresolved
// ReplicaSet are deliberately excluded, matching the old 'o' shortcut's
// scope) and the Ingress fronting this pod, if any.
func resolveRelatedItems(ctx context.Context, lister resources.RawLister, namespace string, podLabels map[string]string, controller string) []relatedItem {
	var items []relatedItem
	if kind, name, ok := splitOwner(controller); ok && (kind == kube.KindDeployment || kind == kube.KindStatefulSet) {
		items = append(items, relatedItem{Kind: kind, Name: name, Label: controller})
	}
	if lister != nil {
		if name, ok := resolveIngressName(ctx, lister, namespace, podLabels); ok {
			items = append(items, relatedItem{Kind: kube.KindIngress, Name: name, Label: string(kube.KindIngress) + "/" + name})
		}
	}
	return items
}

// resolveIngressName resolves the Ingress that fronts a pod carrying
// podLabels: the Services whose label selector matches it, then the
// Ingress whose rules/default backend name one of those Services. ok is
// false when no Ingress can be resolved (no matching Service, no Ingress
// referencing it, or several matches — this keeps the numbered jump
// unambiguous rather than guessing).
func resolveIngressName(ctx context.Context, lister resources.RawLister, namespace string, podLabels map[string]string) (string, bool) {
	svcObjs, err := lister.ListRaw(ctx, kube.KindService, namespace)
	if err != nil {
		return "", false
	}
	selector := labels.Set(podLabels)
	matched := map[string]bool{}
	for _, obj := range svcObjs {
		svc, ok := obj.(*corev1.Service)
		if !ok || len(svc.Spec.Selector) == 0 {
			continue
		}
		if labels.SelectorFromSet(svc.Spec.Selector).Matches(selector) {
			matched[svc.Name] = true
		}
	}
	if len(matched) == 0 {
		return "", false
	}

	ingObjs, err := lister.ListRaw(ctx, kube.KindIngress, namespace)
	if err != nil {
		return "", false
	}
	name := ""
	for _, obj := range ingObjs {
		ing, ok := obj.(*networkingv1.Ingress)
		if !ok || !ingressReferencesServices(ing, matched) {
			continue
		}
		if name != "" && name != ing.Name {
			return "", false // ambiguous — more than one Ingress fronts this pod
		}
		name = ing.Name
	}
	return name, name != ""
}

// ingressReferencesServices reports whether ing's default backend or any
// rule's paths name one of services.
func ingressReferencesServices(ing *networkingv1.Ingress, services map[string]bool) bool {
	if b := ing.Spec.DefaultBackend; b != nil && b.Service != nil && services[b.Service.Name] {
		return true
	}
	for _, rule := range ing.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		for _, path := range rule.HTTP.Paths {
			if path.Backend.Service != nil && services[path.Backend.Service.Name] {
				return true
			}
		}
	}
	return false
}

// splitOwner parses an "Owner/name" string (kube.PodFromObject's ownerRef
// shape, e.g. "ReplicaSet/nva-worker-abc123") into its kind/name.
func splitOwner(owner string) (kube.ResourceKind, string, bool) {
	kind, name, found := strings.Cut(owner, "/")
	if !found || kind == "" || name == "" {
		return "", "", false
	}
	return kube.ResourceKind(kind), name, true
}

// ownerRef mirrors kube.PodFromObject's own unexported helper of the same
// name (pods.go), duplicated here since a ReplicaSet's OwnerReferences need
// the identical "Kind/Name" projection and that helper isn't exported.
func ownerRef(refs []metav1.OwnerReference) string {
	if len(refs) == 0 {
		return ""
	}
	return refs[0].Kind + "/" + refs[0].Name
}

// resolveControllerDisplay resolves owner (pod.Owner, "Kind/name") for 5a's
// CONTROLLER field: a ReplicaSet-owned pod resolves one hop further to its
// own owning Deployment (docs/design README.md §5a: "deploy/nva-worker ↗"),
// since a Deployment never appears as a pod's direct owner — every other
// owner kind (StatefulSet, DaemonSet, Job) already IS the display-worthy
// controller and passes through unchanged. Runs inside load()'s tea.Cmd, not
// the render path, so the extra ReplicaSet lookup is fine here (CLAUDE.md:
// render functions are pure, no I/O).
func resolveControllerDisplay(ctx context.Context, lister resources.RawLister, namespace, owner string) string {
	kind, name, ok := splitOwner(owner)
	if !ok || kind != kube.KindReplicaSet || lister == nil {
		return owner
	}
	objs, err := lister.ListRaw(ctx, kube.KindReplicaSet, namespace)
	if err != nil {
		return owner
	}
	for _, obj := range objs {
		rs, ok := obj.(*appsv1.ReplicaSet)
		if !ok || rs.Name != name {
			continue
		}
		if depOwner := ownerRef(rs.OwnerReferences); depOwner != "" {
			if depKind, _, ok := splitOwner(depOwner); ok && depKind == kube.KindDeployment {
				return depOwner
			}
		}
	}
	return owner
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
	case p.Deleting:
		return "◌", "warn", "Terminating"
	case p.Status == string(corev1.PodSucceeded):
		return "○", "neutral", "Completed"
	case p.Status == string(corev1.PodFailed):
		return "✕", "fail", p.Reason
	case strings.Contains(p.Reason, "CrashLoop"):
		return "✕", "fail", p.Reason
	case p.Status == string(corev1.PodPending):
		return "▲", "warn", p.Reason
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
