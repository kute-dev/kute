package actions

// Tier classifies how a mutating verb is confirmed before it executes.
// Screens and the confirm modal both derive their behavior from a verb's
// Tier (mvp-plan.md §0.4), so the keybar and the confirm policy can never
// disagree. TierFor — the prod-escalation rule (Inline escalates to Modal
// when the active context is PROD) — lands with the confirm-tier controller
// work in Phase 5; this type is declared now so the verb registry has a
// stable field to reference.
type Tier int

const (
	// TierNone executes immediately, no confirmation (e.g. rollout restart,
	// cordon — reversible verbs).
	TierNone Tier = iota
	// TierInline shows a y/N prompt in the keybar.
	TierInline
	// TierModal requires the type-the-name confirm modal. Drain and
	// force-delete always declare this tier; TierInline escalates to this
	// tier when the active context is PROD (Phase 5).
	TierModal
)
