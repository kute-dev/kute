package verbs

import (
	"testing"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
)

func TestHint(t *testing.T) {
	t.Parallel()
	got := Delete.Hint()
	want := tui.KeyHint{Key: "ctrl-d", Label: "delete"}
	if got != want {
		t.Fatalf("Hint() = %+v, want %+v", got, want)
	}
}

func TestEditHint(t *testing.T) {
	t.Parallel()
	got := Edit.Hint()
	want := tui.KeyHint{Key: "E", Label: "edit"}
	if got != want {
		t.Fatalf("Hint() = %+v, want %+v", got, want)
	}
}

// TestHiddenWhileOffline pins the cross-cutting fix for the dead Mutating
// field: HiddenWhileOffline is the one real (non-test) consumer, called by
// poddetail/nodedetail/helmhistory/objectdetail's Keybar() instead of each
// hardcoding which verbs need the offline gate.
func TestHiddenWhileOffline(t *testing.T) {
	t.Parallel()
	if !Delete.HiddenWhileOffline(true) {
		t.Fatal("expected a Mutating verb hidden while offline")
	}
	if Delete.HiddenWhileOffline(false) {
		t.Fatal("expected a Mutating verb shown while online")
	}
	if Goto.HiddenWhileOffline(true) {
		t.Fatal("expected a non-Mutating verb never hidden, even offline")
	}
}

func TestAppliesTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		verb Verb
		kind kube.ResourceKind
		want bool
	}{
		{"nil kinds applies to any kind", Delete, kube.KindConfigMap, true},
		{"restricted kinds match", Logs, kube.KindPod, true},
		{"restricted kinds reject others", Logs, kube.KindNode, false},
		{"drain restricted to node", Drain, kube.KindNode, true},
		{"drain rejects pod", Drain, kube.KindPod, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.verb.AppliesTo(tt.kind); got != tt.want {
				t.Fatalf("AppliesTo(%s) = %v, want %v", tt.kind, got, tt.want)
			}
		})
	}
}

func TestByID(t *testing.T) {
	t.Parallel()

	if v, ok := ByID("delete"); !ok || v.ID != "delete" {
		t.Fatalf("ByID(delete) = %+v, %v", v, ok)
	}
	if _, ok := ByID("does-not-exist"); ok {
		t.Fatalf("ByID(does-not-exist) unexpectedly found")
	}
}

func TestTiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		verb Verb
		want actions.Tier
	}{
		{Delete, actions.TierInline},
		{ForceDelete, actions.TierModal},
		{Drain, actions.TierModal},
		{RolloutRestart, actions.TierInline},
		{Cordon, actions.TierNone},
	}
	for _, tt := range tests {
		if tt.verb.Tier != tt.want {
			t.Errorf("%s tier = %v, want %v", tt.verb.ID, tt.verb.Tier, tt.want)
		}
	}
}

func TestMutatingVerbsCoverAllRegisteredWriteOps(t *testing.T) {
	t.Parallel()

	// Mutating verbs are expected to be tiered (an inline y/N or a
	// type-the-name modal). These are the deliberate exceptions: reversible-
	// and-immediate verbs whose own editor panel is the confirmation step,
	// plus the three tty-handoff verbs, which have no Tier because kubectl
	// owns the session once kute suspends (edit's PROD y/N comes from
	// TierForEdit instead).
	untiered := map[string]bool{
		"cordon": true, "scale": true, "set-image": true, "set-resources": true,
		"meta": true, "add-secret-key": true, "add-configmap-key": true,
		"restart-configmap-consumers": true,
		"exec":                        true, "node-shell": true, "edit": true,
		// §30a. Suspend is Cordon's exact shape: reversible and immediate,
		// and resume restores the prior state exactly. Reconcile requests
		// the sync Flux would have run on its own interval anyway, so it
		// makes no change the controller wasn't already going to make —
		// the one property that lets it keep a bare letter (see
		// FluxReconcile's own comment for why that doesn't generalize).
		"flux-suspend": true, "flux-reconcile": true,
		// §33a. CronJobSuspend is FluxSuspend's exact shape: it only pauses
		// future scheduling, touches nothing already running, and resume
		// restores the prior state exactly (see CronJobSuspend's own doc
		// comment, contrasting it with JobSuspend's TierInline). CronJobSetSchedule
		// is set-image's shape: its own inline panel (cronjobschedule.go) is
		// the confirmation step, TierNone here is nominal — the real
		// PROD-sensitive tier comes from TierForCronJobSetSchedule.
		"cronjob-suspend": true, "cronjob-set-schedule": true,
	}
	for _, v := range All {
		if v.Mutating && v.Tier == actions.TierNone && !untiered[v.ID] {
			t.Errorf("%s is mutating with TierNone but isn't an allow-listed reversible verb", v.ID)
		}
	}
}

func TestTierForEscalatesInlineToModalInProd(t *testing.T) {
	t.Parallel()

	if got := TierFor(Delete, false); got != actions.TierInline {
		t.Errorf("TierFor(Delete, non-prod) = %v, want TierInline", got)
	}
	if got := TierFor(Delete, true); got != actions.TierModal {
		t.Errorf("TierFor(Delete, prod) = %v, want TierModal (escalated)", got)
	}
	if got := TierFor(RolloutRestart, false); got != actions.TierInline {
		t.Errorf("TierFor(RolloutRestart, non-prod) = %v, want TierInline", got)
	}
	if got := TierFor(RolloutRestart, true); got != actions.TierModal {
		t.Errorf("TierFor(RolloutRestart, prod) = %v, want TierModal (escalated)", got)
	}
}

func TestTierForEdit(t *testing.T) {
	t.Parallel()

	if got := TierForEdit(false); got != actions.TierNone {
		t.Errorf("TierForEdit(non-prod) = %v, want TierNone", got)
	}
	if got := TierForEdit(true); got != actions.TierInline {
		t.Errorf("TierForEdit(prod) = %v, want TierInline", got)
	}
}

func TestTierForSetImage(t *testing.T) {
	t.Parallel()

	if got := TierForSetImage(false); got != actions.TierNone {
		t.Errorf("TierForSetImage(non-prod) = %v, want TierNone", got)
	}
	if got := TierForSetImage(true); got != actions.TierInline {
		t.Errorf("TierForSetImage(prod) = %v, want TierInline", got)
	}
}

func TestTierForSetResources(t *testing.T) {
	t.Parallel()

	if got := TierForSetResources(false); got != actions.TierNone {
		t.Errorf("TierForSetResources(non-prod) = %v, want TierNone", got)
	}
	if got := TierForSetResources(true); got != actions.TierInline {
		t.Errorf("TierForSetResources(prod) = %v, want TierInline", got)
	}
}

func TestTierForLeavesNonInlineVerbsAlone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		verb Verb
		want actions.Tier
	}{
		{Drain, actions.TierModal},
		{ForceDelete, actions.TierModal},
		{Cordon, actions.TierNone},
	}
	for _, tt := range tests {
		for _, isProd := range []bool{false, true} {
			if got := TierFor(tt.verb, isProd); got != tt.want {
				t.Errorf("TierFor(%s, isProd=%v) = %v, want %v (unaffected by prod)", tt.verb.ID, isProd, got, tt.want)
			}
		}
	}
}

// TestTtyHandoffVerbsAreGatedOffline pins docs/design README.md §4a's
// "delete/exec/edit verbs are disabled while offline" for the three verbs
// that reach the cluster through a kubectl subprocess rather than
// kube.Mutator: exec hands the user a prompt they can write from, a node
// shell creates a privileged debugger pod before they type anything, and edit
// applies whatever they save. Forward stays exempt — a port-forward is a
// local session, not a cluster write.
func TestTtyHandoffVerbsAreGatedOffline(t *testing.T) {
	t.Parallel()

	for _, v := range []Verb{Exec, NodeShell, Edit} {
		if !v.Mutating {
			t.Errorf("%s should be Mutating so the OFFLINE gate reaches it", v.ID)
		}
		if !v.HiddenWhileOffline(true) {
			t.Errorf("%s should be hidden/refused while offline", v.ID)
		}
		if v.HiddenWhileOffline(false) {
			t.Errorf("%s should be available while connected", v.ID)
		}
	}
	if Forward.HiddenWhileOffline(true) {
		t.Error("Forward should stay available while offline — a local session, not a write")
	}
}
