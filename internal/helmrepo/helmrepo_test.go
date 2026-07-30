package helmrepo

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// repoCache writes a repositories.yaml naming every repo in indexes, plus one
// <repo>-index.yaml per entry, and returns a Loader pointed at them. The
// index bodies are written verbatim so a test can supply a malformed one.
func repoCache(t *testing.T, indexes map[string]string) Loader {
	t.Helper()
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}

	config := "apiVersion: \"\"\nrepositories:\n"
	for repo := range indexes {
		config += "- name: " + repo + "\n  url: https://example.test/" + repo + "\n"
	}
	configPath := filepath.Join(dir, "repositories.yaml")
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	for repo, body := range indexes {
		if body == "" {
			continue // a configured repo that was never fetched
		}
		if err := os.WriteFile(filepath.Join(cacheDir, repo+"-index.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Loader{ConfigPath: configPath, CachePath: cacheDir}
}

// index builds an index.yaml body offering each chart the given versions.
func index(charts map[string][]string) string {
	body := "apiVersion: v1\nentries:\n"
	for name, versions := range charts {
		body += "  " + name + ":\n"
		for _, v := range versions {
			body += "  - name: " + name + "\n    version: " + v + "\n    appVersion: \"1.0\"\n" +
				"    description: a chart that exists only to be parsed\n" +
				"    digest: 0000000000000000000000000000000000000000000000000000000000000000\n"
		}
	}
	body += "generated: \"2026-07-01T00:00:00Z\"\n"
	return body
}

func TestLoadNoHelmSetup(t *testing.T) {
	// Nothing on disk at all: not an error, and Configured must be false so
	// the UI says "no repo cache" rather than letting the misses read as
	// "everything is up to date".
	dir := t.TempDir()
	l := Loader{ConfigPath: filepath.Join(dir, "nope.yaml"), CachePath: filepath.Join(dir, "cache")}
	idx, err := l.Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if idx.Status().Configured {
		t.Error("Status().Configured = true with no repositories.yaml")
	}
	if _, ok := idx.Latest("cert-manager"); ok {
		t.Error("Latest() hit against an empty index")
	}
}

func TestLoadConfiguredButNeverFetched(t *testing.T) {
	// repositories.yaml lists a repo, but `helm repo update` never ran.
	l := repoCache(t, map[string]string{"jetstack": ""})
	idx, err := l.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !idx.Status().Configured {
		t.Error("Status().Configured = false with a repo configured")
	}
	if got := idx.Status().Repos; got != 0 {
		t.Errorf("Status().Repos = %d, want 0", got)
	}
	if _, ok := idx.Latest("cert-manager"); ok {
		t.Error("Latest() hit with no cached index")
	}
}

func TestLoadPicksNewestStableVersion(t *testing.T) {
	l := repoCache(t, map[string]string{
		"jetstack": index(map[string][]string{
			// Deliberately unsorted, and with a prerelease ahead of the
			// newest stable — `helm search repo`'s answer is 1.16.2.
			"cert-manager": {"1.14.4", "1.16.2", "1.9.1", "1.17.0-beta.0"},
			// Prerelease-only chart: nothing else to offer.
			"trust-manager": {"0.1.0-alpha.2", "0.1.0-alpha.1"},
		}),
	})
	idx, err := l.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	got, ok := idx.Latest("cert-manager")
	if !ok {
		t.Fatal("Latest(cert-manager) missed")
	}
	if got.Version != "1.16.2" {
		t.Errorf("Latest(cert-manager).Version = %q, want 1.16.2", got.Version)
	}
	if got.Repo != "jetstack" {
		t.Errorf("Latest(cert-manager).Repo = %q, want jetstack", got.Repo)
	}
	if got.Ambiguous {
		t.Error("Latest(cert-manager).Ambiguous = true for a single-repo chart")
	}

	pre, ok := idx.Latest("trust-manager")
	if !ok {
		t.Fatal("Latest(trust-manager) missed")
	}
	if pre.Version != "0.1.0-alpha.2" {
		t.Errorf("Latest(trust-manager).Version = %q, want 0.1.0-alpha.2 (nothing stable published)", pre.Version)
	}

	if got := idx.Status().Charts; got != 2 {
		t.Errorf("Status().Charts = %d, want 2", got)
	}
}

func TestLoadSameChartInTwoRepos(t *testing.T) {
	// A mirror of the same chart: both repos are on the 1.x series, so the
	// higher one is the answer either way.
	l := repoCache(t, map[string]string{
		"jetstack": index(map[string][]string{"cert-manager": {"1.14.4"}}),
		"mirror":   index(map[string][]string{"cert-manager": {"1.16.2"}}),
	})
	idx, err := l.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got, ok := idx.LatestFor("cert-manager", "1.14.4")
	if !ok {
		t.Fatal("LatestFor(cert-manager) missed")
	}
	if got.Version != "1.16.2" {
		t.Errorf("Version = %q, want the higher of the two (1.16.2)", got.Version)
	}
	if !got.Ambiguous {
		t.Error("Ambiguous = false where two repos on the same major disagree — the UI has to be able to say so")
	}
	if got := idx.Status().Repos; got != 2 {
		t.Errorf("Status().Repos = %d, want 2", got)
	}
}

// TestLatestForPrefersTheDeployedSeries is the false-alarm guard, taken from
// a real repo cache: fluent/fluent-bit (0.57.x) and grafana/fluent-bit (2.x)
// are unrelated charts that share a name. Collapsing to "the highest" would
// report a perfectly current fluent/fluent-bit as two majors behind.
func TestLatestForPrefersTheDeployedSeries(t *testing.T) {
	l := repoCache(t, map[string]string{
		"fluent":  index(map[string][]string{"fluent-bit": {"0.57.9", "0.57.1"}}),
		"grafana": index(map[string][]string{"fluent-bit": {"2.6.0"}}),
	})
	idx, err := l.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	tests := []struct {
		name          string
		current       string
		wantVersion   string
		wantRepo      string
		wantAmbiguous bool
	}{
		{"tracks the 0.x series", "0.57.9", "0.57.9", "fluent", false},
		{"behind on the 0.x series", "0.50.0", "0.57.9", "fluent", false},
		{"tracks the 2.x series", "2.5.0", "2.6.0", "grafana", false},
		// Nothing shares major 9: the highest wins, but flagged, so the
		// column can mark it as needing a human rather than asserting it.
		{"no series match", "9.0.0", "2.6.0", "grafana", true},
		// With no deployed version to go on, Latest is the blunt answer.
		{"no current version", "", "2.6.0", "grafana", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := idx.LatestFor("fluent-bit", tt.current)
			if !ok {
				t.Fatal("LatestFor(fluent-bit) missed")
			}
			if got.Version != tt.wantVersion || got.Repo != tt.wantRepo || got.Ambiguous != tt.wantAmbiguous {
				t.Errorf("LatestFor(fluent-bit, %q) = %+v, want {Version:%s Repo:%s Ambiguous:%v}",
					tt.current, got, tt.wantVersion, tt.wantRepo, tt.wantAmbiguous)
			}
		})
	}
}

func TestLoadSkipsMalformedIndex(t *testing.T) {
	// A half-written index (helm repo update killed mid-write) must not
	// blank out the repos that did parse.
	l := repoCache(t, map[string]string{
		"broken":   "entries:\n  cert-manager:\n  - version: \"1.0.0\"\n   bad indentation here\n",
		"jetstack": index(map[string][]string{"cert-manager": {"1.16.2"}}),
	})
	idx, err := l.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got, ok := idx.Latest("cert-manager")
	if !ok {
		t.Fatal("Latest(cert-manager) missed — a broken sibling index took the good one down with it")
	}
	if got.Version != "1.16.2" {
		t.Errorf("Version = %q, want 1.16.2", got.Version)
	}
	if got := idx.Status().Repos; got != 1 {
		t.Errorf("Status().Repos = %d, want 1 (only the parseable repo counts)", got)
	}
}

func TestStatusOldestIsTheStalestIndex(t *testing.T) {
	l := repoCache(t, map[string]string{
		"fresh": index(map[string][]string{"a": {"1.0.0"}}),
		"stale": index(map[string][]string{"b": {"1.0.0"}}),
	})
	old := time.Now().Add(-10 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(l.CachePath, "stale-index.yaml"), old, old); err != nil {
		t.Fatal(err)
	}
	idx, err := l.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// One repo left behind makes the whole answer partly stale, so Age
	// tracks the stalest index rather than the freshest.
	if age := idx.Status().Age(time.Now()); age < 9*24*time.Hour {
		t.Errorf("Status().Age() = %v, want ≈10d (the stalest index)", age)
	}
	var zero Status
	if got := zero.Age(time.Now()); got != 0 {
		t.Errorf("zero Status.Age() = %v, want 0", got)
	}
}

func TestCacheReparsesOnlyWhenFilesChange(t *testing.T) {
	l := repoCache(t, map[string]string{"jetstack": index(map[string][]string{"cert-manager": {"1.14.4"}})})
	c := NewCache(l)

	if got, _ := c.Index().Latest("cert-manager"); got.Version != "1.14.4" {
		t.Fatalf("first Index() = %q, want 1.14.4", got.Version)
	}
	for range 5 {
		c.Index()
	}
	if c.parses != 1 {
		t.Errorf("parses = %d after 6 unchanged calls, want 1 — the index files are the largest read in the app", c.parses)
	}

	// `helm repo update` lands a newer index: the next read must see it.
	path := filepath.Join(l.CachePath, "jetstack-index.yaml")
	if err := os.WriteFile(path, []byte(index(map[string][]string{"cert-manager": {"1.14.4", "1.16.2"}})), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Index().Latest("cert-manager"); got.Version != "1.16.2" {
		t.Errorf("Index() after helm repo update = %q, want 1.16.2", got.Version)
	}
	if c.parses != 2 {
		t.Errorf("parses = %d, want 2", c.parses)
	}
}

func TestNilCacheIsUsable(t *testing.T) {
	// app wires the Cache in; a screen constructed without one (tests, demo
	// paths) must degrade to "we don't know", not panic.
	var c *Cache
	if _, ok := c.Index().Latest("cert-manager"); ok {
		t.Error("nil Cache returned a chart")
	}
	if c.Index().Status().Configured {
		t.Error("nil Cache reported Configured")
	}
}

// The expectations go through filepath.Join rather than literal "/" strings
// so this runs on Windows, where the separator is "\".
func TestDefaultPathsFollowHelmEnv(t *testing.T) {
	base := filepath.Join(string(filepath.Separator), "somewhere")

	t.Setenv("HELM_REPOSITORY_CONFIG", filepath.Join(base, "repositories.yaml"))
	t.Setenv("HELM_REPOSITORY_CACHE", filepath.Join(base, "cache"))
	if got, want := defaultConfigPath(), filepath.Join(base, "repositories.yaml"); got != want {
		t.Errorf("defaultConfigPath() = %q, want %q", got, want)
	}
	if got, want := defaultCachePath(), filepath.Join(base, "cache"); got != want {
		t.Errorf("defaultCachePath() = %q, want %q", got, want)
	}

	// HELM_*_HOME outranks XDG, and helm uses it verbatim — no "helm"
	// segment appended, unlike every other branch.
	t.Setenv("HELM_REPOSITORY_CONFIG", "")
	t.Setenv("HELM_REPOSITORY_CACHE", "")
	t.Setenv("HELM_CONFIG_HOME", filepath.Join(base, "helmconf"))
	t.Setenv("HELM_CACHE_HOME", filepath.Join(base, "helmcache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "xdg", "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(base, "xdg", "cache"))
	if got, want := defaultConfigPath(), filepath.Join(base, "helmconf", "repositories.yaml"); got != want {
		t.Errorf("defaultConfigPath() = %q, want the HELM_CONFIG_HOME path %q", got, want)
	}
	if got, want := defaultCachePath(), filepath.Join(base, "helmcache", "repository"); got != want {
		t.Errorf("defaultCachePath() = %q, want the HELM_CACHE_HOME path %q", got, want)
	}

	t.Setenv("HELM_CONFIG_HOME", "")
	t.Setenv("HELM_CACHE_HOME", "")
	if got, want := defaultConfigPath(), filepath.Join(base, "xdg", "config", "helm", "repositories.yaml"); got != want {
		t.Errorf("defaultConfigPath() = %q, want the XDG path %q", got, want)
	}
	if got, want := defaultCachePath(), filepath.Join(base, "xdg", "cache", "helm", "repository"); got != want {
		t.Errorf("defaultCachePath() = %q, want the XDG path %q", got, want)
	}
}

// The platform fallbacks are what a real user hits — no XDG vars are set on
// macOS or Windows — and they are the branch most easily broken by accident,
// since CI only ever exercised the Linux one.
func TestPlatformHelmDirs(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HELM_CONFIG_HOME", "")
	t.Setenv("HELM_CACHE_HOME", "")

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}

	var wantConfig, wantCache string
	switch runtime.GOOS {
	case "windows":
		// helm's pkg/helmpath/lazypath_windows.go: %APPDATA% and %TEMP%.
		appData, tmp := os.Getenv("APPDATA"), os.Getenv("TEMP")
		if appData == "" || tmp == "" {
			t.Skip("APPDATA or TEMP is unset")
		}
		wantConfig = filepath.Join(appData, "helm")
		wantCache = filepath.Join(tmp, "helm")
	case "darwin":
		wantConfig = filepath.Join(home, "Library", "Preferences", "helm")
		wantCache = filepath.Join(home, "Library", "Caches", "helm")
	default:
		wantConfig = filepath.Join(home, ".config", "helm")
		wantCache = filepath.Join(home, ".cache", "helm")
	}

	if got := helmConfigDir(); got != wantConfig {
		t.Errorf("helmConfigDir() = %q, want %q", got, wantConfig)
	}
	if got := helmCacheDir(); got != wantCache {
		t.Errorf("helmCacheDir() = %q, want %q", got, wantCache)
	}
}
