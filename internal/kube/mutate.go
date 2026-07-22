package kube

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
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
	// RolloutRestart patches kind's pod template with a fresh restartedAt
	// annotation, the same mechanism `kubectl rollout restart` uses to
	// trigger a new rollout without changing the spec. Deployment,
	// StatefulSet, and DaemonSet are the three kinds with a pod template
	// browse exposes it on (9a's own 'r'; 27a's ctrl-r restarts a
	// ConfigMap's consumers regardless of which of the three they are).
	RolloutRestart(ctx context.Context, kind ResourceKind, namespace, name string) error
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
	// SetResources patches container's resources.requests/limits on kind's
	// pod template — 25a's editor. edits' non-nil fields are the changed
	// ones ("only changed fields go into the command"); a pointer to "" is
	// an explicit unset (the 'u' key), removing that key entirely rather
	// than leaving it untouched. dryRun runs the same patch with the API
	// server's DryRun option set, surfacing an admission rejection (a
	// namespace LimitRange/ResourceQuota violation) without mutating
	// anything — 25a's caller runs this once before the real patch.
	SetResources(ctx context.Context, kind ResourceKind, namespace, name, container string, edits ResourceEdits, dryRun bool) error
	// PatchMeta sets or removes a single label or annotation key on
	// kind/namespace/name — 26a's editor. remove=true deletes the key
	// (kubectl label/annotate's "key-" syntax, patched as a JSON-merge-patch
	// null); otherwise sets key=value ("--overwrite" when the key already
	// existed). Works on every kind including discovered CRDs: kinds outside
	// the typed clientset switch fall back to the dynamic client by GVR.
	PatchMeta(ctx context.Context, kind ResourceKind, namespace, name string, isAnnotation bool, key, value string, remove bool) error
	// PatchSecretData sets or removes a single key in a Secret's data — 27b's
	// add-key editor. remove=true deletes the key (a JSON-merge-patch null
	// under .data, the same null-removes-a-key idiom PatchMeta uses);
	// otherwise sets key=value via .stringData, so the API server does the
	// base64 encoding, not the caller.
	PatchSecretData(ctx context.Context, namespace, name, key, value string, remove bool) error
	// PatchConfigMapData sets or removes a single key in a ConfigMap's data —
	// 27a's value-edit editor. remove=true deletes the key (a JSON-merge-patch
	// null under .data, same idiom as PatchSecretData's removal); otherwise
	// sets key=value directly under .data — unlike Secret, ConfigMap's .data
	// is already plain text, so there's no .stringData split to make.
	PatchConfigMapData(ctx context.Context, namespace, name, key, value string, remove bool) error
}

// ConfigMapConsumerRef is one workload that references a ConfigMap from its
// pod template (27a's consumer strip / ctrl-r restart set) — the kind is
// carried alongside the name since ctrl-r's RolloutRestart call needs it and
// a consumer can be a Deployment, StatefulSet, or DaemonSet.
type ConfigMapConsumerRef struct {
	Kind ResourceKind
	Name string
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

// RolloutRestart patches kind's pod template annotations with a fresh
// restartedAt timestamp — the same strategic-merge patch `kubectl rollout
// restart` issues — which the owning controller treats as a template change
// and rolls out. Deployment, StatefulSet, and DaemonSet all accept the
// identical patch shape via their own typed AppsV1 clients.
func (c *Cluster) RolloutRestart(ctx context.Context, kind ResourceKind, namespace, name string) error {
	if name == "" {
		return fmt.Errorf("cannot restart rollout: empty name")
	}
	patch := []byte(fmt.Sprintf(
		`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":%q}}}}}`,
		time.Now().Format(time.RFC3339),
	))
	var err error
	switch kind {
	case KindStatefulSet:
		_, err = c.clientset.AppsV1().StatefulSets(namespace).Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
	case KindDaemonSet:
		_, err = c.clientset.AppsV1().DaemonSets(namespace).Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
	default:
		_, err = c.clientset.AppsV1().Deployments(namespace).Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
	}
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

// ResourceEdits is 25a's set of changed container resources.requests/limits
// fields. nil = unchanged (omitted from the patch entirely); a pointer to
// "" = an explicit unset (the 'u' key — patches that resource key to JSON
// null, removing it rather than merely not mentioning it, since "no limit"
// is a real and dangerous state the editor must be able to set
// deliberately); a pointer to a quantity string ("250m", "512Mi") = the new
// value.
type ResourceEdits struct {
	CPURequest *string
	CPULimit   *string
	MEMRequest *string
	MEMLimit   *string
}

// anyUnset reports whether edits carries at least one explicit unset (a
// non-nil pointer to "").
func (e ResourceEdits) anyUnset() bool {
	return isUnsetField(e.CPURequest) || isUnsetField(e.CPULimit) || isUnsetField(e.MEMRequest) || isUnsetField(e.MEMLimit)
}

func isUnsetField(p *string) bool {
	return p != nil && *p == ""
}

// SetResources patches container's resources.requests/limits via the same
// containers-by-name strategic-merge-patch idiom as SetImage/Scale — the
// container list patches by its "name" merge key, so one patch shape covers
// Deployment/StatefulSet/DaemonSet without a per-kind container index
// lookup. Only fields edits sets are included in the patch; an explicit
// unset serializes as JSON null under its resource key, which both
// strategic-merge-patch and a plain JSON merge patch interpret as "remove
// this key".
//
// LimitRange/ResourceQuota admission is real-API-server-only behavior — the
// fake clientset's tracker performs no admission checks, so a dry-run
// against kube/fake always succeeds regardless of the edited values; only
// the client-side request>limit check (25a's own validation, before this is
// ever called) is exercisable against the fake cluster.
func (c *Cluster) SetResources(ctx context.Context, kind ResourceKind, namespace, name, container string, edits ResourceEdits, dryRun bool) error {
	if name == "" {
		return fmt.Errorf("cannot set resources on %s: empty name", kind)
	}
	patch, ok := resourcesPatchJSON(container, edits)
	if !ok {
		return fmt.Errorf("cannot set resources on %s/%s: no changed fields", kind, name)
	}
	opts := metav1.PatchOptions{}
	if dryRun {
		opts.DryRun = []string{metav1.DryRunAll}
	}
	var err error
	switch kind {
	case KindDeployment:
		_, err = c.clientset.AppsV1().Deployments(namespace).Patch(ctx, name, types.StrategicMergePatchType, []byte(patch), opts)
	case KindStatefulSet:
		_, err = c.clientset.AppsV1().StatefulSets(namespace).Patch(ctx, name, types.StrategicMergePatchType, []byte(patch), opts)
	case KindDaemonSet:
		_, err = c.clientset.AppsV1().DaemonSets(namespace).Patch(ctx, name, types.StrategicMergePatchType, []byte(patch), opts)
	default:
		err = fmt.Errorf("set resources is not supported for kind %s", kind)
	}
	return err
}

// resourcesPatchJSON builds the strategic-merge-patch document SetResources
// applies (and SetResourcesCommandString's kubectl-patch fallback documents
// verbatim) — shared so the "will run" line always names the literal patch
// that executes. ok is false when edits carries no changed fields at all.
func resourcesPatchJSON(container string, edits ResourceEdits) (patch string, ok bool) {
	requests := resourceFieldsJSON(edits.CPURequest, edits.MEMRequest)
	limits := resourceFieldsJSON(edits.CPULimit, edits.MEMLimit)
	if requests == "" && limits == "" {
		return "", false
	}
	var parts []string
	if requests != "" {
		parts = append(parts, `"requests":`+requests)
	}
	if limits != "" {
		parts = append(parts, `"limits":`+limits)
	}
	resourcesObj := "{" + strings.Join(parts, ",") + "}"
	return fmt.Sprintf(
		`{"spec":{"template":{"spec":{"containers":[{"name":%q,"resources":%s}]}}}}`,
		container, resourcesObj,
	), true
}

// resourceFieldsJSON renders {"cpu":...,"memory":...} for a requests/limits
// pair, omitting nil fields and rendering an explicit unset (a pointer to
// "") as JSON null. Returns "" when both fields are nil.
func resourceFieldsJSON(cpu, mem *string) string {
	var parts []string
	if cpu != nil {
		parts = append(parts, `"cpu":`+quantityJSONLiteral(*cpu))
	}
	if mem != nil {
		parts = append(parts, `"memory":`+quantityJSONLiteral(*mem))
	}
	if len(parts) == 0 {
		return ""
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func quantityJSONLiteral(v string) string {
	if v == "" {
		return "null"
	}
	return strconv.Quote(v)
}

// SetResourcesCommandString renders the exact command SetResources runs —
// 25a's "will run" documentation line. A plain set (no unsets) renders as
// `kubectl set resources`, matching docs/design README.md §25a's own
// example; an explicit unset falls back to the literal `kubectl patch`
// invocation instead, since `kubectl set resources` has no clean field-
// removal flag — the will-run line always names the command that actually
// executes (27a/27b's same principle for their own patches).
func SetResourcesCommandString(kind ResourceKind, namespace, name, container string, edits ResourceEdits) string {
	if edits.anyUnset() {
		patch, _ := resourcesPatchJSON(container, edits)
		return fmt.Sprintf("kubectl patch %s/%s --type strategic -p '%s' -n %s", workloadResourceArg(kind), name, patch, namespace)
	}
	var reqParts, limParts []string
	if edits.CPURequest != nil {
		reqParts = append(reqParts, "cpu="+*edits.CPURequest)
	}
	if edits.MEMRequest != nil {
		reqParts = append(reqParts, "memory="+*edits.MEMRequest)
	}
	if edits.CPULimit != nil {
		limParts = append(limParts, "cpu="+*edits.CPULimit)
	}
	if edits.MEMLimit != nil {
		limParts = append(limParts, "memory="+*edits.MEMLimit)
	}
	cmd := fmt.Sprintf("kubectl set resources %s/%s -c %s", workloadResourceArg(kind), name, container)
	if len(limParts) > 0 {
		cmd += " --limits=" + strings.Join(limParts, ",")
	}
	if len(reqParts) > 0 {
		cmd += " --requests=" + strings.Join(reqParts, ",")
	}
	return cmd + " -n " + namespace
}

// PatchMeta implements Mutator against the live clientset. It mirrors
// deleteResource's per-kind switch (same kind list — the typed clients this
// app talks to), but falls back to the dynamic client by discovered GVR for
// any kind outside that switch, which is what actually makes this work on
// CRDs (deleteResource has no such fallback today; that gap is pre-existing
// and out of scope here).
func (c *Cluster) PatchMeta(ctx context.Context, kind ResourceKind, namespace, name string, isAnnotation bool, key, value string, remove bool) error {
	if name == "" {
		return fmt.Errorf("cannot patch %s: empty name", kind)
	}
	patch := []byte(metaPatchJSON(isAnnotation, key, value, remove))
	cs := c.clientset
	var err error
	switch kind {
	case KindPod:
		_, err = cs.CoreV1().Pods(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	case KindService:
		_, err = cs.CoreV1().Services(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	case KindIngress:
		_, err = cs.NetworkingV1().Ingresses(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	case KindConfigMap:
		_, err = cs.CoreV1().ConfigMaps(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	case KindSecret:
		_, err = cs.CoreV1().Secrets(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	case KindPersistentVolumeClaim:
		_, err = cs.CoreV1().PersistentVolumeClaims(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	case KindEvent:
		_, err = cs.CoreV1().Events(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	case KindDeployment:
		_, err = cs.AppsV1().Deployments(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	case KindDaemonSet:
		_, err = cs.AppsV1().DaemonSets(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	case KindStatefulSet:
		_, err = cs.AppsV1().StatefulSets(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	case KindReplicaSet:
		_, err = cs.AppsV1().ReplicaSets(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	case KindJob:
		_, err = cs.BatchV1().Jobs(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	case KindCronJob:
		_, err = cs.BatchV1().CronJobs(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	case KindNamespace:
		_, err = cs.CoreV1().Namespaces().Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	case KindNode:
		_, err = cs.CoreV1().Nodes().Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	default:
		dk, ok := c.getDynKind(kind)
		if !ok {
			return fmt.Errorf("labels/annotations are not supported for kind %s", kind)
		}
		res := c.dynClient.Resource(dk.gvr)
		var ri dynamic.ResourceInterface = res
		if dk.namespaced && namespace != "" {
			ri = res.Namespace(namespace)
		}
		_, err = ri.Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	}
	return err
}

// metaPatchJSON builds the JSON merge patch (RFC 7386) body PatchMeta sends:
// a null value under the target key removes it (both the typed clientset's
// and the dynamic client's Patch accept types.MergePatchType identically, so
// one body shape covers every kind).
func metaPatchJSON(isAnnotation bool, key, value string, remove bool) string {
	field := "labels"
	if isAnnotation {
		field = "annotations"
	}
	kv := fmt.Sprintf("%q:%q", key, value)
	if remove {
		kv = fmt.Sprintf("%q:null", key)
	}
	return fmt.Sprintf(`{"metadata":{"%s":{%s}}}`, field, kv)
}

// MetaCommandString renders the exact `kubectl label`/`kubectl annotate`
// invocation PatchMeta runs — 26a's "will run" documentation line.
// `--overwrite` only appears when overwrite is true (an existing key being
// changed, not a brand-new one); a removal renders kubectl's `key-` suffix
// instead. `-n namespace` is omitted for a cluster-scoped kind (namespace
// == "").
func MetaCommandString(kind ResourceKind, namespace, name string, isAnnotation bool, key, value string, remove, overwrite bool) string {
	verb := "label"
	if isAnnotation {
		verb = "annotate"
	}
	arg := metaResourceArg(kind) + "/" + name
	cmd := fmt.Sprintf("kubectl %s %s %s-", verb, arg, key)
	if !remove {
		cmd = fmt.Sprintf("kubectl %s %s %s=%s", verb, arg, key, value)
		if overwrite {
			cmd += " --overwrite"
		}
	}
	if namespace != "" {
		cmd += " -n " + namespace
	}
	return cmd
}

// metaResourceArg renders kind as kubectl's resource arg for
// MetaCommandString — the same short forms workloadResourceArg already gives
// Deployment/StatefulSet/DaemonSet (matching docs/design README.md §26a's own
// `deploy/aim-worker` example), falling back to the lowercased kind name for
// every other kind (DeleteCommandString's own convention for arbitrary
// kinds, CRDs included).
func metaResourceArg(kind ResourceKind) string {
	switch kind {
	case KindDeployment, KindStatefulSet, KindDaemonSet:
		return workloadResourceArg(kind)
	default:
		return strings.ToLower(string(kind))
	}
}

// PatchSecretData implements Mutator against the live clientset — a JSON
// merge patch (RFC 7386) targeting .stringData to add/edit (the server
// base64-encodes it and merges the result into .data) or .data with a null
// value to remove (.stringData is write-only and never itself holds a
// delete signal). Secret has no dynamic-client CRD fallback to speak of —
// it's always a typed core/v1 kind.
func (c *Cluster) PatchSecretData(ctx context.Context, namespace, name, key, value string, remove bool) error {
	if name == "" {
		return fmt.Errorf("cannot patch secret: empty name")
	}
	patch := []byte(secretDataPatchJSON(key, value, remove))
	_, err := c.clientset.CoreV1().Secrets(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

// secretDataPatchJSON builds the JSON merge patch body PatchSecretData
// sends — mirrors metaPatchJSON's null-removes-a-key shape, targeting
// .stringData to set (the server encodes/merges it into .data) or .data
// directly to remove (the only field a merge patch can null out — a
// Secret's stored representation never carries .stringData back).
func secretDataPatchJSON(key, value string, remove bool) string {
	if remove {
		return fmt.Sprintf(`{"data":{%q:null}}`, key)
	}
	return fmt.Sprintf(`{"stringData":{%q:%q}}`, key, value)
}

// SecretDataCommandString renders the exact `kubectl patch` invocation
// PatchSecretData runs — 27b's "will run" documentation line. The value
// itself is always masked as a fixed six-dot placeholder, never the real
// secret text (docs/design README.md §27b: "copyable documentation must not
// leak the secret into scrollback or a shared screen") — a removal's null
// literal carries no secret material, so it renders verbatim.
func SecretDataCommandString(namespace, name, key string, remove bool) string {
	arg := "secret/" + name
	if remove {
		return fmt.Sprintf("kubectl patch %s --type merge -p '{\"data\":{%q:null}}' -n %s", arg, key, namespace)
	}
	return fmt.Sprintf("kubectl patch %s --type merge -p '{\"stringData\":{%q:\"••••••\"}}' -n %s", arg, key, namespace)
}

// PatchConfigMapData implements Mutator against the live clientset — a JSON
// merge patch (RFC 7386) targeting .data directly to set or (with a null
// value) remove a key. Unlike Secret, ConfigMap's .data is already plain
// text server-side, so there's no .stringData split to make.
func (c *Cluster) PatchConfigMapData(ctx context.Context, namespace, name, key, value string, remove bool) error {
	if name == "" {
		return fmt.Errorf("cannot patch configmap: empty name")
	}
	patch := []byte(configMapDataPatchJSON(key, value, remove))
	_, err := c.clientset.CoreV1().ConfigMaps(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

// configMapDataPatchJSON builds the JSON merge patch body PatchConfigMapData
// sends — a null value under the target key removes it (same idiom
// metaPatchJSON/secretDataPatchJSON use), otherwise sets it.
func configMapDataPatchJSON(key, value string, remove bool) string {
	if remove {
		return fmt.Sprintf(`{"data":{%q:null}}`, key)
	}
	return fmt.Sprintf(`{"data":{%q:%q}}`, key, value)
}

// ConfigMapDataCommandString renders the exact `kubectl patch` invocation
// PatchConfigMapData runs — 27a's "will run" documentation line. Unlike
// SecretDataCommandString, the value is never masked: ConfigMap data isn't
// sensitive, and docs/design README.md §27a's own mockup line shows it
// verbatim (`kubectl patch cm/aim-config --type merge -p
// '{"data":{"LOG_LEVEL":"debug"}}' -n aim-stage`).
func ConfigMapDataCommandString(namespace, name, key, value string, remove bool) string {
	arg := "cm/" + name
	if remove {
		return fmt.Sprintf("kubectl patch %s --type merge -p '{\"data\":{%q:null}}' -n %s", arg, key, namespace)
	}
	return fmt.Sprintf("kubectl patch %s --type merge -p '{\"data\":{%q:%q}}' -n %s", arg, key, value, namespace)
}

// ConfigMapConsumerRestartCommandString renders the `kubectl rollout
// restart` invocation ctrl-r runs for one consumer — 27a's "prints every
// command it runs," one line per entry in ConfigMapConsumers alongside the
// patch itself.
func ConfigMapConsumerRestartCommandString(namespace string, ref ConfigMapConsumerRef) string {
	return fmt.Sprintf("kubectl rollout restart %s/%s -n %s", workloadResourceArg(ref.Kind), ref.Name, namespace)
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

// ForceDeleteCommandString renders the exact `kubectl delete ... --grace-period=0
// --force` invocation the non-prod inline confirm's force-delete sub-state
// (ctrl-k) runs, DeleteCommandString plus the two flags DeleteResourceForced
// actually passes to the API.
func ForceDeleteCommandString(kind ResourceKind, namespace, name string) string {
	return DeleteCommandString(kind, namespace, []string{name}) + " --grace-period=0 --force"
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
