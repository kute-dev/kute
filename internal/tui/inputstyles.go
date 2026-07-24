package tui

import (
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// TextInputStyles builds a bubbles/v2 textinput.Styles from theme tokens only
// — never call textinput.DefaultStyles/DefaultDarkStyles/DefaultLightStyles
// anywhere user-visible, since those don't know about kute's Theme. Callers
// keep the component's default virtual-cursor mode (never call
// SetVirtualCursor(false)) so View() stays a pure string render, matching
// every other render function's f(model, theme, size) contract.
func TextInputStyles(theme Theme) textinput.Styles {
	state := func(text lipgloss.Style) textinput.StyleState {
		return textinput.StyleState{
			Text:        text,
			Placeholder: lipgloss.NewStyle().Foreground(theme.TextFaint),
			Suggestion:  lipgloss.NewStyle().Foreground(theme.TextFaint),
			Prompt:      lipgloss.NewStyle().Foreground(theme.Accent),
		}
	}
	return textinput.Styles{
		Focused: state(lipgloss.NewStyle().Foreground(theme.Text)),
		Blurred: state(lipgloss.NewStyle().Foreground(theme.TextDim)),
		// Blink: false (CursorStatic) keeps the cursor solidly visible —
		// matching every other hand-rolled cursor in the app, none of which
		// blink — and sidesteps virtual-cursor blink timing (which needs a
		// running tea.Program driving Blink() ticks; a static render, like a
		// golden-test snapshot, would otherwise always land on the
		// blinked-off half of the cycle and show nothing).
		Cursor: textinput.CursorStyle{
			Color: theme.Accent,
			Shape: tea.CursorBlock,
			Blink: false,
		},
	}
}

// NewTextInput builds a styled, prompt-less textinput.Model — every site in
// this app renders its own literal prefix ("/ ", "ns › ", …) ahead of the
// field rather than delegating to textinput's own Prompt, which defaults to
// "> " if left unset. Use this instead of textinput.New() directly so that
// default never leaks through as a stray "> " (see git history: it did,
// silently, at every one of this app's first ~10 sites migrated onto this
// component, since only setting Styles doesn't touch Prompt at all).
func NewTextInput(theme Theme) textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.SetStyles(TextInputStyles(theme))
	return ti
}

// TextAreaStyles is TextInputStyles' textarea.Styles equivalent, used only by
// configmapdata's multi-line buffer editor. Base carries no border/padding —
// callers that want a box own that themselves (per the lipgloss v2
// Width+Border box-model gotcha, see components/card.go), so SetWidth's own
// frame-size accounting here always subtracts zero for Base.
func TextAreaStyles(theme Theme) textarea.Styles {
	state := func(text lipgloss.Style) textarea.StyleState {
		return textarea.StyleState{
			Text:             text,
			LineNumber:       lipgloss.NewStyle().Foreground(theme.TextGhost),
			CursorLineNumber: lipgloss.NewStyle().Foreground(theme.TextDim),
			CursorLine:       lipgloss.NewStyle(),
			EndOfBuffer:      lipgloss.NewStyle().Foreground(theme.TextGhost2),
			Placeholder:      lipgloss.NewStyle().Foreground(theme.TextFaint),
			Prompt:           lipgloss.NewStyle().Foreground(theme.Accent),
		}
	}
	return textarea.Styles{
		Focused: state(lipgloss.NewStyle().Foreground(theme.Text)),
		Blurred: state(lipgloss.NewStyle().Foreground(theme.TextDim)),
		Cursor: textarea.CursorStyle{
			Color: theme.Accent,
			Shape: tea.CursorBlock,
			Blink: false,
		},
	}
}
