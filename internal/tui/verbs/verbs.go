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
// Non-mutating verbs (navigation, Exec/Edit/NodeShell's own tty-handoff
// writes, Forward's local-only session) are never hidden by this, since
// docs/design README.md §301's "mutating actions disabled" applies strictly
// to kube.Mutator-routed writes.
func (v Verb) HiddenWhileOffline(offline bool) bool {
	return v.Mutating && offline
}

// Non-mutating navigation/view verbs.
var (
	Goto   = Verb{ID: "goto", Key: "g", Label: "goto"}
	Filter = Verb{ID: "filter", Key: "/", Label: "filter"}
	Open   = Verb{ID: "open", Key: "↵", Label: "open"}
	Logs   = Verb{ID: "logs", Key: "l", Label: "logs", Kinds: []kube.ResourceKind{kube.KindPod}}
	YAML   = Verb{ID: "yaml", Key: "y", Label: "yaml"}
	Exec   = Verb{ID: "exec", Key: "x", Label: "exec", Kinds: []kube.ResourceKind{kube.KindPod}}
	// NodeShell is 's' on a node — a root shell on the node itself via a
	// kubectl debug subprocess (kube.NodeShellSpec over tea.ExecProcess),
	// the same tty-handoff path as Exec. Like Exec it never goes through
	// kube.Mutator/actions.Controller, so it carries no Tier/Mutating flag
	// even though kubectl debug does leave a node-debugger pod behind.
	NodeShell = Verb{ID: "node-shell", Key: "s", Label: "node shell", Kinds: []kube.ResourceKind{kube.KindNode}}
	// Edit is 'E' on any row, any kind — kubectl edit (kube.EditSpec) over
	// tea.ExecProcess, the same tty-handoff path as Exec/NodeShell. It never
	// goes through kube.Mutator/actions.Controller, so — like Exec/
	// NodeShell — it carries no Tier/Mutating flag even though it's a real
	// mutation; the PROD-only y/N gate each call site applies is driven by
	// TierForEdit below, not this field.
	Edit          = Verb{ID: "edit", Key: "E", Label: "edit"}
	Events        = Verb{ID: "events", Key: "e", Label: "events"}
	Namespace     = Verb{ID: "namespace", Key: "n", Label: "namespace"}
	Context       = Verb{ID: "context", Key: "c", Label: "context"}
	AllNamespaces = Verb{ID: "all-namespaces", Key: "a", Label: "all namespaces"}
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
	Timeline = Verb{ID: "timeline", Key: "t", Label: "timeline"}
	// Mark is 20a's "space" — marks the cursor row and advances, works in
	// any list.
	Mark = Verb{ID: "mark", Key: "space", Label: "mark"}
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
	// literal key the design doc names for 25a.
	RolloutRestart = Verb{
		ID: "rollout-restart", Key: "r", Label: "rollout restart",
		Tier: actions.TierNone, Kinds: []kube.ResourceKind{kube.KindDeployment}, Mutating: true,
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
	Delete, ForceDelete, RolloutRestart, Cordon, Drain, Rollback, Scale, SetImage, SetResources,
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
// always modal, Cordon/RolloutRestart never confirm, regardless of prod.
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
