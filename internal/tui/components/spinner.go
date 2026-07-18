package components

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
)

// SpinnerFrames is the shared "loading" animation (bubbles' MiniDot set) —
// one glyph set/cadence so every Chrome v2 screen's spinner looks and moves
// identically instead of each task package hand-rolling its own.
var SpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// SpinnerInterval is the frame cadence.
const SpinnerInterval = 100 * time.Millisecond

// Spinner is the ticking frame index a task Model embeds while it has a
// TaskStateLoading body. Render stays pure (f(model, theme, size)) — View
// just indexes the stored frame; the ticking itself happens in each task's
// Update on SpinnerTickMsg.
type Spinner struct {
	frame int
}

// SpinnerTickMsg advances the spinner one frame.
type SpinnerTickMsg time.Time

// SpinnerTick starts (or continues) the animation. A task issues this
// whenever it (re-)enters TaskStateLoading; the tick loop self-terminates
// once the task's own SpinnerTickMsg handling stops reissuing it (i.e. once
// the state has moved past loading), rather than ticking forever unseen.
func SpinnerTick() tea.Cmd {
	return tea.Tick(SpinnerInterval, func(t time.Time) tea.Msg { return SpinnerTickMsg(t) })
}

// Advance moves to the next frame, wrapping around.
func (s Spinner) Advance() Spinner {
	s.frame = (s.frame + 1) % len(SpinnerFrames)
	return s
}

// View renders the current frame styled with style.
func (s Spinner) View(style lipgloss.Style) string {
	return style.Render(SpinnerFrames[s.frame])
}

// LoadingBody centers a spinner-prefixed feedback line — the shared render
// for every Chrome v2 screen's TaskStateLoading body, so every task's
// "Loading …" label looks identical rather than each screen composing its
// own spinner+text line.
func LoadingBody(spinner Spinner, style lipgloss.Style, feedback string, width, height int) string {
	return CenterLines([]string{spinner.View(style) + " " + feedback}, width, height)
}
