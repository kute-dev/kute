package tui

import (
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kute-dev/kute/internal/tui/components/textfield"
)

// TextInputStyles builds the shared single-line field styles from theme
// tokens only. The field is value-backed, so View remains a pure render.
func TextInputStyles(theme Theme) textfield.Styles {
	state := func(text lipgloss.Style) textfield.StyleState {
		return textfield.StyleState{
			Text:        text,
			Placeholder: lipgloss.NewStyle().Foreground(theme.TextFaint),
			Prompt:      lipgloss.NewStyle().Foreground(theme.Accent),
		}
	}
	return textfield.Styles{
		Focused: state(lipgloss.NewStyle().Foreground(theme.Text)),
		Blurred: state(lipgloss.NewStyle().Foreground(theme.TextDim)),
		// Explicitly clear bold so a cursor inside a bold field retains the
		// same stable block treatment as the previous text input component.
		Cursor:    lipgloss.NewStyle().Foreground(theme.Accent).Bold(false),
		Selection: lipgloss.NewStyle().Foreground(theme.Bg).Background(theme.Accent),
	}
}

// NewTextInput builds a styled, prompt-less textfield.Model — every site in
// this app renders its own literal prefix ("/ ", "ns › ", …) ahead of the
// field rather than delegating to textinput's own Prompt, which defaults to
// "> " if left unset. Use this instead of textfield.New() directly so that
// default never leaks through as a stray "> " (see git history: it did,
// silently, at every one of this app's first ~10 sites migrated onto this
// component, since only setting Styles doesn't touch Prompt at all).
func NewTextInput(theme Theme) textfield.Model {
	ti := textfield.New()
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
			Selection:        lipgloss.NewStyle().Foreground(theme.Bg).Background(theme.Accent),
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
