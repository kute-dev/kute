package cronjobschedule

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/testutil/goldentest"
	"github.com/kute-dev/kute/internal/tui"
)

// goldenNow is a fixed instant (0.8.0 plan Phase 9's own "regenerate with
// UPDATE_GOLDEN=1" contract needs a deterministic clock) — a Tuesday, so the
// edited weekday schedule's own NEXT 5 RUNS/relative ETA text never drifts
// between the UPDATE_GOLDEN run and a later comparison run.
var goldenNow = time.Date(2026, 3, 3, 9, 15, 0, 0, time.UTC)

// clearSchedule backs the cursor to the start of the buffer and deletes
// forward — scheduleInput.SetValue isn't reachable from outside the
// package's own test files that don't already have a Model handle to poke,
// so every golden builder drives it the same way a real terminal would: key
// by key, through Update.
func clearSchedule(t *testing.T, m Model, n int) Model {
	t.Helper()
	for range n {
		m = step(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	return m
}

// goldenScheduleValidModel builds 36d's own "valid state" golden: a loaded
// CronJob with an explicit time zone, its schedule edited to a new valid
// weekday expression — populating the MIN/HOUR/DOM/MONTH/DOW breakdown, the
// English description, NEXT 5 RUNS, and (via a directly-run runComparison,
// bypassing tickMsg's own real tea.Tick the way this package's other tests
// avoid recursing on it) a populated WHAT CHANGES panel.
func goldenScheduleValidModel(t *testing.T, width, height int) Model {
	t.Helper()
	c := newFakeCronJob("default", "nightly", "0 2 * * *", "America/New_York")
	m := newModel(t, c, "default", "nightly")
	m.SetSize(width, height)
	m.now = goldenNow
	m.recomputeAll()

	m = clearSchedule(t, m, len("0 2 * * *"))
	m = typeText(t, m, "30 3 * * 1-5")

	cmd := runComparison(m.editGen, m.accepted, m.pendingSchedule(), m.pendingTimeZone(), m.now)
	updated, _ := m.Update(cmd())
	m = *updated.(*Model)
	return m
}

// goldenScheduleInvalidModel drives the same loaded CronJob into an invalid
// schedule buffer — a dropped-field typo ("* * *", three fields instead of
// five) rather than free-form text, both because it's the realistic mistake
// and because updateKey's own bare 'y'/'u' shortcuts (safe "only while the
// schedule buffer has focus [since] a valid schedule token never contains
// either letter") would otherwise eat a stray letter out of anything typed
// here instead of inserting it. Pins the "invalid state" golden:
// breakdownLines' own Bad-colored "invalid schedule: …" line, NEXT 5 RUNS'
// "—" fallback, and no WHAT CHANGES panel at all (whatChangesLines bails out
// whenever parseErr is non-nil).
func goldenScheduleInvalidModel(t *testing.T, width, height int) Model {
	t.Helper()
	c := newFakeCronJob("default", "nightly", "0 2 * * *", "America/New_York")
	m := newModel(t, c, "default", "nightly")
	m.SetSize(width, height)
	m.now = goldenNow
	m.recomputeAll()

	m = clearSchedule(t, m, len("0 2 * * *"))
	m = typeText(t, m, "* * *")
	return m
}

func goldenScheduleFixtures(t *testing.T) map[string]string {
	t.Helper()
	valid := goldenScheduleValidModel(t, 120, 36)
	invalid := goldenScheduleInvalidModel(t, 120, 36)
	narrow := goldenScheduleValidModel(t, 80, 24)
	return map[string]string{
		"valid-120x36.golden":   goldentest.Plain(valid.Render()),
		"invalid-120x36.golden": goldentest.Plain(invalid.Render()),
		"narrow-80x24.golden":   goldentest.Plain(narrow.Render()),
	}
}

func goldenScheduleDir() string {
	return filepath.Join("..", "..", "..", "..", "test", "golden", "cronjobschedule")
}

func TestGenerateGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate cronjobschedule golden fixtures")
	}
	if err := os.MkdirAll(goldenScheduleDir(), 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}
	for name, got := range goldenScheduleFixtures(t) {
		if err := os.WriteFile(filepath.Join(goldenScheduleDir(), name), []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestGoldenFixtures(t *testing.T) {
	for name, got := range goldenScheduleFixtures(t) {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(goldenScheduleDir(), name)
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v", path, err)
			}
			if got != string(want) {
				t.Fatalf("golden mismatch for %s\nwant:\n%s\ngot:\n%s", name, string(want), got)
			}
		})
	}
}

// truecolorScheduleGoldenFixtures pins the valid-state screen's per-cell
// color mapping in both themes — the Bad-colored invalid line, the
// TextSecondary/TextDim breakdown ramp, and the will-run strip fill a
// profile-less golden can't see (mirrors browse/golden_test.go's own
// truecolorGoldenFixtures). The profile swap is global, so this file's tests
// must never run t.Parallel (none do).
func truecolorScheduleGoldenFixtures(t *testing.T) map[string]string {
	t.Helper()
	dark := goldenScheduleValidModel(t, 120, 36)
	light := goldenScheduleValidModel(t, 120, 36)
	light.session.Theme = tui.Light()
	return map[string]string{
		"valid-120x36-dark.golden":  goldentest.Truecolor(dark.Render()),
		"valid-120x36-light.golden": goldentest.Truecolor(light.Render()),
	}
}

func TestGenerateTruecolorGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate cronjobschedule golden fixtures")
	}
	if err := os.MkdirAll(goldenScheduleDir(), 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}
	for name, got := range truecolorScheduleGoldenFixtures(t) {
		if err := os.WriteFile(filepath.Join(goldenScheduleDir(), name), []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestTruecolorGoldenFixtures(t *testing.T) {
	for name, got := range truecolorScheduleGoldenFixtures(t) {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(goldenScheduleDir(), name)
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v", path, err)
			}
			if got != string(want) {
				t.Fatalf("golden mismatch for %s\nwant:\n%s\ngot:\n%s", name, string(want), got)
			}
		})
	}
}
