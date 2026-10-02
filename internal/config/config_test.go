package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kute-dev/kute/internal/testutil/testenv"
)

func TestLoadMissingFileYieldsNothingProd(t *testing.T) {
	t.Parallel()
	got := loadFrom(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if got.IsProd("anything") {
		t.Fatalf("expected no prod contexts from a missing file")
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestLoadBrokenFileFailsClosed: a config file kute can't read must never
// quietly drop every context back to non-PROD — that would turn every
// delete into an inline y/N. Whatever prodContexts said is unknowable, so
// every context reads as PROD and LoadErr says why.
func TestLoadBrokenFileFailsClosed(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"syntax error":           "prodContexts: [unterminated",
		"prodContexts a mapping": "prodContexts:\n  prod-eu: true\n",
		"one bad list element":   "prodContexts:\n  - prod-eu\n  - [nested]\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := loadFrom(writeConfig(t, body))
			if got.LoadErr == nil {
				t.Fatalf("LoadErr = nil for %q", body)
			}
			if !got.ProdUnknown() || !got.IsProd("anything") {
				t.Fatalf("a broken prodContexts must fail closed (every context PROD), got %+v", got)
			}
		})
	}
}

// TestLoadSalvagesProdContextsPastABadField: one mistyped key elsewhere in
// the file (nodeShellImage as a list) must not take prodContexts down with
// it — and the mistake is still reported, not swallowed.
func TestLoadSalvagesProdContextsPastABadField(t *testing.T) {
	t.Parallel()
	got := loadFrom(writeConfig(t, "prodContexts: [prod-eu]\ntheme: dark\nnodeShellImage: [x]\n"))
	if got.LoadErr == nil {
		t.Fatalf("LoadErr = nil; the bad nodeShellImage must be reported")
	}
	if got.ProdUnknown() {
		t.Fatalf("prodContexts parsed fine; must not fall back to all-PROD")
	}
	if !got.IsProd("prod-eu") || got.IsProd("dev-kind") {
		t.Fatalf("salvaged prodContexts wrong: %+v", got.ProdContexts)
	}
	if got.Theme != "dark" {
		t.Fatalf("Theme = %q, want the salvaged dark", got.Theme)
	}
}

// TestLoadAcceptsScalarProdContexts: `prodContexts: prod-eu` means exactly
// what it looks like.
func TestLoadAcceptsScalarProdContexts(t *testing.T) {
	t.Parallel()
	got := loadFrom(writeConfig(t, "prodContexts: prod-eu\n"))
	if got.LoadErr != nil {
		t.Fatalf("LoadErr = %v, want a scalar accepted as a one-element list", got.LoadErr)
	}
	if !got.IsProd("prod-eu") || got.IsProd("dev-kind") {
		t.Fatalf("scalar prodContexts = %+v", got.ProdContexts)
	}
}

func TestLoadUnreadableFileFailsClosed(t *testing.T) {
	t.Parallel()
	// A directory where the file should be: ReadFile fails with something
	// other than not-exist.
	got := loadFrom(t.TempDir())
	if got.LoadErr == nil || !got.IsProd("anything") {
		t.Fatalf("an unreadable config must fail closed, got %+v", got)
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
	testenv.SetHome(t, t.TempDir())

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
	testenv.SetHome(t, t.TempDir())

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

// TestSetProdRefusesToOverwriteUnparsableFile: the in-memory Config after a
// broken load is only what survived the parse, so marking a context PROD
// must refuse rather than replace the user's file with that remnant.
func TestSetProdRefusesToOverwriteUnparsableFile(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	const body = "# my prod list\nprodContexts: [prod-eu\n"
	if err := os.MkdirAll(filepath.Dir(Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	c := Load()
	if err := c.SetProd("dev-kind", true); !errors.Is(err, ErrUnparsable) {
		t.Fatalf("SetProd err = %v, want ErrUnparsable", err)
	}
	got, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("config.yaml was rewritten:\n%s", got)
	}
}
