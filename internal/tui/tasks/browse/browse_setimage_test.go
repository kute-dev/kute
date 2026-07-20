package browse

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/config"
	"github.com/kute-dev/kute/internal/kube"
)

func setImageAge(d time.Duration) metav1.Time { return metav1.NewTime(time.Now().Add(-d)) }

func replicasPtr(n int32) *int32 { return &n }

// twoContainerDeployment is "aim-worker": worker (the container under edit)
// plus a sidecar, so tab-cycling has something real to exercise.
func twoContainerDeployment(ns, name, workerImage string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Generation: 1, CreationTimestamp: setImageAge(30 * 24 * time.Hour)},
		Spec: appsv1.DeploymentSpec{Replicas: replicasPtr(4), Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Containers: []corev1.Container{
				{Name: "worker", Image: workerImage},
				{Name: "metrics-sidecar", Image: "sidecar:0.9.1"},
			}},
		}},
		Status: appsv1.DeploymentStatus{Replicas: 4, ReadyReplicas: 4, UpdatedReplicas: 4, AvailableReplicas: 4, ObservedGeneration: 1},
	}
}

func replicaSetRevision(ns, name, deployment, image string, revision int, created time.Duration) *appsv1.ReplicaSet {
	return &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: ns, CreationTimestamp: setImageAge(created),
			OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: deployment}},
			Annotations:     map[string]string{"deployment.kubernetes.io/revision": strconv.Itoa(revision)},
		},
		Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "worker", Image: image}},
		}}},
	}
}

// controllerRevisionFixture builds a StatefulSet/DaemonSet ControllerRevision
// — the apps/v1 rollout-history mechanism those two controllers use in place
// of a Deployment's owned ReplicaSets. Data.Raw mirrors the real patch shape
// (controllerRevisionContainerImage's doc comment) with one container.
func controllerRevisionFixture(ns, name, ownerKind, owner, container, image string, revision int64, created time.Duration) *appsv1.ControllerRevision {
	data, _ := json.Marshal(map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"containers": []map[string]any{{"name": container, "image": image}},
				},
			},
		},
	})
	return &appsv1.ControllerRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: ns, CreationTimestamp: setImageAge(created),
			OwnerReferences: []metav1.OwnerReference{{Kind: ownerKind, Name: owner}},
		},
		Revision: revision,
		Data:     runtime.RawExtension{Raw: data},
	}
}

func newSetImageModel(t *testing.T, mut *fakeMutator, objs map[kube.ResourceKind][]runtime.Object, prod bool) Model {
	t.Helper()
	lister := fakeLister{objs: objs}
	session := newSession()
	if prod {
		session.Config = config.Config{ProdContexts: []string{session.Location.Context}}
	}
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	return step(t, m, m.Init()())
}

func TestIOpensSetImagePrefilledToCurrentTag(t *testing.T) {
	m := newSetImageModel(t, &fakeMutator{}, map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {twoContainerDeployment("default", "aim-worker", "registry.aim.dev/aim-worker:3.4.1")},
	}, false)

	m = step(t, m, tea.KeyPressMsg{Text: "i"})
	if m.pendingSetImage == nil {
		t.Fatal("expected pendingSetImage set after 'i'")
	}
	t2 := m.pendingSetImage
	if t2.repo != "registry.aim.dev/aim-worker" || t2.buffer != "3.4.1" {
		t.Fatalf("repo/buffer = %q/%q, want registry.aim.dev/aim-worker/3.4.1", t2.repo, t2.buffer)
	}
	if !t2.unchanged() {
		t.Fatal("expected the just-opened prefill to read as unchanged (same as current image)")
	}
	if !m.CapturingInput() {
		t.Fatal("expected CapturingInput true while the set-image panel is open")
	}
}

func TestTabCyclesContainersAndRecomputesHistory(t *testing.T) {
	m := newSetImageModel(t, &fakeMutator{}, map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {twoContainerDeployment("default", "aim-worker", "registry.aim.dev/aim-worker:3.4.1")},
	}, false)
	m = step(t, m, tea.KeyPressMsg{Text: "i"})

	m = step(t, m, tea.KeyPressMsg{Text: "tab"})
	t2 := m.pendingSetImage
	if t2.containerIdx != 1 || t2.repo != "sidecar" || t2.buffer != "0.9.1" {
		t.Fatalf("after tab: idx=%d repo=%q buffer=%q, want 1/sidecar/0.9.1", t2.containerIdx, t2.repo, t2.buffer)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "tab"})
	if m.pendingSetImage.containerIdx != 0 {
		t.Fatalf("expected tab to wrap back to container 0, got %d", m.pendingSetImage.containerIdx)
	}
}

func TestHistoryUpDownPicksTagIntoBuffer(t *testing.T) {
	dep := twoContainerDeployment("default", "aim-worker", "registry.aim.dev/aim-worker:3.4.2")
	rsCur := replicaSetRevision("default", "aim-worker-r43", "aim-worker", "registry.aim.dev/aim-worker:3.4.2", 43, 2*24*time.Hour)
	rsOld := replicaSetRevision("default", "aim-worker-r42", "aim-worker", "registry.aim.dev/aim-worker:3.4.1", 42, 21*24*time.Hour)

	m := newSetImageModel(t, &fakeMutator{}, map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {dep},
		kube.KindReplicaSet: {rsCur, rsOld},
	}, false)
	m = step(t, m, tea.KeyPressMsg{Text: "i"})

	if len(m.pendingSetImage.history) != 2 {
		t.Fatalf("history = %+v, want 2 entries (current + rollback target)", m.pendingSetImage.history)
	}
	m = step(t, m, tea.KeyPressMsg{Text: "down"})
	if m.pendingSetImage.buffer != "3.4.1" {
		t.Fatalf("buffer after down = %q, want 3.4.1", m.pendingSetImage.buffer)
	}
	m = step(t, m, tea.KeyPressMsg{Text: "up"})
	if m.pendingSetImage.buffer != "3.4.2" {
		t.Fatalf("buffer after up = %q, want 3.4.2 (back to current)", m.pendingSetImage.buffer)
	}
}

func TestCtrlUTogglesFullRefEditing(t *testing.T) {
	m := newSetImageModel(t, &fakeMutator{}, map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {twoContainerDeployment("default", "aim-worker", "registry.aim.dev/aim-worker:3.4.1")},
	}, false)
	m = step(t, m, tea.KeyPressMsg{Text: "i"})

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+u"})
	t2 := m.pendingSetImage
	if !t2.fullRef || t2.buffer != "registry.aim.dev/aim-worker:3.4.1" {
		t.Fatalf("after ctrl-u: fullRef=%v buffer=%q, want true/registry.aim.dev/aim-worker:3.4.1", t2.fullRef, t2.buffer)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+u"})
	t2 = m.pendingSetImage
	if t2.fullRef || t2.buffer != "3.4.1" || t2.repo != "registry.aim.dev/aim-worker" {
		t.Fatalf("after second ctrl-u: fullRef=%v buffer=%q repo=%q, want false/3.4.1/registry.aim.dev/aim-worker", t2.fullRef, t2.buffer, t2.repo)
	}
}

// TestLeftRightMoveCursorForMidBufferEditing pins the fix for a reported
// bug: ←/→ did nothing, since the original implementation only ever
// appended/backspaced at the end of the buffer. Prefilled tag is "3.4.1"
// (cursor starts at the end, position 5); ← twice parks it between the two
// "4"s... i.e. after "3." (position 2), where a typed digit must insert
// in the middle, not append at the end.
func TestLeftRightMoveCursorForMidBufferEditing(t *testing.T) {
	m := newSetImageModel(t, &fakeMutator{}, map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {twoContainerDeployment("default", "aim-worker", "registry.aim.dev/aim-worker:3.4.1")},
	}, false)
	m = step(t, m, tea.KeyPressMsg{Text: "i"})
	if m.pendingSetImage.cursor != len("3.4.1") {
		t.Fatalf("cursor = %d, want prefilled cursor at the end (%d)", m.pendingSetImage.cursor, len("3.4.1"))
	}

	for range 3 {
		m = step(t, m, tea.KeyPressMsg{Text: "left"})
	}
	if m.pendingSetImage.cursor != 2 {
		t.Fatalf("cursor after 3x left = %d, want 2", m.pendingSetImage.cursor)
	}
	m = step(t, m, tea.KeyPressMsg{Text: "9"})
	if m.pendingSetImage.buffer != "3.94.1" {
		t.Fatalf("buffer after mid-cursor insert = %q, want 3.94.1", m.pendingSetImage.buffer)
	}
	if m.pendingSetImage.cursor != 3 {
		t.Fatalf("cursor after insert = %d, want 3 (advances past the inserted rune)", m.pendingSetImage.cursor)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "backspace"})
	if m.pendingSetImage.buffer != "3.4.1" || m.pendingSetImage.cursor != 2 {
		t.Fatalf("after backspace: buffer=%q cursor=%d, want 3.4.1/2", m.pendingSetImage.buffer, m.pendingSetImage.cursor)
	}

	for range 5 {
		m = step(t, m, tea.KeyPressMsg{Text: "right"})
	}
	if m.pendingSetImage.cursor != len("3.4.1") {
		t.Fatalf("cursor after overshooting right = %d, want clamped at %d", m.pendingSetImage.cursor, len("3.4.1"))
	}
}

func TestEnterOnUnchangedTagIsNoOp(t *testing.T) {
	mut := &fakeMutator{}
	m := newSetImageModel(t, mut, map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {twoContainerDeployment("default", "aim-worker", "registry.aim.dev/aim-worker:3.4.1")},
	}, false)
	m = step(t, m, tea.KeyPressMsg{Text: "i"})

	m = step(t, m, tea.KeyPressMsg{Text: "enter"})
	if m.pendingSetImage == nil {
		t.Fatal("expected the panel to stay open — re-entering the current tag must no-op, not apply")
	}
	if len(mut.setImages) != 0 {
		t.Fatalf("expected no SetImage call, got %v", mut.setImages)
	}
}

func TestEnterCommitsSetImageThroughMutatorNonProd(t *testing.T) {
	mut := &fakeMutator{}
	m := newSetImageModel(t, mut, map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {twoContainerDeployment("default", "aim-worker", "registry.aim.dev/aim-worker:3.4.1")},
	}, false)
	m = step(t, m, tea.KeyPressMsg{Text: "i"})
	m = step(t, m, tea.KeyPressMsg{Text: "2"})
	m = step(t, m, tea.KeyPressMsg{Text: "enter"})

	if m.pendingSetImage != nil {
		t.Fatal("expected pendingSetImage cleared after enter")
	}
	if m.actions.Active() {
		t.Fatal("non-PROD set-image is TierNone and should execute immediately, not show a confirm")
	}
	want := "default/aim-worker worker=registry.aim.dev/aim-worker:3.4.12"
	if len(mut.setImages) != 1 || mut.setImages[0] != want {
		t.Fatalf("setImages = %v, want [%q]", mut.setImages, want)
	}
}

func TestEnterInProdShowsInlineConfirmBeforeApplying(t *testing.T) {
	mut := &fakeMutator{}
	m := newSetImageModel(t, mut, map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {twoContainerDeployment("default", "aim-worker", "registry.aim.dev/aim-worker:3.4.1")},
	}, true)
	m = step(t, m, tea.KeyPressMsg{Text: "i"})
	m = step(t, m, tea.KeyPressMsg{Text: "2"})
	m = step(t, m, tea.KeyPressMsg{Text: "enter"})

	if m.pendingSetImage != nil {
		t.Fatal("expected pendingSetImage cleared once commitSetImage hands off to actions.Controller")
	}
	if !m.actions.Active() {
		t.Fatal("expected a PROD set-image apply to land in actions.Controller's inline y/N confirm")
	}
	if len(mut.setImages) != 0 {
		t.Fatal("expected no SetImage call yet — still awaiting the y/N confirm")
	}

	kb := m.Keybar()
	want := "kubectl set image deploy/aim-worker worker=registry.aim.dev/aim-worker:3.4.12 -n default"
	if !strings.Contains(kb.RightNote, want) {
		t.Fatalf("RightNote = %q, want it to contain %q", kb.RightNote, want)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if len(mut.setImages) != 1 {
		t.Fatalf("expected the confirm's 'y' to execute SetImage, got %v", mut.setImages)
	}
}

func TestEscCancelsSetImagePanel(t *testing.T) {
	mut := &fakeMutator{}
	m := newSetImageModel(t, mut, map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {twoContainerDeployment("default", "aim-worker", "registry.aim.dev/aim-worker:3.4.1")},
	}, false)
	m = step(t, m, tea.KeyPressMsg{Text: "i"})
	m = step(t, m, tea.KeyPressMsg{Text: "esc"})

	if m.pendingSetImage != nil {
		t.Fatal("expected pendingSetImage cleared after esc")
	}
	if len(mut.setImages) != 0 {
		t.Fatalf("expected no SetImage call after cancel, got %v", mut.setImages)
	}
}

func TestIAppliesToStatefulSetsAndDaemonSets(t *testing.T) {
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "default", CreationTimestamp: setImageAge(10 * 24 * time.Hour)},
		Spec: appsv1.StatefulSetSpec{Replicas: replicasPtr(2), Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "db", Image: "postgres:15"}}},
		}},
		Status: appsv1.StatefulSetStatus{Replicas: 2, ReadyReplicas: 2},
	}
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindStatefulSet: {sts}}}
	mut := &fakeMutator{}
	session := newSession()
	session.Location.Kind = kube.KindStatefulSet
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "i"})
	if m.pendingSetImage == nil || m.pendingSetImage.buffer != "15" {
		t.Fatalf("expected StatefulSet's 'i' to open prefilled to tag 15, got %+v", m.pendingSetImage)
	}
	// No ControllerRevision fixtures seeded — the single-row "current"
	// fallback ownRevisionHistory takes when no revision object exists yet.
	if len(m.pendingSetImage.history) != 1 || m.pendingSetImage.history[0].tag != "15" {
		t.Fatalf("history = %+v, want a single fallback current-tag row", m.pendingSetImage.history)
	}
	m = step(t, m, tea.KeyPressMsg{Text: "6"})
	m = step(t, m, tea.KeyPressMsg{Text: "enter"})
	want := "default/db db=postgres:156"
	if len(mut.setImages) != 1 || mut.setImages[0] != want {
		t.Fatalf("setImages = %v, want [%q]", mut.setImages, want)
	}
}

// TestStatefulSetHistoryReadsControllerRevisions pins the fix for a reported
// bug: the Deployments screen showed "rollout history · rollback target"
// rows (from owned ReplicaSets), but StatefulSets only ever showed the
// current tag — because ownRevisionHistory special-cased every non-
// Deployment kind to a single row. StatefulSet/DaemonSet use
// ControllerRevisions (apps/v1), not ReplicaSets, for the same rollout
// history; this confirms that source now feeds real rollback-target rows.
func TestStatefulSetHistoryReadsControllerRevisions(t *testing.T) {
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "default", CreationTimestamp: setImageAge(30 * 24 * time.Hour)},
		Spec: appsv1.StatefulSetSpec{Replicas: replicasPtr(2), Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "db", Image: "postgres:15.2"}}},
		}},
		Status: appsv1.StatefulSetStatus{Replicas: 2, ReadyReplicas: 2},
	}
	crOld := controllerRevisionFixture("default", "db-abc12", "StatefulSet", "db", "db", "postgres:15.1", 41, 20*24*time.Hour)
	crCur := controllerRevisionFixture("default", "db-def34", "StatefulSet", "db", "db", "postgres:15.2", 42, 2*24*time.Hour)
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindStatefulSet:        {sts},
		kube.KindControllerRevision: {crOld, crCur},
	}}
	session := newSession()
	session.Location.Kind = kube.KindStatefulSet
	m := New(Config{Session: session, Lister: lister, Mutator: &fakeMutator{}})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m = step(t, m, tea.KeyPressMsg{Text: "i"})

	h := m.pendingSetImage.history
	if len(h) != 2 {
		t.Fatalf("history = %+v, want 2 entries (current + rollback target)", h)
	}
	if h[0].tag != "15.2" || h[0].from != "rev 42 · this statefulset" {
		t.Fatalf("history[0] = %+v, want current rev 42 (15.2)", h[0])
	}
	if h[1].tag != "15.1" || h[1].from != "rollout history · rollback target" {
		t.Fatalf("history[1] = %+v, want rev 41 rollback target (15.1)", h[1])
	}
}

func TestINoOpsWithoutMutator(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {twoContainerDeployment("default", "aim-worker", "registry.aim.dev/aim-worker:3.4.1")},
	}}
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "i"})
	if m.pendingSetImage != nil {
		t.Fatal("expected 'i' to no-op without a mutator wired")
	}
}
