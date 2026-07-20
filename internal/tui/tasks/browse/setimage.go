// 24a's 'i' inline set-image/set-tag editor (docs/design README.md §24a):
// reversible outside PROD, so — like scale.go's pendingScale — this is a
// bespoke gate (pendingSetImage) rather than actions.Controller's y/N/
// type-name flow, since there's a container/tag/history buffer to gather
// before there's anything to Begin. Once ↵ commits, execution itself does
// go through actions.Controller/kube.Mutator (verbs.TierForSetImage decides
// TierNone outside PROD vs. TierInline in PROD — the ordinary inline y/N
// Controller already renders for rollback/delete). Kept in its own file,
// browse's per-concern split convention (like scale.go/edit.go).
package browse

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// imageHistoryEntry is one row of 24a's TAG · SEEN · FROM table, sourced
// entirely from the watch cache (never a registry call).
type imageHistoryEntry struct {
	tag       string
	seenAt    time.Time // sort key only, never rendered directly
	seenLabel string    // the SEEN column's rendered text
	from      string    // the FROM column's rendered text
}

// setImageTarget is the state pendingSetImage gates on while 24a's panel is
// showing.
type setImageTarget struct {
	kind      kube.ResourceKind
	namespace string
	name      string
	// created is the workload's own CreationTimestamp — the fallback "seen"
	// clock for StatefulSet/DaemonSet, which have no ReplicaSet revision
	// layer to read a more precise one from.
	created      time.Time
	desiredCount int32 // "applying rolls out N pods" — this workload's own currentReplicas(row)
	containers   []kube.ContainerInfo
	containerIdx int
	// repo is the active container's image repo, the dim prefix in
	// non-fullRef mode.
	repo string
	// buffer is the type-ahead value: just the tag outside fullRef mode, the
	// whole repo:tag ref once ctrl-u unlocks it.
	buffer string
	// cursor is a rune index into buffer (0..len(runes(buffer))) — ←/→ move
	// it, backspace/typing act at it. Every wholesale buffer replacement
	// (container switch, ctrl-u toggle, a history pick) resets it to the
	// end, same as scale.go's prompt always leaving the cursor ready to
	// append/backspace.
	cursor     int
	fullRef    bool
	history    []imageHistoryEntry
	historyIdx int // -1 = nothing picked/matched
}

// setBuffer replaces t.buffer wholesale and parks the cursor at its end —
// the shared tail of every place buffer changes as a whole rather than by a
// single keystroke (selectSetImageContainer, ctrl-u, a history pick).
func (t *setImageTarget) setBuffer(s string) {
	t.buffer = s
	t.cursor = len([]rune(s))
}

// activeContainer is the container the panel is currently editing.
func (t setImageTarget) activeContainer() kube.ContainerInfo {
	return t.containers[t.containerIdx]
}

// composedImage is the full image ref the buffer currently represents.
func (t setImageTarget) composedImage() string {
	if t.fullRef {
		return t.buffer
	}
	return t.repo + ":" + t.buffer
}

// unchanged reports whether composedImage equals the active container's
// current image — §24a: "re-entering the current tag flips the strip to
// 'same image — apply is a no-op'".
func (t setImageTarget) unchanged() bool {
	return t.composedImage() == t.activeContainer().Image
}

// imageEditable reports whether kind takes 24a's set-image prompt —
// Deployment, StatefulSet, and DaemonSet, the three kinds with a pod
// template `kubectl set image` can target.
func imageEditable(kind kube.ResourceKind) bool {
	return kind == kube.KindDeployment || kind == kube.KindStatefulSet || kind == kube.KindDaemonSet
}

// beginSetImage opens 24a's panel for the selected row. ok is false when
// nothing applies (wrong kind, no mutator, not ready, no row selected, or
// the raw object/containers can't be resolved from the watch cache) —
// mirroring beginScale's ok-bool contract.
func (m *Model) beginSetImage() bool {
	if !imageEditable(m.kind) || m.mutator == nil || m.state != tui.TaskStateReady {
		return false
	}
	row, ok := m.selectedRow()
	if !ok {
		return false
	}
	obj, ok := workloadObject(m.lister, m.kind, row.Namespace, row.Name)
	if !ok {
		return false
	}
	containers := workloadContainerInfos(obj)
	if len(containers) == 0 {
		return false
	}
	acc, err := apimeta.Accessor(obj)
	created := time.Time{}
	if err == nil {
		created = acc.GetCreationTimestamp().Time
	}
	t := &setImageTarget{
		kind: m.kind, namespace: row.Namespace, name: row.Name,
		created: created, desiredCount: currentReplicas(row),
		containers: containers,
	}
	m.pendingSetImage = t
	m.selectSetImageContainer(0)
	return true
}

// selectSetImageContainer switches the panel's active container tab
// (beginSetImage's initial 0, or 'tab' cycling), recomputing repo/buffer/
// history for the newly active container.
func (m *Model) selectSetImageContainer(idx int) {
	t := m.pendingSetImage
	t.containerIdx = idx
	c := t.activeContainer()
	t.repo = imageRepo(c.Image)
	t.setBuffer(tagOf(c.Image))
	t.fullRef = false
	t.history = imageHistory(m.lister, t.kind, t.namespace, t.name, c.Name, c.Image, t.created)
	t.historyIdx = matchHistoryIndex(t)
}

// updateSetImageKey routes keys while pendingSetImage's panel is showing.
func (m *Model) updateSetImageKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	t := m.pendingSetImage
	switch msg.String() {
	case "esc":
		m.pendingSetImage = nil
	case "enter":
		if t.unchanged() {
			return m, nil
		}
		m.pendingSetImage = nil
		return m, m.commitSetImage(*t)
	case "tab":
		if len(t.containers) > 1 {
			m.selectSetImageContainer((t.containerIdx + 1) % len(t.containers))
		}
	case "ctrl+u":
		if t.fullRef {
			repo := imageRepo(t.buffer)
			tag := tagOf(t.buffer)
			t.repo = repo
			t.setBuffer(tag)
			t.fullRef = false
		} else {
			t.setBuffer(t.repo + ":" + t.buffer)
			t.fullRef = true
		}
		t.historyIdx = matchHistoryIndex(t)
	case "up":
		m.stepSetImageHistory(-1)
	case "down":
		m.stepSetImageHistory(1)
	case "left":
		t.cursor = max(t.cursor-1, 0)
	case "right":
		t.cursor = min(t.cursor+1, len([]rune(t.buffer)))
	case "backspace":
		if t.cursor > 0 {
			r := []rune(t.buffer)
			t.buffer = string(r[:t.cursor-1]) + string(r[t.cursor:])
			t.cursor--
			t.historyIdx = matchHistoryIndex(t)
		}
	default:
		if msg.Text != "" {
			r := []rune(t.buffer)
			ins := []rune(msg.Text)
			t.buffer = string(r[:t.cursor]) + string(ins) + string(r[t.cursor:])
			t.cursor += len(ins)
			t.historyIdx = matchHistoryIndex(t)
		}
	}
	return m, nil
}

// stepSetImageHistory moves historyIdx by delta (clamped) and overwrites
// buffer from the picked entry's tag — §24a's "↑↓ pick from history".
func (m *Model) stepSetImageHistory(delta int) {
	t := m.pendingSetImage
	if len(t.history) == 0 {
		return
	}
	idx := max(min(t.historyIdx+delta, len(t.history)-1), 0)
	t.historyIdx = idx
	tag := t.history[idx].tag
	if t.fullRef {
		t.setBuffer(t.repo + ":" + tag)
	} else {
		t.setBuffer(tag)
	}
}

// matchHistoryIndex finds the history entry (if any) whose tag equals t's
// current buffer — so a typed-exact-match highlights the same as an
// arrow-picked one (docs/design README.md §24a's mockup shows the prefilled
// tag already highlighting its own history row). In fullRef mode, a buffer
// that no longer names the same repo can't match any entry (history was
// built against the original repo).
func matchHistoryIndex(t *setImageTarget) int {
	tag := t.buffer
	if t.fullRef {
		if imageRepo(t.buffer) != t.repo {
			return -1
		}
		tag = tagOf(t.buffer)
	}
	for i, e := range t.history {
		if e.tag == tag {
			return i
		}
	}
	return -1
}

// commitSetImage executes t through actions.Controller — verbs.TierForSetImage
// resolves TierNone outside PROD (Begin runs it immediately, mirroring
// commitScale) or TierInline in PROD (Controller's ordinary inline y/N, the
// same path rollback/delete already take).
func (m *Model) commitSetImage(t setImageTarget) tea.Cmd {
	c := t.activeContainer()
	image := t.composedImage()
	return m.actions.Begin(verbs.TierForSetImage(m.isProd()), tui.TaskAction{
		ID:    "set-image-" + t.namespace + "/" + t.name,
		Label: fmt.Sprintf("Set image for %s?", t.name),
		Scope: tui.TaskScope{
			ResourceKind: string(t.kind),
			ResourceName: t.name,
			Namespace:    t.namespace,
			Verb:         "set-image",
			IsMutating:   true,
			Container:    c.Name,
			Image:        image,
		},
	})
}

// setImageWillRunLine renders the exact "will run: kubectl set image ..."
// line for a pending TierInline (PROD) confirmation's keybar RightNote —
// same idiom as deleteWillRunLine/rollbackPrompt, reading straight off the
// already-resolved actions.Controller Scope rather than pendingSetImage
// (which is nil by the time a PROD confirm is showing — commitSetImage
// clears it before Begin).
func setImageWillRunLine(scope tui.TaskScope) string {
	return "will run: " + kube.SetImageCommandString(kube.ResourceKind(scope.ResourceKind), scope.Namespace, scope.ResourceName, scope.Container, scope.Image)
}

// workloadObject finds the named raw object of kind in namespace via
// lister.ListRaw — the same cache-read lookup shape scale.go's hpaManaging
// already uses for the HPA-managed-workload lookup.
func workloadObject(lister resources.RawLister, kind kube.ResourceKind, namespace, name string) (runtime.Object, bool) {
	if lister == nil {
		return nil, false
	}
	objs, err := lister.ListRaw(context.Background(), kind, namespace)
	if err != nil {
		return nil, false
	}
	for _, obj := range objs {
		acc, err := apimeta.Accessor(obj)
		if err != nil {
			continue
		}
		if acc.GetName() == name && acc.GetNamespace() == namespace {
			return obj, true
		}
	}
	return nil, false
}

// workloadContainerInfos extracts obj's pod-template containers (name/image)
// plus native sidecars (initContainers with restartPolicy: Always, appended
// after the regular containers) — the same merge kube.buildContainerInfos
// does for a live Pod, minus the live-status merge this doesn't need.
func workloadContainerInfos(obj runtime.Object) []kube.ContainerInfo {
	var spec corev1.PodSpec
	switch o := obj.(type) {
	case *appsv1.Deployment:
		spec = o.Spec.Template.Spec
	case *appsv1.StatefulSet:
		spec = o.Spec.Template.Spec
	case *appsv1.DaemonSet:
		spec = o.Spec.Template.Spec
	default:
		return nil
	}
	infos := make([]kube.ContainerInfo, 0, len(spec.Containers)+len(spec.InitContainers))
	for _, c := range spec.Containers {
		infos = append(infos, kube.ContainerInfo{Name: c.Name, Image: c.Image})
	}
	for _, c := range spec.InitContainers {
		if c.RestartPolicy == nil || *c.RestartPolicy != corev1.ContainerRestartPolicyAlways {
			continue // a regular (non-sidecar) init container
		}
		infos = append(infos, kube.ContainerInfo{Name: c.Name, Image: c.Image, IsSidecar: true})
	}
	return infos
}

// imageHistory combines this workload's own revision history with
// cross-workload sightings of the same image repo, newest-seen-first,
// deduped by tag (the most recent sighting of a tag wins regardless of
// source), capped to a reasonable panel-scrollable count.
func imageHistory(lister resources.RawLister, kind kube.ResourceKind, namespace, name, container, currentImage string, created time.Time) []imageHistoryEntry {
	const maxEntries = 8
	repo, currentTag := imageRepo(currentImage), tagOf(currentImage)

	entries := ownRevisionHistory(lister, kind, namespace, name, container, currentImage, created)
	entries = append(entries, crossWorkloadHistory(lister, kind, namespace, name, repo, currentTag)...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].seenAt.After(entries[j].seenAt) })

	seen := make(map[string]bool, len(entries))
	out := make([]imageHistoryEntry, 0, min(len(entries), maxEntries))
	for _, e := range entries {
		if seen[e.tag] {
			continue
		}
		seen[e.tag] = true
		out = append(out, e)
		if len(out) >= maxEntries {
			break
		}
	}
	return out
}

// revisionCandidate is one raw (revision number, image, created-at) sample
// gathered from either a Deployment's owned ReplicaSets or a StatefulSet/
// DaemonSet's owned ControllerRevisions, before labelRevisions turns them
// into imageHistoryEntry rows.
type revisionCandidate struct {
	n       int64
	created time.Time
	image   string
}

// ownRevisionHistory reads this workload's own rollout history: a
// Deployment's owned ReplicaSets' "deployment.kubernetes.io/revision"
// annotation (the same signal kube/timeline.go's TimelineFromRollouts reads
// for 16b's rail), or — since StatefulSet/DaemonSet own no ReplicaSets —
// its owned ControllerRevisions (apps/v1), the same mechanism `kubectl
// rollout history statefulset|daemonset` itself reads. Falls back to a
// single "current" row (built from the workload's own creation time) when
// no revision object has been seen yet — a fresh object, or the informer
// cache still catching up.
func ownRevisionHistory(lister resources.RawLister, kind kube.ResourceKind, namespace, name, container, currentImage string, created time.Time) []imageHistoryEntry {
	fallback := []imageHistoryEntry{{
		tag:       tagOf(currentImage),
		seenAt:    created,
		seenLabel: shortAge(time.Since(created)) + " · current",
		from:      "this " + strings.ToLower(string(kind)),
	}}
	if lister == nil {
		return fallback
	}
	var revs []revisionCandidate
	switch kind {
	case kube.KindDeployment:
		revs = deploymentRevisions(lister, namespace, name, container)
	case kube.KindStatefulSet, kube.KindDaemonSet:
		revs = controllerRevisions(lister, kind, namespace, name, container)
	default:
		return fallback
	}
	if len(revs) == 0 {
		return fallback
	}
	return labelRevisions(revs, kind)
}

// deploymentRevisions gathers revisionCandidates from a Deployment's owned
// ReplicaSets — the source ownRevisionHistory's doc comment describes.
func deploymentRevisions(lister resources.RawLister, namespace, name, container string) []revisionCandidate {
	objs, err := lister.ListRaw(context.Background(), kube.KindReplicaSet, namespace)
	if err != nil {
		return nil
	}
	var revs []revisionCandidate
	for _, obj := range objs {
		rs, ok := obj.(*appsv1.ReplicaSet)
		if !ok || len(rs.OwnerReferences) == 0 || rs.OwnerReferences[0].Kind != "Deployment" || rs.OwnerReferences[0].Name != name {
			continue
		}
		revText, ok := rs.Annotations["deployment.kubernetes.io/revision"]
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(revText, 10, 64)
		if err != nil {
			continue
		}
		image := containerImageByName(rs.Spec.Template.Spec.Containers, container)
		if image == "" {
			continue
		}
		revs = append(revs, revisionCandidate{n: n, created: rs.CreationTimestamp.Time, image: image})
	}
	return revs
}

// controllerRevisions gathers revisionCandidates from a StatefulSet/
// DaemonSet's owned ControllerRevisions. Data.Raw is a JSON encoding of
// {"spec":{"template":{"spec":{"containers":[...]}}}} — the patch shape the
// StatefulSet/DaemonSet controllers themselves generate for each revision
// and `kubectl rollout history` decodes the same way.
func controllerRevisions(lister resources.RawLister, kind kube.ResourceKind, namespace, name, container string) []revisionCandidate {
	objs, err := lister.ListRaw(context.Background(), kube.KindControllerRevision, namespace)
	if err != nil {
		return nil
	}
	var revs []revisionCandidate
	for _, obj := range objs {
		cr, ok := obj.(*appsv1.ControllerRevision)
		if !ok || len(cr.OwnerReferences) == 0 || cr.OwnerReferences[0].Kind != string(kind) || cr.OwnerReferences[0].Name != name {
			continue
		}
		image := controllerRevisionContainerImage(cr, container)
		if image == "" {
			continue
		}
		revs = append(revs, revisionCandidate{n: cr.Revision, created: cr.CreationTimestamp.Time, image: image})
	}
	return revs
}

// controllerRevisionContainerImage decodes cr.Data.Raw's patch just enough
// to pull out container's image — the fields ownRevisionHistory's doc
// comment names, ignoring everything else the patch carries.
func controllerRevisionContainerImage(cr *appsv1.ControllerRevision, container string) string {
	var patch struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []corev1.Container `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(cr.Data.Raw, &patch); err != nil {
		return ""
	}
	return containerImageByName(patch.Spec.Template.Spec.Containers, container)
}

// labelRevisions sorts revs newest-revision-first and labels the top one
// "current" (this workload's live revision) and the rest "rollback
// target" — shared by Deployment's ReplicaSet-sourced revisions and
// StatefulSet/DaemonSet's ControllerRevision-sourced ones so both label
// identically.
func labelRevisions(revs []revisionCandidate, kind kube.ResourceKind) []imageHistoryEntry {
	sort.Slice(revs, func(i, j int) bool { return revs[i].n > revs[j].n })
	out := make([]imageHistoryEntry, 0, len(revs))
	for i, r := range revs {
		e := imageHistoryEntry{tag: tagOf(r.image), seenAt: r.created}
		if i == 0 {
			e.seenLabel = shortAge(time.Since(r.created)) + " · current"
			e.from = fmt.Sprintf("rev %d · this %s", r.n, strings.ToLower(string(kind)))
		} else {
			e.seenLabel = fmt.Sprintf("%s · rev %d", shortAge(time.Since(r.created)), r.n)
			e.from = "rollout history · rollback target"
		}
		out = append(out, e)
	}
	return out
}

// crossWorkloadHistory scans every Deployment/StatefulSet/DaemonSet cluster-
// wide (docs/design README.md §24a: "the same image tag seen on other
// workloads/namespaces ... the 'promote what prod runs' case") for a
// container whose image shares repo but carries a different tag than
// currentTag. seenAt is the sighted workload's own CreationTimestamp — a
// best-effort "seen" clock (the precise per-revision timestamp
// ownRevisionHistory resolves for the workload actually being edited would
// need one extra ReplicaSet lookup per sighted Deployment; this stays a
// single pass per kind).
func crossWorkloadHistory(lister resources.RawLister, kind kube.ResourceKind, namespace, name, repo, currentTag string) []imageHistoryEntry {
	if lister == nil {
		return nil
	}
	var out []imageHistoryEntry
	for _, k := range []kube.ResourceKind{kube.KindDeployment, kube.KindStatefulSet, kube.KindDaemonSet} {
		objs, err := lister.ListRaw(context.Background(), k, "")
		if err != nil {
			continue
		}
		for _, obj := range objs {
			acc, err := apimeta.Accessor(obj)
			if err != nil {
				continue
			}
			if k == kind && acc.GetNamespace() == namespace && acc.GetName() == name {
				continue // the workload being edited itself
			}
			seenAt := acc.GetCreationTimestamp().Time
			for _, c := range workloadContainerInfos(obj) {
				if imageRepo(c.Image) != repo {
					continue
				}
				tag := tagOf(c.Image)
				if tag == currentTag {
					continue
				}
				out = append(out, imageHistoryEntry{
					tag:       tag,
					seenAt:    seenAt,
					seenLabel: shortAge(time.Since(seenAt)) + " ago",
					from:      fmt.Sprintf("%s/%s · %s", workloadArg(k), acc.GetName(), acc.GetNamespace()),
				})
			}
		}
	}
	return out
}

func containerImageByName(containers []corev1.Container, name string) string {
	for _, c := range containers {
		if c.Name == name {
			return c.Image
		}
	}
	return ""
}

// workloadArg renders kind as kubectl's short resource arg, the same
// deploy/sts/ds vocabulary kube.SetImageCommandString's "will run" line
// uses — duplicated here (browse can't import kube's unexported
// workloadResourceArg) per the repo's package-local-seam convention
// (execCmd/editCmd already duplicate across task packages the same way).
func workloadArg(kind kube.ResourceKind) string {
	switch kind {
	case kube.KindStatefulSet:
		return "sts"
	case kube.KindDaemonSet:
		return "ds"
	default:
		return "deploy"
	}
}

// imageRepo splits image into its repo (everything before the tag
// separator). A colon before the last "/" is a registry port
// (registry:5000/repo), not a tag separator, so it's only treated as one
// when nothing after it contains a "/".
func imageRepo(image string) string {
	i := strings.LastIndex(image, ":")
	if i < 0 || strings.Contains(image[i+1:], "/") {
		return image
	}
	return image[:i]
}

// tagOf splits image into its tag, defaulting to "latest" — Docker's own
// implicit default — when the ref carries no explicit tag.
func tagOf(image string) string {
	repo := imageRepo(image)
	if len(repo) == len(image) {
		return "latest"
	}
	return image[len(repo)+1:]
}

// shortAge renders a duration as a compact "12m"/"3h"/"5d" string — the same
// bucketing resources.shortAge uses (unexported there), duplicated here per
// the repo's package-local-seam convention.
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
