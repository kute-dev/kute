package actions

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

func keyPress(s string) tea.KeyPressMsg {
	switch s {
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "ctrl+k":
		return tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

// TestConfirmKeysAreTheRegistryKeys pins the keyPress helper (and so every
// test below) to the strings HandleConfirmKey matches.
func TestConfirmKeysAreTheRegistryKeys(t *testing.T) {
	for _, v := range []verbs.Verb{verbs.ConfirmAccept, verbs.ConfirmDecline, verbs.ConfirmCancel, verbs.ConfirmSubmit, verbs.ForceDelete} {
		if got := keyPress(v.Key).String(); got != v.Key {
			t.Errorf("%s: key press reads %q, registry binds %q", v.ID, got, v.Key)
		}
	}
}

func TestHandleConfirmKeyInline(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want ConfirmOutcome
	}{
		{"y", ConfirmExecuted},
		{"n", ConfirmCancelled},
		{"esc", ConfirmCancelled},
		{"enter", ConfirmSwallowed},
		{"x", ConfirmSwallowed},
	} {
		mut := &fakeMutator{}
		c := New(mut)
		c.Begin(Request{Verb: verbs.Delete}, deleteOf(kube.KindDeployment))
		cmd, got := c.HandleConfirmKey(keyPress(tc.key))
		if got != tc.want {
			t.Errorf("%q: outcome %v, want %v", tc.key, got, tc.want)
		}
		if (cmd != nil) != (tc.want == ConfirmExecuted) {
			t.Errorf("%q: cmd=%v for outcome %v", tc.key, cmd != nil, got)
		}
		if c.Active() != (tc.want == ConfirmSwallowed) {
			t.Errorf("%q: Active=%v after %v", tc.key, c.Active(), got)
		}
	}
}

// TestHandleConfirmKeyModalCard: a TierModal confirm that isn't typed
// (Drain) is a y/N card — y/n answer it, they aren't typed.
func TestHandleConfirmKeyModalCard(t *testing.T) {
	drain := deleteOf(kube.KindNode)
	drain.Scope.Verb = verbs.Drain.ID
	c := New(&fakeMutator{})
	c.Begin(Request{Verb: verbs.Drain}, drain)
	if c.Tier() != TierModal || c.TypeToConfirm() {
		t.Fatalf("drain should be a modal y/N card")
	}
	if _, got := c.HandleConfirmKey(keyPress("enter")); got != ConfirmSwallowed || !c.Active() {
		t.Fatalf("enter on a y/N card: %v", got)
	}
	if cmd, got := c.HandleConfirmKey(keyPress("y")); got != ConfirmExecuted || cmd == nil {
		t.Fatalf("y on a y/N card: %v", got)
	}
}

func TestHandleConfirmKeyTypeTheName(t *testing.T) {
	c := New(&fakeMutator{})
	c.Begin(Request{Verb: verbs.Delete, Prod: true}, deleteOf(kube.KindDeployment))
	if !c.TypeToConfirm() {
		t.Fatal("PROD delete should type the name")
	}
	for _, k := range []string{"y", "n", "a", "p", "x", "backspace", "i"} {
		if _, got := c.HandleConfirmKey(keyPress(k)); got != ConfirmTyped {
			t.Fatalf("%q: outcome %v, want typed", k, got)
		}
	}
	if c.TypedName() != "ynapi" {
		t.Fatalf("typed %q", c.TypedName())
	}
	if _, got := c.HandleConfirmKey(keyPress("enter")); got != ConfirmSwallowed || !c.Active() {
		t.Fatalf("enter before the name matches: %v", got)
	}
	for range "ynapi" {
		c.HandleConfirmKey(keyPress("backspace"))
	}
	for _, r := range "api" {
		c.HandleConfirmKey(keyPress(string(r)))
	}
	if cmd, got := c.HandleConfirmKey(keyPress("enter")); got != ConfirmExecuted || cmd == nil {
		t.Fatalf("enter once the name matches: %v", got)
	}

	c.Begin(Request{Verb: verbs.Delete, Prod: true}, deleteOf(kube.KindDeployment))
	if _, got := c.HandleConfirmKey(keyPress("esc")); got != ConfirmCancelled || c.Active() {
		t.Fatalf("esc: %v", got)
	}
}

// bulkDeleteOf is deleteOf for a marked set of two kind objects in "dev".
func bulkDeleteOf(kind kube.ResourceKind) tui.TaskAction {
	bulk := deleteOf(kind)
	bulk.Label = "Delete things"
	bulk.Scope.ResourceName = ""
	bulk.Scope.BulkTargets = []tui.BulkTarget{{Namespace: "dev", ResourceName: "a"}, {Namespace: "dev", ResourceName: "b"}}
	return bulk
}

// TestHandleConfirmKeyCountTakesDigitsOnly: a marked set's type-the-count
// buffer refuses letters but keeps backspace.
func TestHandleConfirmKeyCountTakesDigitsOnly(t *testing.T) {
	c := New(&fakeMutator{})
	c.Begin(Request{Verb: verbs.Delete, Prod: true}, bulkDeleteOf(kube.KindDeployment))
	if _, got := c.HandleConfirmKey(keyPress("x")); got != ConfirmSwallowed {
		t.Fatalf("a letter into the count: %v", got)
	}
	if _, got := c.HandleConfirmKey(keyPress(verbs.ForceDelete.Key)); got != ConfirmTyped {
		t.Fatalf("a Deployment set has no force delete; ctrl+k reaches the buffer: %v", got)
	}
	c.HandleConfirmKey(keyPress("2"))
	if c.TypedName() != "2" {
		t.Fatalf("typed %q", c.TypedName())
	}
	if _, got := c.HandleConfirmKey(keyPress("enter")); got != ConfirmExecuted {
		t.Fatalf("enter with the count typed: %v", got)
	}
}

func TestHandleConfirmKeyEscalatesForceDelete(t *testing.T) {
	c := New(&fakeMutator{})
	c.Begin(Request{Verb: verbs.Delete}, deleteOf(kube.KindPod))
	want := append(verbs.ConfirmHints(), verbs.ForceDelete.Hint())
	if got := c.ConfirmHints(); !reflect.DeepEqual(got, want) {
		t.Fatalf("hints %v, want %v", got, want)
	}
	if _, got := c.HandleConfirmKey(keyPress(verbs.ForceDelete.Key)); got != ConfirmEscalated {
		t.Fatalf("ctrl+k on a pod delete: %v", got)
	}
	if c.Pending().Scope.Verb != verbs.ForceDelete.ID || !c.TypeToConfirm() {
		t.Fatalf("escalated to %q typed=%v", c.Pending().Scope.Verb, c.TypeToConfirm())
	}
	// Already a force delete: the chord goes to the buffer.
	if _, got := c.HandleConfirmKey(keyPress(verbs.ForceDelete.Key)); got != ConfirmTyped {
		t.Fatalf("ctrl+k inside the force-delete modal: %v", got)
	}
}

// TestHandleConfirmKeyEscalatesBulkForceDelete: ctrl+k on a marked set of
// Pods' inline y/N escalates to the type-the-count modal and force-deletes
// every target once the count is typed.
func TestHandleConfirmKeyEscalatesBulkForceDelete(t *testing.T) {
	mut := &fakeMutator{}
	c := New(mut)
	c.Begin(Request{Verb: verbs.Delete}, bulkDeleteOf(kube.KindPod))
	if c.Tier() != TierInline {
		t.Fatalf("tier %v, want the inline y/N", c.Tier())
	}
	want := append(verbs.ConfirmHints(), verbs.ForceDelete.Hint())
	if got := c.ConfirmHints(); !reflect.DeepEqual(got, want) {
		t.Fatalf("hints %v, want %v", got, want)
	}
	if _, got := c.HandleConfirmKey(keyPress(verbs.ForceDelete.Key)); got != ConfirmEscalated {
		t.Fatalf("ctrl+k on a marked pod set: %v", got)
	}
	if p := c.Pending(); p.Scope.Verb != verbs.ForceDelete.ID || p.Label != "Force delete things" || c.Tier() != TierModal || !c.TypeToConfirm() {
		t.Fatalf("escalated to %q %q tier=%v typed=%v", p.Scope.Verb, p.Label, c.Tier(), c.TypeToConfirm())
	}
	if _, got := c.HandleConfirmKey(keyPress("y")); got != ConfirmSwallowed {
		t.Fatalf("y into the count: %v", got)
	}
	c.HandleConfirmKey(keyPress("2"))
	cmd, got := c.HandleConfirmKey(keyPress("enter"))
	if got != ConfirmExecuted || cmd == nil {
		t.Fatalf("enter with the count typed: %v", got)
	}
	msg, ok := cmd().(BulkResultMsg)
	if !ok || len(msg.Results) != 2 {
		t.Fatalf("result %#v", msg)
	}
	if len(mut.deleted) != 0 || !reflect.DeepEqual(mut.forceDeleted, []string{"Pod/dev/a", "Pod/dev/b"}) {
		t.Fatalf("deleted=%v forceDeleted=%v, want both force-deleted", mut.deleted, mut.forceDeleted)
	}
}

// TestHandleConfirmKeyNoForceDeleteForOtherVerbs: the debug panel's CLEAN
// UP is a Pod "delete" Scope, but it isn't Begun as verbs.Delete — it gets
// neither the escalation nor its hint.
func TestHandleConfirmKeyNoForceDeleteForOtherVerbs(t *testing.T) {
	c := New(&fakeMutator{})
	c.Begin(Request{Verb: verbs.DebugCleanup}, deleteOf(kube.KindPod))
	if got := c.ConfirmHints(); !reflect.DeepEqual(got, verbs.ConfirmHints()) {
		t.Fatalf("hints %v", got)
	}
	if _, got := c.HandleConfirmKey(keyPress(verbs.ForceDelete.Key)); got != ConfirmSwallowed || c.Pending().Scope.Verb != verbs.Delete.ID {
		t.Fatalf("ctrl+k on a cleanup: %v", got)
	}
}

func TestHandleConfirmKeyInactive(t *testing.T) {
	c := New(&fakeMutator{})
	if cmd, got := c.HandleConfirmKey(keyPress("y")); cmd != nil || got != ConfirmSwallowed {
		t.Fatalf("inactive: %v", got)
	}
}
