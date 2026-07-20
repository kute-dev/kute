package state

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestLoadMissingFileReturnsZeroValue(t *testing.T) {
	t.Parallel()
	got := loadFrom(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if got.Version != CurrentVersion {
		t.Fatalf("Version = %d, want %d", got.Version, CurrentVersion)
	}
	if got.PerContext == nil {
		t.Fatalf("expected non-nil PerContext map")
	}
	if len(got.RecentKinds) != 0 {
		t.Fatalf("expected empty RecentKinds, got %v", got.RecentKinds)
	}
}

func TestLoadCorruptFileReturnsZeroValue(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadFrom(path)
	if got.Version != CurrentVersion {
		t.Fatalf("Version = %d, want %d", got.Version, CurrentVersion)
	}
}

func TestLoadUnknownNewerVersionIsDiscarded(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	future := `{"version": 999, "recentKinds": ["Pod"]}`
	if err := os.WriteFile(path, []byte(future), 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadFrom(path)
	if got.Version != CurrentVersion {
		t.Fatalf("Version = %d, want %d (discarded)", got.Version, CurrentVersion)
	}
	if len(got.RecentKinds) != 0 {
		t.Fatalf("expected future-version data discarded, got %v", got.RecentKinds)
	}
}

// TestLoadDropsOldTopLevelRecentNamespaces pins the (deliberately
// migration-free) schema change that moved namespace recents from a global
// State.RecentNamespaces list into per-context PerContext.RecentNamespaces:
// an old file still carrying the top-level "recentNamespaces" key loads
// without error, under the same version, and that data simply isn't present
// anywhere in the result — json.Unmarshal drops an unknown key on its own,
// no explicit migrate() step needed.
func TestLoadDropsOldTopLevelRecentNamespaces(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	old := `{"version": 1, "recentNamespaces": ["default", "prod"], "perContext": {"dev": {"namespace": "default"}}}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadFrom(path)
	if got.Version != CurrentVersion {
		t.Fatalf("Version = %d, want %d", got.Version, CurrentVersion)
	}
	if got.PerContext["dev"].Namespace != "default" {
		t.Fatalf("PerContext[dev].Namespace = %q, want %q", got.PerContext["dev"].Namespace, "default")
	}
	if len(got.PerContext["dev"].RecentNamespaces) != 0 {
		t.Fatalf("expected the old global recentNamespaces to not carry over, got %v", got.PerContext["dev"].RecentNamespaces)
	}
}

// TestLoadMigratesV1FileToV2 pins the version-2 schema bump (State.UpdateCheck,
// 28a/28b): an old v1 file with no updateCheck key loads cleanly at v2 with
// UpdateCheck at its zero value.
func TestLoadMigratesV1FileToV2(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	v1 := `{"version": 1, "recentKinds": ["Pod"]}`
	if err := os.WriteFile(path, []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadFrom(path)
	if got.Version != CurrentVersion {
		t.Fatalf("Version = %d, want %d", got.Version, CurrentVersion)
	}
	if len(got.RecentKinds) != 1 || got.RecentKinds[0] != "Pod" {
		t.Fatalf("RecentKinds = %v, want migrated data preserved", got.RecentKinds)
	}
	if !got.UpdateCheck.LastChecked.IsZero() || got.UpdateCheck.LatestVersion != "" || len(got.UpdateCheck.SeenVersions) != 0 {
		t.Fatalf("UpdateCheck = %+v, want zero value from a v1 file", got.UpdateCheck)
	}
}

func TestMarkUpdateSeenIsIdempotent(t *testing.T) {
	t.Parallel()
	var s State
	s.MarkUpdateSeen("0.2.1")
	s.MarkUpdateSeen("0.2.1")
	s.MarkUpdateSeen("0.2.2")
	if len(s.UpdateCheck.SeenVersions) != 2 {
		t.Fatalf("SeenVersions = %v, want exactly 2 (dedup)", s.UpdateCheck.SeenVersions)
	}
	if !s.UpdateSeen("0.2.1") || !s.UpdateSeen("0.2.2") {
		t.Fatalf("UpdateSeen false for a marked version, SeenVersions = %v", s.UpdateCheck.SeenVersions)
	}
	if s.UpdateSeen("0.3.0") {
		t.Fatal("UpdateSeen true for a version never marked")
	}
}

func TestMarkUpdateSeenIgnoresEmpty(t *testing.T) {
	t.Parallel()
	var s State
	s.MarkUpdateSeen("")
	if len(s.UpdateCheck.SeenVersions) != 0 {
		t.Fatalf("SeenVersions = %v, want unchanged for an empty version", s.UpdateCheck.SeenVersions)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	want := State{
		RecentKinds: []string{"Pod", "Deployment"},
		PerContext: map[string]PerContext{
			"dev": {Namespace: "default", Kind: "Pod", Filter: "api", RecentNamespaces: []string{"default"}},
		},
	}
	if err := want.saveTo(path); err != nil {
		t.Fatalf("saveTo: %v", err)
	}
	got := loadFrom(path)
	if got.Version != CurrentVersion {
		t.Fatalf("Version = %d, want %d", got.Version, CurrentVersion)
	}
	if len(got.RecentKinds) != 2 || got.RecentKinds[0] != "Pod" {
		t.Fatalf("RecentKinds = %v", got.RecentKinds)
	}
	if got.PerContext["dev"].Namespace != "default" {
		t.Fatalf("PerContext[dev] = %+v", got.PerContext["dev"])
	}
	if got := got.PerContext["dev"].RecentNamespaces; len(got) != 1 || got[0] != "default" {
		t.Fatalf("PerContext[dev].RecentNamespaces = %v", got)
	}
}

func TestSaveStampsCurrentVersion(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	s := State{Version: 0}
	if err := s.saveTo(path); err != nil {
		t.Fatal(err)
	}
	got := loadFrom(path)
	if got.Version != CurrentVersion {
		t.Fatalf("Version = %d, want %d", got.Version, CurrentVersion)
	}
}

func TestPathHonorsXDGStateHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/xdg-state")
	got := Path()
	want := filepath.Join("/tmp/xdg-state", "kute", "state.json")
	if got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}

func TestPathFallsBackToHomeLocalState(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := Path()
	want := filepath.Join(home, ".local", "state", "kute", "state.json")
	if got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}

func TestLoadSaveRoundTripThroughPublicAPI(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s := Load()
	if s.Version != CurrentVersion {
		t.Fatalf("initial Load() Version = %d, want %d", s.Version, CurrentVersion)
	}
	s.RecentKinds = PushRecent(s.RecentKinds, "Pod")
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got := Load()
	if len(got.RecentKinds) != 1 || got.RecentKinds[0] != "Pod" {
		t.Fatalf("RecentKinds after round trip = %v", got.RecentKinds)
	}
}

func TestPushRecentDedupesAndCaps(t *testing.T) {
	t.Parallel()
	items := []string{"a", "b", "c"}
	got := PushRecent(items, "b")
	want := []string{"b", "a", "c"}
	if !equalStrings(got, want) {
		t.Fatalf("PushRecent = %v, want %v", got, want)
	}

	full := make([]string, MaxRecent)
	for i := range full {
		full[i] = strconv.Itoa(i + 1) // "1".."MaxRecent"
	}
	pushed := strconv.Itoa(MaxRecent + 1)
	got = PushRecent(full, pushed)
	if len(got) != MaxRecent {
		t.Fatalf("len = %d, want %d", len(got), MaxRecent)
	}
	wantLast := strconv.Itoa(MaxRecent - 1) // full's last entry (MaxRecent) drops off the cap
	if got[0] != pushed || got[len(got)-1] != wantLast {
		t.Fatalf("PushRecent capped = %v", got)
	}
}

func TestPushRecentIgnoresEmpty(t *testing.T) {
	t.Parallel()
	items := []string{"a"}
	got := PushRecent(items, "")
	if !equalStrings(got, items) {
		t.Fatalf("PushRecent with empty value = %v, want unchanged %v", got, items)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
