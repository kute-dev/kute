// Package setup is the recovery screen the root shell shows when there is
// nothing else to show yet (mvp-plan.md Phase 4, docs/design/README.md
// §4c/§10b): a cluster built from a valid kubeconfig context that hasn't
// answered a single ping (State Unreachable, 4c), or no kubeconfig found at
// all (State NoConfig, 10b). Both share one Chrome v2 skeleton and differ
// only in what State renders.
package setup

import (
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// State names which of the two screens this instance renders.
type State string

const (
	// Unreachable is 4c: a live *kube.Cluster exists (valid kubeconfig
	// context) but hasn't yet reported a successful connection.
	Unreachable State = "unreachable"
	// NoConfig is 10b: no kubeconfig could be resolved at all.
	NoConfig State = "no-config"
)

// Config are setup's dependencies, per repo convention (package-local
// Config struct, interface/func-typed fields, New fills zero values).
type Config struct {
	Session *tui.Session
	State   State
	// Err is the reason: NoConfig's build error (often a
	// *kube.ConfigLookupError, rendered as the LOOKED IN box when it is
	// one), or Unreachable's initial conn.Err mirrored here for the very
	// first render before any kube.ConnStateMsg has arrived.
	Err error
	// ClusterName is the context/cluster label the 4c title names.
	ClusterName string
	// Conn is the live connection state (Unreachable only) — updated as the
	// root shell forwards kube.ConnStateMsg, driving the retry attempt/
	// backoff countdown.
	Conn kube.ConnState
	// OtherContexts lists sibling kubeconfig context names for 4c's SWITCH
	// CONTEXT list (excluding the current one) — pre-probed in the
	// background (docs/design README.md §4c) via kube.ProbeContexts, the
	// same mechanism the 7a context palette already uses.
	OtherContexts  []string
	KubeconfigPath string
	// RetryNow re-probes the existing cluster without rebuilding it — 4c's
	// plain 'r' (cheap: no new client, no informer-factory leak). Nil for
	// NoConfig, which has no cluster yet to retry.
	RetryNow func()
	// Reconnect rebuilds the cluster from scratch against path ("" keeps
	// the current $KUBECONFIG resolution) — NoConfig's 'r'/'k', and 4c's
	// 'e' (a changed path can't be applied to an existing client, so
	// editing the path always means a full rebuild).
	Reconnect func(path string) tea.Cmd
	// SwitchToContext rebuilds the cluster pinned to a named sibling
	// kubeconfig context — 4c's SWITCH CONTEXT list's '↵' (docs/design
	// README.md §4c: "↵ connect to selected"). Nil for NoConfig, which has
	// no sibling contexts to switch among.
	SwitchToContext func(name string) tea.Cmd
}

// Model is setup's state.
type Model struct {
	width, height int

	session *tui.Session
	state   State
	err     error

	clusterName string
	conn        kube.ConnState
	// now is the wall-clock time as of the last kube.ConnStateMsg — the
	// retry countdown is computed from this rather than a clock read inside
	// Render (render must stay pure: f(model, theme, size)), mirroring
	// browse's identical 4a treatment.
	now            time.Time
	otherContexts  []string
	kubeconfigPath string

	// probes/probeGen back 4c's SWITCH CONTEXT list's live reachability —
	// same probe-and-drain shape as tui/context.go's 7a palette (a fresh
	// probeGen so a stale drain from a previous probe run, e.g. after 'r'
	// retries the current context, can't clobber a newer one's results).
	probes    map[string]kube.ProbeResult
	probeGen  int
	switchSel int

	retryNow        func()
	reconnect       func(path string) tea.Cmd
	switchToContext func(name string) tea.Cmd

	// editing/pathInput back 'e'/'k''s inline kubeconfig-path input —
	// browse's "/" filter query uses the same free-text-capture pattern.
	editing   bool
	pathInput textinput.Model

	retrying bool
	retryErr error
}

func New(cfg Config) Model {
	// 4c's SWITCH CONTEXT list defaults to the first sibling context
	// selected (not "current", which is the one screen already known to
	// be failing) — falls back to 0 (current) when there are no others.
	switchSel := 0
	if len(cfg.OtherContexts) > 0 {
		switchSel = 1
	}
	return Model{
		width:           tui.DefaultWidth,
		height:          tui.DefaultHeight,
		session:         cfg.Session,
		state:           cfg.State,
		err:             cfg.Err,
		clusterName:     cfg.ClusterName,
		conn:            cfg.Conn,
		now:             time.Now(),
		otherContexts:   cfg.OtherContexts,
		kubeconfigPath:  cfg.KubeconfigPath,
		retryNow:        cfg.RetryNow,
		reconnect:       cfg.Reconnect,
		switchToContext: cfg.SwitchToContext,
		switchSel:       switchSel,
	}
}

func (m Model) Init() tea.Cmd {
	// 4c: "pre-probed in the background" — kicks off as soon as the screen
	// exists, not gated on any keypress.
	if m.state == Unreachable && len(m.otherContexts) > 0 {
		return probeSwitchContextsCmd(m.probeGen, m.otherContexts)
	}
	return nil
}

func (m *Model) SetSize(width, height int) {
	size := tui.NormalizeSize(width, height)
	m.width, m.height = size.Width, size.Height
}

// CapturingInput reports whether the kubeconfig-path input is open, so the
// root shell lets every keystroke — including g/n/c/? — reach setup's own
// key handling instead of treating them as global shortcuts (mirrors
// browse's "/" filter).
func (m Model) CapturingInput() bool { return m.editing }
