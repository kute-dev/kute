package tui

import (
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// keycastMaxTokens caps how many key tokens the --keycast chip shows at
// once — the oldest is dropped once a new one arrives past the cap, rather
// than growing unbounded.
const keycastMaxTokens = 6

// keycastCoalesceWindow is how long a run of printable keypresses can pause
// before the next one starts a fresh token instead of merging onto the
// last one — matches the felt cadence of someone typing a filter query.
const keycastCoalesceWindow = 700 * time.Millisecond

// keycastIdleClear is how long the whole chip stays on screen after the
// last keypress before it clears itself.
const keycastIdleClear = 2500 * time.Millisecond

// keycastTickInterval drives the idle-clear countdown — coarser than
// components.SpinnerInterval since the chip only needs to notice the idle
// deadline has passed, not animate.
const keycastTickInterval = 250 * time.Millisecond

// keycastToken is one entry in the --keycast chip: either a run of merged
// printable keypresses (coalescable) or a single named key (never merged,
// even if it lands mid-run).
type keycastToken struct {
	label       string
	coalescable bool
	at          time.Time
}

// keycastState is the root shell's ring buffer of recent keypresses,
// rendered as a small chip anchored bottom-right (--keycast, a demo-
// recording aid). Ephemeral shell UI state, so it lives directly on Model
// next to helpOpen/quitConfirm/palette rather than Session — nothing
// outside the root shell ever reads it.
type keycastState struct {
	tokens  []keycastToken
	ticking bool
}

// KeycastTickMsg drives keycastState's idle-clear countdown — the tick loop
// self-terminates once the buffer has emptied, the same self-terminating
// shape components.SpinnerTickMsg documents.
type KeycastTickMsg time.Time

// keycastTick starts (or continues) the idle-clear countdown.
func keycastTick() tea.Cmd {
	return tea.Tick(keycastTickInterval, func(t time.Time) tea.Msg { return KeycastTickMsg(t) })
}

// record appends msg's humanized label, merging it onto the previous token
// when both are coalescable printable runs within keycastCoalesceWindow of
// each other. Returns a tea.Cmd to (re)arm the idle-clear tick when one
// isn't already running — a no-op key (label == "") records nothing and
// arms nothing.
func (k *keycastState) record(msg tea.KeyPressMsg, now time.Time) tea.Cmd {
	label, coalescable := humanizeKey(msg)
	if label == "" {
		return nil
	}
	if n := len(k.tokens); n > 0 && coalescable && k.tokens[n-1].coalescable && now.Sub(k.tokens[n-1].at) < keycastCoalesceWindow {
		k.tokens[n-1].label += "-" + label
		k.tokens[n-1].at = now
	} else {
		k.tokens = append(k.tokens, keycastToken{label: label, coalescable: coalescable, at: now})
		if len(k.tokens) > keycastMaxTokens {
			k.tokens = k.tokens[len(k.tokens)-keycastMaxTokens:]
		}
	}
	if k.ticking {
		return nil
	}
	k.ticking = true
	return keycastTick()
}

// prune clears the whole buffer once idle for keycastIdleClear and reports
// whether the tick loop should keep running (false once the buffer is
// empty, which un-arms k.ticking too).
func (k *keycastState) prune(now time.Time) bool {
	if n := len(k.tokens); n > 0 && now.Sub(k.tokens[n-1].at) >= keycastIdleClear {
		k.tokens = nil
	}
	if len(k.tokens) == 0 {
		k.ticking = false
		return false
	}
	return true
}

// humanizeKey renders msg as a keycast label: a printable key's own text
// (coalescable into a run), or a short name for a non-printable one (never
// coalesced — always its own token). Reuses the same vocabulary the keybar
// hints already use elsewhere (↵ for enter, tui.GlyphTab for tab, ctrl-x
// hyphen style for chords) rather than inventing a new one.
func humanizeKey(msg tea.KeyPressMsg) (string, bool) {
	if msg.Text != "" {
		return msg.Text, true
	}
	switch s := msg.String(); s {
	case "enter":
		return "↵", false
	case "up":
		return "↑", false
	case "down":
		return "↓", false
	case "left":
		return "←", false
	case "right":
		return "→", false
	case "tab":
		return GlyphTab, false
	case "space":
		return "space", false
	default:
		return strings.ReplaceAll(s, "+", "-"), false
	}
}

// keycastRamp is the descending-contrast text ramp (Theme's own doc
// comment: "contrast descends from Text to TextGhost2") reused to age each
// token — newest brightest, oldest faintest — with no new Theme tokens
// needed.
func keycastRamp(theme Theme) []color.Color {
	return []color.Color{theme.Text, theme.TextSecondary, theme.TextDim, theme.TextFaint, theme.TextGhost, theme.TextGhost2}
}

// renderKeycastChip renders the current token list as a single dim line,
// most recent token brightest, joined by a faint middot. Returns "" when
// there's nothing to show, so the caller can skip compositing entirely.
func renderKeycastChip(k keycastState, theme Theme) string {
	if len(k.tokens) == 0 {
		return ""
	}
	ramp := keycastRamp(theme)
	n := len(k.tokens)
	parts := make([]string, n)
	for i, tok := range k.tokens {
		age := n - 1 - i
		if age >= len(ramp) {
			age = len(ramp) - 1
		}
		parts[i] = lipgloss.NewStyle().Foreground(ramp[age]).Render(tok.label)
	}
	sep := lipgloss.NewStyle().Foreground(theme.TextFaint).Render(" · ")
	return strings.Join(parts, sep)
}
