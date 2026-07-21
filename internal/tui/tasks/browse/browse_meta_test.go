package browse

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui/actions"
)

// metaDeployment builds a Deployment carrying labels/annotations for 26a's
// editor to open on, with an optional own spec.selector.matchLabels (for the
// immutable-selector-key test).
func metaDeployment(ns, name string, labels, annotations, selector map[string]string) *appsv1.Deployment {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels, Annotations: annotations},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "worker", Image: "aim-worker:1.0"}}},
		}},
	}
	if selector != nil {
		dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: selector}
	}
	return dep
}

func newMetaModel(t *testing.T, mut *fakeMutator, objs map[kube.ResourceKind][]runtime.Object) Model {
	t.Helper()
	mut.metaObjs = objs
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	session.Location.Namespace = "default"
	m := New(Config{Session: session, Lister: fakeLister{objs: objs}, Mutator: mut})
	m.SetSize(120, 36)
	return step(t, m, m.Init()())
}

func TestMetaEditableGating(t *testing.T) {
	cases := []struct {
		kind kube.ResourceKind
		want bool
	}{
		{kube.KindPod, true},
		{kube.KindDeployment, true},
		{kube.KindNode, true},
		{kube.KindCustomResourceDefinition, true},
		{kube.KindForward, false},
		{kube.KindHelmRelease, false},
		{kube.KindWhoCan, false},
		{kube.KindOverview, false},
	}
	for _, c := range cases {
		if got := metaEditable(c.kind); got != c.want {
			t.Errorf("metaEditable(%s) = %v, want %v", c.kind, got, c.want)
		}
	}
}

func TestBeginMetaBuildsSortedRowsFromCurrentValues(t *testing.T) {
	dep := metaDeployment("default", "aim-worker",
		map[string]string{"team": "platform", "app": "aim-worker"},
		map[string]string{"kute.dev/owner": "platform-oncall"},
		nil)
	m := newMetaModel(t, &fakeMutator{}, map[kube.ResourceKind][]runtime.Object{kube.KindDeployment: {dep}})

	if !m.beginMeta() {
		t.Fatal("beginMeta returned false")
	}
	t2 := m.pendingMeta
	if len(t2.labels) != 2 || t2.labels[0].key != "app" || t2.labels[1].key != "team" {
		t.Fatalf("labels = %+v, want sorted [app team]", t2.labels)
	}
	if t2.labels[0].current != "aim-worker" || t2.labels[0].buffer != "aim-worker" {
		t.Errorf("app row = %+v, want current/buffer aim-worker", t2.labels[0])
	}
	if len(t2.annotations) != 1 || t2.annotations[0].key != "kute.dev/owner" {
		t.Fatalf("annotations = %+v, want [kute.dev/owner]", t2.annotations)
	}
}

func TestJoinedLabelGetsSelectorWarning(t *testing.T) {
	dep := metaDeployment("default", "aim-worker", map[string]string{"app": "aim-worker", "env": "stage"}, nil, nil)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "aim-worker", Namespace: "default"},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "aim-worker"}},
	}
	pods := []runtime.Object{
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "default", Labels: map[string]string{"app": "aim-worker"}}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p2", Namespace: "default", Labels: map[string]string{"app": "aim-worker"}}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p3", Namespace: "default", Labels: map[string]string{"app": "other"}}},
	}
	m := newMetaModel(t, &fakeMutator{}, map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {dep}, kube.KindService: {svc}, kube.KindPod: pods,
	})
	if !m.beginMeta() {
		t.Fatal("beginMeta returned false")
	}
	var app, env *metaRow
	for i := range m.pendingMeta.labels {
		switch m.pendingMeta.labels[i].key {
		case "app":
			app = &m.pendingMeta.labels[i]
		case "env":
			env = &m.pendingMeta.labels[i]
		}
	}
	if app.joinService != "aim-worker" || app.joinPodCount != 2 {
		t.Errorf("app row join = %q/%d, want aim-worker/2", app.joinService, app.joinPodCount)
	}
	if env.joinService != "" {
		t.Errorf("env row should carry no join, got %q", env.joinService)
	}
}

func TestImmutableSelectorLabelIsReadOnlyAndBlocksEdits(t *testing.T) {
	dep := metaDeployment("default", "aim-worker",
		map[string]string{"app": "aim-worker", "team": "platform"}, nil,
		map[string]string{"app": "aim-worker"})
	m := newMetaModel(t, &fakeMutator{}, map[kube.ResourceKind][]runtime.Object{kube.KindDeployment: {dep}})
	if !m.beginMeta() {
		t.Fatal("beginMeta returned false")
	}
	if !m.pendingMeta.labels[0].readOnly { // "app" sorts first
		t.Fatal("expected the immutable selector key's row to be read-only")
	}

	// labelIdx starts at 0 ("app"); ↵ (enter editing) and ctrl+d must both
	// be no-ops on a read-only row.
	updated, _ := m.updateMetaKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m2 := updated.(*Model)
	if m2.pendingMeta.editing {
		t.Error("↵ on a read-only row should not enter editing mode")
	}
	updated, cmd := m2.updateMetaKey(tea.KeyPressMsg{Text: "ctrl+d"})
	if cmd != nil {
		t.Error("ctrl+d on a read-only row should be a no-op, got a cmd")
	}
	m3 := updated.(*Model)
	if m3.pendingMeta == nil {
		t.Error("panel should still be open after a no-op ctrl+d")
	}
}

func TestNavigationModeIgnoresPrintableCharacters(t *testing.T) {
	dep := metaDeployment("default", "aim-worker", map[string]string{"team": "platform"}, nil, nil)
	m := newMetaModel(t, &fakeMutator{}, map[kube.ResourceKind][]runtime.Object{kube.KindDeployment: {dep}})
	m.beginMeta()

	// A printable character with no navigation-mode binding (unlike 'a'
	// add, 'y' copy) must not leak into the row buffer — navigation mode has
	// no text-entry context at all, so an unrecognized key is simply a
	// no-op (the row is untouched, the panel stays open).
	updated, cmd := m.updateMetaKey(tea.KeyPressMsg{Text: "z"})
	m2 := updated.(*Model)
	if cmd != nil || m2.pendingMeta == nil || m2.pendingMeta.adding != metaAddNone {
		t.Error("'z' in navigation mode should be a plain no-op")
	}
	if m2.pendingMeta.labels[0].buffer != "platform" {
		t.Errorf("typing in navigation mode changed the row buffer: %q", m2.pendingMeta.labels[0].buffer)
	}
}

// TestAddHotkeyStartsAddModeAndShiftTabReturnsToKey covers this session's
// second fix: 'a' (not 'n') opens the focused grid's add-row, and
// shift+tab — not just tab — moves focus back to the key buffer once the
// value buffer has it, mirroring the rest of the panel's own bidirectional
// tab convention instead of only going key -> value.
func TestAddHotkeyStartsAddModeAndShiftTabReturnsToKey(t *testing.T) {
	dep := metaDeployment("default", "aim-worker", map[string]string{"team": "platform"}, nil, nil)
	m := newMetaModel(t, &fakeMutator{}, map[kube.ResourceKind][]runtime.Object{kube.KindDeployment: {dep}})
	m.beginMeta()

	m = step(t, m, tea.KeyPressMsg{Text: "a"})
	if m.pendingMeta.adding != metaAddLabel {
		t.Fatal("'a' should start add-mode on the focused grid")
	}

	m = step(t, m, tea.KeyPressMsg{Text: "k"})
	m = step(t, m, tea.KeyPressMsg{Text: "tab"})
	m = step(t, m, tea.KeyPressMsg{Text: "v"})
	if m.pendingMeta.addKey != "k" || m.pendingMeta.addValue != "v" || !m.pendingMeta.addOnValue {
		t.Fatalf("after tab, key/value = %q/%q, addOnValue = %v", m.pendingMeta.addKey, m.pendingMeta.addValue, m.pendingMeta.addOnValue)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "shift+tab"})
	if m.pendingMeta.addOnValue {
		t.Fatal("shift+tab should move focus back to the key buffer")
	}
	m = step(t, m, tea.KeyPressMsg{Text: "2"})
	if m.pendingMeta.addKey != "k2" || m.pendingMeta.addValue != "v" {
		t.Fatalf("after shift+tab, key/value = %q/%q, want k2/v", m.pendingMeta.addKey, m.pendingMeta.addValue)
	}
}

func TestTabSwitchesFocusedSectionIndependentOfCursor(t *testing.T) {
	dep := metaDeployment("default", "aim-worker",
		map[string]string{"team": "platform"}, map[string]string{"kute.dev/owner": "platform-oncall"}, nil)
	m := newMetaModel(t, &fakeMutator{}, map[kube.ResourceKind][]runtime.Object{kube.KindDeployment: {dep}})
	m.beginMeta()
	if m.pendingMeta.section != metaSectionLabels {
		t.Fatal("expected the panel to open focused on LABELS")
	}

	m = step(t, m, tea.KeyPressMsg{Text: "tab"})
	if m.pendingMeta.section != metaSectionAnnotations {
		t.Fatal("tab should switch focus to ANNOTATIONS")
	}
	r := m.pendingMeta.selectedRow()
	if r == nil || r.key != "kute.dev/owner" {
		t.Fatalf("selected row after tab = %+v, want kute.dev/owner", r)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "shift+tab"})
	if m.pendingMeta.section != metaSectionLabels {
		t.Fatal("shift+tab should switch focus back to LABELS")
	}
}

func TestControllerManagedAnnotationIsReadOnly(t *testing.T) {
	dep := metaDeployment("default", "aim-worker", nil,
		map[string]string{
			"deployment.kubernetes.io/revision":                "42",
			"kubectl.kubernetes.io/last-applied-configuration": "{}",
			"kute.dev/owner": "platform-oncall",
		}, nil)
	m := newMetaModel(t, &fakeMutator{}, map[kube.ResourceKind][]runtime.Object{kube.KindDeployment: {dep}})
	if !m.beginMeta() {
		t.Fatal("beginMeta returned false")
	}
	for _, a := range m.pendingMeta.annotations {
		want := a.key != "kute.dev/owner"
		if a.readOnly != want {
			t.Errorf("annotation %q readOnly = %v, want %v", a.key, a.readOnly, want)
		}
	}
}

func TestHelmOwnedNoteOnManagedByRowOnly(t *testing.T) {
	dep := metaDeployment("default", "aim-worker",
		map[string]string{"app.kubernetes.io/managed-by": "Helm", "team": "platform"}, nil, nil)
	m := newMetaModel(t, &fakeMutator{}, map[kube.ResourceKind][]runtime.Object{kube.KindDeployment: {dep}})
	if !m.beginMeta() {
		t.Fatal("beginMeta returned false")
	}
	for _, l := range m.pendingMeta.labels {
		want := l.key == "app.kubernetes.io/managed-by"
		if l.helmOwnedNote != want {
			t.Errorf("label %q helmOwnedNote = %v, want %v", l.key, l.helmOwnedNote, want)
		}
	}
}

func TestEditNonJoinedLabelAppliesImmediately(t *testing.T) {
	dep := metaDeployment("default", "aim-worker", map[string]string{"env": "stage"}, nil, nil)
	mut := &fakeMutator{}
	m := newMetaModel(t, mut, map[kube.ResourceKind][]runtime.Object{kube.KindDeployment: {dep}})
	m.beginMeta()

	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // navigation -> editing mode
	m = step(t, m, tea.KeyPressMsg{Text: "g"})          // "stage" -> "stageg"
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // save

	if m.actions.Active() {
		t.Error("a non-joined edit is TierNone and should execute immediately, not confirm")
	}
	if len(mut.metaPatches) != 1 || mut.metaPatches[0] != "default/aim-worker labels env=stageg" {
		t.Errorf("metaPatches = %v, want one env=stageg label patch", mut.metaPatches)
	}
	// docs/design README.md §26a: "confirm → execute → refresh → show
	// result → remain on screen" — the panel stays open, the row is
	// re-fetched from the object (never an optimistic local patch) and no
	// longer reads as changed, and an inline success message appears.
	if m.pendingMeta == nil {
		t.Fatal("the panel should stay open after a successful apply")
	}
	if m.pendingMeta.editing {
		t.Error("editing should end once the apply succeeds")
	}
	if got := m.pendingMeta.labels[0].current; got != "stageg" {
		t.Errorf("refreshed current = %q, want stageg", got)
	}
	if m.pendingMeta.labels[0].changed() {
		t.Error("the refreshed row should no longer read as changed")
	}
	if want := "updated env=stageg"; m.pendingMeta.message != want {
		t.Errorf("message = %q, want %q", m.pendingMeta.message, want)
	}
}

func TestEditJoinedLabelRequiresConfirmThenApplies(t *testing.T) {
	dep := metaDeployment("default", "aim-worker", map[string]string{"app": "aim-worker"}, nil, nil)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "aim-worker", Namespace: "default"},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "aim-worker"}},
	}
	mut := &fakeMutator{}
	m := newMetaModel(t, mut, map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {dep}, kube.KindService: {svc},
	})
	m.beginMeta()

	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // navigation -> editing mode
	m = step(t, m, tea.KeyPressMsg{Text: "2"})          // "aim-worker" -> "aim-worker2"
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // save (join-escalated, so this opens the confirm)

	if len(mut.metaPatches) != 0 {
		t.Fatalf("expected no patch yet (confirm pending), got %v", mut.metaPatches)
	}
	if !m.actions.Active() {
		t.Fatal("editing a Service-selector-joined label should require confirmation")
	}
	if m.pendingMeta == nil {
		t.Fatal("the panel should stay open under the inline confirm, not close to the generic table+confirm view")
	}
	pending := m.actions.Pending()
	if pending == nil || pending.Scope.MetaJoinService != "aim-worker" {
		t.Fatalf("pending scope = %+v, want MetaJoinService aim-worker", pending)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if len(mut.metaPatches) != 1 || mut.metaPatches[0] != "default/aim-worker labels app=aim-worker2" {
		t.Errorf("metaPatches after confirm = %v, want one app=aim-worker2 label patch", mut.metaPatches)
	}
	if m.pendingMeta == nil {
		t.Fatal("the panel should stay open after the confirmed edit applies")
	}
	if got := m.pendingMeta.labels[0].current; got != "aim-worker2" {
		t.Errorf("refreshed current = %q, want aim-worker2", got)
	}
	if want := "updated app=aim-worker2"; m.pendingMeta.message != want {
		t.Errorf("message = %q, want %q", m.pendingMeta.message, want)
	}
}

func TestRemoveKeyRequiresConfirmThenApplies(t *testing.T) {
	dep := metaDeployment("default", "aim-worker", map[string]string{"team": "platform"}, nil, nil)
	mut := &fakeMutator{}
	m := newMetaModel(t, mut, map[kube.ResourceKind][]runtime.Object{kube.KindDeployment: {dep}})
	m.beginMeta()

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+d"})
	if !m.actions.Active() {
		t.Fatal("removing a key should always require inline confirmation")
	}
	if tier := m.actions.Tier(); tier != actions.TierInline {
		t.Errorf("remove tier = %v, want TierInline", tier)
	}
	if m.pendingMeta == nil {
		t.Fatal("the panel should stay open under the inline confirm, not close to the generic table+confirm view")
	}

	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if len(mut.metaPatches) != 1 || mut.metaPatches[0] != "default/aim-worker labels team-" {
		t.Errorf("metaPatches = %v, want one team removal", mut.metaPatches)
	}
	// docs/design README.md §26a: the panel stays open after a removal too
	// — the row is gone (re-fetched, not locally spliced out) and focus
	// lands on the nearest remaining row (here, none — the grid is empty).
	if m.pendingMeta == nil {
		t.Fatal("the panel should stay open once the confirmed removal applies")
	}
	if len(m.pendingMeta.labels) != 0 {
		t.Errorf("labels = %+v, want none left after removal", m.pendingMeta.labels)
	}
	if want := "removed team"; m.pendingMeta.message != want {
		t.Errorf("message = %q, want %q", m.pendingMeta.message, want)
	}
}

// TestCancellingRemoveConfirmKeepsPanelOpen covers 'n'/'esc' during the
// removal confirm: the row survives (nothing was ever applied) and the panel
// stays open in navigation mode rather than closing.
func TestCancellingRemoveConfirmKeepsPanelOpen(t *testing.T) {
	dep := metaDeployment("default", "aim-worker", map[string]string{"team": "platform"}, nil, nil)
	mut := &fakeMutator{}
	m := newMetaModel(t, mut, map[kube.ResourceKind][]runtime.Object{kube.KindDeployment: {dep}})
	m.beginMeta()

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+d"})
	m = step(t, m, tea.KeyPressMsg{Text: "n"})

	if m.actions.Active() {
		t.Error("cancel should end the confirm")
	}
	if m.pendingMeta == nil {
		t.Fatal("cancelling the removal confirm should leave the panel open")
	}
	if len(m.pendingMeta.labels) != 1 || m.pendingMeta.labels[0].key != "team" {
		t.Error("the row should survive an cancelled removal")
	}
	if len(mut.metaPatches) != 0 {
		t.Errorf("metaPatches = %v, want none", mut.metaPatches)
	}
}

// TestJoinedLabelEditKeepsPanelOpenThroughConfirm covers the other
// TierInline path — editing a Service-selector-joined label — verifying the
// panel stays open under the confirm and only closes once the edit actually
// applies, and that cancelling reverts the buffer instead of leaving a
// half-applied edit behind.
func TestJoinedLabelEditKeepsPanelOpenThroughConfirm(t *testing.T) {
	dep := metaDeployment("default", "aim-worker", map[string]string{"app": "aim-worker"}, nil, nil)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "aim-worker", Namespace: "default"},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "aim-worker"}},
	}
	mut := &fakeMutator{}
	m := newMetaModel(t, mut, map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {dep}, kube.KindService: {svc},
	})
	m.beginMeta()

	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // navigation -> editing mode
	m = step(t, m, tea.KeyPressMsg{Text: "2"})           // "aim-worker" -> "aim-worker2"
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // save -> opens the confirm

	if m.pendingMeta == nil {
		t.Fatal("the panel should stay open under the inline confirm")
	}
	if m.pendingMeta.editing {
		t.Error("editing should end once input hands off to the confirm")
	}
	if got := m.pendingMeta.labels[0].buffer; got != "aim-worker2" {
		t.Errorf("buffer while confirming = %q, want aim-worker2", got)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "n"}) // cancel
	if m.pendingMeta == nil {
		t.Fatal("cancelling should leave the panel open")
	}
	if got := m.pendingMeta.labels[0].buffer; got != "aim-worker" {
		t.Errorf("buffer after cancel = %q, want reverted to aim-worker", got)
	}
	if len(mut.metaPatches) != 0 {
		t.Fatalf("metaPatches after cancel = %v, want none", mut.metaPatches)
	}

	// Redo the edit and this time confirm it.
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = step(t, m, tea.KeyPressMsg{Text: "2"})
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = step(t, m, tea.KeyPressMsg{Text: "y"})

	if m.pendingMeta == nil {
		t.Fatal("the panel should stay open once the confirmed edit applies")
	}
	if len(mut.metaPatches) != 1 || mut.metaPatches[0] != "default/aim-worker labels app=aim-worker2" {
		t.Errorf("metaPatches = %v, want one app=aim-worker2 label patch", mut.metaPatches)
	}
	if got := m.pendingMeta.labels[0].current; got != "aim-worker2" {
		t.Errorf("refreshed current = %q, want aim-worker2", got)
	}
}

func TestAddLabelHappyPath(t *testing.T) {
	dep := metaDeployment("default", "aim-worker", map[string]string{"team": "platform"}, nil, nil)
	mut := &fakeMutator{}
	m := newMetaModel(t, mut, map[kube.ResourceKind][]runtime.Object{kube.KindDeployment: {dep}})
	m.beginMeta()

	m = step(t, m, tea.KeyPressMsg{Text: "a"})
	if m.pendingMeta.adding != metaAddLabel {
		t.Fatal("'a' should add to the focused (LABELS, by default) grid")
	}
	for _, r := range "tier" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Text: "tab"})
	for _, r := range "gold" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if len(mut.metaPatches) != 1 || mut.metaPatches[0] != "default/aim-worker labels tier=gold" {
		t.Errorf("metaPatches = %v, want one tier=gold label patch", mut.metaPatches)
	}
	if m.pendingMeta == nil {
		t.Fatal("the panel should stay open after adding")
	}
	if m.pendingMeta.adding != metaAddNone {
		t.Error("add-mode should end once the add succeeds")
	}
	if len(m.pendingMeta.labels) != 2 || m.pendingMeta.labels[1].key != "tier" || m.pendingMeta.labels[1].current != "gold" {
		t.Errorf("labels = %+v, want [team tier=gold] with tier re-fetched from the object", m.pendingMeta.labels)
	}
	if want := "added tier=gold"; m.pendingMeta.message != want {
		t.Errorf("message = %q, want %q", m.pendingMeta.message, want)
	}
}

func TestAddAnnotationHappyPath(t *testing.T) {
	dep := metaDeployment("default", "aim-worker", nil, map[string]string{"kute.dev/owner": "platform-oncall"}, nil)
	mut := &fakeMutator{}
	m := newMetaModel(t, mut, map[kube.ResourceKind][]runtime.Object{kube.KindDeployment: {dep}})
	m.beginMeta()

	m = step(t, m, tea.KeyPressMsg{Text: "tab"}) // focus ANNOTATIONS
	m = step(t, m, tea.KeyPressMsg{Text: "a"})
	if m.pendingMeta.adding != metaAddAnnotation {
		t.Fatal("'a' should add to the focused (ANNOTATIONS, after tab) grid")
	}
	for _, r := range "note" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Text: "tab"})
	for _, r := range "hi" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if len(mut.metaPatches) != 1 || mut.metaPatches[0] != "default/aim-worker annotations note=hi" {
		t.Errorf("metaPatches = %v, want one note=hi annotation patch", mut.metaPatches)
	}
	if want := "added note=hi"; m.pendingMeta == nil || m.pendingMeta.message != want {
		t.Errorf("message = %q, want %q", m.pendingMeta.message, want)
	}
}

func TestAddModeEscCancelsWithoutClosingPanel(t *testing.T) {
	dep := metaDeployment("default", "aim-worker", map[string]string{"team": "platform"}, nil, nil)
	m := newMetaModel(t, &fakeMutator{}, map[kube.ResourceKind][]runtime.Object{kube.KindDeployment: {dep}})
	m.beginMeta()

	m = step(t, m, tea.KeyPressMsg{Text: "a"})
	m = step(t, m, tea.KeyPressMsg{Text: "esc"})
	if m.pendingMeta == nil {
		t.Fatal("esc while adding should cancel add-mode, not close the panel")
	}
	if m.pendingMeta.adding != metaAddNone {
		t.Error("expected add-mode to be cleared")
	}
}

func TestEditingModeInsertsReservedLettersLiterally(t *testing.T) {
	dep := metaDeployment("default", "aim-worker", map[string]string{"team": "platform"}, nil, nil)
	m := newMetaModel(t, &fakeMutator{}, map[kube.ResourceKind][]runtime.Object{kube.KindDeployment: {dep}})
	m.beginMeta()

	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // navigation -> editing mode
	if !m.pendingMeta.editing {
		t.Fatal("↵ on an editable row should enter editing mode")
	}
	// 'a' (add) and 'y' (copy) are navigation-mode shortcuts, 'A' isn't
	// bound to anything — all three still must insert literally inside
	// editing mode, never be swallowed as a shortcut.
	for _, r := range "aAy" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	if got := m.pendingMeta.labels[0].buffer; got != "platformaAy" {
		t.Errorf("buffer after typing aAy in editing mode = %q, want platformaAy", got)
	}
	if m.pendingMeta.adding != metaAddNone {
		t.Error("typing 'a' while editing should not have started add-mode")
	}
}

func TestEditingModeEscRevertsBufferAndReturnsToNavigation(t *testing.T) {
	dep := metaDeployment("default", "aim-worker", map[string]string{"team": "platform"}, nil, nil)
	m := newMetaModel(t, &fakeMutator{}, map[kube.ResourceKind][]runtime.Object{kube.KindDeployment: {dep}})
	m.beginMeta()

	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = step(t, m, tea.KeyPressMsg{Text: "z"})
	m = step(t, m, tea.KeyPressMsg{Text: "esc"})

	if m.pendingMeta == nil {
		t.Fatal("esc while editing should return to navigation, not close the panel")
	}
	if m.pendingMeta.editing {
		t.Error("esc while editing should leave editing mode")
	}
	if m.pendingMeta.labels[0].buffer != "platform" {
		t.Errorf("buffer after esc = %q, want reverted to platform", m.pendingMeta.labels[0].buffer)
	}
}

func TestCopyKeyEqualsValueReturnsClipboardCmd(t *testing.T) {
	dep := metaDeployment("default", "aim-worker", map[string]string{"team": "platform"}, nil, nil)
	m := newMetaModel(t, &fakeMutator{}, map[kube.ResourceKind][]runtime.Object{kube.KindDeployment: {dep}})
	m.beginMeta()

	_, cmd := m.updateMetaKey(tea.KeyPressMsg{Text: "y"})
	if cmd == nil {
		t.Fatal("'y' should return a SetClipboard cmd")
	}
}

func TestEscClosesPanel(t *testing.T) {
	dep := metaDeployment("default", "aim-worker", map[string]string{"team": "platform"}, nil, nil)
	m := newMetaModel(t, &fakeMutator{}, map[kube.ResourceKind][]runtime.Object{kube.KindDeployment: {dep}})
	m.beginMeta()

	updated, _ := m.updateMetaKey(tea.KeyPressMsg{Text: "esc"})
	m2 := updated.(*Model)
	if m2.pendingMeta != nil {
		t.Error("esc should close the panel")
	}
}

// TestFailedEditRestoresEditingModeWithErrorAndAttemptedValue covers
// docs/design README.md §26a's failure contract: "remain in edit mode with
// the attempted value intact and show the server error" — nothing is
// refetched, so the object's real (unchanged) value never overwrites the
// still-in-progress edit.
func TestFailedEditRestoresEditingModeWithErrorAndAttemptedValue(t *testing.T) {
	dep := metaDeployment("default", "aim-worker", map[string]string{"env": "stage"}, nil, nil)
	mut := &fakeMutator{err: errors.New("admission webhook denied the request")}
	m := newMetaModel(t, mut, map[kube.ResourceKind][]runtime.Object{kube.KindDeployment: {dep}})
	m.beginMeta()

	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // navigation -> editing mode
	m = step(t, m, tea.KeyPressMsg{Text: "g"})          // "stage" -> "stageg"
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // save (TierNone, executes and fails)

	if m.pendingMeta == nil {
		t.Fatal("the panel should stay open after a failed apply")
	}
	if !m.pendingMeta.editing {
		t.Error("a failed edit should re-enter editing mode, not fall back to navigation")
	}
	if got := m.pendingMeta.labels[0].buffer; got != "stageg" {
		t.Errorf("buffer after failure = %q, want the attempted stageg intact", got)
	}
	if got := m.pendingMeta.labels[0].current; got != "stage" {
		t.Errorf("current after failure = %q, want unchanged stage (nothing refetched on failure)", got)
	}
	if m.pendingMeta.lastError == "" {
		t.Error("expected the server error to be surfaced")
	}
	if m.pendingMeta.message != "" {
		t.Error("a failure should not also carry a success message")
	}
}

// TestFailedAddRestoresAddModeWithErrorAndAttemptedValues mirrors the edit
// case for the add sub-flow.
func TestFailedAddRestoresAddModeWithErrorAndAttemptedValues(t *testing.T) {
	dep := metaDeployment("default", "aim-worker", map[string]string{"team": "platform"}, nil, nil)
	mut := &fakeMutator{err: errors.New("field is immutable")}
	m := newMetaModel(t, mut, map[kube.ResourceKind][]runtime.Object{kube.KindDeployment: {dep}})
	m.beginMeta()

	m = step(t, m, tea.KeyPressMsg{Text: "a"})
	for _, r := range "tier" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Text: "tab"})
	for _, r := range "gold" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.pendingMeta == nil {
		t.Fatal("the panel should stay open after a failed add")
	}
	if m.pendingMeta.adding != metaAddLabel {
		t.Error("a failed add should re-enter add-mode, not fall back to navigation")
	}
	if m.pendingMeta.addKey != "tier" || m.pendingMeta.addValue != "gold" {
		t.Errorf("add buffers after failure = %q/%q, want tier/gold intact", m.pendingMeta.addKey, m.pendingMeta.addValue)
	}
	if len(m.pendingMeta.labels) != 1 {
		t.Errorf("labels = %+v, want no new row added on failure", m.pendingMeta.labels)
	}
	if m.pendingMeta.lastError == "" {
		t.Error("expected the server error to be surfaced")
	}
}

// TestFailedRemoveRestoresRowWithError covers the removal failure path: the
// row survives (never spliced out locally) and the error shows in
// navigation mode, since there's no buffer to fall back into editing.
func TestFailedRemoveRestoresRowWithError(t *testing.T) {
	dep := metaDeployment("default", "aim-worker", map[string]string{"team": "platform"}, nil, nil)
	mut := &fakeMutator{err: errors.New("etcdserver: request timed out")}
	m := newMetaModel(t, mut, map[kube.ResourceKind][]runtime.Object{kube.KindDeployment: {dep}})
	m.beginMeta()

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+d"})
	m = step(t, m, tea.KeyPressMsg{Text: "y"}) // confirm -> executes and fails

	if m.pendingMeta == nil {
		t.Fatal("the panel should stay open after a failed removal")
	}
	if len(m.pendingMeta.labels) != 1 || m.pendingMeta.labels[0].key != "team" {
		t.Errorf("labels = %+v, want team to survive the failed removal", m.pendingMeta.labels)
	}
	if m.pendingMeta.lastError == "" {
		t.Error("expected the server error to be surfaced")
	}
}
