// Package goldentest provides the shared color-profile handling every
// package's golden_test.go needs. lipgloss v2 removed the global
// Renderer/SetColorProfile mechanism v1 used to force a profile at
// Style.Render() time — Render() now always emits full-fidelity truecolor
// ANSI, with downsampling pushed to the output layer instead. That collapses
// the old implicit distinction between a "plain" golden (colorless, because
// go test isn't a TTY) and a "truecolor" golden (forced): both flavors now
// have to explicitly downsample a Render() call's already-truecolor output.
package goldentest

import (
	"strings"

	"github.com/charmbracelet/colorprofile"
)

// Plain strips all ANSI styling from a rendered view, reproducing the
// zero-escape-byte plain golden fixtures pinned under lipgloss v1's ambient
// no-TTY detection.
func Plain(rendered string) string {
	return downsample(rendered, colorprofile.NoTTY)
}

// Truecolor downsamples a rendered view to the TrueColor profile — a no-op
// today since Render() already emits full truecolor, but the explicit
// stand-in for v1's forced lipgloss.SetColorProfile(termenv.TrueColor), so
// the per-cell color mapping these fixtures pin doesn't depend on Render()'s
// default staying truecolor forever.
func Truecolor(rendered string) string {
	return downsample(rendered, colorprofile.TrueColor)
}

func downsample(rendered string, profile colorprofile.Profile) string {
	var buf strings.Builder
	w := &colorprofile.Writer{Forward: &buf, Profile: profile}
	if _, err := w.WriteString(rendered); err != nil {
		panic(err) // strings.Builder never errors
	}
	return buf.String()
}
