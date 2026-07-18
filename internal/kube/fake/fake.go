// Package fake is a feature-complete in-memory implementation of every
// consumer seam the UI uses against *kube.Cluster (resources.RawLister,
// kube.Mutator, GetYAML, ObjectEvents, pod metrics, log streaming, the
// namespace/context Switcher, and connection health) — mvp-plan.md §0.7.
// It powers task tests (a whole cluster of fixtures beats ad hoc stubs) and
// the --demo flag. Rule: every new seam method on *kube.Cluster gets its
// counterpart here in the same change that adds it.
package fake

import (
	"context"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/kute-dev/kute/internal/kube"
)

// Cluster is the in-memory stand-in for *kube.Cluster. The zero value (via
// New) is an empty cluster; NewDemo seeds it with the fixtures --demo shows.
type Cluster struct {
	mu      sync.Mutex
	objects map[kube.ResourceKind][]runtime.Object
	logs    map[string][]string // "namespace/pod" -> lines to stream

	// discovered is the fake counterpart of kube.Cluster's own discovery
	// cache (discovery.go/dynamic.go) — seeded directly via SeedDiscovered
	// rather than parsed from CRD objects, since --demo/tests already build
	// whatever shape they want by hand (fixtures.go's demoCRD family).
	discovered []kube.DiscoveredKind

	namespace           string
	context             string
	contexts            []string
	perContextNamespace map[string]string
	// userName/userGroups are the fake counterpart of kube.Cluster's
	// Context.UserName/UserGroups — the identity tasks/whocan (22a) pins as
	// "current user". Set via SetUserName/SetUserGroups; zero value unless
	// a fixture sets them.
	userName   string
	userGroups []string

	events chan kube.ResourceChangedMsg
	connCh chan kube.ConnStateMsg
	conn   kube.ConnState
}

// New builds an empty fake cluster scoped to namespace/context.
func New(namespace, contextName string) *Cluster {
	return &Cluster{
		objects:             make(map[kube.ResourceKind][]runtime.Object),
		logs:                make(map[string][]string),
		namespace:           namespace,
		context:             contextName,
		contexts:            []string{contextName},
		perContextNamespace: map[string]string{contextName: namespace},
		events:              make(chan kube.ResourceChangedMsg, 64),
		connCh:              make(chan kube.ConnStateMsg, 8),
		conn:                kube.ConnState{Phase: kube.ConnConnected},
	}
}

// Seed adds objects of kind to the fake cluster (fixtures.go's NewDemo, and
// tests, build a cluster by seeding per kind).
func (c *Cluster) Seed(kind kube.ResourceKind, objs ...runtime.Object) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.objects[kind] = append(c.objects[kind], objs...)
}

// SeedLogs registers the lines StreamPodLogs replays for namespace/pod.
func (c *Cluster) SeedLogs(namespace, pod string, lines []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logs[namespace+"/"+pod] = lines
}

// --- resources.RawLister ---

func (c *Cluster) ListRaw(_ context.Context, kind kube.ResourceKind, namespace string) ([]runtime.Object, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	all := c.objects[kind]
	if namespace == "" {
		out := make([]runtime.Object, len(all))
		copy(out, all)
		return out, nil
	}
	out := make([]runtime.Object, 0, len(all))
	for _, obj := range all {
		accessor, err := apimeta.Accessor(obj)
		if err != nil {
			continue
		}
		if accessor.GetNamespace() == namespace {
			out = append(out, obj)
		}
	}
	return out, nil
}

// --- kube.Mutator ---

func (c *Cluster) DeleteResource(_ context.Context, kind kube.ResourceKind, namespace, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	objs := c.objects[kind]
	for i, obj := range objs {
		accessor, err := apimeta.Accessor(obj)
		if err != nil {
			continue
		}
		if accessor.GetName() == name && (namespace == "" || accessor.GetNamespace() == namespace) {
			c.objects[kind] = append(objs[:i:i], objs[i+1:]...)
			c.notify(kind)
			return nil
		}
	}
	return fmt.Errorf("%s %q not found", kind, name)
}

func (c *Cluster) DeleteResourceForced(ctx context.Context, kind kube.ResourceKind, namespace, name string) error {
	return c.DeleteResource(ctx, kind, namespace, name)
}

func (c *Cluster) RolloutRestart(_ context.Context, namespace, deployment string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, obj := range c.objects[kube.KindDeployment] {
		d, ok := obj.(*appsv1.Deployment)
		if !ok || d.Name != deployment || d.Namespace != namespace {
			continue
		}
		if d.Spec.Template.Annotations == nil {
			d.Spec.Template.Annotations = map[string]string{}
		}
		d.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)
		c.notify(kube.KindDeployment)
		return nil
	}
	return fmt.Errorf("deployment %q not found", deployment)
}

func (c *Cluster) Cordon(_ context.Context, node string, cordon bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, obj := range c.objects[kube.KindNode] {
		n, ok := obj.(*corev1.Node)
		if !ok || n.Name != node {
			continue
		}
		n.Spec.Unschedulable = cordon
		c.notify(kube.KindNode)
		return nil
	}
	return fmt.Errorf("node %q not found", node)
}

func (c *Cluster) Drain(ctx context.Context, node string) (int, error) {
	if err := c.Cordon(ctx, node, true); err != nil {
		return 0, err
	}
	c.mu.Lock()
	pods := append([]runtime.Object(nil), c.objects[kube.KindPod]...)
	c.mu.Unlock()

	evicted := 0
	for _, obj := range pods {
		pod, ok := obj.(*corev1.Pod)
		if !ok || pod.Spec.NodeName != node {
			continue
		}
		if isDaemonSetOwned(pod) || isMirror(pod) {
			continue
		}
		if err := c.DeleteResource(ctx, kube.KindPod, pod.Namespace, pod.Name); err != nil {
			continue
		}
		evicted++
	}
	return evicted, nil
}

// Scale sets Deployment/StatefulSet spec.Replicas in place — 17b's +/−
// inline prompt against the fake cluster.
func (c *Cluster) Scale(_ context.Context, kind kube.ResourceKind, namespace, name string, replicas int32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, obj := range c.objects[kind] {
		switch o := obj.(type) {
		case *appsv1.Deployment:
			if o.Name != name || o.Namespace != namespace {
				continue
			}
			o.Spec.Replicas = &replicas
			c.notify(kind)
			return nil
		case *appsv1.StatefulSet:
			if o.Name != name || o.Namespace != namespace {
				continue
			}
			o.Spec.Replicas = &replicas
			c.notify(kind)
			return nil
		}
	}
	return fmt.Errorf("%s %q not found", kind, name)
}

// HelmRollback simulates `helm rollback` against the fake cluster's seeded
// helm.sh/release.v1 Secrets: it decodes every revision of name, picks the
// target (toRevision, or the previous revision when 0 — Helm's own
// default), marks the currently-deployed revision superseded, and appends a
// new highest-revision Secret carrying the target's chart/values/manifest
// with Status "deployed" — the same "rollback creates a new revision"
// semantics real Helm has, without needing a real helm binary or the Helm
// SDK (kube/helm.go's EncodeHelmReleaseSecret does the inverse encode).
func (c *Cluster) HelmRollback(_ context.Context, namespace, name string, toRevision int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	all := kube.DecodeHelmReleases(c.objects[kube.KindSecret])
	history := kube.HelmReleaseHistory(all, namespace, name)
	if len(history) == 0 {
		return fmt.Errorf("release %q not found in namespace %q", name, namespace)
	}
	current := history[0] // HelmReleaseHistory sorts newest-first
	var target *kube.HelmRelease
	for i := range history {
		if (toRevision == 0 && history[i].Revision == current.Revision-1) || (toRevision != 0 && history[i].Revision == toRevision) {
			target = &history[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("release %q has no revision to roll back to", name)
	}

	objs := c.objects[kube.KindSecret]
	for i, obj := range objs {
		secret, ok := obj.(*corev1.Secret)
		if !ok || secret.Type != kube.HelmReleaseSecretType {
			continue
		}
		r, err := kube.DecodeHelmReleaseSecret(secret)
		if err != nil || r.Namespace != namespace || r.Name != name || r.Revision != current.Revision {
			continue
		}
		r.Status = "superseded"
		objs[i] = kube.EncodeHelmReleaseSecret(r)
	}

	next := *target
	next.Revision = current.Revision + 1
	next.Status = "deployed"
	next.StatusReason = ""
	next.Updated = time.Now()
	c.objects[kube.KindSecret] = append(objs, kube.EncodeHelmReleaseSecret(next))

	c.notify(kube.KindSecret)
	return nil
}

func isDaemonSetOwned(pod *corev1.Pod) bool {
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

func isMirror(pod *corev1.Pod) bool {
	_, ok := pod.Annotations[corev1.MirrorPodAnnotationKey]
	return ok
}

// --- GetYAML / ObjectEvents ---

func (c *Cluster) GetYAML(ctx context.Context, kind kube.ResourceKind, namespace, name string) (string, string, error) {
	objs, err := c.ListRaw(ctx, kind, namespace)
	if err != nil {
		return "", "", err
	}
	for _, obj := range objs {
		accessor, err := apimeta.Accessor(obj)
		if err != nil || accessor.GetName() != name {
			continue
		}
		copyObj := obj.DeepCopyObject()
		copyAccessor, err := apimeta.Accessor(copyObj)
		if err != nil {
			return "", "", err
		}
		rv := copyAccessor.GetResourceVersion()
		copyAccessor.SetManagedFields(nil)
		data, err := sigsyaml.Marshal(copyObj)
		if err != nil {
			return "", "", err
		}
		return string(data), rv, nil
	}
	return "", "", fmt.Errorf("%s %q not found", kind, name)
}

func (c *Cluster) ObjectEvents(ctx context.Context, namespace string, kind kube.ResourceKind, name string) ([]kube.Event, error) {
	objs, err := c.ListRaw(ctx, kube.KindEvent, namespace)
	if err != nil {
		return nil, err
	}
	out := make([]kube.Event, 0, len(objs))
	for _, obj := range objs {
		ev, ok := obj.(*corev1.Event)
		if !ok || ev.InvolvedObject.Kind != string(kind) || ev.InvolvedObject.Name != name {
			continue
		}
		out = append(out, kube.Event{
			Type:      ev.Type,
			Reason:    ev.Reason,
			Message:   ev.Message,
			Object:    ev.InvolvedObject.Kind + "/" + ev.InvolvedObject.Name,
			Namespace: ev.Namespace,
			Count:     max32(ev.Count, 1),
			FirstSeen: ev.FirstTimestamp.Time,
			LastSeen:  ev.LastTimestamp.Time,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out, nil
}

func (c *Cluster) NamespaceEvents(ctx context.Context, namespace string) ([]kube.Event, error) {
	objs, err := c.ListRaw(ctx, kube.KindEvent, namespace)
	if err != nil {
		return nil, err
	}
	out := make([]kube.Event, 0, len(objs))
	for _, obj := range objs {
		ev, ok := obj.(*corev1.Event)
		if !ok {
			continue
		}
		out = append(out, kube.Event{
			Type:      ev.Type,
			Reason:    ev.Reason,
			Message:   ev.Message,
			Object:    ev.InvolvedObject.Kind + "/" + ev.InvolvedObject.Name,
			Namespace: ev.Namespace,
			Count:     max32(ev.Count, 1),
			FirstSeen: ev.FirstTimestamp.Time,
			LastSeen:  ev.LastTimestamp.Time,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out, nil
}

func max32(v, floor int32) int32 {
	if v == 0 {
		return floor
	}
	return v
}

// --- pod metrics ---

func (c *Cluster) PodMetricsByNamespace(_ context.Context, namespace string) (map[string]kube.PodMetrics, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]kube.PodMetrics)
	for _, obj := range c.objects[kube.KindPod] {
		pod, ok := obj.(*corev1.Pod)
		if !ok || (namespace != "" && pod.Namespace != namespace) {
			continue
		}
		out[pod.Name] = kube.PodMetrics{CPU: "n/a", MEM: "n/a"}
	}
	return out, nil
}

func (c *Cluster) NodeMetrics(_ context.Context) (map[string]kube.NodeMetric, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]kube.NodeMetric)
	for _, obj := range c.objects[kube.KindNode] {
		n, ok := obj.(*corev1.Node)
		if !ok {
			continue
		}
		out[n.Name] = kube.NodeMetric{CPU: "n/a", MEM: "n/a"}
	}
	return out, nil
}

// --- log streaming ---

func (c *Cluster) StreamPodLogs(_ context.Context, req kube.LogStreamRequest) (io.ReadCloser, error) {
	c.mu.Lock()
	lines := c.logs[req.Namespace+"/"+req.PodName]
	c.mu.Unlock()
	return io.NopCloser(strings.NewReader(strings.Join(lines, "\n") + "\n")), nil
}

// --- Switcher (home.Switcher successor) ---

func (c *Cluster) Contexts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.contexts...)
}

func (c *Cluster) CurrentContext() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.context
}

func (c *Cluster) CurrentNamespace() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.namespace
}

func (c *Cluster) SwitchNamespace(namespace string) {
	if namespace == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.namespace = namespace
	c.perContextNamespace[c.context] = namespace
}

func (c *Cluster) SwitchContext(_ context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !slices.Contains(c.contexts, name) {
		return fmt.Errorf("context %q not found", name)
	}
	c.context = name
	if ns, ok := c.perContextNamespace[name]; ok && ns != "" {
		c.namespace = ns
	}
	return nil
}

// AddContext registers an additional switchable context (for palette/probe
// fixtures and tests).
func (c *Cluster) AddContext(name, namespace string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.contexts = append(c.contexts, name)
	c.perContextNamespace[name] = namespace
}

// SetUserName sets the identity tasks/whocan (22a) pins as "current user"
// (fixtures.go's NewDemo calls this so --demo has someone to pin).
func (c *Cluster) SetUserName(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.userName = name
}

// SetUserGroups sets the pinned current user's known group memberships —
// the fake counterpart of a client cert's Subject Organization fields
// (kube.Context.UserGroups), so --demo can also exercise a Group-only
// grant (the common cluster-admin/system:masters shape).
func (c *Cluster) SetUserGroups(groups []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.userGroups = groups
}

// --- WhoCan (22a) ---

// WhoCan resolves query against the fake cluster's seeded RBAC objects,
// mirroring kube.Cluster.WhoCan's shape over kube.ResolveWhoCan's shared,
// cluster-agnostic matching logic.
func (c *Cluster) WhoCan(ctx context.Context, query kube.WhoCanQuery) (kube.WhoCanResult, error) {
	crObjs, err := c.ListRaw(ctx, kube.KindClusterRole, "")
	if err != nil {
		return kube.WhoCanResult{}, err
	}
	rObjs, err := c.ListRaw(ctx, kube.KindRole, query.Namespace)
	if err != nil {
		return kube.WhoCanResult{}, err
	}
	crbObjs, err := c.ListRaw(ctx, kube.KindClusterRoleBinding, "")
	if err != nil {
		return kube.WhoCanResult{}, err
	}
	rbObjs, err := c.ListRaw(ctx, kube.KindRoleBinding, query.Namespace)
	if err != nil {
		return kube.WhoCanResult{}, err
	}

	clusterRoles := make([]*rbacv1.ClusterRole, 0, len(crObjs))
	for _, o := range crObjs {
		if cr, ok := o.(*rbacv1.ClusterRole); ok {
			clusterRoles = append(clusterRoles, cr)
		}
	}
	roles := make([]*rbacv1.Role, 0, len(rObjs))
	for _, o := range rObjs {
		if r, ok := o.(*rbacv1.Role); ok {
			roles = append(roles, r)
		}
	}
	clusterRoleBindings := make([]*rbacv1.ClusterRoleBinding, 0, len(crbObjs))
	for _, o := range crbObjs {
		if crb, ok := o.(*rbacv1.ClusterRoleBinding); ok {
			clusterRoleBindings = append(clusterRoleBindings, crb)
		}
	}
	roleBindings := make([]*rbacv1.RoleBinding, 0, len(rbObjs))
	for _, o := range rbObjs {
		if rb, ok := o.(*rbacv1.RoleBinding); ok {
			roleBindings = append(roleBindings, rb)
		}
	}

	c.mu.Lock()
	user, groups := c.userName, c.userGroups
	c.mu.Unlock()
	return kube.ResolveWhoCan(query, user, groups, clusterRoles, roles, clusterRoleBindings, roleBindings), nil
}

// --- connection health / events ---

func (c *Cluster) Events() <-chan kube.ResourceChangedMsg { return c.events }
func (c *Cluster) ConnEvents() <-chan kube.ConnStateMsg   { return c.connCh }

func (c *Cluster) ConnState() kube.ConnState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn
}

// RetryNow is a no-op for the fake — there's nothing to retry against.
func (c *Cluster) RetryNow() {}

// SetConnState lets tests drive 4a/4b/4c states through the fake.
func (c *Cluster) SetConnState(s kube.ConnState) {
	c.mu.Lock()
	c.conn = s
	c.mu.Unlock()
	select {
	case c.connCh <- kube.ConnStateMsg(s):
	default:
	}
}

func (c *Cluster) notify(kind kube.ResourceKind) {
	select {
	case c.events <- kube.ResourceChangedMsg{Kind: kind}:
	default:
	}
}
