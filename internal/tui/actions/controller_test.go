package actions

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// typeRune feeds a single character into the type-ahead buffer via
// HandleTypeKey — the test-only equivalent of the old TypeRune(string(r)).
func typeRune(c *Controller, r rune) {
	c.HandleTypeKey(tea.KeyPressMsg{Text: string(r)})
}

// backspace feeds a backspace keypress into the type-ahead buffer via
// HandleTypeKey — the test-only equivalent of the old Backspace().
func backspace(c *Controller) {
	c.HandleTypeKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
}

type fakeMutator struct {
	deleted              []string
	forceDeleted         []string
	metaPatches          []string
	secretDataPatches    []string
	configMapDataPatches []string
	rolloutRestarts      []string
	err                  error
}

func (f *fakeMutator) DeleteResource(_ context.Context, kind kube.ResourceKind, ns, name string) error {
	f.deleted = append(f.deleted, string(kind)+"/"+ns+"/"+name)
	return f.err
}

func (f *fakeMutator) DeleteResourceForced(_ context.Context, kind kube.ResourceKind, ns, name string) error {
	f.forceDeleted = append(f.forceDeleted, string(kind)+"/"+ns+"/"+name)
	return f.err
}

func (f *fakeMutator) RolloutRestart(_ context.Context, kind kube.ResourceKind, ns, name string) error {
	f.rolloutRestarts = append(f.rolloutRestarts, string(kind)+"/"+ns+"/"+name)
	return f.err
}

func (f *fakeMutator) Cordon(context.Context, string, bool) error { return f.err }

func (f *fakeMutator) Drain(context.Context, string) (int, error)              { return 0, f.err }
func (f *fakeMutator) HelmRollback(context.Context, string, string, int) error { return f.err }
func (f *fakeMutator) RolloutUndo(context.Context, string, string, int) error  { return f.err }
func (f *fakeMutator) Scale(context.Context, kube.ResourceKind, string, string, int32) error {
	return f.err
}
func (f *fakeMutator) SetImage(context.Context, kube.ResourceKind, string, string, string, string) error {
	return f.err
}
func (f *fakeMutator) SetResources(context.Context, kube.ResourceKind, string, string, string, kube.ResourceEdits, bool) error {
	return f.err
}
func (f *fakeMutator) PatchMeta(_ context.Context, kind kube.ResourceKind, ns, name string, isAnnotation bool, key, value string, remove bool) error {
	field := "labels"
	if isAnnotation {
		field = "annotations"
	}
	entry := key + "=" + value
	if remove {
		entry = key + "-"
	}
	f.metaPatches = append(f.metaPatches, string(kind)+"/"+ns+"/"+name+" "+field+" "+entry)
	return f.err
}

func (f *fakeMutator) PatchSecretData(_ context.Context, namespace, name, key, value string, remove bool) error {
	entry := key + "=" + value
	if remove {
		entry = key + "-"
	}
	f.secretDataPatches = append(f.secretDataPatches, namespace+"/"+name+" "+entry)
	return f.err
}

func (f *fakeMutator) PatchConfigMapData(_ context.Context, namespace, name, key, value string, remove bool) error {
	entry := key + "=" + value
	if remove {
		entry = key + "-"
	}
	f.configMapDataPatches = append(f.configMapDataPatches, namespace+"/"+name+" "+entry)
	return f.err
}

func deleteAction() tui.TaskAction {
	return tui.TaskAction{
		ID:    "delete-pod",
		Label: "delete pod api",
		Scope: tui.TaskScope{
			ResourceKind: string(kube.KindPod),
			ResourceName: "api",
			Namespace:    "prod",
			Verb:         "delete",
			IsMutating:   true,
		},
	}
}

func TestBeginRequiresConfirmationBeforeExecuting(t *testing.T) {
	mut := &fakeMutator{}
	c := New(mut)
	if cmd := c.Begin(TierInline, deleteAction()); cmd != nil {
		t.Fatal("Begin with TierInline should not execute immediately")
	}
	if !c.Active() {
		t.Fatalf("state = %q, want confirming", c.State())
	}
	if len(mut.deleted) != 0 {
		t.Fatalf("mutator called before confirmation: %v", mut.deleted)
	}
	if !strings.Contains(c.Prompt(), "prod/api") {
		t.Fatalf("prompt %q missing target", c.Prompt())
	}
}

func TestConfirmExecutesThroughMutator(t *testing.T) {
	mut := &fakeMutator{}
	c := New(mut)
	c.Begin(TierInline, deleteAction())
	cmd := c.Confirm()
	if cmd == nil {
		t.Fatal("Confirm should return an execution command")
	}
	msg, ok := cmd().(ResultMsg)
	if !ok {
		t.Fatalf("command returned %T, want ResultMsg", cmd())
	}
	if msg.Err != nil {
		t.Fatalf("unexpected error: %v", msg.Err)
	}
	if len(mut.deleted) != 1 || mut.deleted[0] != "Pod/prod/api" {
		t.Fatalf("deleted = %v, want [Pod/prod/api]", mut.deleted)
	}
	c.HandleResult(msg)
	if c.State() != tui.TaskStateSuccess {
		t.Fatalf("state = %q, want success", c.State())
	}
}

func TestCancelDoesNotExecute(t *testing.T) {
	mut := &fakeMutator{}
	c := New(mut)
	c.Begin(TierInline, deleteAction())
	c.Cancel()
	if c.Active() {
		t.Fatal("still active after cancel")
	}
	if c.State() != tui.TaskStateCancelled {
		t.Fatalf("state = %q, want cancelled", c.State())
	}
	if len(mut.deleted) != 0 {
		t.Fatalf("mutator called after cancel: %v", mut.deleted)
	}
}

func TestHandleResultSurfacesError(t *testing.T) {
	mut := &fakeMutator{err: errors.New("forbidden")}
	c := New(mut)
	c.Begin(TierInline, deleteAction())
	msg := c.Confirm()().(ResultMsg)
	c.HandleResult(msg)
	if c.State() != tui.TaskStateError {
		t.Fatalf("state = %q, want error", c.State())
	}
	if !strings.Contains(c.Message(), "forbidden") {
		t.Fatalf("message %q missing cause", c.Message())
	}
}

func TestBeginRejectsIncompleteScope(t *testing.T) {
	c := New(&fakeMutator{})
	c.Begin(TierInline, tui.TaskAction{ID: "x", Label: "bad", Scope: tui.TaskScope{Verb: "delete"}})
	if c.State() != tui.TaskStateError {
		t.Fatalf("state = %q, want error for incomplete scope", c.State())
	}
	if c.Active() {
		t.Fatal("incomplete action should not enter confirmation")
	}
}

func TestNilMutatorReportsUnconfigured(t *testing.T) {
	c := New(nil)
	c.Begin(TierInline, deleteAction())
	if c.State() != tui.TaskStateError {
		t.Fatalf("state = %q, want error", c.State())
	}
	if !strings.Contains(c.Message(), "not configured") {
		t.Fatalf("message %q", c.Message())
	}
}

func TestBeginRefusesWhileOffline(t *testing.T) {
	mut := &fakeMutator{}
	c := New(mut)
	c.SetOffline(true)

	if cmd := c.Begin(TierNone, deleteAction()); cmd != nil {
		t.Fatal("Begin should not return an execution command while offline")
	}
	if c.Active() {
		t.Fatal("Begin should not enter the confirming state while offline")
	}
	if c.State() != tui.TaskStateError {
		t.Fatalf("state = %q, want error", c.State())
	}
	if !strings.Contains(c.Message(), "offline") {
		t.Fatalf("message %q, want mention of offline", c.Message())
	}
	if len(mut.deleted) != 0 {
		t.Fatalf("mutator must not run while offline, got %v", mut.deleted)
	}

	c.SetOffline(false)
	if cmd := c.Begin(TierNone, deleteAction()); cmd == nil {
		t.Fatal("expected Begin to execute again once back online")
	}
}

func TestBeginTierNoneExecutesImmediately(t *testing.T) {
	mut := &fakeMutator{}
	c := New(mut)
	cmd := c.Begin(TierNone, deleteAction())
	if cmd == nil {
		t.Fatal("expected TierNone to return an execution command immediately")
	}
	if c.Active() {
		t.Fatal("expected TierNone to skip the confirming state entirely")
	}
	msg := cmd().(ResultMsg)
	if msg.Err != nil {
		t.Fatalf("unexpected error: %v", msg.Err)
	}
	if len(mut.deleted) != 1 {
		t.Fatalf("expected the mutator called once, got %v", mut.deleted)
	}
}

func TestExecuteDispatchesSetMetaToPatchMeta(t *testing.T) {
	mut := &fakeMutator{}
	c := New(mut)
	cmd := c.Begin(TierNone, tui.TaskAction{
		ID:    "set-meta-default/aim-worker/env",
		Label: "Set label env on aim-worker?",
		Scope: tui.TaskScope{
			ResourceKind: string(kube.KindDeployment), ResourceName: "aim-worker", Namespace: "default",
			Verb: "set-meta", IsMutating: true,
			MetaKey: "env", MetaValue: "staging", MetaOverwrite: true,
		},
	})
	if cmd == nil {
		t.Fatal("expected a TierNone set-meta action to return an execution command")
	}
	msg := cmd().(ResultMsg)
	if msg.Err != nil {
		t.Fatalf("unexpected error: %v", msg.Err)
	}
	if len(mut.metaPatches) != 1 || mut.metaPatches[0] != "Deployment/default/aim-worker labels env=staging" {
		t.Fatalf("metaPatches = %v, want one Deployment/default/aim-worker labels env=staging patch", mut.metaPatches)
	}
}

func TestExecuteDispatchesSecretDataToPatchSecretData(t *testing.T) {
	mut := &fakeMutator{}
	c := New(mut)
	cmd := c.Begin(TierNone, tui.TaskAction{
		ID:    "add-secret-key-default/aim-secrets/SMTP_PASSWORD",
		Label: "Add key SMTP_PASSWORD to aim-secrets?",
		Scope: tui.TaskScope{
			ResourceKind: string(kube.KindSecret), ResourceName: "aim-secrets", Namespace: "default",
			Verb: "secret-data", IsMutating: true,
			SecretKey: "SMTP_PASSWORD", SecretValue: "hunter2-staging",
		},
	})
	if cmd == nil {
		t.Fatal("expected a TierNone secret-data action to return an execution command")
	}
	msg := cmd().(ResultMsg)
	if msg.Err != nil {
		t.Fatalf("unexpected error: %v", msg.Err)
	}
	if len(mut.secretDataPatches) != 1 || mut.secretDataPatches[0] != "default/aim-secrets SMTP_PASSWORD=hunter2-staging" {
		t.Fatalf("secretDataPatches = %v, want one default/aim-secrets SMTP_PASSWORD=hunter2-staging patch", mut.secretDataPatches)
	}
}

func TestExecuteDispatchesConfigMapDataToPatchConfigMapData(t *testing.T) {
	mut := &fakeMutator{}
	c := New(mut)
	cmd := c.Begin(TierNone, tui.TaskAction{
		ID:    "add-configmap-key-default/aim-config/LOG_LEVEL",
		Label: "Add key LOG_LEVEL to aim-config?",
		Scope: tui.TaskScope{
			ResourceKind: string(kube.KindConfigMap), ResourceName: "aim-config", Namespace: "default",
			Verb: "configmap-data", IsMutating: true,
			ConfigMapKey: "LOG_LEVEL", ConfigMapValue: "debug",
		},
	})
	if cmd == nil {
		t.Fatal("expected a TierNone configmap-data action to return an execution command")
	}
	msg := cmd().(ResultMsg)
	if msg.Err != nil {
		t.Fatalf("unexpected error: %v", msg.Err)
	}
	if len(mut.configMapDataPatches) != 1 || mut.configMapDataPatches[0] != "default/aim-config LOG_LEVEL=debug" {
		t.Fatalf("configMapDataPatches = %v, want one default/aim-config LOG_LEVEL=debug patch", mut.configMapDataPatches)
	}
	if len(mut.rolloutRestarts) != 0 {
		t.Fatalf("rolloutRestarts = %v, want none for a plain apply", mut.rolloutRestarts)
	}
}

// TestExecuteConfigMapDataChainsRolloutRestart pins 27a's ctrl-r behavior:
// the patch runs, then every consumer in ConfigMapConsumers gets its own
// RolloutRestart call, kind carried through so a StatefulSet/DaemonSet
// consumer restarts correctly rather than being coerced to Deployment.
func TestExecuteConfigMapDataChainsRolloutRestart(t *testing.T) {
	mut := &fakeMutator{}
	c := New(mut)
	cmd := c.Begin(TierNone, tui.TaskAction{
		ID:    "edit-configmap-key-default/aim-config/LOG_LEVEL",
		Label: "Update key LOG_LEVEL on aim-config?",
		Scope: tui.TaskScope{
			ResourceKind: string(kube.KindConfigMap), ResourceName: "aim-config", Namespace: "default",
			Verb: "configmap-data", IsMutating: true,
			ConfigMapKey: "LOG_LEVEL", ConfigMapValue: "debug",
			ConfigMapRestartConsumers: true,
			ConfigMapConsumers: []kube.ConfigMapConsumerRef{
				{Kind: kube.KindDeployment, Name: "aim-worker"},
				{Kind: kube.KindStatefulSet, Name: "aim-db"},
			},
		},
	})
	if cmd == nil {
		t.Fatal("expected a TierNone configmap-data action to return an execution command")
	}
	msg := cmd().(ResultMsg)
	if msg.Err != nil {
		t.Fatalf("unexpected error: %v", msg.Err)
	}
	want := []string{"Deployment/default/aim-worker", "StatefulSet/default/aim-db"}
	if len(mut.rolloutRestarts) != len(want) || mut.rolloutRestarts[0] != want[0] || mut.rolloutRestarts[1] != want[1] {
		t.Fatalf("rolloutRestarts = %v, want %v", mut.rolloutRestarts, want)
	}
}

func TestBeginTierInlineConfirmsWithoutTypedName(t *testing.T) {
	mut := &fakeMutator{}
	c := New(mut)
	c.Begin(TierInline, deleteAction())
	if c.Tier() != TierInline {
		t.Fatalf("Tier() = %v, want TierInline", c.Tier())
	}
	// TierInline's Confirm must work with no typed name at all — it's a
	// bare y/N prompt, not the type-the-name modal.
	cmd := c.Confirm()
	if cmd == nil {
		t.Fatal("expected TierInline Confirm to execute without a typed name")
	}
	cmd()
	if len(mut.deleted) != 1 {
		t.Fatalf("expected the mutator called once, got %v", mut.deleted)
	}
}

func TestBeginTierModalRequiresNameMatch(t *testing.T) {
	mut := &fakeMutator{}
	c := New(mut)
	c.Begin(TierModal, deleteAction())
	if c.Tier() != TierModal {
		t.Fatalf("Tier() = %v, want TierModal", c.Tier())
	}

	if cmd := c.Confirm(); cmd != nil {
		t.Fatal("expected Confirm to no-op before any name is typed")
	}
	for _, r := range "ap" {
		typeRune(&c, r)
	}
	if cmd := c.Confirm(); cmd != nil {
		t.Fatal("expected Confirm to no-op on a partial match")
	}
	if len(mut.deleted) != 0 {
		t.Fatalf("expected no delete yet, got %v", mut.deleted)
	}

	typeRune(&c, 'i')
	if !c.NameMatches() {
		t.Fatalf("expected NameMatches once typed == %q, got typed %q", "api", c.TypedName())
	}
	cmd := c.Confirm()
	if cmd == nil {
		t.Fatal("expected Confirm to execute once the name matches")
	}
	msg := cmd().(ResultMsg)
	if msg.Err != nil {
		t.Fatalf("unexpected error: %v", msg.Err)
	}
	if len(mut.deleted) != 1 || mut.deleted[0] != "Pod/prod/api" {
		t.Fatalf("deleted = %v, want [Pod/prod/api]", mut.deleted)
	}
}

func TestBackspaceRemovesLastRuneUnicodeSafe(t *testing.T) {
	c := New(&fakeMutator{})
	c.Begin(TierModal, deleteAction())
	for _, r := range "aβc" {
		typeRune(&c, r)
	}
	if c.TypedName() != "aβc" {
		t.Fatalf("TypedName() = %q, want %q", c.TypedName(), "aβc")
	}
	backspace(&c)
	if c.TypedName() != "aβ" {
		t.Fatalf("TypedName() after Backspace = %q, want %q", c.TypedName(), "aβ")
	}
}

func TestTypeRuneAndBackspaceNoOpOutsideTierModal(t *testing.T) {
	c := New(&fakeMutator{})
	c.Begin(TierInline, deleteAction())
	typeRune(&c, 'x')
	if c.TypedName() != "" {
		t.Fatalf("expected TypeRune to no-op for TierInline, got %q", c.TypedName())
	}
}

func TestEscalateSwitchesToForceDelete(t *testing.T) {
	mut := &fakeMutator{}
	c := New(mut)
	c.Begin(TierModal, deleteAction())
	c.Escalate()
	for _, r := range "api" {
		typeRune(&c, r)
	}
	cmd := c.Confirm()
	if cmd == nil {
		t.Fatal("expected Confirm to execute after Escalate + name match")
	}
	cmd()
	if len(mut.forceDeleted) != 1 || mut.forceDeleted[0] != "Pod/prod/api" {
		t.Fatalf("forceDeleted = %v, want [Pod/prod/api]", mut.forceDeleted)
	}
	if len(mut.deleted) != 0 {
		t.Fatalf("expected the plain delete path untouched, got %v", mut.deleted)
	}
}

func TestEscalateNoOpsForNonPodDelete(t *testing.T) {
	c := New(&fakeMutator{})
	c.Begin(TierModal, tui.TaskAction{
		ID: "delete-deploy", Label: "delete deployment api",
		Scope: tui.TaskScope{ResourceKind: "Deployment", ResourceName: "api", Namespace: "prod", Verb: "delete", IsMutating: true},
	})
	c.Escalate()
	if c.Pending().Scope.Verb != "delete" {
		t.Fatalf("expected Escalate to no-op for a non-Pod delete, got verb %q", c.Pending().Scope.Verb)
	}
}

func TestEscalateNoOpsForDrain(t *testing.T) {
	c := New(&fakeMutator{})
	c.Begin(TierModal, tui.TaskAction{
		ID: "drain-node", Label: "drain node-a",
		Scope: tui.TaskScope{ResourceKind: string(kube.KindNode), ResourceName: "node-a", Verb: "drain", IsMutating: true},
	})
	c.Escalate()
	if c.Pending().Scope.Verb != "drain" {
		t.Fatalf("expected Escalate to no-op for a drain, got verb %q", c.Pending().Scope.Verb)
	}
}

// TestArmForceDeleteStagesWithoutExecuting covers the non-prod inline
// counterpart to Escalate: ctrl-k on a TierInline Pod delete must not run
// anything by itself — DeleteResourceForced only fires once "y" (Confirm)
// follows, same as a plain delete needs "y" after ctrl-d.
func TestArmForceDeleteStagesWithoutExecuting(t *testing.T) {
	mut := &fakeMutator{}
	c := New(mut)
	c.Begin(TierInline, deleteAction())
	c.ArmForceDelete()
	if !c.ForceArmed() {
		t.Fatal("expected ForceArmed() = true after ArmForceDelete")
	}
	if len(mut.deleted) != 0 || len(mut.forceDeleted) != 0 {
		t.Fatalf("expected ArmForceDelete alone to run nothing, deleted=%v forceDeleted=%v", mut.deleted, mut.forceDeleted)
	}
	// The pending verb itself stays "delete" until Confirm actually runs —
	// only the staged flag flips, so a stray read of Pending() mid-arm
	// doesn't see a verb that hasn't executed yet.
	if c.Pending().Scope.Verb != "delete" {
		t.Fatalf("expected the pending verb to stay \"delete\" while armed, got %q", c.Pending().Scope.Verb)
	}

	cmd := c.Confirm()
	if cmd == nil {
		t.Fatal("expected Confirm to return a command once armed")
	}
	cmd()
	if len(mut.forceDeleted) != 1 || mut.forceDeleted[0] != "Pod/prod/api" {
		t.Fatalf("forceDeleted = %v, want [Pod/prod/api]", mut.forceDeleted)
	}
	if len(mut.deleted) != 0 {
		t.Fatalf("expected the plain delete path untouched, got %v", mut.deleted)
	}
}

// TestDisarmForceDeleteBacksOutWithoutCancelling covers "n" while armed:
// it must return to the plain delete prompt (still Active/TierInline, still
// the same pending target), not cancel the confirm outright.
func TestDisarmForceDeleteBacksOutWithoutCancelling(t *testing.T) {
	mut := &fakeMutator{}
	c := New(mut)
	c.Begin(TierInline, deleteAction())
	c.ArmForceDelete()
	c.DisarmForceDelete()
	if c.ForceArmed() {
		t.Fatal("expected ForceArmed() = false after DisarmForceDelete")
	}
	if !c.Active() || c.Pending() == nil {
		t.Fatal("expected DisarmForceDelete to leave the confirm active, not cancel it")
	}

	cmd := c.Confirm()
	if cmd == nil {
		t.Fatal("expected Confirm to still work after disarming")
	}
	cmd()
	if len(mut.deleted) != 1 || mut.deleted[0] != "Pod/prod/api" {
		t.Fatalf("deleted = %v, want [Pod/prod/api] (the plain delete, not force)", mut.deleted)
	}
	if len(mut.forceDeleted) != 0 {
		t.Fatalf("expected no force-delete after disarming, got %v", mut.forceDeleted)
	}
}

// TestCancelClearsForceArmed covers esc while armed: the whole confirm ends,
// forceArmed doesn't leak into the next Begin.
func TestCancelClearsForceArmed(t *testing.T) {
	c := New(&fakeMutator{})
	c.Begin(TierInline, deleteAction())
	c.ArmForceDelete()
	c.Cancel()
	if c.ForceArmed() {
		t.Fatal("expected Cancel to clear ForceArmed")
	}
	if c.Active() {
		t.Fatal("expected Cancel to end the confirm entirely, even while armed")
	}
}

// TestArmForceDeleteNoOpsAtTierModal: the PROD path keeps using Escalate,
// not this — ArmForceDelete must stay inert there so the two mechanisms
// never fight over the same pending action.
func TestArmForceDeleteNoOpsAtTierModal(t *testing.T) {
	c := New(&fakeMutator{})
	c.Begin(TierModal, deleteAction())
	c.ArmForceDelete()
	if c.ForceArmed() {
		t.Fatal("expected ArmForceDelete to no-op at TierModal")
	}
	if c.Pending().Scope.Verb != "delete" {
		t.Fatalf("expected the pending verb untouched, got %q", c.Pending().Scope.Verb)
	}
}

// TestArmForceDeleteNoOpsForNonPodDelete mirrors Escalate's own kind gate —
// force-delete is Pod-only (verbs.ForceDelete.Kinds).
func TestArmForceDeleteNoOpsForNonPodDelete(t *testing.T) {
	c := New(&fakeMutator{})
	c.Begin(TierInline, tui.TaskAction{
		ID: "delete-deploy", Label: "delete deployment api",
		Scope: tui.TaskScope{ResourceKind: "Deployment", ResourceName: "api", Namespace: "prod", Verb: "delete", IsMutating: true},
	})
	c.ArmForceDelete()
	if c.ForceArmed() {
		t.Fatal("expected ArmForceDelete to no-op for a non-Pod delete")
	}
}
