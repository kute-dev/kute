//go:build e2e

package app

import tea "charm.land/bubbletea/v2"

// RunE2E starts the real program with caller-supplied tea options. It is the
// end-to-end harness's only door into run, and it is behind the e2e build
// tag so a normal build's exported surface stays exactly what it was: the
// harness needs headless input/output, but nothing shipping to users does.
func RunE2E(cfg Config, opts ...tea.ProgramOption) error {
	return run(cfg, opts...)
}
