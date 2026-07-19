package tui

import (
	"reflect"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui/components"
	"github.com/kute-dev/kute/internal/tui/components/palette"
)

// TaskState identifies the current user-facing state of the active task screen.
type TaskState string

const (
	TaskStateLoading          TaskState = "loading"
	TaskStateReady            TaskState = "ready"
	TaskStateEmpty            TaskState = "empty"
	TaskStateError            TaskState = "error"
	TaskStatePermissionDenied TaskState = "permission-denied"
	TaskStateConfirming       TaskState = "confirming"
	TaskStateSuccess          TaskState = "success"
	TaskStateCancelled        TaskState = "cancelled"
)

// KubernetesContext describes the active Kubernetes target shown in the TUI.
type KubernetesContext struct {
	ClusterName string
	ContextName string
	Namespace   string
}

// TaskScope describes the Kubernetes target and verb for a task action.
type TaskScope struct {
	ResourceKind string
	ResourceName string
	Namespace    string
	Verb         string
	IsMutating   bool
	// Revision is the target revision for a "rollback" verb (18a) — 0 means
	// Helm's own default (the previous revision). Unset (0) for every other
	// verb.
	Revision int
	// Replicas is the target replica count for a "scale" verb (17b). Unset
	// (0) for every other verb.
	Replicas int32
}

// TaskAction describes an operation available from a task screen.
// Confirmation-needed is derived from the Tier passed to
// actions.Controller.Begin (mvp-plan.md §8b), not a field here. Owner is an
// optional presentation hint for the 8b type-the-name modal — "Kind/name"
// of the controller that will recreate this resource once deleted (e.g.
// "ReplicaSet/nva-worker-abc123"), empty when unknown/not applicable.
type TaskAction struct {
	ID    string
	Label string
	Scope TaskScope
	Owner string
	// GracePeriodSeconds is the target pod's actual termination grace
	// period (8b's delete confirm shows this concrete figure per docs/
	// design README.md §8b, e.g. "30s") — nil when not known/applicable
	// (every non-Pod kind, or Pod kinds whose caller doesn't resolve it).
	GracePeriodSeconds *int64
}

// Task is the contract each active Bubble Tea task model must implement.
type Task interface {
	tea.Model
	SetSize(width, height int)
}

// BackMsg requests returning to the previous task without quitting the program.
type BackMsg struct{}

// Model wraps the active task and applies shared root-level TUI behavior:
// the task stack, the jump/namespace/context palette overlay, the help
// overlay, and connection state (mvp-plan.md §0.9). The overlay/mode
// machinery only engages for tasks implementing the Chrome v2 Screen
// contract — legacy (LegacyScreen) tasks get exactly today's passthrough
// behavior, so their own key handling (e.g. home's internal picker) is
// never shadowed.
type Model struct {
	task  Task
	stack []Task

	width, height int
	mode          Mode
	conn          kube.ConnState
	palette       *palette.Model
	helpOpen      bool
	session       *Session

	// probes/probeGen back the 7a context palette's lazily-streamed
	// reachability results (kube.ProbeContexts): probes holds what's
	// arrived so far, keyed by context name. probeGen is the current probe
	// run's generation (bumped by startContextProbe, mirroring browse's
	// reloadEpoch/metricsEpoch guard) — contextProbeMsg/contextProbesDoneMsg
	// carry the generation they belong to, so reopening/re-probing while a
	// previous run is still draining doesn't redirect the stale drain loop
	// onto the new run. Probing continues in the background even after the
	// palette closes (docs/design README.md's 4c phrasing: "probing other
	// kubeconfig contexts in the background").
	probes   map[string]kube.ProbeResult
	probeGen int

	// namespaceItemsCache holds the 6a namespace palette's unfiltered item
	// list (live pod counts, +CPU shares once they land — see
	// namespaceCPUSharesMsg) for the palette session currently open.
	// namespaceItems does one informer Count call per namespace (fast,
	// cache-backed), so it's fetched once when the palette opens rather
	// than on every query edit — refetching per keystroke made typing feel
	// unresponsive whenever the round trip was slow (each fuzzy-filter edit
	// paid the full N-namespace fetch again). refreshNamespacePalette only
	// re-filters this cache.
	namespaceItemsCache []palette.Item
	// namespaceGen guards namespaceCPUSharesMsg against a stale fetch (from
	// a since-closed/reopened namespace palette) landing after a newer one
	// already replaced namespaceItemsCache — mirrors probeGen.
	namespaceGen int

	// whoCanVerbItemsCache/whoCanResourceItemsCache hold tasks/whocan's (22a)
	// 'v'/'k' palette's unfiltered item lists for the palette session
	// currently open — both lists are static (a fixed verb vocabulary, the
	// registry's kind list), so unlike namespaceItemsCache there's no live
	// fetch to avoid repeating; the cache still exists so refresh*Palette can
	// re-filter by query without losing each row's "current" tag.
	whoCanVerbItemsCache     []palette.Item
	whoCanResourceItemsCache []palette.Item

	// neverConnected/showingSetup/buildSetup/buildBrowse back the 4c
	// "unreachable at launch" swap (mvp-plan.md Phase 4): neverConnected is
	// true from construction whenever a real (non-demo) cluster exists that
	// hasn't yet reported a successful connection; if the *first* signal
	// about it is trouble (Reconnecting/Failed) rather than Connected, the
	// root shell swaps the active task to tasks/setup (built via buildSetup,
	// since tui can't import tasks/setup — or tasks/browse for buildBrowse —
	// without an import cycle, the same constraint Session.HelpScope/
	// HelpGlobal already documents) and swaps back to a fresh browse task
	// the moment a Connected state arrives. Once any Connected state has
	// been observed, neverConnected latches false for good — a later
	// mid-session drop is 4a (handled entirely inside browse), not this.
	neverConnected bool
	showingSetup   bool
	buildSetup     func(kube.ConnState) Task
	buildBrowse    func() Task
}

// New creates a root TUI model for the provided task, with no Session (no
// overlay/mode routing — legacy-screen behavior).
func New(task Task) Model {
	return Model{task: task, mode: ModeBrowse}
}

// NewWithSession creates a root TUI model with a Session, enabling the
// overlay/mode shell routing for tasks that implement Screen.
func NewWithSession(task Task, session *Session) Model {
	return Model{task: task, mode: ModeBrowse, session: session}
}

// WithRootFactories installs the 4c/10b task factories (see the doc comment
// on Model's neverConnected field) and arms neverConnected whenever session
// carries a live, not-yet-confirmed-reachable cluster. Composed onto
// NewWithSession's result by the composition root (internal/app), which is
// the only package that can import both tui and tasks/setup/tasks/browse.
// A no-op call (buildSetup nil) leaves 4c disabled — the --demo and
// no-kubeconfig-at-launch paths have no cluster to watch for that swap.
func (m Model) WithRootFactories(buildSetup func(kube.ConnState) Task, buildBrowse func() Task) Model {
	m.buildSetup = buildSetup
	m.buildBrowse = buildBrowse
	m.neverConnected = buildSetup != nil && m.session != nil && m.session.Cluster != nil
	return m
}

// Mode is the current shell mode (drives the keybar pill while an overlay
// is open).
func (m Model) Mode() Mode { return m.mode }

// Conn is the last known connection state (from kube.ConnStateMsg).
func (m Model) Conn() kube.ConnState { return m.conn }

// Session is the shell's cross-screen state, or nil if the model wasn't
// built with one.
func (m Model) Session() *Session { return m.session }

// PaletteOpen reports whether the jump/namespace/context palette overlay is
// showing.
func (m Model) PaletteOpen() bool { return m.palette != nil }

// HelpOpen reports whether the help overlay is showing.
func (m Model) HelpOpen() bool { return m.helpOpen }

func (m Model) Init() tea.Cmd {
	return m.task.Init()
}

// resizeTask pushes the last known terminal size onto a task that was just
// swapped in outside the normal push path (4c's setup↔browse swaps,
// ReplaceRootMsg) — a fresh task is built at tui.Default* dimensions and
// would otherwise render letterboxed until the user resizes the terminal. A
// no-op before the first WindowSizeMsg, so such a task keeps its defaults.
func (m Model) resizeTask() {
	if m.width > 0 && m.height > 0 {
		m.task.SetSize(m.width, m.height)
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Unwrap a reconnected cluster's forwarded event/conn message (see
	// clusterwatch.go) to the same kube types the switch below and every
	// task already handle, and re-arm the drain — from here on this frame
	// behaves exactly as if the original forwardEvents goroutine had sent
	// msg directly.
	var rewatch tea.Cmd
	switch wm := msg.(type) {
	case clusterEventMsg:
		msg = wm.inner
		rewatch = WatchCluster(wm.events, wm.conn)
	case clusterConnMsg:
		msg = wm.inner
		rewatch = WatchCluster(wm.events, wm.conn)
	}

	switch msg := msg.(type) {
	case ReplaceRootMsg:
		m.task = msg.Task
		m.resizeTask()
		m.stack = nil
		m.showingSetup = false
		m.buildSetup = msg.BuildSetup
		m.buildBrowse = msg.BuildBrowse
		m.neverConnected = msg.BuildSetup != nil
		cmds := []tea.Cmd{m.task.Init()}
		if msg.Events != nil {
			cmds = append(cmds, WatchCluster(msg.Events, msg.Conn))
		}
		return m, tea.Batch(cmds...)
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.task.SetSize(msg.Width, msg.Height)
	case WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.task.SetSize(msg.Width, msg.Height)
	case BackMsg:
		if len(m.stack) > 0 {
			m.task = m.stack[len(m.stack)-1]
			m.stack = m.stack[:len(m.stack)-1]
		}
		return m, nil
	case kube.ConnStateMsg:
		// Also forwarded to the task below (unchanged msg), so browse can
		// render its own stale strip (Phase 4).
		m.conn = kube.ConnState(msg)
		switch {
		case m.conn.Phase == kube.ConnConnected:
			m.neverConnected = false
			if m.showingSetup && m.buildBrowse != nil {
				m.showingSetup = false
				m.task = m.buildBrowse()
				m.resizeTask()
				m.stack = nil
				next := tea.Cmd(m.task.Init())
				if rewatch != nil {
					next = tea.Batch(next, rewatch)
				}
				return m, next
			}
		case m.neverConnected && !m.showingSetup && m.buildSetup != nil && m.conn.Offline():
			m.showingSetup = true
			m.task = m.buildSetup(m.conn)
			m.resizeTask()
			m.stack = nil
			next := tea.Cmd(m.task.Init())
			if rewatch != nil {
				next = tea.Batch(next, rewatch)
			}
			return m, next
		}
	case GotoKindMsg:
		// Also forwarded to the task below: Session.Location stays the
		// single source of truth for the breadcrumb, browse mutates its own
		// kind/rows off the same message (mvp-plan.md Phase 2).
		if m.session != nil {
			m.session.Location.Kind = msg.Kind
		}
	case GotoResourceMsg:
		if m.session != nil {
			m.session.Location.Kind = msg.Kind
			m.session.Location.Namespace = msg.Namespace
			m.session.Location.Resource = msg.Name
		}
	case SwitchNamespaceMsg:
		if m.session != nil {
			m.session.Location.Namespace = msg.Namespace
		}
	case SwitchContextMsg:
		// Also forwarded to the task below (unchanged msg), so browse can
		// switch its own kind/namespace and reload against the rebuilt
		// cluster (mvp-plan.md Phase 3, 7a). Err set means the switch
		// failed — Session.Location and the task both stay put. The
		// registry/groups rebuild happens here (main Update goroutine),
		// not inside switchContextCmd's closure — that closure already
		// documents "only touches the stable *kube.Cluster pointer" to
		// avoid racing the render goroutine's Session reads, and
		// cluster.SwitchContext (which folds discovery in) has already
		// completed synchronously by the time this message arrives.
		if m.session != nil && msg.Err == nil {
			m.session.Location = Location{Context: msg.Context, Namespace: msg.Namespace, Kind: msg.Kind, Filter: msg.Filter}
			if m.session.Cluster != nil {
				m.session.Registry, m.session.Groups = resources.BuildDiscoveredRegistry(m.session.Cluster.DiscoveredKinds(), m.session.Cluster)
			}
		}
	case kube.CRDsDiscoveredMsg:
		// The one connect path where discovery finishes outside any
		// tea.Cmd the root can await synchronously (context.go's
		// switchContextCmd and app.attemptReconnect both already rebuild
		// the registry inline, before their own result message is
		// returned) — see kube.CRDsDiscoveredMsg's doc comment. Swallowed
		// (not forwarded to the task): browse.switchKind already re-reads
		// Session.Registry fresh on every kind switch, so there's nothing
		// for the active task to react to here.
		if m.session != nil && m.session.Cluster != nil {
			m.session.Registry, m.session.Groups = resources.BuildDiscoveredRegistry(m.session.Cluster.DiscoveredKinds(), m.session.Cluster)
		}
		return m, nil
	case contextProbeMsg:
		// A stale gen (a since-superseded probe run) still drains its own
		// channel to completion — see contextProbeMsg's doc comment — but
		// its result isn't applied to m.probes/the palette.
		if msg.gen == m.probeGen {
			if m.probes == nil {
				m.probes = map[string]kube.ProbeResult{}
			}
			m.probes[msg.res.Name] = msg.res
			if m.palette != nil && m.palette.Scope == palette.ScopeContext {
				m.refreshPalette()
			}
		}
		return m, waitForProbe(msg.gen, msg.ch)
	case contextProbesDoneMsg:
		return m, nil
	case namespaceCPUSharesMsg:
		// A stale gen (a since-closed/reopened namespace palette) is
		// dropped — see namespaceGen's doc comment.
		if msg.gen == m.namespaceGen {
			applyNamespaceCPUShares(m.namespaceItemsCache, msg.shares)
			if m.palette != nil && m.palette.Scope == palette.ScopeNamespace {
				m.refreshNamespacePalette()
			}
		}
		return m, nil
	case namespaceSyncRetryMsg:
		// A stale gen (a since-closed/reopened namespace palette) is
		// dropped — see namespaceGen's doc comment.
		if msg.gen != m.namespaceGen || m.palette == nil || m.palette.Scope != palette.ScopeNamespace {
			return m, nil
		}
		return m, m.loadNamespacePalette(msg.gen)
	case tea.KeyPressMsg:
		if _, ok := m.task.(Screen); ok && !taskCapturingInput(m.task) {
			if handled, next, cmd := m.handleShellKey(msg); handled {
				return next, cmd
			}
		}
	}

	updated, cmd := m.task.Update(msg)
	if task, ok := updated.(Task); ok {
		if !sameTask(task, m.task) {
			m.stack = append(m.stack, m.task)
		}
		m.task = task
	}
	if rewatch != nil {
		cmd = tea.Batch(cmd, rewatch)
	}

	return m, cmd
}

// handleShellKey routes keys the root shell owns: overlay navigation while
// a palette/help panel is open, otherwise g/n/c/? to open one. Reports
// false when the key isn't the shell's to handle, so the caller falls
// through to the task.
func (m Model) handleShellKey(msg tea.KeyPressMsg) (bool, Model, tea.Cmd) {
	if m.palette != nil {
		return m.handlePaletteKey(msg)
	}
	if m.helpOpen {
		switch msg.String() {
		case "esc", "?":
			m.helpOpen = false
			m.mode = ModeBrowse
		}
		return true, m, nil
	}
	if m.session == nil {
		return false, m, nil
	}
	if wc, ok := m.task.(WhoCanScoped); ok {
		switch msg.String() {
		case "v":
			verb, _ := wc.WhoCanQuery()
			return true, m, m.openVerbPalette(verb)
		case "k":
			_, resource := wc.WhoCanQuery()
			return true, m, m.openResourcePalette(resource)
		}
	}
	switch msg.String() {
	case "g", ":":
		return true, m, m.openPalette(palette.ScopeGoto, "›", "jump anywhere")
	case "n":
		return true, m, m.openPalette(palette.ScopeNamespace, "ns ›", "")
	case "c":
		return true, m, m.openPalette(palette.ScopeContext, "ctx ›", "")
	case "?":
		m.helpOpen = true
		m.mode = ModeHelp
	default:
		return false, m, nil
	}
	return true, m, nil
}

// handlePaletteKey drives the open palette: linear navigation, typing/
// backspace re-filtering, Enter's navigation dispatch (per-scope:
// gotoDispatch/namespaceDispatch/contextDispatch), "r" re-probing and
// "ctrl+p" mark/unmark-prod on the context palette. Every scope shares one
// alt-tab grammar (docs/design
// README.md §2b/§6a/§7a): opening the palette pre-selects the most recent
// *other* entry (mostRecentOther), so a bare open+Enter with no typing toggles
// straight back to it — same two keystrokes the old "n n"/"c c" double-tap
// used, now visible through the palette instead of bypassing it.
func (m Model) handlePaletteKey(msg tea.KeyPressMsg) (bool, Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.palette = nil
		m.mode = ModeBrowse
	case "enter":
		item, ok := m.palette.Selected()
		scope := m.palette.Scope
		m.palette = nil
		m.mode = ModeBrowse
		if !ok {
			return true, m, nil
		}
		var cmd tea.Cmd
		switch scope {
		case palette.ScopeGoto:
			cmd = gotoDispatch(m.session, item)
			if cmd != nil && len(m.stack) > 0 {
				// The goto palette can be opened from any Screen, not just
				// the root browse task (e.g. poddetail) — but only browse
				// handles GotoKindMsg/GotoResourceMsg. Unwind the stack back
				// to the root task first, or the message lands on a screen
				// that ignores it and the jump silently does nothing.
				m.task = m.stack[0]
				m.stack = nil
			}
		case palette.ScopeNamespace:
			cmd = namespaceDispatch(m.session, item)
		case palette.ScopeContext:
			cmd = contextDispatch(m.session, item)
		case palette.ScopeVerb:
			cmd = whoCanVerbDispatch(item)
		case palette.ScopeResource:
			cmd = whoCanResourceDispatch(item)
		}
		if cmd != nil {
			return true, m, cmd
		}
	// No plain j/k synonyms here, unlike list screens: the palette is a text
	// input, and 12a's "type to narrow" must let those letters reach the
	// query (its keybar advertises ↑↓ accordingly). alt+j/alt+k are safe
	// additions though — a Key with ModAlt set never carries Text
	// (charm.land/bubbletea/v2's Key.Text doc), so they can't leak into the
	// query and fall through to typeChar's default case.
	case "up", "alt+k":
		m.movePalette(-1)
	case "down", "alt+j":
		m.movePalette(1)
	case "backspace":
		if q := m.palette.Query; len(q) > 0 {
			m.palette.Query = q[:len(q)-1]
		}
		m.palette.Browse = m.palette.Scope == palette.ScopeGoto && m.palette.Query == ""
		m.refreshPalette()
	case "tab":
		// docs/design README.md §2b: "tab complete" fills the query in to the
		// highlighted result's own label, so a fuzzy match can be completed
		// then narrowed further rather than jumped to immediately.
		if item, ok := m.palette.Selected(); ok {
			m.palette.Query = item.Label
			m.palette.Browse = false
			m.refreshPalette()
		}
	case "r":
		if m.palette.Scope == palette.ScopeContext {
			return true, m, m.startContextProbe()
		}
		m.typeChar(msg)
	case "ctrl+p":
		// Ctrl-chorded (like browse's ctrl-d/ctrl-k) rather than a bare
		// letter: "p"/"P" are common leading characters for prod context
		// names ("prod-eks") and must keep reaching the fuzzy query.
		if m.palette.Scope == palette.ScopeContext {
			m.toggleSelectedContextProd()
		}
	default:
		m.typeChar(msg)
	}
	return true, m, nil
}

// otherRecents filters current out of recents (most-recent-first, see
// state.State's doc comment), preserving order. Every numbered/alt-tab
// mechanic — mostRecentOther's bare-Enter target, digitRecentTarget's
// query-digit lookup, recentNumbers' gutter-digit assignment — indexes
// against this same "recents minus current" list, so they always agree on
// what "1" means.
func otherRecents(recents []string, current string) []string {
	out := make([]string, 0, len(recents))
	for _, r := range recents {
		if r != current {
			out = append(out, r)
		}
	}
	return out
}

// mostRecentOther returns the first entry in recents that isn't current, or
// ("", false) if there is none — the shared alt-tab target every scope's
// *BrowseSelection helper (gotoBrowseSelection, namespaceBrowseSelection,
// contextBrowseSelection) resolves against, and the same entry recentNumbers
// assigns digit "1" to.
func mostRecentOther(recents []string, current string) (string, bool) {
	others := otherRecents(recents, current)
	if len(others) == 0 {
		return "", false
	}
	return others[0], true
}

// numberedRecents is otherRecents with its own first entry (index 0, the
// "previous" alt-tab target) also dropped: current and previous both
// already have their own on-row tag ("current"/"previous"), so repeating
// them in the numbered pick or the RECENT summary row would be redundant —
// digit "1" is whatever's next after that. mostRecentOther/the bare-Enter
// alt-tab toggle are unaffected — they keep resolving against otherRecents,
// not this. Shared by recentNumbers (row gutter), the RECENT row's label
// list (namespaceRecentLabels/contextRecentLabels), and
// refreshNamespacePalette/refreshContextPalette's digitRecentTarget lookup,
// so all three always agree on what "1" means.
func numberedRecents(recents []string, current string) []string {
	others := otherRecents(recents, current)
	if len(others) == 0 {
		return nil
	}
	return others[1:]
}

// recentNumbers assigns each of numberedRecents' entries a 1-based digit,
// most-recent-first, capped at 9 (only '1'-'9' are addressable from the
// keyboard). Used both to render each row's gutter digit
// (palette.Item.RecentNum) and, via numberedRecents, to resolve
// digitRecentTarget's query-digit lookup — so the number on screen always
// matches what typing it does.
func recentNumbers(recents []string, current string) map[string]int {
	others := numberedRecents(recents, current)
	if len(others) > 9 {
		others = others[:9]
	}
	nums := make(map[string]int, len(others))
	for i, r := range others {
		nums[r] = i + 1
	}
	return nums
}

// promoteRecentItems reorders items in place so current, then the row
// tagged "previous", then the numbered 1-9 recents (in digit order) lead —
// current/previous/recentNumbers must already be applied to each item's
// Tag/RecentNum before calling this. Everything else keeps its existing
// relative order (stable sort). Callers pass only the plain result rows —
// never a pinned trailer (6a's "all namespaces" row, capNamespaceItems'
// "+N more" note), which stay fixed at the bottom by construction.
//
// This only visibly affects the empty-query browse state: once a query is
// typed, palette.Filter's fuzzy.Find re-sorts by match score regardless of
// input order, so a promoted item's position here has no effect on filtered
// results — exactly the "recency ordering, not while searching" behavior
// wanted (docs/design README.md §6a/§7a).
func promoteRecentItems(items []palette.Item) {
	priority := func(it palette.Item) int {
		switch {
		case it.Tag == "current":
			return 0
		case it.Tag == "previous":
			return 1
		case it.RecentNum > 0:
			return 1 + it.RecentNum // 2..10, in digit order
		default:
			return 1 << 30
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return priority(items[i]) < priority(items[j])
	})
}

// digitRecentTarget resolves a typed query to a 1-based index into others
// (a numberedRecents result — current and previous both already excluded,
// matching recentNumbers' gutter digits) — so numbers give direct keyboard
// access to what's already on screen. Only a single-digit '1'-'9' query
// matches; anything else (empty, two-plus characters, a non-digit, or a
// digit past the end of others) returns false and the caller falls back to
// normal fuzzy filtering. This mirrors gotoAliasMatch's single-rune-only
// rule: typing a second character makes the digit "just the query's first
// char" instead of a shortcut, so e.g. "2048" still filters to a
// namespace/context named that.
func digitRecentTarget(query string, others []string) (string, bool) {
	r := []rune(query)
	if len(r) != 1 {
		return "", false
	}
	d := r[0]
	if d < '1' || d > '9' {
		return "", false
	}
	n := int(d - '0')
	if n > len(others) {
		return "", false
	}
	return others[n-1], true
}

// recentPickHint is 6a/7a's shared RECENT-row trailing hint — "1-9 recent ·
// ↵ toggles last" — advertising both digitRecentTarget's numbered shortcut
// (whose digits also appear directly on the rows themselves, recentNumbers)
// and the alt-tab bare-Enter grammar in one line.
func recentPickHint() []palette.FooterSpan {
	return []palette.FooterSpan{
		{Text: "1-9", Tone: palette.FooterKey},
		{Text: " recent · ", Tone: palette.FooterDim},
		{Text: "↵", Tone: palette.FooterKey},
		{Text: " toggles last", Tone: palette.FooterDim},
	}
}

// typeChar appends a printable keypress to the palette's query and
// re-filters, common to every scope's default typing path.
func (m *Model) typeChar(msg tea.KeyPressMsg) {
	if msg.Text == "" {
		return
	}
	m.palette.Query += msg.Text
	m.palette.Browse = false
	m.refreshPalette()
}

// startContextProbe (re)opens the 7a context palette's data: reset probe
// results, populate items immediately (every row starts "probing…"), and
// kick off a fresh kube.ProbeContexts run — used both by openPalette and by
// the palette's own "r" re-probe key.
func (m *Model) startContextProbe() tea.Cmd {
	m.palette.Hint = contextHint()
	m.probes = map[string]kube.ProbeResult{}
	m.probeGen++
	m.refreshContextPalette()
	return probeContextsCmd(m.probeGen, contextNames())
}

// movePalette routes an up/down press to the palette's linear list.
func (m *Model) movePalette(delta int) {
	m.palette.Move(delta)
}

// refreshPalette rebuilds the open palette's Items/Hint/Recent/Sel for its
// current scope/Browse/Query state — called after every query edit. A no-op
// with no session.
func (m *Model) refreshPalette() {
	if m.palette == nil || m.session == nil {
		return
	}
	switch m.palette.Scope {
	case palette.ScopeGoto:
		m.refreshGotoPalette()
	case palette.ScopeNamespace:
		m.refreshNamespacePalette()
	case palette.ScopeContext:
		m.refreshContextPalette()
	case palette.ScopeVerb:
		m.refreshWhoCanVerbPalette()
	case palette.ScopeResource:
		m.refreshWhoCanResourcePalette()
	}
}

// refreshGotoPalette rebuilds the goto palette's Items/Hint/Recent/Sel/
// Footer for its current Browse/Query state — called after every query edit
// so typing a character switches 12a's ranked-chips list to 2b/12b's fuzzy
// results (and clearing back to an empty query restores the ranked list).
func (m *Model) refreshGotoPalette() {
	m.palette.Hint = gotoHint(m.session)
	if m.palette.Browse {
		m.palette.Items = gotoBrowseItems(m.session)
		m.palette.Recent = nil
		m.palette.Sel = gotoBrowseSelection(m.palette.Items, m.session.State.RecentKinds, m.session.Location.Kind)
		m.palette.Footer = gotoAliasFooter()
		return
	}
	m.palette.Items = gotoFuzzyItems(m.session, m.palette.Query)
	m.palette.Recent = gotoRecentKindLabels(m.session)
	m.palette.Sel = 0
	m.palette.Footer = gotoAliasMatchFooter(m.session, m.palette.Query)
}

// refreshNamespacePalette re-filters the 6a namespace list against the
// current query (the same "one palette shell, same fuzzy input" as every
// other scope — docs/design README.md's system-wide interactions). It
// filters m.namespaceItemsCache rather than re-fetching from the cluster —
// see that field's doc comment — so it never blocks on cluster/metrics
// calls. A query that's a bare digit '1'-'9' short-circuits to
// digitRecentTarget's RECENT-row pick instead of fuzzy-filtering on the
// digit text itself (see that func's doc comment).
func (m *Model) refreshNamespacePalette() {
	// namespaceRecentLabels already excludes current and previous (see its
	// doc comment) — it IS the numberedRecents list, so it doubles as
	// digitRecentTarget's lookup with no further filtering.
	recents := namespaceRecentLabels(m.session)
	capped := capNamespaceItems(m.namespaceItemsCache)
	if target, ok := digitRecentTarget(m.palette.Query, recents); ok {
		if i, ok := namespaceItemIndex(capped, target); ok {
			m.palette.Items = capped
			m.palette.Recent = recents
			m.palette.Sel = i
			m.palette.Footer = namespaceRecentFooter(target)
			return
		}
	}
	items := m.namespaceItemsCache
	if m.palette.Query != "" {
		items = palette.Filter(items, m.palette.Query)
	}
	m.palette.Items = capNamespaceItems(items)
	m.palette.Recent = recents
	m.palette.Sel = namespacePaletteSelection(m.session, m.palette.Items, m.palette.Query)
	m.palette.Footer = nil
}

// refreshContextPalette rebuilds the 7a context list against the latest
// probe results, fuzzy-filtered by the current query — or, for a bare digit
// '1'-'9' query, jumps Sel straight to that RECENT-row entry instead
// (digitRecentTarget), mirroring refreshNamespacePalette.
func (m *Model) refreshContextPalette() {
	// contextRecentLabels already excludes current and previous (see its
	// doc comment) — it IS the numberedRecents list, so it doubles as
	// digitRecentTarget's lookup with no further filtering.
	recents := contextRecentLabels(m.session)
	items := contextItems(m.session, m.probes)
	if target, ok := digitRecentTarget(m.palette.Query, recents); ok {
		if i, ok := contextItemIndex(items, target); ok {
			m.palette.Items = items
			m.palette.Recent = recents
			m.palette.Sel = i
			m.palette.Footer = contextRecentFooter(target)
			return
		}
	}
	if m.palette.Query != "" {
		items = palette.Filter(items, m.palette.Query)
	}
	m.palette.Items = items
	m.palette.Recent = recents
	m.palette.Sel = contextPaletteSelection(m.session, m.palette.Items, m.palette.Query)
	m.palette.Footer = nil
}

// openPalette opens the shell's one palette instance scoped to scope,
// populating real Items/Hint/Sel from Session, and returns the tea.Cmd the
// scope needs to kick off (nil for goto/namespace; the context palette's
// probe drain for context). Browse (12a's ranked-chips list) is goto-only —
// namespace/context are always plain lists.
func (m *Model) openPalette(scope palette.Scope, prompt, hint string) tea.Cmd {
	m.palette = &palette.Model{Scope: scope, Prompt: prompt, Hint: hint, Browse: scope == palette.ScopeGoto}
	m.mode = ModeGoto
	if m.session == nil {
		return nil
	}
	switch scope {
	case palette.ScopeGoto:
		m.refreshGotoPalette()
	case palette.ScopeNamespace:
		m.palette.Hint = namespaceHint(m.session)
		m.palette.ColumnHeaders = namespaceColumnHeadersFor(namespaceCountDescriptor(m.session))
		m.palette.NameColumnLabel = "NAMESPACE"
		m.palette.GutterGlyph = namespaceGutterGlyph
		m.palette.RecentHint = recentPickHint()
		m.palette.Recent = namespaceRecentLabels(m.session)
		m.namespaceGen++
		return m.loadNamespacePalette(m.namespaceGen)
	case palette.ScopeContext:
		m.palette.ColumnHeaders = contextColumnHeaders()
		m.palette.NameColumnLabel = "CONTEXT"
		m.palette.RecentHint = recentPickHint()
		return m.startContextProbe()
	}
	return nil
}

// taskCapturingInput reports whether the active task has an open free-text
// input that wants every keystroke (see InputCapturer) — false for tasks
// that don't implement the interface at all.
func taskCapturingInput(task Task) bool {
	c, ok := task.(InputCapturer)
	return ok && c.CapturingInput()
}

func sameTask(a, b Task) bool {
	if a == nil || b == nil {
		return a == b
	}
	av := reflect.ValueOf(a)
	bv := reflect.ValueOf(b)
	if av.Kind() == reflect.Pointer && bv.Kind() == reflect.Pointer {
		return av.Pointer() == bv.Pointer()
	}
	return reflect.TypeOf(a) == reflect.TypeOf(b)
}

func (m Model) View() tea.View {
	view := m.task.View()
	view.AltScreen = true
	if m.session == nil || (m.palette == nil && !m.helpOpen) {
		return view
	}

	width, height := m.width, m.height
	if width <= 0 {
		width = DefaultWidth
	}
	if height <= 0 {
		height = DefaultHeight
	}
	theme := m.session.Theme
	dim := lipgloss.NewStyle().Foreground(theme.TextGhost)

	var panel string
	switch {
	case m.palette != nil:
		panel = m.palette.Render(paletteStyles(theme), width)
	case m.helpOpen:
		screen, ok := m.task.(Screen)
		if !ok {
			return view
		}
		panel = renderHelp(theme, screen, m.session.HelpScope, m.session.HelpGlobal, width)
	}
	view.Content = components.Compose(view.Content, panel, width, height, paletteTop, dim)

	// 2b: "Main keybar while open: GOTO mode pill + one-line explanation" —
	// Compose dims the whole base uniformly (including the underlying
	// screen's own keybar band, which still reads its own PillText), so the
	// goto palette needs its own undimmed keybar line spliced in on top,
	// the same way the palette panel itself stays undimmed. Scoped to goto
	// only: 6a/7a's namespace/context palettes have no equivalent spec'd
	// bullet, so inventing pill copy for them isn't a spec-driven fix.
	if m.palette != nil && m.palette.Scope == palette.ScopeGoto {
		panelHeight := len(strings.Split(panel, "\n"))
		actualTop := max(min(paletteTop, height-panelHeight), 0)
		if actualTop+panelHeight <= height-1 {
			view.Content = replaceLastLine(view.Content, gotoKeybarLine(theme, width))
		}
	}
	// 7b: "Keybar pill HELP" — same reasoning as goto's splice above: Compose
	// dims the whole base uniformly, including the underlying screen's own
	// keybar band, so the help overlay needs its own undimmed HELP-pilled
	// line spliced in on top rather than inventing a new compose path.
	if m.helpOpen {
		view.Content = replaceLastLine(view.Content, helpKeybarLine(theme, width))
	}
	return view
}

// gotoKeybarLine renders 2b's main-keybar-while-open treatment: a GOTO mode
// pill plus a one-line explanation, reusing renderKeybarV2 for the exact
// same chrome (border/inset/pill shape) every other screen's keybar gets.
func gotoKeybarLine(theme Theme, width int) string {
	kb := Keybar{
		Pill:      ModeGoto,
		PillText:  "GOTO",
		RightNote: "jump to any kind, resource, namespace, or context",
	}
	return renderKeybarV2(kb, theme, width)
}

// helpKeybarLine renders 7b's "Keybar pill HELP" treatment, the same
// undimmed-splice shape gotoKeybarLine already establishes for 2b.
func helpKeybarLine(theme Theme, width int) string {
	kb := Keybar{
		Pill:     ModeHelp,
		PillText: "HELP",
	}
	return renderKeybarV2(kb, theme, width)
}

// replaceLastLine swaps content's final "\n"-joined line for line — the
// keybar band is always exactly Frame's last line (LegendHeight == 1).
func replaceLastLine(content, line string) string {
	idx := strings.LastIndex(content, "\n")
	if idx < 0 {
		return line
	}
	return content[:idx+1] + line
}

// paletteTop is the palette overlay's fixed anchor row — a couple of rows
// below the header, like the mockups' 44/56px offsets. Anchoring (rather
// than vertical centering) keeps the panel still while its height changes
// with every keystroke's result count.
const paletteTop = 2
