// Package verbs is the shared vocabulary for keybars, the help overlay, and
// the confirm flow (mvp-plan.md §0.4). Every verb — delete, logs, cordon,
// … — is one registry entry: stable ID, key, label, destructiveness tier,
// applicable kinds, mutating flag. Screens hand-compose their keybars
// (curated per-screen grouping and order, only applicable keys shown) but
// only from these entries, never from key/label string literals; the help
// overlay and the confirm policy read the same entries, so they cannot
// drift apart.
package verbs

import (
	"slices"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
)

// Verb is one registry entry.
type Verb struct {
	ID    string // stable id ("delete", "logs") — future key-remapping hook
	Key   string // "ctrl-d"
	Label string // "delete"
	Tier  actions.Tier
	Kinds []kube.ResourceKind // applicable kinds; nil = all
	// Mutating verbs are hidden from the keybar and refused at
	// actions.Controller.Begin while OFFLINE (docs/design README.md §52,
	// §301) — screens call HiddenWhileOffline when building their own
	// Keybar() rather than hardcoding which verbs need the gate; browse's
	// list view instead swaps its whole keybar to the OFFLINE pill, which
	// covers every verb at once without consulting this field per-verb.
	Mutating bool
	// Bulk declares whether this verb can act on 20a's marked set instead of
	// just the cursor row (docs/design README.md §20a: "bulk-capability is
	// declared per-verb in the command table"). Per-row-only verbs (logs,
	// exec, ↵ open, …) leave this false.
	Bulk bool
	// Global marks a verb as identical on every screen — goto/filter/mark/
	// yaml/edit/events/timeline/meta/namespace/context/all-namespaces
	// (docs/design v.0.3.0.dc.html §29a: "the bar shows what's different
	// here, grammar lives in ?"). A Global verb's Hint never renders in a
	// screen's own Keybar() Groups; it's taught exactly once, in the ?
	// overlay's SCOPE/GLOBAL columns (helpScopeKeys/helpGlobalKeys).
	Global bool
}

// Hint renders the verb as a keybar key/label pair.
func (v Verb) Hint() tui.KeyHint {
	return tui.KeyHint{Key: v.Key, Label: v.Label}
}

// AppliesTo reports whether the verb is offered for kind. A nil Kinds list
// means "every kind".
func (v Verb) AppliesTo(kind kube.ResourceKind) bool {
	if len(v.Kinds) == 0 {
		return true
	}
	return slices.Contains(v.Kinds, kind)
}

// HiddenWhileOffline reports whether v's keybar hint should be omitted
// given the connection state — true only for a Mutating verb while offline.
// Mutating covers more than kube.Mutator-routed writes: Exec and NodeShell
// hand a tty to kubectl instead, and are gated at their own dispatch sites
// rather than in actions.Controller.Begin, but docs/design README.md §52's
// "mutating actions disabled while OFFLINE" applies to them the same way.
// Forward stays exempt — a port-forward is a local session, not a write.
func (v Verb) HiddenWhileOffline(offline bool) bool {
	return v.Mutating && offline
}

// Navigation/view verbs, plus the two tty-handoff verbs (Exec/NodeShell)
// that are Mutating without being tiered.
var (
	Goto   = Verb{ID: "goto", Key: "g", Label: "goto", Global: true}
	Filter = Verb{ID: "filter", Key: "/", Label: "filter", Global: true}
	Open   = Verb{ID: "open", Key: "↵", Label: "open"}
	Logs   = Verb{ID: "logs", Key: "l", Label: "logs", Kinds: []kube.ResourceKind{kube.KindPod}}
	YAML   = Verb{ID: "yaml", Key: "y", Label: "yaml"}
	// Exec is 'x' on a pod — an interactive shell inside a container, handed
	// off to a kubectl subprocess (kube.ExecSpec over tea.ExecProcess) rather
	// than routed through kube.Mutator, hence TierNone: there's no confirm
	// step to run. Mutating even so, because whatever the user types at that
	// prompt is a write, and a shell opened against a dead API connection
	// only fails at the handoff with a raw kubectl error (docs/design
	// README.md §52: OFFLINE disables mutating actions).
	Exec = Verb{ID: "exec", Key: "x", Label: "exec", Mutating: true, Kinds: []kube.ResourceKind{kube.KindPod}}
	// NodeShell is 's' on a node — a root shell on the node itself via a
	// kubectl debug subprocess (kube.NodeShellSpec over tea.ExecProcess),
	// the same tty-handoff path, TierNone for the same reason, and Mutating
	// for a stronger version of Exec's: kubectl debug creates a privileged
	// node-debugger pod, so this writes to the cluster before the user has
	// typed anything.
	NodeShell = Verb{ID: "node-shell", Key: "s", Label: "node shell", Mutating: true, Kinds: []kube.ResourceKind{kube.KindNode}}
	// Edit is 'E' on any row, any kind — kubectl edit (kube.EditSpec) over
	// tea.ExecProcess, the same tty-handoff path as Exec/NodeShell, and
	// Mutating for the plainest reason of the three: it applies whatever the
	// user saves. Tier stays None because kubectl owns the session once kute
	// suspends; the PROD-only y/N gate each call site applies is driven by
	// TierForEdit below, not this field. §4a names it alongside delete and
	// exec as disabled while offline.
	Edit          = Verb{ID: "edit", Key: "E", Label: "edit", Mutating: true, Global: true}
	Events        = Verb{ID: "events", Key: "e", Label: "events", Global: true}
	Namespace     = Verb{ID: "namespace", Key: "n", Label: "namespace", Global: true}
	Context       = Verb{ID: "context", Key: "c", Label: "context", Global: true}
	AllNamespaces = Verb{ID: "all-namespaces", Key: "a", Label: "all namespaces", Global: true}
	// JumpNamespace is 6b's "N" — jump into the selected row's namespace
	// without leaving the all-namespaces triage view for the palette.
	JumpNamespace = Verb{ID: "jump-namespace", Key: "N", Label: "jump into namespace"}
	// ToggleGroup is 6b's "tab" — expand/collapse the namespace group the
	// cursor is currently in (docs/design README.md §6b's "↹"), same key
	// events (9b) and yamlview (8a) already use for their own local fold
	// toggles.
	ToggleGroup = Verb{ID: "toggle-group", Key: "tab", Label: "expand/collapse"}
	Help        = Verb{ID: "help", Key: "?", Label: "help"}
	// Retry is the reconnect/re-probe key shared by the 4a offline banner,
	// 4b's 403 card, and the 4c/10b tasks/setup screens (mvp-plan.md Phase
	// 4) — one registry entry so every error surface's "r" reads the same.
	Retry = Verb{ID: "retry", Key: "r", Label: "retry"}
	// WhoCan is 'w' — both the 4b 403 card's recovery line ("w who-can")
	// and tasks/whocan's own reachability (docs/design README.md §22a: "w
	// on a 403 card opens who-can pre-filled"), one registry entry so the
	// two surfaces can never drift apart.
	WhoCan = Verb{ID: "who-can", Key: "w", Label: "who-can"}
	// HelmValues is 18a's 'v' — the selected release's decoded values in the
	// read-only YAML viewer.
	HelmValues = Verb{ID: "helm-values", Key: "v", Label: "values", Kinds: []kube.ResourceKind{kube.KindHelmRelease}}
	// HelmHistory is 18a's 'h' — the selected release's full revision rail
	// (16b's rail idiom).
	HelmHistory = Verb{ID: "helm-history", Key: "h", Label: "history", Kinds: []kube.ResourceKind{kube.KindHelmRelease}}
	// Timeline is 't' — the incident timeline (16a namespace-scoped from
	// lists, 16b object-scoped from detail views), docs/design README.md's
	// system-wide interactions list: "t opens the incident timeline
	// (namespace-scoped from lists, object-scoped from detail)".
	Timeline = Verb{ID: "timeline", Key: "t", Label: "timeline", Global: true}
	// Mark is 20a's "space" — marks the cursor row and advances, works in
	// any list.
	Mark = Verb{ID: "mark", Key: "space", Label: "mark", Global: true}
	// MarkAll is 20a's "*" — marks every row the current filter matches
	// ("filter-then-mark is the bulk grammar — no range-mark chord").
	MarkAll = Verb{ID: "mark-all", Key: "*", Label: "mark all"}
)

// Mutating verbs — every write path funnels through kube.Mutator, gated by
// Tier + actions.Controller (mvp-plan.md §0.4, §8b).
var (
	Delete = Verb{
		ID: "delete", Key: "ctrl-d", Label: "delete",
		Tier: actions.TierInline, Mutating: true, Bulk: true,
	}
	ForceDelete = Verb{
		ID: "force-delete", Key: "ctrl-k", Label: "force delete",
		Tier: actions.TierModal, Kinds: []kube.ResourceKind{kube.KindPod}, Mutating: true,
	}
	// RolloutRestart's key moved off 'R' (was 9a's original binding) to make
	// room for 25a's SetResources, which the design doc spec'd as 'R' on the
	// same Deployment row — resolved in favor of SetResources since it's the
	// literal key the design doc names for 25a. It moved a second time, off
	// bare 'r' onto 'ctrl-r': plain 'r' already meant retry/re-probe
	// everywhere else in the app (offline banner, permission-denied card,
	// Forward's own restart), so a bare keypress here both fought that
	// meaning and fired an unconfirmed pod restart across a whole
	// Deployment — the only TierNone mutating verb bound to a single
	// unmodified letter with zero friction. TierInline now gives it the same
	// will-run y/N (escalating to the type-the-name modal in PROD via
	// TierFor) every other single-target mutation gets; the deliberate
	// two-key chord plus that confirm makes an accidental fire meaningfully
	// harder, not just documented against.
	RolloutRestart = Verb{
		ID: "rollout-restart", Key: "ctrl-r", Label: "restart",
		Tier: actions.TierInline, Kinds: []kube.ResourceKind{kube.KindDeployment}, Mutating: true,
	}
	// JobRetry clones a Job's spec into a brand-new Job — not a delete+
	// recreate of the same object, so nothing about the source Job is
	// destroyed, but it does start a fresh run of the Job's business logic
	// (which can carry real side effects: emails, charges, writes). Same
	// physical key as RolloutRestart above (never both applicable to one
	// row — Kinds is disjoint) and the same TierInline reasoning: a bare
	// unmodified key firing an unconfirmed run of arbitrary workload code
	// would repeat the exact mistake RolloutRestart's own doc comment
	// describes moving off of.
	JobRetry = Verb{
		ID: "job-retry", Key: "ctrl-r", Label: "retry",
		Tier: actions.TierInline, Kinds: []kube.ResourceKind{kube.KindJob}, Mutating: true,
	}
	// FluxReconcile is §30a's 'r' on a Flux row: a fresh
	// reconcile.fluxcd.io/requestedAt timestamp — the same
	// annotate-to-trigger mechanism RolloutRestart uses, on a different
	// object. TierNone, no confirm.
	//
	// It keeps bare 'r' against the objection recorded on RolloutRestart
	// above, deliberately and for a reason that does not generalize. That
	// verb was moved off 'r' because a single unmodified letter fired an
	// unconfirmed pod restart across a whole Deployment with zero friction.
	// Reconcile is not that: it asks for the reconciliation Flux would have
	// performed on its own interval anyway, so the worst case of a
	// mistaken press is that a sync happens early. 'r' is also what `flux
	// reconcile` is spelled, and this screen's other meanings for 'r'
	// (retry a failed load, restart a forward) are reachable only in states
	// a Flux row is not in. Do not copy this exemption to a verb that
	// changes cluster state the controller wasn't already going to make.
	FluxReconcile = Verb{
		ID: "flux-reconcile", Key: "r", Label: "reconcile",
		Tier: actions.TierNone, Mutating: true,
	}
	// FluxSuspend is §30a's 's': one verb, two directions, exactly Cordon's
	// shape — the Scope.Verb is "flux-suspend" or "flux-resume" depending on
	// the row, and browse flips the keybar label to match. TierNone for
	// Cordon's reason: reversible and immediate, and resume puts the object
	// back exactly where it was.
	//
	// Kinds stays nil because the Flux kind set is *discovered* at connect,
	// not known at compile time; browse gates it on Descriptor.Flux instead.
	FluxSuspend = Verb{
		ID: "flux-suspend", Key: "s", Label: "suspend",
		Tier: actions.TierNone, Mutating: true,
	}
	// JobSuspend is a Job's own 's': one verb, two directions, same shape as
	// FluxSuspend above — Scope.Verb is "job-suspend" or "job-resume"
	// depending on the row's own Suspended state. Unlike FluxSuspend this is
	// TierInline, not TierNone: setting spec.suspend:true on a Job tears
	// down its currently-active pods immediately, a real visible side
	// effect Flux's own suspend (which only pauses future reconciliation,
	// touching nothing already applied) doesn't have. Resume shares the
	// same Tier for the toggle's symmetry rather than an asymmetric-tier
	// special case. Never contends with NodeShell's own 's' (Node-only) or
	// FluxSuspend's (gated on Descriptor.Flux, never true for a Job row).
	JobSuspend = Verb{
		ID: "job-suspend", Key: "s", Label: "suspend",
		Tier: actions.TierInline, Kinds: []kube.ResourceKind{kube.KindJob}, Mutating: true,
	}
	// CronJobRunNow is 'ctrl-r' on a CronJob row — the exact
	// `kubectl create job --from=cronjob/<name>` recipe JobRetry already
	// gives Jobs, one level up: it creates a brand-new, standalone Job from
	// the CronJob's own jobTemplate, so the CronJob object itself (its
	// schedule, its own spawned-Job history) is never touched. Same physical
	// key as JobRetry/RolloutRestart above (Kinds is disjoint — a row is
	// never both a Job and a CronJob) and the same TierInline reasoning
	// JobRetry's own doc comment gives: this fires a real, unconfirmed run of
	// the CronJob's business logic on a bare keypress, so it gets the same
	// one-line y/N friction every other TierInline verb does, never the
	// type-the-name modal — this is a clone into a new object, not a
	// delete+recreate, the same non-destructive argument JobRetry's doc
	// comment makes for staying out of requiresTypedName.
	CronJobRunNow = Verb{
		ID: "cronjob-run-now", Key: "ctrl-r", Label: "run now",
		Tier: actions.TierInline, Kinds: []kube.ResourceKind{kube.KindCronJob}, Mutating: true,
	}
	// CronJobSuspend is a CronJob's own 's': one verb, two directions,
	// exactly FluxSuspend's shape — Scope.Verb is
	// "cronjob-suspend"/"cronjob-resume" depending on the row's own Suspended
	// state. TierNone, unlike JobSuspend's TierInline: JobSuspend's own doc
	// comment explains that setting a Job's spec.suspend tears down its
	// currently-active pods immediately. A CronJob's spec.suspend does
	// nothing of the kind — it only stops the controller from creating
	// *future* Jobs on schedule; any Job (and pod) it has already spawned
	// keeps running untouched, exactly FluxSuspend's own "reversible and
	// immediate, touches nothing already applied" case, not JobSuspend's.
	// Never contends with Job's own 's' (disjoint Kinds) or NodeShell's
	// (Node-only).
	CronJobSuspend = Verb{
		ID: "cronjob-suspend", Key: "s", Label: "suspend",
		Tier: actions.TierNone, Kinds: []kube.ResourceKind{kube.KindCronJob}, Mutating: true,
	}
	// CronJobSetSchedule is 'S' (shift) on a CronJob row — an inline text
	// editor for spec.schedule, gathered in browse's own pendingCronSchedule
	// buffer (cronjobschedule.go) before there's an action to Begin, the same
	// nominal-TierNone/TierForCronJobSetSchedule-resolves-the-real-tier shape
	// SetImage/Scale use. Uppercase to stay clear of lowercase 's' (this
	// row's own suspend/resume), ctrl-r (run now), and every Global key
	// already live on a CronJob row (g/n/c/a/E/e/y/m/t/…) plus Open's ↵ —
	// checked against the full verbs.go key table, nothing else claims 'S'
	// anywhere in the app.
	CronJobSetSchedule = Verb{
		ID: "cronjob-set-schedule", Key: "S", Label: "edit schedule",
		Tier: actions.TierNone, Kinds: []kube.ResourceKind{kube.KindCronJob}, Mutating: true,
	}
	// FluxSource is §30a's 'o': jump to the object this one reconciles
	// from. Read-only navigation, so no tier and not mutating.
	FluxSource = Verb{
		ID: "flux-source", Key: "o", Label: "source",
	}
	Cordon = Verb{
		ID: "cordon", Key: "C", Label: "cordon",
		Tier: actions.TierNone, Kinds: []kube.ResourceKind{kube.KindNode}, Mutating: true,
	}
	Drain = Verb{
		ID: "drain", Key: "D", Label: "drain",
		Tier: actions.TierModal, Kinds: []kube.ResourceKind{kube.KindNode}, Mutating: true,
	}
	// Rollback is 18a's 'R' on a Helm release — "inherits 8b friction":
	// inline y/N in non-prod, escalated to TierModal in PROD by TierFor,
	// same as Delete's own Tier. Shells out to the real helm binary
	// (kube.Mutator.HelmRollback) rather than reading the watch cache.
	Rollback = Verb{
		ID: "rollback", Key: "R", Label: "rollback",
		Tier: actions.TierInline, Kinds: []kube.ResourceKind{kube.KindHelmRelease}, Mutating: true,
	}
	// RolloutUndo is 16b's 'R' on the revision rail — a deliberately
	// separate verb from the Helm-only Rollback above (same key/label,
	// different Kind and a different Scope.Verb string: "rollout-undo") so
	// extending actions.requiresTypedName for this one can't change 18a's
	// existing plain-y/N-even-in-PROD rollback behavior. §16b: "y/N in the
	// keybar for non-prod, type-the-deployment-name modal in PROD".
	RolloutUndo = Verb{
		ID: "rollout-undo", Key: "R", Label: "rollback",
		Tier: actions.TierInline, Kinds: []kube.ResourceKind{kube.KindDeployment}, Mutating: true,
	}
	// Scale is 17b's '+'/'−' on a Deployment/StatefulSet row — TierNone
	// (docs/design README.md §17b: "reversible → inline keybar prompt, never
	// a modal"). The confirming step every other TierNone verb skips is
	// replaced here by browse's own numeric type-ahead gate
	// (tasks/browse/scale.go's pendingScale) rather than
	// actions.Controller's y/N/type-name flow, since Scale needs to gather a
	// replica count before there's anything to Begin.
	Scale = Verb{
		ID: "scale", Key: "+/−", Label: "scale",
		Tier: actions.TierNone, Kinds: []kube.ResourceKind{kube.KindDeployment, kube.KindStatefulSet}, Mutating: true,
	}
	// SetImage is 24a's 'i' on a Deployment/StatefulSet/DaemonSet row — like
	// Scale, TierNone here is a nominal default: the real tier comes from
	// TierForSetImage below (TierNone outside PROD, TierInline in PROD),
	// since Controller.Begin needs a resolved actions.Tier and TierFor only
	// escalates TierInline→TierModal, not TierNone→TierInline (mirrors
	// TierForEdit's own doc comment on this same constraint).
	SetImage = Verb{
		ID: "set-image", Key: "i", Label: "set image",
		Tier: actions.TierNone, Kinds: []kube.ResourceKind{kube.KindDeployment, kube.KindStatefulSet, kube.KindDaemonSet}, Mutating: true,
	}
	// SetResources is 25a's 'R' on a Deployment/StatefulSet/DaemonSet row —
	// same TierNone-nominal/TierForSetResources-resolves-the-real-tier shape
	// as SetImage above (see SetImage's own doc comment for why TierNone here
	// is nominal rather than final).
	SetResources = Verb{
		ID: "set-resources", Key: "R", Label: "resources",
		Tier: actions.TierNone, Kinds: []kube.ResourceKind{kube.KindDeployment, kube.KindStatefulSet, kube.KindDaemonSet}, Mutating: true,
	}
	// Meta is 26a's 'm' on any row, any kind (CRDs included) — the
	// labels/annotations editor. Tier here is nominal (TierNone), like
	// Scale/SetImage/SetResources: the real per-edit tier is resolved in
	// browse's own meta.go, not through TierFor's PROD escalation, since
	// 26a's own escalation is join-driven (editing a Service-selector-
	// matched label, or any key removal) rather than PROD-driven — see
	// meta.go's commitMeta/commitMetaRemove.
	Meta = Verb{
		ID: "meta", Key: "m", Label: "labels/annotations",
		Tier: actions.TierNone, Mutating: true, Global: true,
	}
	// AddSecretKey is 27b's 'a' line-insert add on a Secret's Data view —
	// same TierNone-nominal/TierForAddSecretKey-resolves-the-real-tier shape
	// as SetImage/SetResources above (docs/design README.md §27b: "PROD
	// contexts add the inline y/N on apply"), routed through
	// actions.Controller/kube.Mutator.
	AddSecretKey = Verb{
		ID: "add-secret-key", Key: "a", Label: "add key",
		Tier: actions.TierNone, Kinds: []kube.ResourceKind{kube.KindSecret}, Mutating: true,
	}
	// RemoveSecretKey is 27b's ctrl-d removal of an existing Data-view key —
	// always TierInline regardless of PROD, the same "removal always
	// confirms inline, never the type-the-name modal" shape 26a's Meta
	// removal uses (docs/design README.md §27b: "removing a key keeps the
	// y/N too" — recoverable only if the old value exists elsewhere, so the
	// prompt says so, not destructive enough for 8b's modal tier).
	RemoveSecretKey = Verb{
		ID: "remove-secret-key", Key: "ctrl-d", Label: "remove key",
		Tier: actions.TierInline, Kinds: []kube.ResourceKind{kube.KindSecret}, Mutating: true,
	}
	// AddConfigMapKey is 27a's 'a' line-insert add on a ConfigMap's Data
	// view — same TierNone-nominal/TierForConfigMapData-resolves-the-real-tier
	// shape as AddSecretKey above.
	AddConfigMapKey = Verb{
		ID: "add-configmap-key", Key: "a", Label: "add key",
		Tier: actions.TierNone, Kinds: []kube.ResourceKind{kube.KindConfigMap}, Mutating: true,
	}
	// RemoveConfigMapKey is 27a's ctrl-d removal of an existing Data-view
	// key — always TierInline regardless of PROD, the same "removal always
	// confirms inline" policy RemoveSecretKey uses.
	RemoveConfigMapKey = Verb{
		ID: "remove-configmap-key", Key: "ctrl-d", Label: "remove key",
		Tier: actions.TierInline, Kinds: []kube.ResourceKind{kube.KindConfigMap}, Mutating: true,
	}
	// RestartConfigMapConsumers is 27a's ctrl-r — chains a value apply with
	// `kubectl rollout restart` for every workload that consumes the
	// ConfigMap. TierNone here is nominal like AddConfigMapKey/SetImage/etc:
	// the real tier is TierForConfigMapData, the same PROD gate the plain
	// apply uses (it's the same patch, plus extra restarts).
	RestartConfigMapConsumers = Verb{
		ID: "restart-configmap-consumers", Key: "ctrl-r", Label: "apply + restart consumers",
		Tier: actions.TierNone, Kinds: []kube.ResourceKind{kube.KindConfigMap}, Mutating: true,
	}
)

// Port-forward verbs (13a/13c, docs/design README.md). Forward pushes the
// picker from a Pod/Service/Deployment row; Stop/Restart/CopyForwardURL act
// on a Forwards row immediately (Mutating: false — a local port-forward
// session isn't cluster state, so OFFLINE/NoCluster shouldn't grey these
// out); StopAllForwards is the app's only forward verb with a confirm
// (TierInline, a bespoke browse-owned gate rather than actions.Controller,
// since it isn't a kube.Mutator operation — see browse/forwards.go).
var (
	Forward = Verb{
		ID: "forward", Key: "f", Label: "forward",
		Kinds: []kube.ResourceKind{kube.KindPod, kube.KindService, kube.KindDeployment},
	}
	StopForward = Verb{
		ID: "stop-forward", Key: "x", Label: "stop",
		Kinds: []kube.ResourceKind{kube.KindForward},
	}
	RestartForward = Verb{
		ID: "restart-forward", Key: "r", Label: "restart",
		Kinds: []kube.ResourceKind{kube.KindForward},
	}
	StopAllForwards = Verb{
		ID: "stop-all-forwards", Key: "X", Label: "stop all",
		Tier: actions.TierInline, Kinds: []kube.ResourceKind{kube.KindForward},
	}
	CopyForwardURL = Verb{
		ID: "copy-forward-url", Key: "y", Label: "copy url",
		Kinds: []kube.ResourceKind{kube.KindForward},
	}
)

// Routing-table verbs (23a/23b, docs/design README.md), used only inside
// tasks/routetable's own Keybar — not gated by Kinds since that field only
// matters to browse's per-kind row keybar, which never renders this screen's
// keys itself.
var (
	// CopyRouteURL is 23a's "y copies the full URL" — a screen-local
	// override of the 'y' key, the same reuse CopyForwardURL already makes
	// for Forwards rather than the global YAML verb.
	CopyRouteURL = Verb{ID: "copy-route-url", Key: "y", Label: "copy url"}
	// OpenParentGateway is 23b's "p opens the Gateway" from an HTTPRoute's
	// routing table.
	OpenParentGateway = Verb{ID: "open-parent-gateway", Key: "p", Label: "parent gateway"}
	// CopyRouteYAML is 23a/23b's "Y copies the full yaml" — a screen-local
	// override of the 'Y' key, copying the viewed object's YAML to the
	// clipboard directly rather than opening 8a (same "reuse a local key"
	// precedent as CopyRouteURL/CopyForwardURL).
	CopyRouteYAML = Verb{ID: "copy-route-yaml", Key: "Y", Label: "copy yaml"}
	// FocusTLSStrip is 23a's "tab" toggle onto the below-table TLS-secret
	// strip, so ↵ there can jump to the referenced Secret (docs/design
	// README.md §23a: "a strip above the keybar names each secret — ↵ there
	// jumps to it").
	FocusTLSStrip = Verb{ID: "focus-tls-strip", Key: "tab", Label: "tls secret"}
	// OpenTLSSecret is 23a's "↵ jumps to it" once FocusTLSStrip has moved
	// focus onto the TLS strip.
	OpenTLSSecret = Verb{ID: "open-tls-secret", Key: "↵", Label: "open secret"}
)

// All is every registered verb, for the help overlay's fixed SCOPE/GLOBAL
// columns and for ByID lookups.
var All = []Verb{
	Goto, Filter, Open, Logs, YAML, Exec, NodeShell, Edit, Events,
	Namespace, Context, AllNamespaces, JumpNamespace, ToggleGroup, Help, Retry, WhoCan,
	HelmValues, HelmHistory, Mark, MarkAll,
	FluxReconcile, FluxSuspend, FluxSource,
	Delete, ForceDelete, RolloutRestart, CronJobRunNow, CronJobSuspend, CronJobSetSchedule, Cordon, Drain, Rollback, RolloutUndo, Scale, SetImage, SetResources, Meta,
	AddSecretKey, RemoveSecretKey,
	AddConfigMapKey, RemoveConfigMapKey, RestartConfigMapConsumers,
	Forward, StopForward, RestartForward, StopAllForwards, CopyForwardURL,
	CopyRouteURL, OpenParentGateway, CopyRouteYAML, FocusTLSStrip, OpenTLSSecret,
}

// ByID looks up a registered verb by its stable ID.
func ByID(id string) (Verb, bool) {
	for _, v := range All {
		if v.ID == id {
			return v, true
		}
	}
	return Verb{}, false
}

// TierFor returns v's effective confirmation tier: v.Tier, escalated
// TierInline to TierModal when isProd (mvp-plan.md §8b, docs/design
// README.md §8b: "PROD contexts... = centered modal with type-the-name
// confirmation"). Non-Inline tiers are untouched — Drain/ForceDelete are
// always modal regardless of prod; Cordon is TierNone and never confirms at
// all. RolloutRestart is TierInline like Delete, so it escalates the same
// way: inline y/N in non-prod, type-the-name modal in PROD (§9a/§419).
//
// This lives here rather than on actions.Controller because verbs already
// imports actions (for the Tier type on Verb.Tier); actions importing verbs
// back for this function would cycle (mvp-tasks.md's Phase 5/8b exit notes
// have the full explanation — the same class of problem Session.HelpScope/
// HelpGlobal already solves for the root shell).
func TierFor(v Verb, isProd bool) actions.Tier {
	if v.Tier == actions.TierInline && isProd {
		return actions.TierModal
	}
	return v.Tier
}

// TierForEdit resolves Edit's confirmation policy for the call sites
// (browse/poddetail/nodedetail) that drive their own bespoke gate, since
// Edit never reaches actions.Controller for TierFor's usual Inline→Modal
// escalation to apply to (Edit has no Verb.Tier — see Edit's doc comment).
// TierNone (launch immediately) outside PROD — kubectl aborts cleanly on an
// unchanged save, so an accidental launch costs nothing, the same
// "reversible, no confirm" bucket Cordon/RolloutRestart sit in — escalating
// to TierInline (one y/N line, not the type-the-name modal — editing isn't
// delete/drain-grade destructive) only in PROD contexts.
func TierForEdit(isProd bool) actions.Tier {
	if isProd {
		return actions.TierInline
	}
	return actions.TierNone
}

// TierForSetImage resolves SetImage's confirmation policy — the same
// TierNone-outside-PROD/TierInline-in-PROD shape as TierForEdit (docs/design
// README.md §24a: "PROD contexts get the inline y/N on apply, per 8b's
// tiering"), but SetImage — unlike Edit — does execute through
// actions.Controller/kube.Mutator, so its PROD confirm is the ordinary
// TierInline y/N Controller already renders for rollback/delete, not a
// screen-local gate.
func TierForSetImage(isProd bool) actions.Tier {
	if isProd {
		return actions.TierInline
	}
	return actions.TierNone
}

// TierForCronJobSetSchedule resolves CronJobSetSchedule's confirmation
// policy — the same TierNone-outside-PROD/TierInline-in-PROD shape as
// TierForSetImage, routed through actions.Controller/kube.Mutator like
// SetImage rather than a screen-local gate.
func TierForCronJobSetSchedule(isProd bool) actions.Tier {
	if isProd {
		return actions.TierInline
	}
	return actions.TierNone
}

// TierForSetResources resolves SetResources's confirmation policy — the same
// TierNone-outside-PROD/TierInline-in-PROD shape as TierForSetImage (docs/design
// README.md §25a inherits 8b's PROD tiering the same way 24a does), routed
// through actions.Controller/kube.Mutator like SetImage rather than a
// screen-local gate.
func TierForSetResources(isProd bool) actions.Tier {
	if isProd {
		return actions.TierInline
	}
	return actions.TierNone
}

// TierForAddSecretKey resolves AddSecretKey's confirmation policy — the same
// TierNone-outside-PROD/TierInline-in-PROD shape as TierForSetImage/
// TierForSetResources (docs/design README.md §27b: "PROD contexts add the
// inline y/N on apply"), routed through actions.Controller/kube.Mutator like
// those two rather than a screen-local gate.
func TierForAddSecretKey(isProd bool) actions.Tier {
	if isProd {
		return actions.TierInline
	}
	return actions.TierNone
}

// TierForConfigMapData resolves AddConfigMapKey's/an edit's/ctrl-r's
// confirmation policy — the same TierNone-outside-PROD/TierInline-in-PROD
// shape as TierForAddSecretKey (docs/design README.md §27a inherits 8b's
// PROD tiering the same way 27b does), used for both a plain apply and a
// ctrl-r apply+restart commit since both run through the same
// actions.Controller/kube.Mutator path.
func TierForConfigMapData(isProd bool) actions.Tier {
	if isProd {
		return actions.TierInline
	}
	return actions.TierNone
}
