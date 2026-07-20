package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileYieldsNothingProd(t *testing.T) {
	t.Parallel()
	got := loadFrom(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if got.IsProd("anything") {
		t.Fatalf("expected no prod contexts from a missing file")
	}
}

func TestLoadUnparsableFileYieldsZeroValue(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("prodContexts: [unterminated"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadFrom(path)
	if got.IsProd("anything") {
		t.Fatalf("expected zero value from unparsable file")
	}
}

func TestLoadParsesProdContexts(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := "prodContexts:\n  - prod-eks\n  - prod-gke\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadFrom(path)
	if !got.IsProd("prod-eks") || !got.IsProd("prod-gke") {
		t.Fatalf("expected both prod contexts recognized, got %+v", got)
	}
	if got.IsProd("dev-kind") {
		t.Fatalf("dev-kind should not be prod")
	}
}

func TestLoadParsesTheme(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("theme: light\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadFrom(path)
	if got.Theme != "light" {
		t.Fatalf("Theme = %q, want light", got.Theme)
	}
}

func TestUpdateCheckEnabledDefaultsTrue(t *testing.T) {
	t.Parallel()
	if !(Config{}).UpdateCheckEnabled() {
		t.Fatalf("UpdateCheckEnabled must default to true when update.check is absent")
	}
}

func TestUpdateCheckEnabledParsesFalse(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("update:\n  check: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadFrom(path)
	if got.UpdateCheckEnabled() {
		t.Fatalf("expected update.check: false to disable the check")
	}
}

func TestUpdateCheckEnabledParsesExplicitTrue(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("update:\n  check: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadFrom(path)
	if !got.UpdateCheckEnabled() {
		t.Fatalf("expected update.check: true to keep the check enabled")
	}
}

func TestIsProdNameHeuristicNotApplied(t *testing.T) {
	t.Parallel()
	// A context literally named "prod-looking-but-not-listed" must not be
	// treated as prod — PROD comes only from the explicit list, never a
	// name heuristic (mvp-plan.md decision #2).
	c := Config{ProdContexts: []string{"prod-eks"}}
	if c.IsProd("prod-looking-but-not-listed") {
		t.Fatalf("IsProd must not use a name heuristic")
	}
}

// TestSetProdPersistsAndRoundTrips drives SetProd end to end through Path()
// (t.Setenv("HOME", …) rather than a loadFrom(path)-style injected path,
// since SetProd/save have no path parameter — not t.Parallel-safe per
// testing.T.Setenv's restriction, matching context_test.go's
// writeContextTestKubeconfig).
func TestSetProdPersistsAndRoundTrips(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	c := Load()
	if c.IsProd("prod-eks") {
		t.Fatalf("fresh config must start with nothing prod")
	}
	if err := c.SetProd("prod-eks", true); err != nil {
		t.Fatalf("SetProd(true): %v", err)
	}
	if !c.IsProd("prod-eks") {
		t.Fatalf("in-memory Config not updated by SetProd(true)")
	}

	reloaded := Load()
	if !reloaded.IsProd("prod-eks") {
		t.Fatalf("prod-eks not persisted across reload: %+v", reloaded)
	}

	if err := reloaded.SetProd("prod-eks", false); err != nil {
		t.Fatalf("SetProd(false): %v", err)
	}
	if reloaded.IsProd("prod-eks") {
		t.Fatalf("in-memory Config not updated by SetProd(false)")
	}
	if got := Load(); got.IsProd("prod-eks") {
		t.Fatalf("prod-eks still persisted after unmark: %+v", got)
	}
}

// TestSetProdNoopWhenStatusUnchanged asserts SetProd doesn't write the file
// (and doesn't error) when the requested status already holds — toggling
// off a context that's already off, or on one already on.
func TestSetProdNoopWhenStatusUnchanged(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	c := Config{ProdContexts: []string{"prod-eks"}}
	if err := c.SetProd("dev-kind", false); err != nil {
		t.Fatalf("SetProd(false) on already-non-prod: %v", err)
	}
	if _, err := os.Stat(Path()); err == nil {
		t.Fatalf("SetProd must not write the file when status is unchanged")
	}

	if err := c.SetProd("prod-eks", true); err != nil {
		t.Fatalf("SetProd(true) on already-prod: %v", err)
	}
	if _, err := os.Stat(Path()); err == nil {
		t.Fatalf("SetProd must not write the file when status is unchanged")
	}
}
