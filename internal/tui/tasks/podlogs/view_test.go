package podlogs

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/kute-dev/kute/internal/tui"
)

func TestRenderShowsLoadingEmptyAndPermissionDeniedFeedback(t *testing.T) {
	t.Parallel()

	model := testModel()
	model.stream = StreamLoading
	model.feedback = "Loading logs..."
	if view := ansi.Strip(model.Render()); !strings.Contains(view, "loading logs for app") || !strings.Contains(view, "history loads before follow starts") || !strings.Contains(view, "waiting for log history") {
		t.Fatalf("loading view missing feedback:\n%s", view)
	}

	_, _ = model.Update(streamEmptyMsg{})
	if view := model.Render(); !strings.Contains(view, "No logs found") {
		t.Fatalf("empty view missing feedback:\n%s", view)
	}

	_, _ = model.Update(streamErrorMsg{err: stringError("pods/log is forbidden")})
	if view := model.Render(); !strings.Contains(view, "Permission denied") {
		t.Fatalf("permission view missing feedback:\n%s", view)
	}
}

// TestRenderWaitingForContainerShowsReasonNotAnError pins the feature:
// a container that hasn't started yet must render as a pending/amber
// "starting" state — badge, toolbar, and body all naming the reason — not
// as the generic "loading logs" spinner state or an error.
func TestRenderWaitingForContainerShowsReasonNotAnError(t *testing.T) {
	t.Parallel()

	model := testModel()
	model.SetSize(140, 24) // wide enough for the strip's left + right content to both fit
	model.stream = StreamWaitingForContainer
	model.waitingReason = "ContainerCreating"
	view := ansi.Strip(model.Render())
	for _, want := range []string{"starting", "waiting for container app to start: ContainerCreating", "streams automatically once running"} {
		if !strings.Contains(view, want) {
			t.Fatalf("waiting view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "loading logs for") {
		t.Fatalf("waiting view should not show the generic loading strip:\n%s", view)
	}
	if strings.Contains(view, tui.GlyphFailed+" error") || strings.Contains(view, "Permission denied") {
		t.Fatalf("waiting view should not render an error state:\n%s", view)
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

func TestLongLogLineWrapsAndIndentsContinuationRows(t *testing.T) {
	t.Parallel()

	model := testModel()
	model.view.Timestamps = true
	entry := LogEntry{
		Timestamp: "10:00:00",
		Severity:  SeverityWarn,
		Message:   "request failed while contacting the upstream service and will be retried automatically",
	}
	const width = 36
	rows := model.visualRows([]LogEntry{entry}, width)
	if len(rows) < 3 {
		t.Fatalf("visual rows = %d, want a wrapped message", len(rows))
	}

	prefix := strings.Repeat(" ", model.entryPrefixWidth(entry))
	var rendered []string
	for i, row := range rows {
		line := ansi.Strip(model.formatVisualRow(model.Theme(), entry, row, width, false))
		if got := ansi.StringWidth(line); got > width {
			t.Fatalf("row %d width = %d, want <= %d: %q", i, got, width, line)
		}
		if i > 0 && !strings.HasPrefix(line, prefix) {
			t.Fatalf("continuation row %d is not aligned to the message column: %q", i, line)
		}
		rendered = append(rendered, strings.TrimSpace(line))
	}
	joined := strings.Join(rendered, " ")
	for _, want := range []string{"request failed", "upstream service", "retried automatically"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("wrapped output lost %q: %q", want, joined)
		}
	}
	if strings.Contains(joined, "…") || strings.Contains(joined, "...") {
		t.Fatalf("wrapped output was ellipsized: %q", joined)
	}
}

func TestWrappedLatestErrorTintsEveryPhysicalRow(t *testing.T) {
	model := testModel()
	entry := LogEntry{
		Timestamp: "10:00:00",
		Severity:  SeverityErr,
		Message:   "fatal request failure repeated across enough words to occupy several terminal rows",
	}
	model.view.Timestamps = true
	const width = 34
	rows := model.visualRows([]LogEntry{entry}, width)
	if len(rows) < 2 {
		t.Fatalf("visual rows = %d, want wrapped error", len(rows))
	}
	bg := "48;2;42;21;24" // theme.ErrBannerBg dark-mode RGB
	for i, row := range rows {
		line := model.formatVisualRow(model.Theme(), entry, row, width, true)
		if !strings.Contains(line, bg) {
			t.Errorf("wrapped error row %d lacks the error background: %q", i, line)
		}
		if got := ansi.StringWidth(line); got != width {
			t.Errorf("wrapped error row %d width = %d, want %d", i, got, width)
		}
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

type stringError string

func (e stringError) Error() string { return string(e) }
