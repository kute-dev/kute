package tui

import (
	"context"

	"github.com/kute-dev/kute/internal/config"
	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/state"
)

// MetricsReader is the live pod-usage seam the namespace palette needs for
// its CPU-share column (docs/design README.md §6a) — satisfied by
// *kube.Cluster and *kube/fake.Cluster, the same concrete types
// browse.MetricsReader accepts. Declared again here rather than reused from
// browse: tui can't import tasks/browse (browse imports tui — the same
// import-cycle constraint Session.HelpScope/HelpGlobal already documents),
// so the composition root wires the same value into both fields.
type MetricsReader interface {
	PodMetricsByNamespace(ctx context.Context, namespace string) (map[string]kube.PodMetrics, error)
}

// Location is the active browse position: kube-context, namespace ("" means
// all namespaces), resource kind, and (optionally) a selected resource
// name. Filter mirrors browse's live filterQuery (browse writes it on every
// change via its setFilter helper) so context.go's switchContextCmd — which
// runs in this package, not browse — can read the outgoing context's filter
// to persist into state.PerContext and restore it on switching back
// (docs/design README.md §7a: "each context remembers its own namespace +
// kind + filter").
type Location struct {
	Context   string
	Namespace string
	Kind      kube.ResourceKind
	Resource  string
	Filter    string
}

// Session is the cross-screen state the root shell owns and hands to every
// task (mvp-plan.md §0.9): the live cluster seam, the resource catalog,
// persisted recents/per-context state, user config (prod contexts), the
// current browse location, and the selected Theme/Styles (picked once at
// startup — see decision #3 — and threaded through Session rather than a
// package-level var, so a --theme override only ever touches this one
// value).
type Session struct {
	Cluster  *kube.Cluster // nil when no kubeconfig / cluster unreachable
	Registry resources.Registry
	Groups   []resources.Group
	State    state.State
	Config   config.Config
	Location Location
	Theme    Theme
	Styles   Styles
	// Lister is the same RawLister browse reads through (the real
	// *kube.Cluster or, in --demo, *fake.Cluster) — the root shell needs it
	// too, to build the jump palette's live kind counts and resource-name
	// corpus (mvp-plan.md Phase 2) without depending on a concrete cluster
	// type. Nil when no cluster is reachable.
	Lister resources.RawLister
	// Metrics is the same seam as browse.Config.Metrics, wired to the same
	// concrete cluster value — used by the namespace palette (namespace.go)
	// for its CPU-share column. Nil when no cluster is reachable, or when a
	// reachable cluster has no metrics-server (PodMetricsByNamespace then
	// errors per-call) — both degrade to the column simply being omitted.
	Metrics MetricsReader
	// Forwards is the app-wide port-forward registry (13a/13c/13d) — built
	// once at the composition root and never rebuilt on context switch, so
	// forwards survive one (docs/design README.md §13d: "global across
	// context switches"). Nil when no cluster is reachable.
	Forwards *kube.ForwardManager
	// Version is kute's own running build version ("0.2.0", no leading
	// "v") — set once at the composition root from the ldflags-injected
	// build version (main.go), the "you run X" side of every 28a/28b
	// comparison.
	Version string
	// Update is 28a/28b's ambient check result for this process — nil until
	// the startup check resolves (or forever, if update.check is disabled).
	// See UpdateInfo's doc comment for why this is richer than, and
	// separate from, the State.UpdateCheck trio that actually persists.
	Update *UpdateInfo
	// HelpScope and HelpGlobal are the 7b help overlay's two fixed columns,
	// pre-built at the composition root (internal/app.BuildSession) from the
	// verbs registry: tui itself can't import verbs (verbs depends on tui,
	// directly and via actions), so the registry lookup happens where the
	// import direction allows it and the result is threaded through Session
	// like everything else cross-screen. Keys with no registry entry (pure
	// movement/exit — ↑↓, esc, q) are literals baked in alongside the
	// verb-sourced ones at the same call site.
	HelpScope, HelpGlobal []KeyHint
}

// SyncLocationToPerContext snapshots s.Location's namespace/kind/filter into
// s.State.PerContext[s.Location.Context], overwriting whatever was there.
// switchContextCmd calls this for the outgoing context right before
// rebuilding the cluster (so 7a's per-context restore survives switching
// away); RunWithConfig calls it once more at exit for whatever context is
// still active — otherwise a session that only ever visits one context (e.g.
// a single-cluster dev setup) would never persist its browsed
// namespace/kind, since the switch-time snapshot never runs. A no-op when no
// context is set (no reachable cluster, or --demo, whose location Session
// never restores from PerContext anyway).
func (s *Session) SyncLocationToPerContext() {
	if s == nil || s.Location.Context == "" {
		return
	}
	pc := s.State.PerContext[s.Location.Context]
	pc.Namespace = s.Location.Namespace
	pc.Kind = string(s.Location.Kind)
	pc.Filter = s.Location.Filter
	s.State.PerContext[s.Location.Context] = pc
}

// ForwardSummary tallies s.Forwards for 13d's header chip — zero value
// (BuildForwardChip renders nothing) when Forwards is nil.
func (s *Session) ForwardSummary() ForwardSummary {
	if s == nil || s.Forwards == nil {
		return ForwardSummary{}
	}
	var summary ForwardSummary
	for _, session := range s.Forwards.List() {
		if session.State == kube.ForwardReconnecting {
			summary.Reconnecting++
		} else {
			summary.Active++
		}
	}
	return summary
}
