package components

import (
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
)

// loadingSpinner is bubbles' Dot preset with the trailing space stripped
// from every frame: callers (LoadingBody, browse/nodedetail's loading header
// and strip) all put their own single space between the mark and the text,
// so the preset's built-in one would render a double gap. Frames and FPS are
// otherwise Dot's verbatim.
var loadingSpinner = spinner.Spinner{
	Frames: []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"},
	FPS:    time.Second / 10,
}

// NewSpinner builds the shared "loading" animation (bubbles' Dot preset)
// — one glyph set/cadence so every Chrome v2 screen's spinner looks and
// moves identically instead of each task package hand-rolling its own. A
// task Model embeds the returned Model while it has a TaskStateLoading
// body: Init kicks off the tick loop with its own Tick method (used as a
// tea.Cmd), and Update routes spinner.TickMsg through Model.Update — see
// e.g. internal/tui/tasks/nodedetail/update.go. Render stays pure (f(model,
// theme, size)): LoadingBody just calls View, it never ticks the clock
// itself.
func NewSpinner() spinner.Model {
	return spinner.New(spinner.WithSpinner(loadingSpinner))
}

// LoadingBody centers a spinner-prefixed feedback line — the shared render
// for every Chrome v2 screen's TaskStateLoading body, so every task's
// "Loading …" label looks identical rather than each screen composing its
// own spinner+text line.
func LoadingBody(sp spinner.Model, style lipgloss.Style, feedback string, width, height int) string {
	return CenterLines([]string{style.Render(sp.View()) + " " + feedback}, width, height)
}
