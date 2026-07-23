package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

type staticTask struct {
	name string
	next tui.Task
}

func (t *staticTask) Init() tea.Cmd    { return nil }
func (t *staticTask) SetSize(int, int) {}
func (t *staticTask) View() tea.View   { return tea.NewView(t.name) }

func (t *staticTask) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok && key.String() == "open" && t.next != nil {
		return t.next, nil
	}
	return t, nil
}

func TestRootModelPreservesTaskUpdates(t *testing.T) {
	t.Parallel()

	task := &staticTask{name: "pods api"}
	model := tui.New(task)
	updated, _ := model.Update(tea.KeyPressMsg{Text: "noop"})
	view := updated.(tui.Model).View().Content
	if !strings.Contains(view, "pods api") {
		t.Fatalf("root model did not preserve task update:\n%s", view)
	}
}

func TestRootModelBackMessageRestoresPreviousTask(t *testing.T) {
	t.Parallel()

	logs := &staticTask{name: "Logs: api"}
	pods := &staticTask{name: "Pods api", next: logs}
	model := tui.New(pods)

	updated, _ := model.Update(tea.KeyPressMsg{Text: "open"})
	logsView := updated.(tui.Model).View().Content
	if !strings.Contains(logsView, "Logs: api") {
		t.Fatalf("logs view missing after open:\n%s", logsView)
	}

	updated, _ = updated.(tui.Model).Update(tui.BackMsg{})
	podsView := updated.(tui.Model).View().Content
	if !strings.Contains(podsView, "Pods api") {
		t.Fatalf("pods view missing after back:\n%s", podsView)
	}
}

// screenTask implements both tui.Task and tui.Screen (Chrome v2), so it
// exercises the root shell's overlay/mode routing — staticTask above
// deliberately does not, to guard the legacy-screen passthrough path.
type screenTask struct {
	name    string
	pill    string
	updates []string
}

func (t *screenTask) Init() tea.Cmd    { return nil }
func (t *screenTask) SetSize(int, int) {}
func (t *screenTask) View() tea.View   { return tea.NewView(t.name) }
func (t *screenTask) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case kube.ConnStateMsg:
		t.updates = append(t.updates, "conn")
	case tui.GotoKindMsg:
		t.updates = append(t.updates, "goto")
	}
	return t, nil
}
func (t *screenTask) Theme() tui.Theme        { return tui.Dark() }
func (t *screenTask) Header() tui.HeaderState { return tui.HeaderState{} }
func (t *screenTask) Strips(int) []string     { return nil }
func (t *screenTask) Keybar() tui.Keybar      { return tui.Keybar{PillText: t.pill} }
func (t *screenTask) Body(int, int) string    { return t.name }

// capturingTask is a screenTask with an always-open free-text input (like
// browse's filter box), so it exercises the InputCapturer opt-out from
// shell-key interception.
type capturingTask struct {
	screenTask
	received []string
}

func (t *capturingTask) CapturingInput() bool { return true }
func (t *capturingTask) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		t.received = append(t.received, key.String())
	}
	return t, nil
}

func testSession() *tui.Session {
	return &tui.Session{Theme: tui.Dark(), Styles: tui.NewStyles(tui.Dark())}
}

func TestRootModelLegacyTaskUnaffectedByShellKeys(t *testing.T) {
	t.Parallel()

	task := &staticTask{name: "legacy"}
	model := tui.NewWithSession(task, testSession())
	updated, _ := model.Update(tea.KeyPressMsg{Text: "g"})
	m := updated.(tui.Model)
	if m.PaletteOpen() {
		t.Fatalf("a legacy (non-Screen) task must not have its 'g' key intercepted by the shell")
	}
}

func TestRootModelGOpensGotoPalette(t *testing.T) {
	t.Parallel()

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, testSession())
	updated, _ := model.Update(tea.KeyPressMsg{Text: "g"})
	m := updated.(tui.Model)
	if !m.PaletteOpen() {
		t.Fatalf("expected 'g' to open the palette")
	}
	if m.Mode() != tui.ModeGoto {
		t.Fatalf("Mode() = %v, want ModeGoto", m.Mode())
	}
}

func TestRootModelColonOpensGotoPalette(t *testing.T) {
	t.Parallel()

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, testSession())
	updated, _ := model.Update(tea.KeyPressMsg{Text: ":"})
	m := updated.(tui.Model)
	if !m.PaletteOpen() {
		t.Fatalf("expected ':' to open the palette")
	}
	if m.Mode() != tui.ModeGoto {
		t.Fatalf("Mode() = %v, want ModeGoto", m.Mode())
	}
}

func TestRootModelWithoutSessionDoesNotOpenPalette(t *testing.T) {
	t.Parallel()

	task := &screenTask{name: "browse"}
	model := tui.New(task) // no session
	updated, _ := model.Update(tea.KeyPressMsg{Text: "g"})
	if updated.(tui.Model).PaletteOpen() {
		t.Fatalf("expected no palette without a Session")
	}
}

func TestRootModelEscClosesPalette(t *testing.T) {
	t.Parallel()

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, testSession())
	updated, _ := model.Update(tea.KeyPressMsg{Text: "g"})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m := updated.(tui.Model)
	if m.PaletteOpen() {
		t.Fatalf("expected esc to close the palette")
	}
	if m.Mode() != tui.ModeBrowse {
		t.Fatalf("Mode() = %v, want ModeBrowse after closing", m.Mode())
	}
}

func TestRootModelQuestionMarkOpensHelp(t *testing.T) {
	t.Parallel()

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, testSession())
	updated, _ := model.Update(tea.KeyPressMsg{Text: "?"})
	m := updated.(tui.Model)
	if !m.HelpOpen() {
		t.Fatalf("expected '?' to open help")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Text: "?"})
	if updated.(tui.Model).HelpOpen() {
		t.Fatalf("expected a second '?' to close help")
	}
}

// TestRootModelHelpOverlayRendersScopeGlobalAndViewColumns covers 7b: the
// active screen's own Keybar() groups become the current-view column, and
// the fixed SCOPE/GLOBAL columns come from Session.HelpScope/HelpGlobal
// (built at the composition root from the verbs registry — session.go).
func TestRootModelHelpOverlayRendersScopeGlobalAndViewColumns(t *testing.T) {
	t.Parallel()

	task := &screenTask{name: "browse"}
	sess := testSession()
	sess.HelpScope = []tui.KeyHint{{Key: "g", Label: "jump anywhere"}}
	sess.HelpGlobal = []tui.KeyHint{{Key: "ctrl+q", Label: "quit"}}
	model := tui.NewWithSession(task, sess)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "?"})
	view := updated.(tui.Model).View().Content

	for _, want := range []string{"? help", "SCOPE", "GLOBAL", "jump anywhere", "quit", "esc", "close"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in the help overlay:\n%s", want, view)
		}
	}
}

// TestRootModelQuestionMarkShowsHelpKeybarPill pins 7b (docs/design
// README.md:114: "Keybar pill HELP"): the underlying screen's own keybar
// pill (e.g. "PODS") must be replaced by a HELP pill while the help overlay
// is open, the same undimmed-splice treatment goto's own GOTO pill gets
// (TestRootModelGShowsGotoKeybarPill, goto_test.go).
func TestRootModelQuestionMarkShowsHelpKeybarPill(t *testing.T) {
	t.Parallel()

	task := &screenTask{name: "browse", pill: "PODS"}
	model := tui.NewWithSession(task, testSession())
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "?"})
	view := updated.(tui.Model).View().Content

	lines := strings.Split(view, "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(last, "HELP") {
		t.Fatalf("expected the HELP mode pill in the main keybar's last line, got:\n%s", last)
	}
	if strings.Contains(last, "PODS") {
		t.Fatalf("expected the underlying screen's own pill replaced, still saw it in:\n%s", last)
	}
}

func TestRootModelPaletteTypingAccumulatesQuery(t *testing.T) {
	t.Parallel()

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, testSession())
	updated, _ := model.Update(tea.KeyPressMsg{Text: "g"})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "p"})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "o"})
	view := updated.(tui.Model).View().Content
	if !strings.Contains(view, "po") {
		t.Fatalf("expected typed query 'po' reflected in the palette overlay:\n%s", view)
	}
}

func TestRootModelInputCapturingTaskGetsGNCQ(t *testing.T) {
	t.Parallel()

	task := &capturingTask{screenTask: screenTask{name: "browse-filter"}}
	model := tui.NewWithSession(task, testSession())
	for _, key := range []string{"g", "n", "c", "?"} {
		updated, _ := model.Update(tea.KeyPressMsg{Text: key})
		model = updated.(tui.Model)
	}
	if m := model; m.PaletteOpen() || m.HelpOpen() {
		t.Fatalf("shell must not intercept g/n/c/? while the task is capturing input (palette=%v help=%v)", m.PaletteOpen(), m.HelpOpen())
	}
	if got := task.received; len(got) != 4 || got[0] != "g" || got[1] != "n" || got[2] != "c" || got[3] != "?" {
		t.Fatalf("expected the task to receive g/n/c/? verbatim, got %v", got)
	}
}

func TestRootModelConnStateMsgUpdatesConnAndForwardsToTask(t *testing.T) {
	t.Parallel()

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, testSession())
	updated, _ := model.Update(kube.ConnStateMsg{Phase: kube.ConnReconnecting, Err: "dial timeout"})
	m := updated.(tui.Model)
	if m.Conn().Phase != kube.ConnReconnecting {
		t.Fatalf("Conn().Phase = %v, want Reconnecting", m.Conn().Phase)
	}
	if len(task.updates) != 1 {
		t.Fatalf("expected ConnStateMsg forwarded to the task, got %d forwards", len(task.updates))
	}
}

func TestRootModelViewComposesPaletteOverBase(t *testing.T) {
	t.Parallel()

	task := &screenTask{name: "browse"}
	model := tui.NewWithSession(task, testSession())
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	updated, _ = updated.(tui.Model).Update(tea.KeyPressMsg{Text: "g"})
	view := updated.(tui.Model).View().Content
	// 'g' with no query opens the 12a ranked list (bare-cursor input, per
	// the mockup — no placeholder text) — testSession has no Groups/Lister,
	// so the list is empty but the input row's hint still renders.
	if !strings.Contains(view, "jump anywhere") {
		t.Fatalf("expected the palette panel spliced into the view:\n%s", view)
	}
	lines := strings.Split(view, "\n")
	if len(lines) != 30 {
		t.Fatalf("got %d lines, want 30 (composed view keeps the base size)", len(lines))
	}
}

// TestNeverConnectedSwapsToSetupThenBackOnConnect pins 4c (mvp-plan.md
// Phase 4): a Session with a live-but-unconfirmed cluster swaps the root
// task to buildSetup's result on the first Reconnecting/Failed
// ConnStateMsg, and back to buildBrowse's result on the first Connected.
func TestNeverConnectedSwapsToSetupThenBackOnConnect(t *testing.T) {
	t.Parallel()

	browseTask := &screenTask{name: "browse-view"}
	setupTask := &screenTask{name: "setup-view"}
	sess := testSession()
	sess.Cluster = &kube.Cluster{}

	var builtSetup, builtBrowse int
	model := tui.NewWithSession(browseTask, sess).WithRootFactories(
		func(kube.ConnState) tui.Task { builtSetup++; return setupTask },
		func() tui.Task { builtBrowse++; return browseTask },
	)

	updated, _ := model.Update(kube.ConnStateMsg{Phase: kube.ConnReconnecting, Err: "boom"})
	m := updated.(tui.Model)
	if builtSetup != 1 {
		t.Fatalf("buildSetup calls = %d, want 1", builtSetup)
	}
	if !strings.Contains(m.View().Content, "setup-view") {
		t.Fatalf("expected setup active after Reconnecting:\n%s", m.View().Content)
	}

	updated, _ = m.Update(kube.ConnStateMsg{Phase: kube.ConnConnected})
	m = updated.(tui.Model)
	if builtBrowse != 1 {
		t.Fatalf("buildBrowse calls = %d, want 1", builtBrowse)
	}
	if !strings.Contains(m.View().Content, "browse-view") {
		t.Fatalf("expected browse active again after Connected:\n%s", m.View().Content)
	}
}

// sizedTask is a screenTask that records SetSize calls, for pinning that
// root-level task swaps propagate the terminal size.
type sizedTask struct {
	screenTask
	sizes [][2]int
}

func (t *sizedTask) SetSize(w, h int) { t.sizes = append(t.sizes, [2]int{w, h}) }

func (t *sizedTask) lastSize() [2]int {
	if len(t.sizes) == 0 {
		return [2]int{0, 0}
	}
	return t.sizes[len(t.sizes)-1]
}

// TestRootTaskSwapsPropagateTerminalSize pins the 4c swap regression where a
// task built by buildSetup/buildBrowse/ReplaceRootMsg rendered at
// tui.Default* dimensions until the user resized the terminal: every swap
// path must push the last known WindowSizeMsg size onto the new task.
func TestRootTaskSwapsPropagateTerminalSize(t *testing.T) {
	t.Parallel()

	initial := &sizedTask{screenTask: screenTask{name: "initial-browse"}}
	setupTask := &sizedTask{screenTask: screenTask{name: "setup-view"}}
	browseTask := &sizedTask{screenTask: screenTask{name: "browse-view"}}
	sess := testSession()
	sess.Cluster = &kube.Cluster{}

	model := tui.NewWithSession(initial, sess).WithRootFactories(
		func(kube.ConnState) tui.Task { return setupTask },
		func() tui.Task { return browseTask },
	)

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	updated, _ = updated.(tui.Model).Update(kube.ConnStateMsg{Phase: kube.ConnReconnecting, Err: "boom"})
	if got := setupTask.lastSize(); got != [2]int{200, 50} {
		t.Fatalf("setup task size after swap = %v, want [200 50]", got)
	}

	updated, _ = updated.(tui.Model).Update(kube.ConnStateMsg{Phase: kube.ConnConnected})
	if got := browseTask.lastSize(); got != [2]int{200, 50} {
		t.Fatalf("browse task size after reconnect swap = %v, want [200 50]", got)
	}

	replacement := &sizedTask{screenTask: screenTask{name: "replacement"}}
	updated, _ = updated.(tui.Model).Update(tui.ReplaceRootMsg{Task: replacement})
	if got := replacement.lastSize(); got != [2]int{200, 50} {
		t.Fatalf("replacement task size = %v, want [200 50]", got)
	}
	_ = updated
}

// TestNeverConnectedLatchesOffAfterFirstConnect pins the "latches false for
// good" half of the same doc comment: once any Connected state has been
// observed, a later mid-session drop is 4a (browse's own job), not another
// 4c swap.
func TestNeverConnectedLatchesOffAfterFirstConnect(t *testing.T) {
	t.Parallel()

	browseTask := &screenTask{name: "browse-view"}
	sess := testSession()
	sess.Cluster = &kube.Cluster{}
	builtSetup := 0
	model := tui.NewWithSession(browseTask, sess).WithRootFactories(
		func(kube.ConnState) tui.Task { builtSetup++; return &screenTask{name: "setup-view"} },
		func() tui.Task { return browseTask },
	)

	updated, _ := model.Update(kube.ConnStateMsg{Phase: kube.ConnConnected})
	updated, _ = updated.(tui.Model).Update(kube.ConnStateMsg{Phase: kube.ConnReconnecting, Err: "boom"})
	m := updated.(tui.Model)

	if builtSetup != 0 {
		t.Fatalf("buildSetup fired %d times after a Connected state was already observed, want 0", builtSetup)
	}
	if !strings.Contains(m.View().Content, "browse-view") {
		t.Fatalf("expected browse to stay active:\n%s", m.View().Content)
	}
}

// TestWithRootFactoriesNoopWithoutCluster pins the --demo/no-cluster guard:
// WithRootFactories only arms neverConnected when Session carries a live
// Cluster — a demo session must never swap to setup.
func TestWithRootFactoriesNoopWithoutCluster(t *testing.T) {
	t.Parallel()

	task := &screenTask{name: "browse-view"}
	builtSetup := 0
	model := tui.NewWithSession(task, testSession()).WithRootFactories(
		func(kube.ConnState) tui.Task { builtSetup++; return &screenTask{name: "setup-view"} },
		func() tui.Task { return task },
	)

	updated, _ := model.Update(kube.ConnStateMsg{Phase: kube.ConnReconnecting, Err: "boom"})
	if builtSetup != 0 {
		t.Fatalf("buildSetup fired without a Session.Cluster, want 0 calls")
	}
	if !strings.Contains(updated.(tui.Model).View().Content, "browse-view") {
		t.Fatalf("expected browse to stay active without a Cluster")
	}
}
