// Package update is 28b (docs/design/README.md §28b): the what's-new panel
// — changelog plus the exact upgrade command, reachable via the root
// shell's 'U' key or the goto palette's synthetic ":update" item from
// anywhere in the app.
//
// Unlike most task packages, this one does no I/O of its own: it renders
// straight from *tui.Session.Update, populated once by the app-level
// ambient release-feed check (one GET per 24h, per docs/design README.md
// §28a's "check hygiene") — opening the panel never triggers its own
// fetch. Recheck (Config.Recheck) is the only escape hatch, wired to 'r' in
// the empty state, which is "the only place a manual check exists" per
// §28b.
package update

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/update"
)

// Config are update's dependencies, per repo convention (package-local
// Config struct, New fills zero values).
type Config struct {
	Session *tui.Session
	// Recheck triggers a fresh release-feed check that bypasses 28a's 24h
	// cache — built at the composition root, wrapping the same
	// update.Checker the ambient startup check uses. Its result flows back
	// as a tui.UpdateCheckedMsg (handled by the root shell first, then
	// forwarded here — see model.go's doc comment on that message).
	Recheck func() tea.Cmd
	// OpenBrowser is 'o's release-notes seam — defaults to the real
	// update.OpenBrowser (an OS process spawn) when nil, so tests can
	// inject a no-op fake instead of actually launching a browser. Never
	// leave this nil in a test that presses 'o'.
	OpenBrowser func(url string) error
}

type Model struct {
	width, height int

	session     *tui.Session
	recheck     func() tea.Cmd
	openBrowser func(url string) error

	// checking is true while an 'r'-forced recheck is in flight — cleared
	// on the next UpdateCheckedMsg, regardless of outcome.
	checking bool
	// feedback is the keybar's transient RightNote after y/o/x/r — "command
	// copied", "opened in browser", "0.2.1 skipped", "checking…", or a
	// recheck failure note.
	feedback string

	// conn is the last kube.ConnStateMsg forwarded by the root shell — 28b
	// deliberately doesn't render a Conn badge in its own header (matches
	// the mockup; see Header()), but SetSize/Update still track it like
	// every other screen for consistency, in case that ever changes.
	conn kube.ConnState

	// nowAt is captured once at construction (New), never read live inside
	// Body/Header — Render functions stay pure per CLAUDE.md's "no clock
	// reads in render paths" invariant, the same reasoning events' own
	// fetchedAt field documents.
	nowAt time.Time
}

func New(cfg Config) Model {
	openBrowser := cfg.OpenBrowser
	if openBrowser == nil {
		openBrowser = update.OpenBrowser
	}
	return Model{
		width:       tui.DefaultWidth,
		height:      tui.DefaultHeight,
		session:     cfg.Session,
		recheck:     cfg.Recheck,
		openBrowser: openBrowser,
		nowAt:       time.Now(),
	}
}

func (m Model) now() time.Time { return m.nowAt }

func (m Model) Init() tea.Cmd { return nil }

func (m *Model) SetSize(width, height int) {
	size := tui.NormalizeSize(width, height)
	m.width, m.height = size.Width, size.Height
}

// currentVersion is the "you run X" side of every comparison this screen
// renders.
func (m Model) currentVersion() string {
	if m.session == nil {
		return ""
	}
	return m.session.Version
}

// info is m.session.Update, or the zero value when no check has resolved
// yet this session (still loading, or update.check is disabled and no
// Recheck has ever succeeded).
func (m Model) info() (tui.UpdateInfo, bool) {
	if m.session == nil || m.session.Update == nil {
		return tui.UpdateInfo{}, false
	}
	return *m.session.Update, true
}

// available reports whether info holds a genuinely newer release than
// currentVersion — the "ready" vs "empty" state split every other method
// here reads off.
func (m Model) available() (update.Release, bool) {
	info, ok := m.info()
	if !ok || !update.IsNewer(m.currentVersion(), info.Latest.Version) {
		return update.Release{}, false
	}
	return info.Latest, true
}

// state is this screen's TaskState: loading while checking or before any
// check has resolved, ready when an update is available, empty when
// current (a resolved check with nothing newer).
func (m Model) state() tui.TaskState {
	if m.checking {
		return tui.TaskStateLoading
	}
	if _, ok := m.info(); !ok {
		return tui.TaskStateLoading
	}
	if _, ok := m.available(); ok {
		return tui.TaskStateReady
	}
	return tui.TaskStateEmpty
}
