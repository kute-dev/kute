//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kute-dev/kute/internal/state"
)

// TestPersistedStateSurvivesARestart covers the "persisted state is
// versioned" invariant from the only angle that can observe it: two real
// sessions sharing one state directory.
//
// Every other test launches into a fresh t.TempDir(), so nothing until now
// exercised the save-on-exit → restore-on-start path at all — and both ends
// of it live outside any task. The save happens after program.Run returns,
// and the restore happens in BuildSession before a single frame is drawn, so
// a unit test of internal/state proves the document round-trips while saying
// nothing about whether kute writes or reads it.
func TestPersistedStateSurvivesARestart(t *testing.T) {
	RequireCluster(t)
	home := t.TempDir()
	contextName := ContextName(t)

	first := Launch(t, WithHome(home))
	first.WaitFor("api-", Connect)
	first.gotoKind(t, "configmaps", "ConfigMaps")
	first.WaitFor("app-config", Settle)

	// Switch namespace through the palette — the per-context namespace is
	// only written by a completed switch, not by the -n flag the launch
	// already carried.
	first.Press("n")
	first.Type("kute-e2e-b")
	first.Enter()
	first.WaitFor("namespace-b-config", Settle)

	// Exiting is what triggers the save (app.run's SyncLocationToPerContext
	// + State.Save, after the program returns).
	first.Quit()

	saved := readState(t, home)
	if saved.Version != state.CurrentVersion {
		t.Fatalf("saved state version = %d, want the current schema %d", saved.Version, state.CurrentVersion)
	}
	pc, ok := saved.PerContext[contextName]
	if !ok {
		t.Fatalf("no per-context entry for %q; saved state: %+v", contextName, saved)
	}
	if pc.Namespace != "kute-e2e-b" {
		t.Errorf("saved namespace = %q, want kute-e2e-b", pc.Namespace)
	}
	if pc.Kind != "ConfigMap" {
		t.Errorf("saved kind = %q, want ConfigMap", pc.Kind)
	}

	// The restore. WithNamespace("") drops the -n flag: it is the
	// highest-precedence namespace source, and leaving it on would make this
	// assertion pass with the restore removed entirely.
	second := Launch(t, WithHome(home), WithNamespace(""))
	// Both halves of the location, from the file rather than from a flag:
	// the namespace's own object and the kind's list title.
	second.WaitForAll(Connect, "kute-e2e-b", "ConfigMaps", "namespace-b-config")
}

// TestOlderStateVersionMigratesForward launches onto a state file written by
// an older schema.
//
// "Any change to the recents/perContext schema bumps the version and adds a
// migration" is an invariant about upgrades, and an upgrade is exactly what
// no test performs: every launch in this suite starts from an empty state
// dir, which is the one input the migration never runs on. A v2 document is
// what a user upgrading from a v2 build has on disk.
func TestOlderStateVersionMigratesForward(t *testing.T) {
	RequireCluster(t)
	home := t.TempDir()
	contextName := ContextName(t)

	// Written as raw JSON rather than through internal/state: the point is
	// to produce a document this build's own writer can no longer emit.
	writeStateFile(t, home, map[string]any{
		"version":     2,
		"recentKinds": []string{"ConfigMap", "Pod"},
		"perContext": map[string]any{
			contextName: map[string]any{
				"namespace":        "kute-e2e-b",
				"kind":             "ConfigMap",
				"recentNamespaces": []string{"kute-e2e-b", "kute-e2e"},
			},
		},
	})

	a := Launch(t, WithHome(home), WithNamespace(""))
	// The old document is honoured rather than discarded: a version kute no
	// longer writes is still a version it has to read.
	a.WaitForAll(Connect, "kute-e2e-b", "ConfigMaps", "namespace-b-config")
	a.Quit()

	// And it is rewritten at the current version, carrying the old fields
	// forward — a migration that dropped them would leave this session's
	// user looking at a cluster's worth of forgotten context.
	migrated := readState(t, home)
	if migrated.Version != state.CurrentVersion {
		t.Fatalf("state version after a v2 launch = %d, want %d", migrated.Version, state.CurrentVersion)
	}
	pc := migrated.PerContext[contextName]
	if pc.Namespace != "kute-e2e-b" {
		t.Errorf("migrated namespace = %q, want kute-e2e-b", pc.Namespace)
	}
	if len(pc.RecentNamespaces) == 0 {
		t.Errorf("migrated per-context recents were dropped: %+v", pc)
	}
}

func stateFilePath(home string) string {
	return filepath.Join(home, ".local", "state", "kute", "state.json")
}

func readState(t *testing.T, home string) state.State {
	t.Helper()
	data, err := os.ReadFile(stateFilePath(home))
	if err != nil {
		t.Fatalf("reading the persisted state file: %v", err)
	}
	var s state.State
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("parsing the persisted state file: %v", err)
	}
	return s
}

func writeStateFile(t *testing.T, home string, document map[string]any) {
	t.Helper()
	path := stateFilePath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating the state dir: %v", err)
	}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the seeded state document: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing the seeded state file: %v", err)
	}
}
