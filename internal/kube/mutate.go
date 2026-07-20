package kube

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Mutator is the write seam to the cluster: every mutating verb (delete,
// cordon, drain, rollout restart, …) gets its own method here as screens
// grow to need them, so every write funnels through one contract that
// app.NewModel wires and screens depend on only as an interface.
type Mutator interface {
	// DeleteResource deletes a single named resource of kind in namespace. For
	// cluster-scoped kinds namespace is ignored.
	DeleteResource(ctx context.Context, kind ResourceKind, namespace, name string) error
	// DeleteResourceForced deletes immediately (grace period 0 — ctrl-k).
	DeleteResourceForced(ctx context.Context, kind ResourceKind, namespace, name string) error
	// RolloutRestart patches deployment's pod template with a fresh
	// restartedAt annotation, the same mechanism `kubectl rollout restart`
	// uses to trigger a new rollout without changing the spec.
	RolloutRestart(ctx context.Context, namespace, deployment string) error
	// Cordon sets (cordon=true) or clears spec.unschedulable on node.
	Cordon(ctx context.Context, node string, cordon bool) error
	// Drain cordons node then evicts every evictable pod on it (skipping
	// DaemonSet-owned and mirror pods), returning the number evicted.
	Drain(ctx context.Context, node string) (int, error)
	// HelmRollback rolls release back to toRevision (0 = Helm's own default:
	// the previous revision) — the one Helm verb that shells out to the real
	// `helm` binary rather than reading the watch cache (docs/design
	// README.md §18a).
	HelmRollback(ctx context.Context, namespace, name string, toRevision int) error
	// Scale patches kind's spec.replicas to replicas — 17b's +/− inline
	// prompt. Deployment and StatefulSet are the only kinds with a
	// spec.replicas field browse exposes.
	Scale(ctx context.Context, kind ResourceKind, namespace, name string, replicas int32) error
	// SetImage patches container's image on kind's pod template — 24a's
	// tag-first inline editor. Deployment, StatefulSet, and DaemonSet are the
	// three kinds with a pod template browse exposes it on.
	SetImage(ctx context.Context, kind ResourceKind, namespace, name, container, image string) error
}

// DeleteResource implements Mutator against the live clientset. It maps the kind
// to the appropriate typed client. This is the only place the client-go delete
// verbs are called, so authorization failures surface uniformly through
// IsPermissionError at the call site.
func (c *Cluster) DeleteResource(ctx context.Context, kind ResourceKind, namespace, name string) error {
	return c.deleteResource(ctx, kind, namespace, name, metav1.DeleteOptions{})
}

// DeleteResourceForced deletes with GracePeriodSeconds 0 (ctrl-k force
// delete — always modal-confirmed, see actions.Tier).
func (c *Cluster) DeleteResourceForced(ctx context.Context, kind ResourceKind, namespace, name string) error {
	zero := int64(0)
	return c.deleteResource(ctx, kind, namespace, name, metav1.DeleteOptions{GracePeriodSeconds: &zero})
}

func (c *Cluster) deleteResource(ctx context.Context, kind ResourceKind, namespace, name string, opts metav1.DeleteOptions) error {
	if name == "" {
		return fmt.Errorf("cannot delete %s: empty name", kind)
	}
	cs := c.clientset
	switch kind {
	case KindPod:
		return cs.CoreV1().Pods(namespace).Delete(ctx, name, opts)
	case KindService:
		return cs.CoreV1().Services(namespace).Delete(ctx, name, opts)
	case KindIngress:
		return cs.NetworkingV1().Ingresses(namespace).Delete(ctx, name, opts)
	case KindConfigMap:
		return cs.CoreV1().ConfigMaps(namespace).Delete(ctx, name, opts)
	case KindSecret:
		return cs.CoreV1().Secrets(namespace).Delete(ctx, name, opts)
	case KindPersistentVolumeClaim:
		return cs.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, name, opts)
	case KindEvent:
		return cs.CoreV1().Events(namespace).Delete(ctx, name, opts)
	case KindDeployment:
		return cs.AppsV1().Deployments(namespace).Delete(ctx, name, opts)
	case KindDaemonSet:
		return cs.AppsV1().DaemonSets(namespace).Delete(ctx, name, opts)
	case KindStatefulSet:
		return cs.AppsV1().StatefulSets(namespace).Delete(ctx, name, opts)
	case KindReplicaSet:
		return cs.AppsV1().ReplicaSets(namespace).Delete(ctx, name, opts)
	case KindJob:
		return cs.BatchV1().Jobs(namespace).Delete(ctx, name, opts)
	case KindCronJob:
		return cs.BatchV1().CronJobs(namespace).Delete(ctx, name, opts)
	case KindNamespace:
		return cs.CoreV1().Namespaces().Delete(ctx, name, opts)
	case KindNode:
		return cs.CoreV1().Nodes().Delete(ctx, name, opts)
	default:
		return fmt.Errorf("delete is not supported for kind %s", kind)
	}
}

// RolloutRestart patches deployment's pod template annotations with a fresh
// restartedAt timestamp — the same strategic-merge patch `kubectl rollout
// restart` issues — which the deployment controller treats as a template
// change and rolls out.
func (c *Cluster) RolloutRestart(ctx context.Context, namespace, deployment string) error {
	if deployment == "" {
		return fmt.Errorf("cannot restart rollout: empty deployment name")
	}
	patch := fmt.Sprintf(
		`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":%q}}}}}`,
		time.Now().Format(time.RFC3339),
	)
	_, err := c.clientset.AppsV1().Deployments(namespace).Patch(ctx, deployment, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{})
	return err
}

// Cordon sets (cordon=true) or clears spec.unschedulable on node via a
// strategic-merge patch.
func (c *Cluster) Cordon(ctx context.Context, node string, cordon bool) error {
	if node == "" {
		return fmt.Errorf("cannot cordon: empty node name")
	}
	patch := fmt.Sprintf(`{"spec":{"unschedulable":%t}}`, cordon)
	_, err := c.clientset.CoreV1().Nodes().Patch(ctx, node, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{})
	return err
}

// Drain cordons node, then evicts every pod scheduled on it except
// DaemonSet-owned and mirror (static) pods, which a cordon+evict cycle
// can't move anywhere else. Returns the count evicted; a partial failure
// returns that count alongside a joined error for whichever pods failed.
func (c *Cluster) Drain(ctx context.Context, node string) (int, error) {
	if node == "" {
		return 0, fmt.Errorf("cannot drain: empty node name")
	}
	if err := c.Cordon(ctx, node, true); err != nil {
		return 0, fmt.Errorf("cordon %s before drain: %w", node, err)
	}

	// List cluster-wide and filter by NodeName in Go rather than relying on
	// a server-side field selector — pod field selectors aren't universally
	// honored (notably the client-go fake clientset ignores them), so this
	// stays correct against both.
	pods, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, fmt.Errorf("list pods on %s: %w", node, err)
	}

	evicted := 0
	var errs []error
	for _, pod := range pods.Items {
		if pod.Spec.NodeName != node || isDaemonSetOwnedPod(pod) || isMirrorPod(pod) {
			continue
		}
		eviction := &policyv1.Eviction{
			ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace},
		}
		if err := c.clientset.PolicyV1().Evictions(pod.Namespace).Evict(ctx, eviction); err != nil {
			errs = append(errs, fmt.Errorf("evict %s/%s: %w", pod.Namespace, pod.Name, err))
			continue
		}
		evicted++
	}
	return evicted, errors.Join(errs...)
}

// HelmRollback shells out to the real `helm` binary (kube/helm.go's
// HelmRollback) — the live clientset has no equivalent API call to make;
// Helm's own storage/rollback logic isn't reproduced here.
func (c *Cluster) HelmRollback(ctx context.Context, namespace, name string, toRevision int) error {
	return HelmRollback(ctx, namespace, name, toRevision)
}

// Scale patches kind's spec.replicas via the same strategic-merge-patch
// idiom as Cordon/RolloutRestart, rather than the clientset's dedicated
// UpdateScale call — one fewer API shape to special-case per kind.
func (c *Cluster) Scale(ctx context.Context, kind ResourceKind, namespace, name string, replicas int32) error {
	if name == "" {
		return fmt.Errorf("cannot scale %s: empty name", kind)
	}
	patch := fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas)
	var err error
	switch kind {
	case KindDeployment:
		_, err = c.clientset.AppsV1().Deployments(namespace).Patch(ctx, name, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{})
	case KindStatefulSet:
		_, err = c.clientset.AppsV1().StatefulSets(namespace).Patch(ctx, name, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{})
	default:
		err = fmt.Errorf("scale is not supported for kind %s", kind)
	}
	return err
}

// ScaleCommandString renders the exact `kubectl scale` invocation Scale
// runs — 17b's "will run" documentation line (the same copyable-command
// idiom as 10a/13a/18a).
func ScaleCommandString(kind ResourceKind, namespace, name string, replicas int32) string {
	return fmt.Sprintf("kubectl scale %s/%s --replicas=%d -n %s", workloadResourceArg(kind), name, replicas, namespace)
}

// SetImage patches container's image on kind's pod template via the same
// strategic-merge-patch idiom as Scale/Cordon — the container list patches
// by its "name" merge key, so one patch shape covers Deployment/StatefulSet/
// DaemonSet without a per-kind container index lookup.
func (c *Cluster) SetImage(ctx context.Context, kind ResourceKind, namespace, name, container, image string) error {
	if name == "" {
		return fmt.Errorf("cannot set image on %s: empty name", kind)
	}
	patch := fmt.Sprintf(
		`{"spec":{"template":{"spec":{"containers":[{"name":%q,"image":%q}]}}}}`,
		container, image,
	)
	var err error
	switch kind {
	case KindDeployment:
		_, err = c.clientset.AppsV1().Deployments(namespace).Patch(ctx, name, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{})
	case KindStatefulSet:
		_, err = c.clientset.AppsV1().StatefulSets(namespace).Patch(ctx, name, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{})
	case KindDaemonSet:
		_, err = c.clientset.AppsV1().DaemonSets(namespace).Patch(ctx, name, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{})
	default:
		err = fmt.Errorf("set image is not supported for kind %s", kind)
	}
	return err
}

// SetImageCommandString renders the exact `kubectl set image` invocation
// SetImage runs — 24a's "will run" documentation line (the same
// copyable-command idiom as 10a/13a/17b/18a).
func SetImageCommandString(kind ResourceKind, namespace, name, container, image string) string {
	return fmt.Sprintf("kubectl set image %s/%s %s=%s -n %s", workloadResourceArg(kind), name, container, image, namespace)
}

// DeleteCommandString renders the exact `kubectl delete` invocation a 20a
// bulk delete runs — one call naming every marked object, the same
// copyable-documentation idiom as 10a/13a/17b/18a. namespace == "" (a
// cluster-scoped kind, or a marked set spanning more than one namespace in
// 6b's all-namespaces triage) omits `-n` rather than naming a namespace the
// command wouldn't actually be scoped to.
func DeleteCommandString(kind ResourceKind, namespace string, names []string) string {
	cmd := fmt.Sprintf("kubectl delete %s %s", strings.ToLower(string(kind)), strings.Join(names, " "))
	if namespace != "" {
		cmd += " -n " + namespace
	}
	return cmd
}

// workloadResourceArg renders kind as kubectl's short resource arg for a
// "will run" line (ScaleCommandString/SetImageCommandString) — Deployment is
// the default for any kind that isn't StatefulSet/DaemonSet.
func workloadResourceArg(kind ResourceKind) string {
	switch kind {
	case KindStatefulSet:
		return "sts"
	case KindDaemonSet:
		return "ds"
	default:
		return "deploy"
	}
}

func isDaemonSetOwnedPod(pod corev1.Pod) bool {
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

func isMirrorPod(pod corev1.Pod) bool {
	_, ok := pod.Annotations[corev1.MirrorPodAnnotationKey]
	return ok
}
