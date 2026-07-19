package resources

import (
	"context"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

// metaOf pulls the common ObjectMeta fields off any API object.
func metaOf(obj runtime.Object) (namespace, name string, age time.Duration) {
	m, err := apimeta.Accessor(obj)
	if err != nil {
		return "", "", 0
	}
	ts := m.GetCreationTimestamp().Time
	if ts.IsZero() {
		return m.GetNamespace(), m.GetName(), 0
	}
	return m.GetNamespace(), m.GetName(), time.Since(ts)
}

// shortAge renders a duration as a compact "12m"/"3h"/"5d" string.
func shortAge(d time.Duration) string {
	switch {
	case d <= 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// metaRow is the fallback projection: name + age only. Kinds override it with
// richer cells; it also guards against an unexpected object type.
func metaRow(obj runtime.Object) Row {
	ns, name, age := metaOf(obj)
	return Row{Namespace: ns, Name: name, Cells: []string{name, shortAge(age)}, Status: StatusNeutral}
}

func int32ptr(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

func readyRatio(ready, want int32) (string, StatusClass) {
	label := fmt.Sprintf("%d/%d", ready, want)
	switch {
	case want == 0:
		return label, StatusNeutral
	case ready >= want:
		return label, StatusOK
	case ready == 0:
		return label, StatusFail
	default:
		return label, StatusWarn
	}
}

// podWaitingReason returns the first non-empty Waiting reason across a pod's
// container statuses (e.g. "CrashLoopBackOff", "ImagePullBackOff"), for
// distinguishing a crashlooping pod from one that's merely still starting —
// both otherwise look identical (Running phase, not-yet-ready containers).
func podWaitingReason(statuses []corev1.ContainerStatus) string {
	for _, cs := range statuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			return cs.State.Waiting.Reason
		}
	}
	return ""
}

func projectPod(obj runtime.Object) Row {
	p, ok := obj.(*corev1.Pod)
	if !ok {
		return metaRow(obj)
	}
	ns, name, age := metaOf(obj)
	var ready, restarts int32
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Ready {
			ready++
		}
		restarts += cs.RestartCount
	}
	total := int32(len(p.Spec.Containers))
	readyLabel := fmt.Sprintf("%d/%d", ready, total)

	// status is the STATUS cell text — the mockup (docs/design §2a) shows the
	// failure reason ("CrashLoopBackOff") and kubectl's "Completed" wording,
	// not the raw phase.
	status := string(p.Status.Phase)
	var glyph string
	var class StatusClass
	switch {
	case p.Status.Phase == corev1.PodSucceeded:
		glyph, class, status = "○", StatusNeutral, "Completed"
	case p.Status.Phase == corev1.PodFailed:
		glyph, class = "✕", StatusFail
	case p.Status.Phase == corev1.PodPending:
		glyph, class = "◐", StatusWarn
	case strings.Contains(podWaitingReason(p.Status.ContainerStatuses), "CrashLoop"):
		glyph, class, status = "✕", StatusFail, podWaitingReason(p.Status.ContainerStatuses)
	case total > 0 && ready >= total:
		glyph, class = "●", StatusOK
	default:
		glyph, class = "◐", StatusWarn
	}

	node := p.Spec.NodeName
	if node == "" {
		node = "–"
	}
	// CPU/MEM are placeholders: live usage isn't on the object, browse fills
	// them in from a separate metrics poll (resources.Cells' metrics param).
	return Row{
		Namespace:  ns,
		Name:       name,
		Cells:      []string{name, readyLabel, status, fmt.Sprintf("%d", restarts), "–", "–", node, shortAge(age)},
		Status:     class,
		Glyph:      glyph,
		GlyphClass: class,
	}
}

// projectDeployment returns 9a's Deployment projection. lister is nil-safe
// (pre-connect fallback: IMAGE never shows the "new ← old" transition, same
// as projectIngress's own nil-lister fallback) — BuildDiscoveredRegistry
// swaps in a live one once a cluster is reachable, the same pattern
// ingressDesc.Project already establishes.
func projectDeployment(lister RawLister) func(obj runtime.Object) Row {
	return func(obj runtime.Object) Row {
		d, ok := obj.(*appsv1.Deployment)
		if !ok {
			return metaRow(obj)
		}
		ns, name, age := metaOf(obj)
		readyLabel, readyStatus := readyRatio(d.Status.ReadyReplicas, int32ptr(d.Spec.Replicas))
		rolloutText, rolloutStatus := deploymentRollout(d)
		status := rolloutStatus
		if readyStatus == StatusFail {
			// Zero ready replicas is an outage even if the rollout's own
			// conditions haven't caught up to say so yet (docs/design README.md
			// §9a doesn't cover this edge case explicitly; treated the same way
			// projectPod treats a still-scheduling pod as worse than "progressing").
			status = StatusFail
		}
		return Row{
			Namespace: ns,
			Name:      name,
			Cells:     []string{name, readyLabel, rolloutText, deploymentImage(lister, d, rolloutStatus), shortAge(age)},
			Status:    status,
		}
	}
}

// deploymentRollout derives 9a's ROLLOUT cell (docs/design README.md §9a:
// "stable dim · 2m 14s ▸ yellow while progressing · degraded red") from the
// deployment's generation/observedGeneration and its Progressing condition —
// the same signals `kubectl rollout status` uses, simplified for a
// single-line summary rather than a live poll loop.
func deploymentRollout(d *appsv1.Deployment) (string, StatusClass) {
	if d.Generation > d.Status.ObservedGeneration {
		return "progressing " + rolloutArrow, StatusWarn
	}

	var progressing *appsv1.DeploymentCondition
	for i := range d.Status.Conditions {
		if d.Status.Conditions[i].Type == appsv1.DeploymentProgressing {
			progressing = &d.Status.Conditions[i]
		}
	}
	if progressing != nil && (progressing.Status == corev1.ConditionFalse || progressing.Reason == "ProgressDeadlineExceeded") {
		return "degraded", StatusFail
	}

	want := int32ptr(d.Spec.Replicas)
	stillRolling := d.Status.UpdatedReplicas < want ||
		d.Status.Replicas > d.Status.UpdatedReplicas ||
		(want > 0 && d.Status.AvailableReplicas < want)
	if stillRolling {
		text := "progressing " + rolloutArrow
		if progressing != nil && !progressing.LastUpdateTime.IsZero() {
			text = shortAge(time.Since(progressing.LastUpdateTime.Time)) + " " + text
		}
		return text, StatusWarn
	}
	return "stable", StatusOK
}

// rolloutArrow is the 9a ROLLOUT cell's "still going" marker (docs/design
// README.md §9a: "2m 14s ▸ yellow while progressing").
const rolloutArrow = "▸"

// deploymentImage is the ROLLOUT column's IMAGE cell: the pod template's
// first container image, "+N" appended for additional containers, with the
// previous ReplicaSet's image appended as "new ← old" while the rollout is
// mid-transition (docs/design README.md §9a) — rolloutStatus gates the
// extra ReplicaSet lookup to only the "progressing" case, since a
// stable/degraded Deployment has no "old" side to show.
func deploymentImage(lister RawLister, d *appsv1.Deployment, rolloutStatus StatusClass) string {
	containers := d.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		return "–"
	}
	img := containers[0].Image
	if len(containers) > 1 {
		img += fmt.Sprintf(" +%d", len(containers)-1)
	}
	if rolloutStatus == StatusWarn {
		if old := previousReplicaSetImage(lister, d, containers[0].Image); old != "" {
			img += " ← " + old
		}
	}
	return img
}

// previousReplicaSetImage finds a ReplicaSet owned by d that still has live
// pods (Status.Replicas > 0) but whose own template image differs from the
// Deployment's current one — the "old" side of a mid-rollout "new ← old"
// transition. Returns "" when there's no such ReplicaSet (lister is nil, no
// old ReplicaSet is still scaling down, or the lookup fails) rather than
// guessing.
func previousReplicaSetImage(lister RawLister, d *appsv1.Deployment, currentImage string) string {
	if lister == nil {
		return ""
	}
	objs, err := lister.ListRaw(context.Background(), kube.KindReplicaSet, d.Namespace)
	if err != nil {
		return ""
	}
	for _, obj := range objs {
		rs, ok := obj.(*appsv1.ReplicaSet)
		if !ok || rs.Status.Replicas == 0 || !ownedByDeployment(rs.OwnerReferences, d.Name) {
			continue
		}
		containers := rs.Spec.Template.Spec.Containers
		if len(containers) == 0 || containers[0].Image == currentImage {
			continue
		}
		return containers[0].Image
	}
	return ""
}

// ownedByDeployment reports whether refs names a Deployment owner named
// name — the ReplicaSet→Deployment link every rollout walks.
func ownedByDeployment(refs []metav1.OwnerReference, name string) bool {
	for _, ref := range refs {
		if ref.Kind == "Deployment" && ref.Name == name {
			return true
		}
	}
	return false
}

func projectDaemonSet(obj runtime.Object) Row {
	d, ok := obj.(*appsv1.DaemonSet)
	if !ok {
		return metaRow(obj)
	}
	ns, name, age := metaOf(obj)
	readyLabel, status := readyRatio(d.Status.NumberReady, d.Status.DesiredNumberScheduled)
	return Row{
		Namespace: ns,
		Name:      name,
		Cells:     []string{name, readyLabel, fmt.Sprintf("%d", d.Status.NumberAvailable), shortAge(age)},
		Status:    status,
	}
}

func projectStatefulSet(obj runtime.Object) Row {
	s, ok := obj.(*appsv1.StatefulSet)
	if !ok {
		return metaRow(obj)
	}
	ns, name, age := metaOf(obj)
	readyLabel, status := readyRatio(s.Status.ReadyReplicas, int32ptr(s.Spec.Replicas))
	return Row{Namespace: ns, Name: name, Cells: []string{name, readyLabel, shortAge(age)}, Status: status}
}

func projectReplicaSet(obj runtime.Object) Row {
	r, ok := obj.(*appsv1.ReplicaSet)
	if !ok {
		return metaRow(obj)
	}
	ns, name, age := metaOf(obj)
	readyLabel, status := readyRatio(r.Status.ReadyReplicas, int32ptr(r.Spec.Replicas))
	return Row{Namespace: ns, Name: name, Cells: []string{name, readyLabel, fmt.Sprintf("%d", r.Status.Replicas), shortAge(age)}, Status: status}
}

func projectJob(obj runtime.Object) Row {
	j, ok := obj.(*batchv1.Job)
	if !ok {
		return metaRow(obj)
	}
	ns, name, age := metaOf(obj)
	want := int32ptr(j.Spec.Completions)
	completions := fmt.Sprintf("%d/%d", j.Status.Succeeded, want)
	status := StatusWarn
	if want > 0 && j.Status.Succeeded >= want {
		status = StatusOK
	}
	if j.Status.Failed > 0 {
		status = StatusFail
	}
	return Row{Namespace: ns, Name: name, Cells: []string{name, completions, fmt.Sprintf("%d", j.Status.Active), shortAge(age)}, Status: status}
}

func projectCronJob(obj runtime.Object) Row {
	c, ok := obj.(*batchv1.CronJob)
	if !ok {
		return metaRow(obj)
	}
	ns, name, age := metaOf(obj)
	suspended := "False"
	status := StatusOK
	if c.Spec.Suspend != nil && *c.Spec.Suspend {
		suspended = "True"
		status = StatusNeutral
	}
	return Row{Namespace: ns, Name: name, Cells: []string{name, c.Spec.Schedule, suspended, fmt.Sprintf("%d", len(c.Status.Active)), shortAge(age)}, Status: status}
}

func projectService(obj runtime.Object) Row {
	s, ok := obj.(*corev1.Service)
	if !ok {
		return metaRow(obj)
	}
	ns, name, age := metaOf(obj)
	ports := make([]string, 0, len(s.Spec.Ports))
	for _, p := range s.Spec.Ports {
		ports = append(ports, fmt.Sprintf("%d", p.Port))
	}
	return Row{Namespace: ns, Name: name, Cells: []string{name, string(s.Spec.Type), s.Spec.ClusterIP, strings.Join(ports, ","), shortAge(age)}, Status: StatusOK}
}

// projectIngress renders the docs/design README.md §23a list columns:
// NAME/CLASS/HOSTS/TLS/BACKENDS/AGE — HOSTS falls back to "*" (matches
// everything) when no rule names a host, TLS is "●" once any Spec.TLS block
// is configured, BACKENDS summarizes ResolveServiceBackend across every
// rule's Service backend ("3 ok · 1 broken", "–" only when the Ingress has
// no rule backends at all to resolve). lister is the closure's only
// live-cluster seam (the same "Project as a closure over live state" shape
// projectCRD's counter param already established) — nil (DefaultRegistry's
// pre-connect fallback, never exercised against real rows since nothing can
// be listed without a lister in the first place) makes every backend
// resolve "not found" rather than lying about health.
func projectIngress(lister RawLister) func(obj runtime.Object) Row {
	return func(obj runtime.Object) Row {
		ing, ok := obj.(*networkingv1.Ingress)
		if !ok {
			return metaRow(obj)
		}
		ns, name, age := metaOf(obj)
		class := "-"
		if ing.Spec.IngressClassName != nil && *ing.Spec.IngressClassName != "" {
			class = *ing.Spec.IngressClassName
		}
		var hosts []string
		for _, r := range ing.Spec.Rules {
			if r.Host != "" {
				hosts = append(hosts, r.Host)
			}
		}
		hostText := "*"
		if len(hosts) > 0 {
			hostText = strings.Join(hosts, ",")
		}
		tls := "–"
		if len(ing.Spec.TLS) > 0 {
			tls = "●"
		}

		backendsText, status, glyph := ingressBackendsCell(lister, ns, ing)
		return Row{
			Namespace: ns, Name: name,
			Cells:      []string{name, class, hostText, tls, backendsText, shortAge(age)},
			Status:     status,
			Glyph:      glyph,
			GlyphClass: status,
		}
	}
}

// ingressBackendsCell resolves every rule/path Service backend via
// ResolveServiceBackend and tallies ok vs. broken (not-found or 0-ready),
// unhealthy-first (docs/design README.md §23a: "Strip counts unhealthy-
// first") — any broken backend fails the whole row.
func ingressBackendsCell(lister RawLister, namespace string, ing *networkingv1.Ingress) (text string, status StatusClass, glyph string) {
	ctx := context.Background()
	var ok, broken int
	for _, r := range ing.Spec.Rules {
		if r.HTTP == nil {
			continue
		}
		for _, p := range r.HTTP.Paths {
			if p.Backend.Service == nil {
				continue
			}
			state := ResolveServiceBackend(ctx, lister, namespace, p.Backend.Service.Name)
			if g, _ := state.Glyph(); g == "●" {
				ok++
			} else {
				broken++
			}
		}
	}
	switch {
	case ok+broken == 0:
		return "–", StatusNeutral, "·"
	case broken == 0:
		return fmt.Sprintf("%d ok", ok), StatusOK, "●"
	default:
		return fmt.Sprintf("%d ok · %d broken", ok, broken), StatusFail, "✕"
	}
}

func projectConfigMap(obj runtime.Object) Row {
	c, ok := obj.(*corev1.ConfigMap)
	if !ok {
		return metaRow(obj)
	}
	ns, name, age := metaOf(obj)
	return Row{Namespace: ns, Name: name, Cells: []string{name, fmt.Sprintf("%d", len(c.Data)+len(c.BinaryData)), shortAge(age)}, Status: StatusNeutral}
}

func projectSecret(obj runtime.Object) Row {
	s, ok := obj.(*corev1.Secret)
	if !ok {
		return metaRow(obj)
	}
	ns, name, age := metaOf(obj)
	return Row{Namespace: ns, Name: name, Cells: []string{name, string(s.Type), fmt.Sprintf("%d", len(s.Data)), shortAge(age)}, Status: StatusNeutral}
}

func projectPVC(obj runtime.Object) Row {
	c, ok := obj.(*corev1.PersistentVolumeClaim)
	if !ok {
		return metaRow(obj)
	}
	ns, name, age := metaOf(obj)
	capacity := "-"
	if q, found := c.Status.Capacity[corev1.ResourceStorage]; found {
		capacity = q.String()
	}
	status := StatusWarn
	if c.Status.Phase == corev1.ClaimBound {
		status = StatusOK
	}
	return Row{Namespace: ns, Name: name, Cells: []string{name, string(c.Status.Phase), capacity, shortAge(age)}, Status: status}
}

// nodeRoleTag returns the node's control-plane/worker role from its
// node-role.kubernetes.io/<role> label, if any — 11a's "NAME (role tag)"
// (docs/design README.md §11a). Empty when the node carries no role label
// (a plain worker node).
func nodeRoleTag(labels map[string]string) string {
	const prefix = "node-role.kubernetes.io/"
	for k := range labels {
		if role, ok := strings.CutPrefix(k, prefix); ok && role != "" {
			return role
		}
	}
	return ""
}

// nodePressureConditions are checked in this order — the first one True
// wins the STATUS cell (11a: "MemPressure yellow"), since a node can in
// principle report more than one simultaneously.
var nodePressureConditions = []struct {
	typ   corev1.NodeConditionType
	label string
}{
	{corev1.NodeMemoryPressure, "MemPressure"},
	{corev1.NodeDiskPressure, "DiskPressure"},
	{corev1.NodePIDPressure, "PIDPressure"},
}

func projectNode(obj runtime.Object) Row {
	n, ok := obj.(*corev1.Node)
	if !ok {
		return metaRow(obj)
	}
	_, name, age := metaOf(obj)

	ready := false
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
			ready = true
		}
	}

	// STATUS/glyph/class per docs/design README.md §11a: cordoned (blue ◈)
	// takes priority over a pressure/ready reading — an operator cordoned it
	// on purpose — then NotReady (fail), then pressure (warn), else Ready.
	cordoned := n.Spec.Unschedulable
	statusText, class, glyph := "Ready", StatusOK, "●"
	switch {
	case cordoned:
		statusText, class, glyph = "cordoned", StatusNeutral, "◈"
	case !ready:
		statusText, class, glyph = "NotReady", StatusFail, "✕"
	default:
		for _, cond := range nodePressureConditions {
			for _, c := range n.Status.Conditions {
				if c.Type == cond.typ && c.Status == corev1.ConditionTrue {
					statusText, class, glyph = cond.label, StatusWarn, "◐"
				}
			}
		}
	}

	nameSuffix := ""
	if role := nodeRoleTag(n.Labels); role != "" {
		nameSuffix = " (" + role + ")"
	}

	// PODS/CPU/MEM are placeholders: live pod counts and usage aren't on the
	// object alone (PODS needs a cluster-wide pod scan, CPU/MEM a metrics
	// poll) — browse fills them in from its own node-scoped maps, the same
	// pattern Pod's CPU/MEM placeholders use (resources.Cells' metrics
	// param).
	return Row{
		Name:       name,
		Cells:      []string{name, statusText, "–", "–", "–", n.Status.NodeInfo.KubeletVersion, shortAge(age)},
		Status:     class,
		Glyph:      glyph,
		GlyphClass: class,
		Cordoned:   cordoned,
		NameSuffix: nameSuffix,
	}
}

func projectNamespace(obj runtime.Object) Row {
	n, ok := obj.(*corev1.Namespace)
	if !ok {
		return metaRow(obj)
	}
	_, name, age := metaOf(obj)
	status := StatusOK
	if n.Status.Phase == corev1.NamespaceTerminating {
		status = StatusWarn
	}
	return Row{Name: name, Cells: []string{name, string(n.Status.Phase), shortAge(age)}, Status: status}
}

// projectForward turns one kube.ForwardObject (ForwardManager.ListRaw's
// wrapper) into 13c's row: LOCAL/TARGET/NAMESPACE/UPTIME/TRAFFIC cells. Name
// is a fuzzy-searchable "port→target" label (docs/design README.md §13c:
// "individual forwards fuzzy-match... by port and target name"); Key
// carries the real session ID the stop/restart verbs need.
func projectForward(obj runtime.Object) Row {
	fo, ok := obj.(*kube.ForwardObject)
	if !ok {
		return metaRow(obj)
	}
	s := fo.Session
	status, glyph := StatusOK, "●"
	if s.State == kube.ForwardReconnecting {
		status, glyph = StatusWarn, "◌"
	}
	return Row{
		Name:      fmt.Sprintf("%d→%s", s.LocalPort, s.Target.Name),
		Namespace: s.Target.Namespace,
		Key:       s.ID,
		Cells: []string{
			fmt.Sprintf("localhost:%d", s.LocalPort),
			forwardTargetCell(s),
			s.Target.Namespace,
			shortAge(time.Since(s.StartedAt)),
			forwardTrafficCell(s),
		},
		Status:     status,
		Glyph:      glyph,
		GlyphClass: status,
	}
}

// forwardTargetCell renders TARGET: "kind/name:port", plus the resolved
// backing pod for Service/Deployment targets, plus the verbatim reconnect
// error while State is Reconnecting.
func forwardTargetCell(s kube.ForwardSession) string {
	text := fmt.Sprintf("%s/%s:%d", strings.ToLower(string(s.Target.Kind)), s.Target.Name, s.RemotePort)
	if s.Target.Kind != kube.KindPod && s.ResolvedPod != "" {
		text += " → pod/" + s.ResolvedPod
	}
	if s.State == kube.ForwardReconnecting && s.Err != "" {
		text += " · " + s.Err
	}
	return text
}

// forwardTrafficCell renders TRAFFIC: the reconnect countdown while
// Reconnecting, else a recency-of-use signal (docs/design README.md §13c:
// "makes stale forwards safe to kill") — "active" within the last few
// seconds of a proxied connection, else "idle <age>". client-go's
// portforward package exposes no byte-rate counter without forking it, so
// this substitutes connection recency for the mockup's literal "41 KB/s".
func forwardTrafficCell(s kube.ForwardSession) string {
	if s.State == kube.ForwardReconnecting {
		wait := max(time.Until(s.NextRetryAt), 0)
		return fmt.Sprintf("retry %d · next in %ds", s.Attempt, int(wait.Round(time.Second).Seconds()))
	}
	idle := time.Since(s.LastActivityAt)
	if idle < 5*time.Second {
		return "active"
	}
	return "idle " + shortAge(idle)
}

// helmReleaseStatusClass buckets kube.HelmRelease.Status into the shared
// OK/Warn/Fail/Neutral health classification — docs/design README.md §18a's
// strip example ("3 deployed · 1 pending-upgrade · 1 failed").
func helmReleaseStatusClass(status string) StatusClass {
	switch {
	case status == "deployed":
		return StatusOK
	case strings.HasPrefix(status, "pending-"):
		return StatusWarn
	case status == "failed":
		return StatusFail
	default:
		return StatusNeutral
	}
}

// projectHelmRelease renders 18a's list columns: RELEASE/CHART/APP VER/REV/
// STATUS/UPDATED — STATUS carries the failure reason verbatim for a failed
// release (kube.HelmRelease.StatusCell).
func projectHelmRelease(obj runtime.Object) Row {
	ho, ok := obj.(*kube.HelmReleaseObject)
	if !ok {
		return metaRow(obj)
	}
	r := ho.Release
	updated := "–"
	if !r.Updated.IsZero() {
		updated = shortAge(time.Since(r.Updated)) + " ago"
	}
	return Row{
		Namespace: r.Namespace,
		Name:      r.Name,
		Cells: []string{
			r.Name, r.Chart + " " + r.ChartVersion, r.AppVersion,
			fmt.Sprintf("%d", r.Revision), r.StatusCell(), updated,
		},
		Status: helmReleaseStatusClass(r.Status),
	}
}

func projectEvent(obj runtime.Object) Row {
	e, ok := obj.(*corev1.Event)
	if !ok {
		return metaRow(obj)
	}
	ns, name, age := metaOf(obj)
	status := StatusNeutral
	if e.Type == "Warning" {
		status = StatusWarn
	}
	object := strings.TrimSpace(e.InvolvedObject.Kind + "/" + e.InvolvedObject.Name)
	return Row{Namespace: ns, Name: name, Cells: []string{e.Type, e.Reason, object, shortAge(age)}, Status: status}
}
