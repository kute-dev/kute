package podlogs

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
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
	// ansi.Strip: the toolbar's "container "/"app" segments render as
	// separately styled spans, so an un-stripped view can split
	// "container app" across an escape boundary.
	view := ansi.Strip(model.Render())
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
	got := model.formatEntry(theme, LogEntry{Container: "app", Message: "0123456789"}, 80, false)
	if strings.Contains(got, "0123456789") || !strings.Contains(got, "89") {
		t.Fatalf("horizontal offset did not trim: %q", got)
	}
}

// TestOnlyMostRecentErrLineGetsFullTint pins 5b's two-tier ERR treatment
// (docs/design README.md §5b: "ERR lines get message text … and a
// full-width red-tinted row … for the most significant one"): every ERR
// line's message renders red, but only the latest (most recent) one gets
// the extra full-width ErrBannerBg tint — a stale error scrolling further
// up the buffer must lose the tint once a newer one arrives.
func TestOnlyMostRecentErrLineGetsFullTint(t *testing.T) {
	model := testModel()
	model.SetSize(120, 24)
	model.appendEntry(LogEntry{Container: "app", Message: "first failure", Severity: SeverityErr})
	model.appendEntry(LogEntry{Container: "app", Message: "second failure", Severity: SeverityErr})
	view := model.Render()

	lines := strings.Split(view, "\n")
	var first, second string
	for _, l := range lines {
		switch {
		case strings.Contains(l, "first failure"):
			first = l
		case strings.Contains(l, "second failure"):
			second = l
		}
	}
	if first == "" || second == "" {
		t.Fatalf("expected both ERR lines in the rendered view:\n%s", view)
	}
	bg := "48;2;42;21;24" // theme.ErrBannerBg dark-mode RGB, per lipgloss TrueColor encoding
	if strings.Contains(first, bg) {
		t.Errorf("expected the older ERR line to NOT carry the full-width tint:\n%q", first)
	}
	if !strings.Contains(second, bg) {
		t.Errorf("expected the most recent ERR line to carry the full-width tint:\n%q", second)
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
