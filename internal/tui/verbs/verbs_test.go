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
		{RolloutRestart, actions.TierNone},
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

	for _, v := range All {
		if v.Mutating && v.Tier == actions.TierNone && v.ID != "rollout-restart" && v.ID != "cordon" && v.ID != "scale" {
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

func TestTierForLeavesNonInlineVerbsAlone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		verb Verb
		want actions.Tier
	}{
		{Drain, actions.TierModal},
		{ForceDelete, actions.TierModal},
		{Cordon, actions.TierNone},
		{RolloutRestart, actions.TierNone},
	}
	for _, tt := range tests {
		for _, isProd := range []bool{false, true} {
			if got := TierFor(tt.verb, isProd); got != tt.want {
				t.Errorf("TierFor(%s, isProd=%v) = %v, want %v (unaffected by prod)", tt.verb.ID, isProd, got, tt.want)
			}
		}
	}
}
