package actions

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

type fakeMutator struct {
	deleted      []string
	forceDeleted []string
	metaPatches  []string
	err          error
}

func (f *fakeMutator) DeleteResource(_ context.Context, kind kube.ResourceKind, ns, name string) error {
	f.deleted = append(f.deleted, string(kind)+"/"+ns+"/"+name)
	return f.err
}

func (f *fakeMutator) DeleteResourceForced(_ context.Context, kind kube.ResourceKind, ns, name string) error {
	f.forceDeleted = append(f.forceDeleted, string(kind)+"/"+ns+"/"+name)
	return f.err
}

func (f *fakeMutator) RolloutRestart(context.Context, string, string) error { return f.err }

func (f *fakeMutator) Cordon(context.Context, string, bool) error { return f.err }

func (f *fakeMutator) Drain(context.Context, string) (int, error)              { return 0, f.err }
func (f *fakeMutator) HelmRollback(context.Context, string, string, int) error { return f.err }
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
		c.TypeRune(string(r))
	}
	if cmd := c.Confirm(); cmd != nil {
		t.Fatal("expected Confirm to no-op on a partial match")
	}
	if len(mut.deleted) != 0 {
		t.Fatalf("expected no delete yet, got %v", mut.deleted)
	}

	c.TypeRune("i")
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
		c.TypeRune(string(r))
	}
	if c.TypedName() != "aβc" {
		t.Fatalf("TypedName() = %q, want %q", c.TypedName(), "aβc")
	}
	c.Backspace()
	if c.TypedName() != "aβ" {
		t.Fatalf("TypedName() after Backspace = %q, want %q", c.TypedName(), "aβ")
	}
}

func TestTypeRuneAndBackspaceNoOpOutsideTierModal(t *testing.T) {
	c := New(&fakeMutator{})
	c.Begin(TierInline, deleteAction())
	c.TypeRune("x")
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
		c.TypeRune(string(r))
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
