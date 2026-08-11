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
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
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
	// CronJobSchedule is populated only on a successful "cronjob-set-
	// schedule" action — the API's own accepted schedule/timezone plus the
	// CronJob's new resourceVersion (0.8.0 plan Phase 2 task 10). nil for
	// every other verb, and nil on failure. The schedule editor (§36d) must
	// key its refresh/undo readiness off this authoritative value rather
	// than an immediate, possibly-stale informer cache read.
	CronJobSchedule *kube.CronJobScheduleResult
}

// TargetResult is one bulk target's individual outcome — BulkResultMsg's
// per-object detail (0.8.0 plan Phase 2 task 11): the controller owns
// iteration over a pending action's Scope.BulkTargets, so a caller sees
// which targets failed rather than one joined error that can't tell success
// from failure per object.
type TargetResult struct {
	Namespace    string
	ResourceName string
	Err          error
}

// BulkResultMsg reports a bulk action's per-target outcomes (Scope.
// BulkTargets non-empty). Screens pass it to HandleBulkResult; Results is in
// the same order as the pending action's Scope.BulkTargets.
type BulkResultMsg struct {
	ActionID string
	Label    string
	Results  []TargetResult
}

// Failed returns the subset of Results with a non-nil Err — the marks a
// caller should retain after a partial failure (0.8.0 plan Phase 2 task 11:
// "let the caller retain only failed marks").
func (m BulkResultMsg) Failed() []TargetResult {
	var out []TargetResult
	for _, r := range m.Results {
		if r.Err != nil {
			out = append(out, r)
		}
	}
	return out
}

// Controller holds the state of the at-most-one in-flight action for a screen.
// The zero value (no mutator) is usable: Begin reports that mutations are not
// configured rather than panicking.
type Controller struct {
	mutator kube.Mutator
	pending *tui.TaskAction
	tier    Tier
	// typedInput is the type-ahead buffer for a TierModal confirmation
	// (mvp-plan.md §8b: "type the name to confirm") — unused for
	// TierNone/TierInline. Never rendered via its own View(): components/
	// confirmmodal.go's TypeNameModal/TypeCountModal keep their own bespoke
	// "N/M" progress-counter + "█" cursor look (a deliberate departure from
	// every other text-entry site in the app), reading TypedName() as a
	// plain string — this exists purely so HandleTypeKey gets Home/End/
	// Ctrl-arrow word-jump for free instead of hand-rolling a 10th
	// implementation. Paste reaches it through PasteTarget, not from here:
	// a bracketed paste is never a keypress.
	typedInput textinput.Model
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
func (c Controller) TypedName() string { return c.typedInput.Value() }

// NameMatches reports whether TypedName equals the pending action's target
// resource name — the gate Confirm() checks for TierModal.
func (c Controller) NameMatches() bool {
	return c.pending != nil && c.typedInput.Value() == c.pending.Scope.ResourceName
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
	c.typedInput = textinput.New()
	c.typedInput.Focus()
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
// typed-name match — the delete family, 16b's rollout-undo (docs/design
// README.md §16b: "type-the-deployment-name modal in PROD"), and 9a's
// rollout-restart (§9a/§419: "delete and rollout restart are both tiered —
// inline y/N in non-prod, type-the-name modal in PROD contexts"). Drain's
// TierModal confirm, and 18a's Helm rollback (a different Scope.Verb,
// "rollback"), both stay the simple y/N ConfirmCard. Job's own "job-retry"
// deliberately never reaches TierModal at all (browse/jobs.go's
// beginJobRetry) — components.TypeNameModal this gates is reserved for
// destructive confirms, and Retry (a clone, not a delete+recreate) isn't
// one.
func requiresTypedName(verb string) bool {
	return verb == "delete" || verb == "force-delete" || verb == "rollout-undo" || verb == "rollout-restart"
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
	c.typedInput.Blur()
	c.forceArmed = false
	c.state = tui.TaskStateCancelled
	c.message = "Cancelled " + label + "."
}

// HandleTypeKey routes a keypress that isn't one of the type-the-name
// modal's own chords (esc/enter/ctrl-k, intercepted by the caller first)
// into the type-ahead buffer — this is where Home/End and Ctrl-arrow
// word-jump arrive for free. A no-op unless a TierModal confirmation is
// active. Paste does *not* arrive here (it isn't a keypress at all) — see
// PasteTarget.
func (c *Controller) HandleTypeKey(msg tea.KeyPressMsg) tea.Cmd {
	if c.state != tui.TaskStateConfirming || c.tier != TierModal {
		return nil
	}
	var cmd tea.Cmd
	c.typedInput, cmd = c.typedInput.Update(msg)
	return cmd
}

// PasteTarget exposes the type-ahead buffer to the screen's paste router
// (tui.RoutePaste), or nil when no TierModal confirmation is active. Pasting
// the name is deliberately allowed: the gate is "you produced the exact
// name", not "you typed it by hand" — the same bar GitHub's repo-delete and
// Terraform Cloud's destroy prompts set.
func (c *Controller) PasteTarget() tui.PasteTarget {
	if c.state != tui.TaskStateConfirming || c.tier != TierModal {
		return nil
	}
	return tui.PasteInto(&c.typedInput)
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
	c.typedInput.Blur()
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

// HandleBulkResult is HandleResult's counterpart for a bulk action (0.8.0
// plan Phase 2 task 11): success when every target succeeded, error with a
// count when any failed. Callers that need per-target detail (e.g. to keep
// only the failed rows marked) read msg.Results/msg.Failed() themselves —
// this only drives the controller's own State()/Message().
func (c *Controller) HandleBulkResult(msg BulkResultMsg) {
	c.pending = nil
	c.tier = TierNone
	c.typedInput.Blur()
	c.forceArmed = false
	failed := msg.Failed()
	if len(failed) == 0 {
		c.state = tui.TaskStateSuccess
		c.message = fmt.Sprintf("Done: %s (%d).", msg.Label, len(msg.Results))
		return
	}
	c.state = tui.TaskStateError
	if len(failed) == len(msg.Results) {
		c.message = fmt.Sprintf("Failed to %s: all %d targets failed.", msg.Label, len(failed))
		return
	}
	c.message = fmt.Sprintf("%s: %d of %d targets failed.", msg.Label, len(failed), len(msg.Results))
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

	if len(action.Scope.BulkTargets) > 0 {
		return executeBulk(mutator, action)
	}

	// cronjob-set-schedule is handled outside executeScope because its
	// mutator method returns a value (kube.CronJobScheduleResult) that has
	// to reach ResultMsg — every other verb's execution collapses to a
	// plain error, which executeScope's shared switch returns.
	if action.Scope.Verb == "cronjob-set-schedule" {
		return func() tea.Msg {
			result, err := mutator.SetCronJobSchedule(context.Background(),
				action.Scope.Namespace, action.Scope.ResourceName, scheduleEdit(action.Scope))
			msg := ResultMsg{ActionID: action.ID, Label: action.Label, Err: err}
			if err == nil {
				msg.CronJobSchedule = &result
			}
			return msg
		}
	}

	return func() tea.Msg {
		return ResultMsg{ActionID: action.ID, Label: action.Label, Err: executeScope(mutator, action.Scope)}
	}
}

// executeBulk runs action.Scope.Verb once per action.Scope.BulkTargets entry
// (0.8.0 plan Phase 2 task 11), substituting each target's own Namespace/
// ResourceName/CronJobResourceVersion/CronJobGeneration onto a copy of the
// shared Scope. Every other Scope field (Verb, TriggerCreator, StagedAt, …)
// stays common across every target — only the fields a per-target
// precondition can differ on are overridden.
func executeBulk(mutator kube.Mutator, action tui.TaskAction) tea.Cmd {
	targets := append([]tui.BulkTarget(nil), action.Scope.BulkTargets...)
	baseScope := action.Scope
	baseScope.BulkTargets = nil
	return func() tea.Msg {
		results := make([]TargetResult, 0, len(targets))
		for _, target := range targets {
			scope := baseScope
			scope.Namespace = target.Namespace
			scope.ResourceName = target.ResourceName
			scope.CronJobResourceVersion = target.ResourceVersion
			scope.CronJobGeneration = target.Generation
			results = append(results, TargetResult{
				Namespace:    target.Namespace,
				ResourceName: target.ResourceName,
				Err:          executeScope(mutator, scope),
			})
		}
		return BulkResultMsg{ActionID: action.ID, Label: action.Label, Results: results}
	}
}

// scheduleEdit builds a kube.CronJobScheduleEdit from a "cronjob-set-
// schedule" Scope — the single place that translation happens, so the
// single-target and (a future bulk-capable, per Plan §4/Phase 3) schedule
// path can never drift.
func scheduleEdit(scope tui.TaskScope) kube.CronJobScheduleEdit {
	var timezone *string
	if scope.CronJobTimeZone != nil {
		tz := *scope.CronJobTimeZone
		timezone = &tz
	}
	return kube.CronJobScheduleEdit{
		Schedule:        scope.Schedule,
		TimeZone:        timezone,
		ResourceVersion: scope.CronJobResourceVersion,
	}
}

// executeScope runs one action's mutation and returns its error — the
// shared body both execute()'s single-target path and executeBulk's
// per-target loop call, so a bulk-capable verb (§36c's marked-set suspend/
// resume) executes through exactly the same dispatch a single Enter would.
// Takes mutator explicitly rather than a *Controller receiver so a bulk
// action's per-target loop (running on a tea.Cmd goroutine) never touches
// shared controller state.
func executeScope(mutator kube.Mutator, scope tui.TaskScope) error {
	var err error
	switch scope.Verb {
	case "delete":
		err = mutator.DeleteResource(context.Background(),
			kube.ResourceKind(scope.ResourceKind), scope.Namespace, scope.ResourceName)
	case "force-delete":
		err = mutator.DeleteResourceForced(context.Background(),
			kube.ResourceKind(scope.ResourceKind), scope.Namespace, scope.ResourceName)
	case "flux-suspend", "flux-resume":
		err = mutator.SetFluxSuspend(context.Background(),
			kube.ResourceKind(scope.ResourceKind), scope.Namespace,
			scope.ResourceName, scope.Verb == "flux-suspend")
	case "job-retry":
		err = mutator.RetryJob(context.Background(),
			scope.Namespace, scope.ResourceName, scope.NewName)
	case "job-suspend", "job-resume":
		err = mutator.SetJobSuspend(context.Background(),
			scope.Namespace, scope.ResourceName, scope.Verb == "job-suspend")
	case "cronjob-run-now":
		err = mutator.TriggerCronJob(context.Background(),
			scope.Namespace, scope.ResourceName, scope.NewName, scope.TriggerCreator, scope.StagedAt)
	case "cronjob-suspend", "cronjob-resume":
		err = mutator.SetCronJobSuspend(context.Background(),
			scope.Namespace, scope.ResourceName, scope.Verb == "cronjob-suspend",
			scope.CronJobResourceVersion, scope.CronJobGeneration, scope.StagedAt)
	case "cronjob-set-schedule":
		// Only reached from executeBulk (execute()'s single-target path
		// intercepts this verb before ever calling executeScope, since it
		// needs the mutator's returned CronJobScheduleResult). Schedule
		// isn't bulk-capable today, but keeping this branch means a future
		// caller that does mark a bulk schedule edit still executes
		// correctly, just without a per-target CronJobScheduleResult.
		_, err = mutator.SetCronJobSchedule(context.Background(), scope.Namespace, scope.ResourceName, scheduleEdit(scope))
	case "flux-reconcile":
		// §30b's with-source reconcile stamps the source first: asking a
		// reconciler to sync against an artifact that is still stale
		// just re-applies what it already has.
		if scope.FluxSourceName != "" {
			err = mutator.RequestFluxReconcile(context.Background(),
				kube.ResourceKind(scope.FluxSourceKind), scope.FluxSourceNamespace, scope.FluxSourceName)
		}
		if err == nil {
			err = mutator.RequestFluxReconcile(context.Background(),
				kube.ResourceKind(scope.ResourceKind), scope.Namespace, scope.ResourceName)
		}
	case "argo-refresh":
		err = mutator.RequestArgoRefresh(context.Background(),
			kube.ResourceKind(scope.ResourceKind), scope.Namespace, scope.ResourceName)
	case "argo-sync":
		err = mutator.RequestArgoSync(context.Background(),
			kube.ResourceKind(scope.ResourceKind), scope.Namespace,
			scope.ResourceName, scope.ArgoSyncRevision)
	case "rollout-restart":
		err = mutator.RolloutRestart(context.Background(),
			kube.ResourceKind(scope.ResourceKind), scope.Namespace, scope.ResourceName)
	case "cert-renew":
		err = mutator.RenewCertificate(context.Background(), scope.Namespace, scope.ResourceName)
	case "cordon":
		err = mutator.Cordon(context.Background(), scope.ResourceName, true)
	case "uncordon":
		err = mutator.Cordon(context.Background(), scope.ResourceName, false)
	case "drain":
		_, err = mutator.Drain(context.Background(), scope.ResourceName)
	case "rollback":
		err = mutator.HelmRollback(context.Background(), scope.Namespace, scope.ResourceName, scope.Revision)
	case "rollout-undo":
		err = mutator.RolloutUndo(context.Background(), scope.Namespace, scope.ResourceName, scope.Revision)
	case "scale":
		err = mutator.Scale(context.Background(),
			kube.ResourceKind(scope.ResourceKind), scope.Namespace, scope.ResourceName, scope.Replicas)
	case "set-image":
		err = mutator.SetImage(context.Background(),
			kube.ResourceKind(scope.ResourceKind), scope.Namespace, scope.ResourceName, scope.Container, scope.Image)
	case "set-resources":
		err = mutator.SetResources(context.Background(),
			kube.ResourceKind(scope.ResourceKind), scope.Namespace, scope.ResourceName, scope.Container, *scope.Resources, false)
	case "set-meta":
		err = mutator.PatchMeta(context.Background(),
			kube.ResourceKind(scope.ResourceKind), scope.Namespace, scope.ResourceName,
			scope.MetaIsAnnotation, scope.MetaKey, scope.MetaValue, scope.MetaRemove)
	case "secret-data":
		err = mutator.PatchSecretData(context.Background(),
			scope.Namespace, scope.ResourceName, scope.SecretKey, scope.SecretValue, scope.SecretRemove)
	case "configmap-data":
		err = mutator.PatchConfigMapData(context.Background(),
			scope.Namespace, scope.ResourceName, scope.ConfigMapKey, scope.ConfigMapValue, scope.ConfigMapRemove)
		if err == nil && scope.ConfigMapRestartConsumers {
			var errs []error
			for _, ref := range scope.ConfigMapConsumers {
				if rerr := mutator.RolloutRestart(context.Background(), ref.Kind, scope.Namespace, ref.Name); rerr != nil {
					errs = append(errs, rerr)
				}
			}
			err = errors.Join(errs...)
		}
	default:
		err = fmt.Errorf("unsupported verb %q", scope.Verb)
	}
	return err
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
