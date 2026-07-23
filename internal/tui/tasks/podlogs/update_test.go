package podlogs

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/tui"
)

func press(model *Model, key string) {
	_, _ = model.Update(tea.KeyPressMsg{Text: key})
}

func TestStreamLifecycleMessages(t *testing.T) {
	t.Parallel()

	model := testModel()
	_, _ = model.Update(streamStartedMsg{state: StreamLoading})
	if model.stream != StreamLoading {
		t.Fatalf("stream = %s", model.stream)
	}
	_, _ = model.Update(logLineMsg{entry: LogEntry{Container: "app", Message: "ready"}})
	if model.stream != StreamStreaming || len(model.buffer.Entries) != 1 {
		t.Fatalf("model = %+v", model)
	}
	_, _ = model.Update(streamEmptyMsg{})
	if model.stream != StreamEmpty || !strings.Contains(model.feedback, "No logs found") {
		t.Fatalf("empty state = %s %q", model.stream, model.feedback)
	}
	_, _ = model.Update(streamErrorMsg{err: errors.New("pods/log is forbidden")})
	if model.stream != StreamError || !strings.Contains(model.feedback, "Permission denied") {
		t.Fatalf("error state = %s %q", model.stream, model.feedback)
	}
	if model.taskState() != tui.TaskStatePermissionDenied {
		t.Fatalf("taskState = %s, want permission-denied", model.taskState())
	}
}

func TestExitClosesStream(t *testing.T) {
	t.Parallel()

	model := testModel()
	_, cmd := model.Update(tea.KeyPressMsg{Text: "ctrl+q"})
	if cmd == nil || model.stream != StreamClosed {
		t.Fatalf("cmd = %v stream = %s", cmd, model.stream)
	}
}

func TestEscapeReturnsBackMessageAndClosesStream(t *testing.T) {
	t.Parallel()

	model := testModel()
	_, cmd := model.Update(tea.KeyPressMsg{Text: "esc"})
	if cmd == nil {
		t.Fatalf("esc command is nil")
	}
	if _, ok := cmd().(tui.BackMsg); !ok {
		t.Fatalf("esc command did not return BackMsg")
	}
	if model.stream != StreamClosed {
		t.Fatalf("stream = %s, want closed", model.stream)
	}
}

func TestVerticalNavigationKeys(t *testing.T) {
	t.Parallel()

	model := testModel()
	seedLines(&model, 20)
	model.view.AutoScroll = false
	model.view.VerticalOffset = 0

	press(&model, "j")
	if model.view.VerticalOffset != 1 {
		t.Fatalf("j offset = %d", model.view.VerticalOffset)
	}
	press(&model, "k")
	if model.view.VerticalOffset != 0 {
		t.Fatalf("k offset = %d", model.view.VerticalOffset)
	}
	press(&model, "pgdown")
	if model.view.VerticalOffset == 0 {
		t.Fatalf("pgdown did not move")
	}
	press(&model, "G")
	if model.view.VerticalOffset != model.maxVerticalOffset() {
		t.Fatalf("G offset = %d want %d", model.view.VerticalOffset, model.maxVerticalOffset())
	}
	press(&model, "home")
	if model.view.VerticalOffset != 0 {
		t.Fatalf("home offset = %d", model.view.VerticalOffset)
	}
}

func TestHalfPageNavigationKeys(t *testing.T) {
	t.Parallel()

	model := testModel()
	seedLines(&model, 30)
	model.view.AutoScroll = false
	model.view.VerticalOffset = 0
	press(&model, "ctrl+d")
	if model.view.VerticalOffset != max(1, model.entryViewportHeight()/2) {
		t.Fatalf("ctrl+d offset = %d", model.view.VerticalOffset)
	}
	press(&model, "ctrl+u")
	if model.view.VerticalOffset != 0 {
		t.Fatalf("ctrl+u offset = %d", model.view.VerticalOffset)
	}
}

func TestHorizontalNavigationWhenWrapOff(t *testing.T) {
	t.Parallel()

	model := testModel()
	model.view.Wrap = false
	press(&model, "l")
	press(&model, "right")
	if model.view.HorizontalOffset != 2 {
		t.Fatalf("horizontal offset = %d", model.view.HorizontalOffset)
	}
	press(&model, "h")
	if model.view.HorizontalOffset != 1 {
		t.Fatalf("horizontal offset after h = %d", model.view.HorizontalOffset)
	}
	model.view.Wrap = true
	press(&model, "l")
	if model.view.HorizontalOffset != 0 {
		t.Fatalf("wrap horizontal offset = %d", model.view.HorizontalOffset)
	}
}

func TestFollowToggleAndAutoScroll(t *testing.T) {
	t.Parallel()

	model := testModel()
	model.SetSize(80, 8)
	seedLines(&model, 20)
	bottom := model.maxVerticalOffset()
	if model.view.VerticalOffset != bottom {
		t.Fatalf("autoscroll offset = %d want %d", model.view.VerticalOffset, bottom)
	}

	press(&model, "space")
	if model.view.AutoScroll {
		t.Fatalf("space did not pause following")
	}
	model.view.VerticalOffset = 2
	model.appendEntry(LogEntry{Container: "app", Message: "new"})
	if model.view.VerticalOffset != 2 {
		t.Fatalf("paused follow moved to %d", model.view.VerticalOffset)
	}

	press(&model, "space")
	if !model.view.AutoScroll || model.view.VerticalOffset != model.maxVerticalOffset() {
		t.Fatalf("space did not resume following: %+v", model.view)
	}
}

func TestWrapAndTimestampTogglesAreDisplayOnly(t *testing.T) {
	t.Parallel()

	model := testModel()
	_, cmd := model.Update(tea.KeyPressMsg{Text: "W"})
	if cmd != nil || model.view.Wrap {
		t.Fatalf("wrap toggle = %v cmd = %v", model.view.Wrap, cmd)
	}
	_, cmd = model.Update(tea.KeyPressMsg{Text: "t"})
	if cmd != nil || !model.view.Timestamps {
		t.Fatalf("timestamps toggle = %v cmd = %v", model.view.Timestamps, cmd)
	}
}

func TestTabCyclesContainerAndRestartsStream(t *testing.T) {
	t.Parallel()

	model := testModel() // app, sidecar
	_, cmd := model.Update(tea.KeyPressMsg{Text: "tab"})
	if cmd == nil {
		t.Fatalf("tab did not restart the stream")
	}
	if container, _ := model.activeContainer(); container != "sidecar" {
		t.Fatalf("container after tab = %q", container)
	}
}

func TestSinceKeyCyclesWindowAndRestartsStream(t *testing.T) {
	t.Parallel()

	model := testModel()
	start := model.sinceLabel()
	_, cmd := model.Update(tea.KeyPressMsg{Text: "s"})
	if cmd == nil {
		t.Fatalf("s did not restart the stream")
	}
	if model.sinceLabel() == start {
		t.Fatalf("since label did not change from %q", start)
	}
}

// entryVisible reports whether index idx (into model.filteredEntries())
// falls within the current viewport window — a jump-to-severity result is
// "correct" once the target line is on screen, whether or not it lands
// exactly at the top (a target near the buffer's end clamps the offset to
// maxVerticalOffset, per clampOffsets, which still keeps it visible).
func entryVisible(model Model, idx int) bool {
	offset := model.view.VerticalOffset
	height := model.entryViewportHeight()
	return idx >= offset && idx < offset+height
}

func TestJumpSeverityMovesToNextWarningOrError(t *testing.T) {
	t.Parallel()

	model := testModel()
	model.SetSize(80, 12)
	model.stream = StreamStreaming
	for range 5 {
		model.buffer.Append(LogEntry{Message: "info"})
	}
	model.buffer.Append(LogEntry{Message: "warn one", Severity: SeverityWarn}) // index 5
	for range 5 {
		model.buffer.Append(LogEntry{Message: "info"})
	}
	model.buffer.Append(LogEntry{Message: "error one", Severity: SeverityErr}) // index 11
	model.view.VerticalOffset = 0

	press(&model, "w")
	if !entryVisible(model, 5) {
		t.Fatalf("warning entry not visible after w: offset=%d height=%d", model.view.VerticalOffset, model.entryViewportHeight())
	}
	model.view.VerticalOffset = 0
	press(&model, "e")
	if !entryVisible(model, 11) {
		t.Fatalf("error entry not visible after e: offset=%d height=%d", model.view.VerticalOffset, model.entryViewportHeight())
	}
}

func TestFilterOpensNarrowsAndClears(t *testing.T) {
	t.Parallel()

	model := testModel()
	model.stream = StreamStreaming
	model.buffer.Append(LogEntry{Message: "starting up"})
	model.buffer.Append(LogEntry{Message: "request failed"})

	_, cmd := model.Update(tea.KeyPressMsg{Text: "/"})
	if cmd != nil || !model.filterActive {
		t.Fatalf("filterActive = %v cmd = %v", model.filterActive, cmd)
	}
	if !model.CapturingInput() {
		t.Fatalf("CapturingInput = false while filtering")
	}

	_, _ = model.Update(tea.KeyPressMsg{Text: "failed"})
	if len(model.filteredEntries()) != 1 {
		t.Fatalf("filtered = %+v, want 1 match", model.filteredEntries())
	}
	// docs/design system-wide interactions: "items never silently
	// disappear" — the strip must say a line was hidden by the filter, not
	// just show a bare matched count.
	if view := model.Render(); !strings.Contains(view, "hidden by filter") {
		t.Fatalf("expected the 'hidden by filter' notice:\n%s", view)
	}

	press(&model, "esc")
	if model.filterActive || model.filterQuery != "" {
		t.Fatalf("esc did not clear filter: active=%v query=%q", model.filterActive, model.filterQuery)
	}
}

func TestFilterAltJKHLMoveWithoutTyping(t *testing.T) {
	t.Parallel()

	model := testModel()
	seedLines(&model, 20)
	model.view.AutoScroll = false
	model.view.Wrap = false
	model.view.VerticalOffset = 0
	model.view.HorizontalOffset = 0

	_, _ = model.Update(tea.KeyPressMsg{Text: "/"})
	if !model.filterActive {
		t.Fatalf("expected / to activate the filter")
	}

	_, _ = model.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModAlt})
	if model.view.VerticalOffset != 1 {
		t.Fatalf("VerticalOffset = %d, want 1 after alt+j", model.view.VerticalOffset)
	}
	_, _ = model.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModAlt})
	if model.view.VerticalOffset != 0 {
		t.Fatalf("VerticalOffset = %d, want 0 after alt+k", model.view.VerticalOffset)
	}
	_, _ = model.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModAlt})
	if model.view.HorizontalOffset != 1 {
		t.Fatalf("HorizontalOffset = %d, want 1 after alt+l", model.view.HorizontalOffset)
	}
	_, _ = model.Update(tea.KeyPressMsg{Code: 'h', Mod: tea.ModAlt})
	if model.view.HorizontalOffset != 0 {
		t.Fatalf("HorizontalOffset = %d, want 0 after alt+h", model.view.HorizontalOffset)
	}
	if model.filterQuery != "" {
		t.Fatalf("filterQuery = %q, want empty (alt+j/k/h/l must move, not type)", model.filterQuery)
	}
}

func TestCopyVisibleViewSetsClipboard(t *testing.T) {
	t.Parallel()

	model := testModel()
	model.buffer.Append(LogEntry{Message: "hello"})
	_, cmd := model.Update(tea.KeyPressMsg{Text: "ctrl+y"})
	if cmd == nil {
		t.Fatalf("ctrl+y did not return a command")
	}
}

func TestRateTickUpdatesLastRateAndDropsStaleGeneration(t *testing.T) {
	t.Parallel()

	model := testModel()
	model.rateGen = 5
	model.linesSinceTick = 3

	_, cmd := model.Update(rateTickMsg{gen: 5})
	if model.lastRate != 3 || model.linesSinceTick != 0 || cmd == nil {
		t.Fatalf("model = %+v cmd nil = %v", model, cmd == nil)
	}

	model.linesSinceTick = 9
	_, cmd = model.Update(rateTickMsg{gen: 1})
	if model.lastRate != 3 || cmd != nil {
		t.Fatalf("stale tick applied: lastRate=%d cmd=%v", model.lastRate, cmd)
	}
}
