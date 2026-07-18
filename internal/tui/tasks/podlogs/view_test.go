package podlogs

import (
	"strings"
	"testing"
)

func TestRenderShowsLoadingEmptyAndPermissionDeniedFeedback(t *testing.T) {
	t.Parallel()

	model := testModel()
	model.stream = StreamLoading
	model.feedback = "Loading logs..."
	if view := model.Render(); !strings.Contains(view, "Loading logs") {
		t.Fatalf("loading view missing feedback:\n%s", view)
	}

	_, _ = model.Update(streamEmptyMsg{})
	if view := model.Render(); !strings.Contains(view, "No logs found") {
		t.Fatalf("empty view missing feedback:\n%s", view)
	}

	_, _ = model.Update(streamErrorMsg{err: errString("pods/log is forbidden")})
	if view := model.Render(); !strings.Contains(view, "Permission denied") {
		t.Fatalf("permission view missing feedback:\n%s", view)
	}
}

func TestRenderStreamingShowsToolbarAndLogLines(t *testing.T) {
	t.Parallel()

	model := testModel()   // app, sidecar
	model.SetSize(140, 24) // wide enough for the toolbar's left + right content to both fit
	model.appendEntry(LogEntry{Container: "app", Message: "ready", Timestamp: "10:00:00"})
	model.appendEntry(LogEntry{Container: "app", Message: "careful now", Severity: SeverityWarn})
	model.appendEntry(LogEntry{Container: "app", Message: "boom", Severity: SeverityErr})
	view := model.Render()
	for _, want := range []string{"container app", "tab: sidecar", "since 15m", "wrap on", "timestamps", "ready", "WRN", "ERR", "in view"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestRenderShowsRestartBoundaryAsCenteredRule(t *testing.T) {
	t.Parallel()

	model := testModel()
	model.stream = StreamStreaming
	model.buffer.Append(LogEntry{Boundary: true, Message: "container restarted · restart 6", Timestamp: "10:24:02"})
	view := model.Render()
	if !strings.Contains(view, "container restarted · restart 6 · 10:24:02") || !strings.Contains(view, "───") {
		t.Fatalf("view missing boundary rule:\n%s", view)
	}
}

func TestFormatEntryTrimsHorizontalOffsetWhenWrapOff(t *testing.T) {
	t.Parallel()

	model := testModel()
	model.view.Wrap = false
	model.view.HorizontalOffset = 8
	theme := model.Theme()
	got := model.formatEntry(theme, LogEntry{Container: "app", Message: "0123456789"}, 80)
	if strings.Contains(got, "0123456789") || !strings.Contains(got, "89") {
		t.Fatalf("horizontal offset did not trim: %q", got)
	}
}

func TestStatusLineReflectsFollowAndPauseState(t *testing.T) {
	t.Parallel()

	model := testModel()
	model.stream = StreamStreaming
	view := model.Render()
	if !strings.Contains(view, "live") {
		t.Fatalf("following status missing:\n%s", view)
	}

	model.view.AutoScroll = false
	view = model.Render()
	if !strings.Contains(view, "paused") {
		t.Fatalf("paused status missing:\n%s", view)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
