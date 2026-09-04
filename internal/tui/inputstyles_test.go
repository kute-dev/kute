package tui

import (
	"reflect"
	"testing"
)

func TestTextSelectionStylesUseSemanticTokensInBothThemes(t *testing.T) {
	for name, theme := range map[string]Theme{"dark": Dark(), "light": Light()} {
		t.Run(name, func(t *testing.T) {
			single := TextInputStyles(theme)
			multi := TextAreaStyles(theme)
			for surface, style := range map[string]struct{ foreground, background any }{
				"single-line": {single.Selection.GetForeground(), single.Selection.GetBackground()},
				"multi-line":  {multi.Focused.Selection.GetForeground(), multi.Focused.Selection.GetBackground()},
			} {
				if !reflect.DeepEqual(style.foreground, theme.Bg) {
					t.Errorf("%s selection foreground = %v, want theme.Bg", surface, style.foreground)
				}
				if !reflect.DeepEqual(style.background, theme.Accent) {
					t.Errorf("%s selection background = %v, want theme.Accent", surface, style.background)
				}
			}
		})
	}
}
