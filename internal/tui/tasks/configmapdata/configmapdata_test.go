package configmapdata

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/config"
	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
)

func plain(s string) string { return ansi.Strip(s) }

type fakeLister struct {
	objs map[kube.ResourceKind][]runtime.Object
}

func (f fakeLister) ListRaw(_ context.Context, kind kube.ResourceKind, _ string) ([]runtime.Object, error) {
	return f.objs[kind], nil
}

// fakeMutator's PatchConfigMapData mutates the shared configmap object in
// place, and RolloutRestart records every call, so ctrl-r's restart-chaining
// contract is directly assertable — the same fidelity fix secretdata_test.go's
// own fakeMutator makes for 27b's tests.
type fakeMutator struct {
	cm              *corev1.ConfigMap
	err             error
	rolloutRestarts []string // "kind/namespace/name"
}

func (f *fakeMutator) DeleteResource(context.Context, kube.ResourceKind, string, string) error {
	return nil
}
func (f *fakeMutator) DeleteResourceForced(context.Context, kube.ResourceKind, string, string) error {
	return nil
}
func (f *fakeMutator) RolloutRestart(_ context.Context, kind kube.ResourceKind, namespace, name string) error {
	f.rolloutRestarts = append(f.rolloutRestarts, string(kind)+"/"+namespace+"/"+name)
	return nil
}
func (f *fakeMutator) HelmRollback(context.Context, string, string, int) error {
	return nil
}
func (f *fakeMutator) RolloutUndo(context.Context, string, string, int) error {
	return nil
}
func (f *fakeMutator) Cordon(context.Context, string, bool) error { return nil }
func (f *fakeMutator) Drain(context.Context, string) (int, error) { return 0, nil }
func (f *fakeMutator) Scale(context.Context, kube.ResourceKind, string, string, int32) error {
	return nil
}
func (f *fakeMutator) SetImage(context.Context, kube.ResourceKind, string, string, string, string) error {
	return nil
}
func (f *fakeMutator) SetResources(context.Context, kube.ResourceKind, string, string, string, kube.ResourceEdits, bool) error {
	return nil
}
func (f *fakeMutator) PatchMeta(context.Context, kube.ResourceKind, string, string, bool, string, string, bool) error {
	return nil
}
func (f *fakeMutator) PatchSecretData(context.Context, string, string, string, string, bool) error {
	return nil
}
func (f *fakeMutator) PatchConfigMapData(_ context.Context, namespace, name, key, value string, remove bool) error {
	if f.err != nil {
		return f.err
	}
	if f.cm == nil || f.cm.Name != name || f.cm.Namespace != namespace {
		return nil
	}
	if f.cm.Data == nil {
		f.cm.Data = map[string]string{}
	}
	if remove {
		delete(f.cm.Data, key)
	} else {
		f.cm.Data[key] = value
	}
	return nil
}

func newSession() *tui.Session {
	return &tui.Session{Theme: tui.Dark(), Location: tui.Location{Context: "test-cluster"}}
}

func prodSession() *tui.Session {
	sess := newSession()
	sess.Location.Context = "prod-cluster"
	sess.Config = config.Config{ProdContexts: []string{"prod-cluster"}}
	return sess
}

func step(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c != nil {
				m = step(t, m, c())
			}
		}
		return m
	}
	updated, cmd := m.Update(msg)
	next := *updated.(*Model)
	if cmd != nil {
		return step(t, next, cmd())
	}
	return next
}

func cmObj(namespace, name string, data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data:       data,
	}
}

func deploymentEnvFrom(namespace, name, cmName string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:    "app",
						EnvFrom: []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: cmName}}}},
					}},
				},
			},
		},
	}
}

func statefulSetVolume(namespace, name, cmName string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{{
						Name:         "config",
						VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: cmName}}},
					}},
				},
			},
		},
	}
}

func newModel(t *testing.T, sess *tui.Session, cm *corev1.ConfigMap, mut *fakeMutator, extra map[kube.ResourceKind][]runtime.Object) Model {
	t.Helper()
	objs := map[kube.ResourceKind][]runtime.Object{kube.KindConfigMap: {cm}}
	for k, v := range extra {
		objs[k] = v
	}
	lister := fakeLister{objs: objs}
	var mutator kube.Mutator
	if mut != nil {
		mutator = mut
	}
	m := New(Config{Session: sess, Lister: lister, Mutator: mutator, Namespace: cm.Namespace, Name: cm.Name})
	m.SetSize(120, 36)
	return step(t, m, m.Init()())
}

func TestLoadRendersRowsAndKeyCount(t *testing.T) {
	cm := cmObj("aim-stage", "aim-config", map[string]string{
		"LOG_LEVEL": "info",
		"FEATURE_X": "on",
	})
	m := newModel(t, newSession(), cm, nil, nil)

	if m.state != tui.TaskStateReady {
		t.Fatalf("state = %s, want ready (feedback %q)", m.state, m.feedback)
	}
	if len(m.keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(m.keys))
	}
	body := plain(m.gridBody(m.Theme(), 120, 20))
	if !strings.Contains(body, "info") || !strings.Contains(body, "on") {
		t.Fatalf("expected real plaintext values rendered (ConfigMap values aren't sensitive), got:\n%s", body)
	}
	strip := plain(strings.Join(m.Strips(120), "\n"))
	if !strings.Contains(strip, "2 keys") {
		t.Fatalf("expected the 2 keys strip, got %q", strip)
	}
	if !strings.Contains(strip, "no consumers found") {
		t.Fatalf("expected the empty consumer strip, got %q", strip)
	}
}

func TestConsumerStripListsEnvAndVolumeReferences(t *testing.T) {
	cm := cmObj("aim-stage", "aim-config", map[string]string{"LOG_LEVEL": "info"})
	extra := map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment:  {deploymentEnvFrom("aim-stage", "aim-worker", "aim-config")},
		kube.KindStatefulSet: {statefulSetVolume("aim-stage", "aim-db", "aim-config")},
	}
	m := newModel(t, newSession(), cm, nil, extra)

	if len(m.consumers) != 2 {
		t.Fatalf("expected 2 consumers, got %+v", m.consumers)
	}
	strip := plain(strings.Join(m.Strips(120), "\n"))
	if !strings.Contains(strip, "deploy/aim-worker") || !strings.Contains(strip, "↗ env") {
		t.Fatalf("expected the Deployment env consumer in the strip, got %q", strip)
	}
	if !strings.Contains(strip, "sts/aim-db") || !strings.Contains(strip, "↗ volume") {
		t.Fatalf("expected the StatefulSet volume consumer in the strip, got %q", strip)
	}
}

func TestAddKeyNonProdAppliesImmediatelyAndRefreshes(t *testing.T) {
	cm := cmObj("aim-stage", "aim-config", map[string]string{"LOG_LEVEL": "info"})
	mut := &fakeMutator{cm: cm}
	m := newModel(t, newSession(), cm, mut, nil)

	m = step(t, m, tea.KeyPressMsg{Text: "a"})
	if m.adding == nil {
		t.Fatal("expected 'a' to open the add row")
	}
	for _, r := range "FEATURE_X" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Text: "tab"})
	for _, r := range "on" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Text: "enter"})

	if m.actions.Active() {
		t.Fatal("expected non-prod add to apply immediately, no confirm")
	}
	if m.adding != nil {
		t.Fatal("expected the add row to close after a successful apply")
	}
	if cm.Data["FEATURE_X"] != "on" {
		t.Fatalf("mutator did not receive the add, cm.Data = %+v", cm.Data)
	}
	if m.message != "added FEATURE_X" {
		t.Fatalf("message = %q, want %q", m.message, "added FEATURE_X")
	}
	if len(m.keys) != 2 {
		t.Fatalf("expected the refreshed grid to show 2 keys, got %d", len(m.keys))
	}
	if key, ok := m.selectedKeyRow(); !ok || key.key != "FEATURE_X" {
		t.Fatalf("expected focus to follow the added key, got %+v (ok=%v)", key, ok)
	}
}

func TestAddKeyProdRequiresConfirmThenApplies(t *testing.T) {
	cm := cmObj("aim-stage", "aim-config", nil)
	mut := &fakeMutator{cm: cm}
	m := newModel(t, prodSession(), cm, mut, nil)

	m = step(t, m, tea.KeyPressMsg{Text: "a"})
	for _, r := range "LOG_LEVEL" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Text: "tab"})
	for _, r := range "debug" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Text: "enter"})

	if !m.actions.Active() {
		t.Fatal("expected a PROD add to require confirmation before applying")
	}
	if _, ok := cm.Data["LOG_LEVEL"]; ok {
		t.Fatal("expected the mutator not to be called before confirming")
	}
	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if cm.Data["LOG_LEVEL"] != "debug" {
		t.Fatalf("expected the add applied after confirming, cm.Data = %+v", cm.Data)
	}
	if m.message != "added LOG_LEVEL" {
		t.Fatalf("message = %q, want %q", m.message, "added LOG_LEVEL")
	}
}

func TestEnterOnSingleLineRowEditsInPlaceNonProdAppliesImmediately(t *testing.T) {
	cm := cmObj("aim-stage", "aim-config", map[string]string{"LOG_LEVEL": "info"})
	mut := &fakeMutator{cm: cm}
	m := newModel(t, newSession(), cm, mut, nil)

	m = step(t, m, tea.KeyPressMsg{Text: "enter"})
	if m.editing == nil {
		t.Fatal("expected '↵' to open the in-place edit row")
	}
	if m.editing.value != "info" {
		t.Fatalf("expected the buffer pre-filled with the current value, got %q", m.editing.value)
	}
	for range "info" {
		m = step(t, m, tea.KeyPressMsg{Text: "backspace"})
	}
	for _, r := range "debug" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Text: "enter"})

	if m.editing != nil {
		t.Fatal("expected the edit row to close after a successful apply")
	}
	if cm.Data["LOG_LEVEL"] != "debug" {
		t.Fatalf("expected the edit applied, cm.Data = %+v", cm.Data)
	}
	if m.message != "updated LOG_LEVEL" {
		t.Fatalf("message = %q, want %q", m.message, "updated LOG_LEVEL")
	}
}

func TestCtrlRChainsRolloutRestartOfEveryConsumer(t *testing.T) {
	cm := cmObj("aim-stage", "aim-config", map[string]string{"LOG_LEVEL": "info"})
	mut := &fakeMutator{cm: cm}
	extra := map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment:  {deploymentEnvFrom("aim-stage", "aim-worker", "aim-config")},
		kube.KindStatefulSet: {statefulSetVolume("aim-stage", "aim-db", "aim-config")},
	}
	m := newModel(t, newSession(), cm, mut, extra)
	if len(m.consumers) != 2 {
		t.Fatalf("expected 2 consumers wired for the test, got %+v", m.consumers)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "enter"})
	for range "info" {
		m = step(t, m, tea.KeyPressMsg{Text: "backspace"})
	}
	for _, r := range "debug" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+r"})

	if cm.Data["LOG_LEVEL"] != "debug" {
		t.Fatalf("expected the edit applied, cm.Data = %+v", cm.Data)
	}
	want := map[string]bool{"Deployment/aim-stage/aim-worker": true, "StatefulSet/aim-stage/aim-db": true}
	if len(mut.rolloutRestarts) != 2 {
		t.Fatalf("rolloutRestarts = %v, want 2 entries", mut.rolloutRestarts)
	}
	for _, r := range mut.rolloutRestarts {
		if !want[r] {
			t.Fatalf("unexpected restart target %q, want one of %v", r, want)
		}
	}
	if !strings.Contains(m.message, "restarted 2 consumers") {
		t.Fatalf("message = %q, want it to mention restarting 2 consumers", m.message)
	}
}

func TestRemoveKeyAlwaysRequiresConfirmRegardlessOfProd(t *testing.T) {
	cm := cmObj("aim-stage", "aim-config", map[string]string{
		"LOG_LEVEL": "info",
		"FEATURE_X": "on",
	})
	mut := &fakeMutator{cm: cm}
	m := newModel(t, newSession(), cm, mut, nil) // non-prod

	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+d"})
	if !m.actions.Active() {
		t.Fatal("expected removal to always require an inline y/N, even outside PROD")
	}
	if m.actions.Tier() != actions.TierInline {
		t.Fatalf("Tier() = %v, want TierInline (never the type-the-name modal)", m.actions.Tier())
	}
	m = step(t, m, tea.KeyPressMsg{Text: "y"})
	if _, ok := cm.Data["FEATURE_X"]; ok {
		t.Fatalf("expected FEATURE_X removed, cm.Data = %+v", cm.Data)
	}
	if m.message != "removed FEATURE_X" {
		t.Fatalf("message = %q, want %q", m.message, "removed FEATURE_X")
	}
	if len(m.keys) != 1 || m.keys[0].key != "LOG_LEVEL" {
		t.Fatalf("expected only LOG_LEVEL left, got %+v", m.keys)
	}
}

func TestFailedEditRestoresEditModeWithErrorAndAttemptedValue(t *testing.T) {
	cm := cmObj("aim-stage", "aim-config", map[string]string{"LOG_LEVEL": "info"})
	mut := &fakeMutator{cm: cm, err: errFake{}}
	m := newModel(t, newSession(), cm, mut, nil)

	m = step(t, m, tea.KeyPressMsg{Text: "enter"})
	for range "info" {
		m = step(t, m, tea.KeyPressMsg{Text: "backspace"})
	}
	for _, r := range "debug" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Text: "enter"})

	if m.editing == nil {
		t.Fatal("expected a failed edit to restore the edit row")
	}
	if m.editing.value != "debug" {
		t.Fatalf("expected the attempted value intact, got %q", m.editing.value)
	}
	if m.lastError == "" {
		t.Fatal("expected the server error to be surfaced")
	}
	if cm.Data["LOG_LEVEL"] != "info" {
		t.Fatalf("expected no local mutation on failure, cm.Data = %+v", cm.Data)
	}
}

type errFake struct{}

func (errFake) Error() string { return "configmap is immutable" }

func TestMultilineRowFoldsAndEOpensBufferEditor(t *testing.T) {
	cm := cmObj("aim-stage", "aim-config", map[string]string{
		"nginx.conf": "server {\n  listen 80;\n}",
	})
	m := newModel(t, newSession(), cm, &fakeMutator{cm: cm}, nil)

	body := plain(m.gridBody(m.Theme(), 120, 20))
	if !strings.Contains(body, "3 lines") {
		t.Fatalf("expected a folded '3 lines' summary, got:\n%s", body)
	}
	if strings.Contains(body, "listen 80") {
		t.Fatalf("expected the multi-line value not rendered inline in the grid, got:\n%s", body)
	}

	m = step(t, m, tea.KeyPressMsg{Text: "e"})
	if m.multiline == nil {
		t.Fatal("expected 'e' to open the buffer editor on a multi-line row")
	}
	if len(m.multiline.lines) != 3 || m.multiline.lines[1] != "  listen 80;" {
		t.Fatalf("expected the buffer split into 3 lines, got %+v", m.multiline.lines)
	}
}

func TestEnterOnMultilineRowAlsoOpensBufferEditor(t *testing.T) {
	cm := cmObj("aim-stage", "aim-config", map[string]string{"nginx.conf": "a\nb"})
	m := newModel(t, newSession(), cm, &fakeMutator{cm: cm}, nil)

	m = step(t, m, tea.KeyPressMsg{Text: "enter"})
	if m.multiline == nil {
		t.Fatal("expected '↵' on a multi-line row to open the buffer editor too, not leave a dead key")
	}
	if m.editing != nil {
		t.Fatal("expected the single-line edit row not to open for a multi-line value")
	}
}

func TestMultilineBufferEditorEditsAndAppliesWithCtrlO(t *testing.T) {
	cm := cmObj("aim-stage", "aim-config", map[string]string{"nginx.conf": "a\nb"})
	mut := &fakeMutator{cm: cm}
	m := newModel(t, newSession(), cm, mut, nil)

	m = step(t, m, tea.KeyPressMsg{Text: "e"})
	// Cursor starts at the end of the last line ("b") — append 'c'.
	m = step(t, m, tea.KeyPressMsg{Text: "c"})
	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+o"})

	if m.multiline != nil {
		t.Fatal("expected the buffer editor to close after a successful apply")
	}
	if cm.Data["nginx.conf"] != "a\nbc" {
		t.Fatalf("cm.Data[nginx.conf] = %q, want %q", cm.Data["nginx.conf"], "a\nbc")
	}
	if m.message != "updated nginx.conf" {
		t.Fatalf("message = %q, want %q", m.message, "updated nginx.conf")
	}
}

func TestMultilineBufferEditorEnterInsertsNewline(t *testing.T) {
	cm := cmObj("aim-stage", "aim-config", map[string]string{"nginx.conf": "ab\ncd"})
	m := newModel(t, newSession(), cm, &fakeMutator{cm: cm}, nil)

	m = step(t, m, tea.KeyPressMsg{Text: "e"})
	if m.multiline.row != 1 || m.multiline.col != 2 {
		t.Fatalf("expected the cursor to start at the end of the last line (row=1 col=2), got row=%d col=%d", m.multiline.row, m.multiline.col)
	}
	// Move up onto "ab" (cursor col preserved, capped to line length), then
	// left once so the split lands between 'a' and 'b'.
	m = step(t, m, tea.KeyPressMsg{Text: "up"})
	m = step(t, m, tea.KeyPressMsg{Text: "left"})
	m = step(t, m, tea.KeyPressMsg{Text: "enter"})

	if len(m.multiline.lines) != 3 || m.multiline.lines[0] != "a" || m.multiline.lines[1] != "b" || m.multiline.lines[2] != "cd" {
		t.Fatalf("expected enter to split \"ab\" into [a b cd], got %+v", m.multiline.lines)
	}
	if m.multiline.row != 1 || m.multiline.col != 0 {
		t.Fatalf("expected cursor at row=1 col=0, got row=%d col=%d", m.multiline.row, m.multiline.col)
	}
}

func TestMultilineBufferEditorEscDiscardsWithoutApplying(t *testing.T) {
	cm := cmObj("aim-stage", "aim-config", map[string]string{"nginx.conf": "a\nb"})
	mut := &fakeMutator{cm: cm}
	m := newModel(t, newSession(), cm, mut, nil)

	m = step(t, m, tea.KeyPressMsg{Text: "e"})
	m = step(t, m, tea.KeyPressMsg{Text: "c"})
	m = step(t, m, tea.KeyPressMsg{Text: "esc"})

	if m.multiline != nil {
		t.Fatal("expected esc to close the buffer editor")
	}
	if cm.Data["nginx.conf"] != "a\nb" {
		t.Fatalf("expected no mutation on esc, cm.Data = %+v", cm.Data)
	}
}

// TestKeybarGoesOfflineAndHidesAddRemove pins the cross-cutting 4a fix
// (docs/design README.md §52, §301): configmapdata must show the OFFLINE
// pill and drop add/remove from the keybar while disconnected, not just
// browse.
func TestKeybarGoesOfflineAndHidesAddRemove(t *testing.T) {
	cm := cmObj("aim-stage", "aim-config", map[string]string{"LOG_LEVEL": "info"})
	m := newModel(t, newSession(), cm, &fakeMutator{cm: cm}, nil)

	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnReconnecting, Err: "dial timeout"})
	kb := m.Keybar()
	if kb.Pill != tui.ModeOffline || kb.PillText != "OFFLINE" {
		t.Fatalf("Pill/PillText = %v/%q while offline, want ModeOffline/OFFLINE", kb.Pill, kb.PillText)
	}
	for _, g := range kb.Groups {
		for _, h := range g {
			if h.Label == "add key" || h.Label == "remove key" {
				t.Fatalf("expected add/remove hints hidden while offline, got groups %+v", kb.Groups)
			}
		}
	}

	// Begin() itself refuses to execute while offline (actions.Controller's
	// own gate) — a PROD add's confirm never even applies.
	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnConnected})
	m = step(t, m, tea.KeyPressMsg{Text: "a"})
	for _, r := range "FEATURE_X" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Text: "tab"})
	for _, r := range "on" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnReconnecting, Err: "dial timeout"})
	_ = step(t, m, tea.KeyPressMsg{Text: "enter"})
	if cm.Data["FEATURE_X"] != "" {
		t.Fatalf("expected the add refused while offline, cm.Data = %+v", cm.Data)
	}
}
