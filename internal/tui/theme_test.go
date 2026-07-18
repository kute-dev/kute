package tui

import (
	"reflect"
	"testing"
)

// zeroValueFields are the Theme fields intentionally left at their zero
// value (the empty lipgloss.Color) in both variants: dialog panel fills that
// deliberately render on the terminal's own background instead of a themed
// one (see theme.go's field comments).
var zeroValueFields = map[string]bool{
	"BgPalette":       true,
	"BgInput":         true,
	"ConfirmHeaderBg": true,
}

// TestThemeCompleteness asserts no Theme field is left at its zero value in
// either variant, except zeroValueFields, so a screen can never end up
// rendering an empty color by accident.
func TestThemeCompleteness(t *testing.T) {
	t.Parallel()

	for name, theme := range map[string]Theme{"Dark": Dark(), "Light": Light()} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertNoZeroField(t, name, reflect.ValueOf(theme), "")
		})
	}
}

func assertNoZeroField(t *testing.T, theme string, v reflect.Value, path string) {
	t.Helper()
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		fv := v.Field(i)
		name := path + field.Name
		if fv.Kind() == reflect.Struct {
			assertNoZeroField(t, theme, fv, name+".")
			continue
		}
		if zeroValueFields[field.Name] {
			continue
		}
		if fv.IsZero() {
			t.Errorf("%s: field %s is zero-valued", theme, name)
		}
	}
}
