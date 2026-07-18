package tui

import "github.com/kute-dev/kute/internal/kube"

type WindowSizeMsg struct {
	Width  int
	Height int
}

// GotoKindMsg asks the active task to switch kind in place, keeping the
// current namespace (the jump palette's kind-result Enter, mvp-plan.md
// Phase 2). The root shell updates Session.Location.Kind on this message
// (see model.go's Update) before forwarding it to the task.
type GotoKindMsg struct {
	Kind kube.ResourceKind
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
type ReplaceRootMsg struct {
	Task        Task
	Events      <-chan kube.ResourceChangedMsg
	Conn        <-chan kube.ConnStateMsg
	BuildSetup  func(kube.ConnState) Task
	BuildBrowse func() Task
}
