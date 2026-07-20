package tui

import (
	"time"

	"github.com/kute-dev/kute/internal/kube"
)

type WindowSizeMsg struct {
	Width  int
	Height int
}

// GotoKindMsg asks the active task to switch kind in place, keeping the
// current namespace (the jump palette's kind-result Enter, mvp-plan.md
// Phase 2). The root shell updates Session.Location.Kind on this message
// (see model.go's Update) before forwarding it to the task. Filter, when
// non-empty, is applied as the destination kind's live filter query after
// the switch — 23b's "↵ on a listener filters to attached routes" (routetable
// jumping into the HTTPRoute list) is the one caller that sets it; every
// other GotoKindMsg site leaves it empty.
type GotoKindMsg struct {
	Kind   kube.ResourceKind
	Filter string
}

// GotoResourceMsg asks the active task to switch to Kind/Namespace (if not
// already showing it) and select Name once loaded — the jump palette's
// resource-result Enter.
type GotoResourceMsg struct {
	Kind      kube.ResourceKind
	Namespace string
	Name      string
}

// SwitchNamespaceMsg asks the active task to switch namespace in place,
// keeping kind and filter — the jump palette's namespace-result Enter.
type SwitchNamespaceMsg struct {
	Namespace string
}

// SetWhoCanVerbMsg asks the active task (tasks/whocan) to set its query's
// verb slot — the 'v' palette edit's Enter (docs/design README.md §22a).
// Forwarded straight to the active task, unlike GotoKindMsg/
// SwitchNamespaceMsg: whocan owns this state itself, there's no
// Session.Location mirror to update first.
type SetWhoCanVerbMsg struct {
	Verb string
}

// SetWhoCanResourceMsg asks the active task (tasks/whocan) to set its
// query's resource slot — the 'k' palette edit's Enter (§22a).
type SetWhoCanResourceMsg struct {
	Resource string
}

// SwitchContextMsg is the result of an async kube.Cluster.SwitchContext
// rebuild (7a's context palette Enter, mvp-plan.md Phase 3). Err set means
// the switch failed (e.g. an unreachable context) — the root shell and
// browse both leave Session.Location/their own state untouched rather than
// acting on a partial result. Filter is the target context's remembered
// filter query (state.PerContext, computed by context.go's
// switchContextCmd), restored by browse alongside Namespace/Kind.
type SwitchContextMsg struct {
	Context   string
	Namespace string
	Kind      kube.ResourceKind
	Filter    string
	Err       error
}

type ContextLoadedMsg struct {
	ClusterName string
	ContextName string
	Namespace   string
	Err         error
}

type TaskFeedbackMsg struct {
	State   TaskState
	Message string
}

type TaskActionMsg struct {
	ActionID string
}

// ReplaceRootMsg swaps the root shell's active task outright — no stack
// push, no BackMsg symmetry, unlike the generic "task.Update returned a
// different task" push in Model.Update — for the 4c/10b tasks/setup
// screen's successful reconnect (mvp-plan.md Phase 4). tui can't construct
// a tasks/setup or tasks/browse task itself (both import tui; tui importing
// either back would cycle — the same constraint Session.HelpScope/
// HelpGlobal already documents), so the composition root (internal/app,
// which imports all three) builds the replacement Task and sends it here.
// Events/Conn, when non-nil, are the freshly (re)built cluster's channels —
// WatchCluster starts forwarding them into the program without needing a
// *tea.Program handle (app.RunWithConfig's own forwardEvents goroutine only
// ever knew about the cluster live at process start). BuildSetup/
// BuildBrowse re-arm Model's 4c swap (see its neverConnected doc comment)
// for the new cluster — nil leaves 4c disabled for it (e.g. a --demo
// reconnect, if that's ever wired).
// UpdateCheckedMsg carries 28a/28b's ambient (or 'r'-forced) release-feed
// check's result. Err non-nil means the check failed (offline, airgapped,
// GitHub unreachable) — the root shell does nothing with it beyond that:
// no chip, no retry, no error surfaced anywhere (docs/design README.md
// §28a: "offline/airgapped → silently no chip, no retry storm"). LatestVersion/
// CheckedAt are what get folded into Session.State.UpdateCheck (persisted
// at exit, like every other State field); Info populates Session.Update
// in-memory for 28b to render.
type UpdateCheckedMsg struct {
	Info          UpdateInfo
	LatestVersion string
	CheckedAt     time.Time
	Err           error
}

// OpenUpdatePanelMsg asks the root shell to push 28b (docs/design
// README.md §28a/28b) — sent by the goto palette's synthetic "update" item
// (gotoDispatch's gotoOpenUpdatePanel case), the ":update" path, so it
// flows through the same Cmd-returns-a-Msg path as every other palette
// dispatch. The direct 'U' shortcut (model.go's handleShellKey) does the
// same push without needing a message hop.
type OpenUpdatePanelMsg struct{}

type ReplaceRootMsg struct {
	Task        Task
	Events      <-chan kube.ResourceChangedMsg
	Conn        <-chan kube.ConnStateMsg
	BuildSetup  func(kube.ConnState) Task
	BuildBrowse func() Task
}
