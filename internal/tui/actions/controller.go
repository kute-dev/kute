// Package actions is the shared confirm→execute framework for mutating
// operations. A screen embeds a Controller, calls Begin with the action's
// resolved Tier (verbs.TierFor) when the user presses a mutating key,
// routes confirm-state keys to Confirm/Cancel/TypeRune/Backspace/Escalate
// while the controller is Active, and feeds ResultMsg back through
// HandleResult. Execution runs through kube.Mutator, so no screen calls a
// write verb directly and the confirmation gate is enforced in exactly one
// place.
package actions

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// ResultMsg reports the outcome of an executed action. Screens pass it to
// HandleResult; the ActionID lets a screen ignore results for actions it no
// longer cares about.
type ResultMsg struct {
	ActionID string
	Label    string
	Err      error
}

// Controller holds the state of the at-most-one in-flight action for a screen.
// The zero value (no mutator) is usable: Begin reports that mutations are not
// configured rather than panicking.
type Controller struct {
	mutator kube.Mutator
	pending *tui.TaskAction
	tier    Tier
	// typedName is the type-ahead buffer for a TierModal confirmation
	// (mvp-plan.md §8b: "type the name to confirm") — unused for
	// TierNone/TierInline.
	typedName string
	// forceArmed stages a pending TierInline Pod "delete" for force-delete
	// (ctrl-k) — the non-prod counterpart to Escalate's PROD-modal
	// escalation: staged rather than immediate, so a second "y" is still
	// required to actually execute and "n" backs out of just this
	// sub-state (DisarmForceDelete) rather than cancelling the whole
	// confirm. Confirm reads it to switch the verb that actually executes.
	forceArmed bool
	state      tui.TaskState
	message    string
	// offline mirrors the screen's kube.ConnState.Offline() (docs/design
	// README.md §4a: "mutating actions disabled" while OFFLINE) — set via
	// SetOffline from each screen's ConnStateMsg handler, checked in Begin
	// so the gate lives in the one place every mutating verb funnels
	// through, not duplicated per screen/per-key.
	offline bool
}

// New builds a Controller that executes through mutator (which may be nil).
func New(mutator kube.Mutator) Controller {
	return Controller{mutator: mutator}
}

// SetOffline records whether the connection is mid-outage. Call it from the
// screen's kube.ConnStateMsg handler, passing conn.Offline() — Begin refuses
// to start any mutating action while true.
func (c *Controller) SetOffline(offline bool) {
	c.offline = offline
}

// Active reports whether a confirmation prompt is currently showing.
func (c Controller) Active() bool { return c.state == tui.TaskStateConfirming }

// State is the controller's current state (empty until the first action).
func (c Controller) State() tui.TaskState { return c.state }

// Message is the human-readable status for the current state (prompt, success,
// error, or cancellation text).
func (c Controller) Message() string { return c.message }

// Pending returns the action awaiting confirmation, if any.
func (c Controller) Pending() *tui.TaskAction { return c.pending }

// Tier is the pending/active action's confirmation tier (mvp-plan.md §8b) —
// TierNone never reaches Active(), so this is only meaningful while Active()
// is true.
func (c Controller) Tier() Tier { return c.tier }

// TypedName is the current type-ahead buffer for a TierModal confirmation.
func (c Controller) TypedName() string { return c.typedName }

// NameMatches reports whether TypedName equals the pending action's target
// resource name — the gate Confirm() checks for TierModal.
func (c Controller) NameMatches() bool {
	return c.pending != nil && c.typedName == c.pending.Scope.ResourceName
}

// Begin starts an action at the given tier (resolved by the caller via
// verbs.TierFor — Controller never imports verbs, see mvp-tasks.md's Phase
// 5/8b exit notes on the import-cycle constraint). TierNone executes
// immediately; TierInline/TierModal both move to the confirming state,
// remembering which so screens can render the right surface (inline keybar
// prompt vs. the centered type-the-name modal).
func (c *Controller) Begin(tier Tier, action tui.TaskAction) tea.Cmd {
	if c.offline {
		c.fail("Mutating actions are disabled while offline.")
		return nil
	}
	if c.mutator == nil {
		c.fail("Mutations are not configured.")
		return nil
	}
	if action.Scope.ResourceKind == "" || action.Scope.ResourceName == "" || action.Scope.Verb == "" {
		c.fail("Cannot run " + action.Label + ": missing target metadata.")
		return nil
	}
	c.pending = &action
	c.tier = tier
	c.typedName = ""
	c.forceArmed = false
	c.message = ""
	if tier == TierNone {
		return c.execute()
	}
	c.state = tui.TaskStateConfirming
	return nil
}

// Confirm executes the pending action. It is a no-op unless a confirmation
// is showing, and — for a delete/force-delete at TierModal specifically —
// unless the typed name matches. Other TierModal verbs (Drain) stay a plain
// y/N confirm even at TierModal: only delete's PROD escalation gets the
// type-the-name treatment (mvp-tasks.md's Phase 5/8b exit notes: "Cordon/
// Drain... left exactly as Phase 9 built them").
func (c *Controller) Confirm() tea.Cmd {
	if c.state != tui.TaskStateConfirming || c.pending == nil {
		return nil
	}
	if c.tier == TierModal && requiresTypedName(c.pending.Scope.Verb) && !c.NameMatches() {
		return nil
	}
	if c.forceArmed {
		c.pending.Scope.Verb = "force-delete"
		c.pending.Label = "Force delete " + c.pending.Scope.ResourceName + "?"
	}
	return c.execute()
}

// requiresTypedName reports whether verb's TierModal confirmation needs the
// typed-name match — just the delete family; Drain's TierModal confirm
// stays the simple y/N ConfirmCard.
func requiresTypedName(verb string) bool {
	return verb == "delete" || verb == "force-delete"
}

// Cancel abandons the pending confirmation.
func (c *Controller) Cancel() {
	if c.state != tui.TaskStateConfirming {
		return
	}
	label := "action"
	if c.pending != nil {
		label = c.pending.Label
	}
	c.pending = nil
	c.tier = TierNone
	c.typedName = ""
	c.forceArmed = false
	c.state = tui.TaskStateCancelled
	c.message = "Cancelled " + label + "."
}

// TypeRune appends s to the type-ahead buffer — a no-op unless a TierModal
// confirmation is active.
func (c *Controller) TypeRune(s string) {
	if c.state != tui.TaskStateConfirming || c.tier != TierModal || s == "" {
		return
	}
	c.typedName += s
}

// Backspace removes the last rune from the type-ahead buffer (rune-safe) —
// a no-op unless a TierModal confirmation is active.
func (c *Controller) Backspace() {
	if c.state != tui.TaskStateConfirming || c.tier != TierModal || c.typedName == "" {
		return
	}
	r := []rune(c.typedName)
	c.typedName = string(r[:len(r)-1])
}

// Escalate switches a pending Pod "delete" into a "force-delete" (ctrl-k,
// mvp-plan.md §8b's "harder chord for force delete") — a no-op for any
// other pending verb/kind. The typed-name progress is kept: it's still
// matching the same resource name regardless of which delete variant runs.
// This is the PROD type-the-name modal's own escalation path (TierModal);
// ArmForceDelete/DisarmForceDelete below is the separate, staged
// counterpart for the non-prod inline confirm.
func (c *Controller) Escalate() {
	if c.pending == nil || c.pending.Scope.ResourceKind != string(kube.KindPod) || c.pending.Scope.Verb != "delete" {
		return
	}
	c.pending.Scope.Verb = "force-delete"
	c.pending.Label = "Force delete " + c.pending.Scope.ResourceName + "?"
}

// ArmForceDelete stages a pending TierInline Pod "delete" confirm for
// force-delete (ctrl-k) — a no-op for any other tier/verb/kind. Unlike
// Escalate, this doesn't touch the pending verb yet: "y" (Confirm) still
// has to follow before DeleteResourceForced actually runs, and "n"
// (DisarmForceDelete) backs out to the plain delete prompt instead of
// cancelling the whole confirm.
func (c *Controller) ArmForceDelete() {
	if c.state != tui.TaskStateConfirming || c.pending == nil || c.tier != TierInline {
		return
	}
	if c.pending.Scope.ResourceKind != string(kube.KindPod) || c.pending.Scope.Verb != "delete" {
		return
	}
	c.forceArmed = true
}

// DisarmForceDelete backs a force-armed inline delete confirm out of the
// force sub-state, back to the plain delete prompt — a no-op unless armed.
func (c *Controller) DisarmForceDelete() {
	c.forceArmed = false
}

// ForceArmed reports whether the pending inline delete confirm is staged
// for force-delete — screens use it to swap the keybar into the
// destructive treatment (pill text, hints, will-run line).
func (c Controller) ForceArmed() bool { return c.forceArmed }

// HandleResult applies an execution outcome, transitioning to success or error.
func (c *Controller) HandleResult(msg ResultMsg) {
	c.pending = nil
	c.tier = TierNone
	c.typedName = ""
	c.forceArmed = false
	if msg.Err != nil {
		c.state = tui.TaskStateError
		verb := "run"
		c.message = fmt.Sprintf("Failed to %s %s: %v", verb, msg.Label, msg.Err)
		return
	}
	c.state = tui.TaskStateSuccess
	c.message = "Done: " + msg.Label + "."
}

// Prompt is the confirmation question shown while Active.
func (c Controller) Prompt() string {
	if c.pending == nil {
		return ""
	}
	s := c.pending.Scope
	target := s.ResourceKind
	if s.Namespace != "" {
		target += " " + s.Namespace + "/" + s.ResourceName
	} else {
		target += " " + s.ResourceName
	}
	return fmt.Sprintf("%s %s? (y) confirm  (n) cancel", capitalize(s.Verb), target)
}

func (c *Controller) execute() tea.Cmd {
	action := *c.pending
	mutator := c.mutator
	c.state = tui.TaskStateLoading
	c.message = capitalize(action.Scope.Verb) + " " + action.Scope.ResourceName + "…"
	return func() tea.Msg {
		var err error
		switch action.Scope.Verb {
		case "delete":
			err = mutator.DeleteResource(context.Background(),
				kube.ResourceKind(action.Scope.ResourceKind), action.Scope.Namespace, action.Scope.ResourceName)
		case "force-delete":
			err = mutator.DeleteResourceForced(context.Background(),
				kube.ResourceKind(action.Scope.ResourceKind), action.Scope.Namespace, action.Scope.ResourceName)
		case "rollout-restart":
			err = mutator.RolloutRestart(context.Background(), action.Scope.Namespace, action.Scope.ResourceName)
		case "cordon":
			err = mutator.Cordon(context.Background(), action.Scope.ResourceName, true)
		case "uncordon":
			err = mutator.Cordon(context.Background(), action.Scope.ResourceName, false)
		case "drain":
			_, err = mutator.Drain(context.Background(), action.Scope.ResourceName)
		case "rollback":
			err = mutator.HelmRollback(context.Background(), action.Scope.Namespace, action.Scope.ResourceName, action.Scope.Revision)
		case "scale":
			err = mutator.Scale(context.Background(),
				kube.ResourceKind(action.Scope.ResourceKind), action.Scope.Namespace, action.Scope.ResourceName, action.Scope.Replicas)
		case "set-image":
			err = mutator.SetImage(context.Background(),
				kube.ResourceKind(action.Scope.ResourceKind), action.Scope.Namespace, action.Scope.ResourceName, action.Scope.Container, action.Scope.Image)
		case "set-resources":
			err = mutator.SetResources(context.Background(),
				kube.ResourceKind(action.Scope.ResourceKind), action.Scope.Namespace, action.Scope.ResourceName, action.Scope.Container, *action.Scope.Resources, false)
		case "set-meta":
			err = mutator.PatchMeta(context.Background(),
				kube.ResourceKind(action.Scope.ResourceKind), action.Scope.Namespace, action.Scope.ResourceName,
				action.Scope.MetaIsAnnotation, action.Scope.MetaKey, action.Scope.MetaValue, action.Scope.MetaRemove)
		case "secret-data":
			err = mutator.PatchSecretData(context.Background(),
				action.Scope.Namespace, action.Scope.ResourceName, action.Scope.SecretKey, action.Scope.SecretValue, action.Scope.SecretRemove)
		default:
			err = fmt.Errorf("unsupported verb %q", action.Scope.Verb)
		}
		return ResultMsg{ActionID: action.ID, Label: action.Label, Err: err}
	}
}

func (c *Controller) fail(message string) {
	c.pending = nil
	c.state = tui.TaskStateError
	c.message = message
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
