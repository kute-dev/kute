package app

import (
	"slices"

	"github.com/charmbracelet/lipgloss"

	"github.com/kute-dev/kute/internal/config"
	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/state"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// BuildSession loads persisted state/config, selects the Theme (decision
// #3: --theme flag, then the config file's theme: key, then terminal
// background detection), and builds the resource catalog. Cluster is a
// real *kube.Cluster unless cfg.Demo is set or the real cluster can't be
// reached, in which case it's nil (err carries the reason, nil in demo
// mode) — callers wire screens against kube/fake instead, behind the same
// seams (§0.10).
func BuildSession(cfg Config) (sess *tui.Session, cluster *kube.Cluster, err error) {
	userConfig := config.Load()
	sessionState := state.Load()
	theme := selectTheme(cfg.Theme, userConfig.Theme)

	sess = &tui.Session{
		Registry:   resources.DefaultRegistry(),
		Groups:     resources.DefaultGroups(),
		State:      sessionState,
		Config:     userConfig,
		Theme:      theme,
		Styles:     tui.NewStyles(theme),
		Version:    sessionVersion(cfg.Version),
		HelpScope:  helpScopeKeys(),
		HelpGlobal: helpGlobalKeys(),
	}

	if cfg.Demo {
		return sess, nil, nil
	}
	cluster, err = kube.NewClusterForContext(startupContext(sessionState))
	if err != nil {
		return sess, nil, err
	}
	sess.Cluster = cluster
	sess.Location.Context = cluster.Context.ContextName
	sess.Location.Namespace = cluster.Context.Namespace
	if pc, ok := sessionState.PerContext[cluster.Context.ContextName]; ok {
		if pc.Namespace != "" {
			sess.Location.Namespace = pc.Namespace
		}
		if pc.Kind != "" {
			sess.Location.Kind = kube.ResourceKind(pc.Kind)
		}
		sess.Location.Filter = pc.Filter
	}
	return sess, cluster, nil
}

// startupContext picks the kubeconfig context to launch against: the most
// recently used one (sessionState.RecentContexts[0], mirroring 7a's own
// per-context namespace/kind/filter restore) if the kubeconfig still has it,
// otherwise "" — the kubeconfig's own current-context — for a fresh install
// or a recent context that's since been removed.
func startupContext(sessionState state.State) string {
	if len(sessionState.RecentContexts) == 0 {
		return ""
	}
	recent := sessionState.RecentContexts[0]
	names, _, err := kube.AvailableContexts()
	if err != nil || !slices.Contains(names, recent) {
		return ""
	}
	return recent
}

// helpScopeKeys is the 7b help overlay's fixed SCOPE column (docs/design
// README.md §7b): the registered navigation verbs. The alt-tab bare-Enter
// toggle (model.go's mostRecentOther) isn't listed here — it's a modifier on
// the palette's own pre-selection, not a distinct action, and the palette's
// own footer (recentPickHint) already surfaces it in context.
func helpScopeKeys() []tui.KeyHint {
	return []tui.KeyHint{
		verbs.Goto.Hint(),
		verbs.Namespace.Hint(),
		verbs.Context.Hint(),
		verbs.AllNamespaces.Hint(),
	}
}

// helpGlobalKeys is 7b's fixed GLOBAL column, trimmed to bindings that
// actually exist today — the mockup's "p pause sync"/"r reconnect" aren't
// implemented yet (Phase 4), so listing them would document a lie.
//
// v.0.3.0.dc.html §29a moved the "identical on every screen" verbs (filter,
// mark, yaml, edit, events, timeline, meta — goto/namespace/context/
// all-namespaces already live in helpScopeKeys) out of every screen's own
// keybar; this column is their one remaining home so they stay discoverable.
func helpGlobalKeys() []tui.KeyHint {
	return []tui.KeyHint{
		{Key: "↑↓ jk", Label: "move"},
		{Key: "1-9", Label: "sort column"},
		verbs.Filter.Hint(),
		verbs.Mark.Hint(),
		verbs.YAML.Hint(),
		verbs.Edit.Hint(),
		verbs.Events.Hint(),
		verbs.Timeline.Hint(),
		verbs.Meta.Hint(),
		{Key: "esc", Label: "back"},
		{Key: "U", Label: "what's new"},
		verbs.Help.Hint(),
		{Key: "ctrl+q", Label: "quit"},
	}
}

// selectTheme resolves decision #3's precedence: flagTheme (--theme), then
// configTheme (config.yaml's theme: key), then terminal background
// detection. Any value other than "dark"/"light" falls through (so "auto"
// and typos both defer to detection rather than erroring).
func selectTheme(flagTheme, configTheme string) tui.Theme {
	for _, v := range []string{flagTheme, configTheme} {
		switch v {
		case "dark":
			return tui.Dark()
		case "light":
			return tui.Light()
		}
	}
	if lipgloss.HasDarkBackground() {
		return tui.Dark()
	}
	return tui.Light()
}
