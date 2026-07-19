// Package state persists cross-session UI state — palette recents and the
// per-context namespace/kind/filter the user was last looking at
// (mvp-plan.md §0.6) — to $XDG_STATE_HOME/kute/state.json, falling back to
// ~/.local/state/kute/state.json. It is best-effort: a missing, corrupt,
// or unrecognized-future-version file never blocks startup, it just yields
// a fresh zero value.
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// CurrentVersion is the schema version stamped on every save. Bump it and
// extend migrate whenever the schema changes.
const CurrentVersion = 1

// MaxRecent caps every Recent* list, most-recent-first. 11, not some
// rounder number: 6a/7a's numbered recent-pick (tui.recentNumbers) excludes
// the list's own first two entries — index 0 is always current, index 1 is
// always the "previous" alt-tab target (tui.mostRecentOther) — before
// assigning digits 1-9 to what's left, so the list needs current + previous
// + 9 addressable slots = 11 for the full '1'-'9' range to ever be reachable.
const MaxRecent = 11

// PerContext is the namespace/kind/filter remembered for one kube-context,
// keyed by context name in State.PerContext, plus that context's own
// namespace-recents list. Namespace is the singular restore value (what
// context-switch restores); RecentNamespaces is the full most-recent-first
// list the namespace palette's RECENT row/digit-pick read — kept as a
// separate field rather than derived from RecentNamespaces[0], mirroring how
// Kind and the global RecentKinds list already coexist independently.
// Namespaces don't get a global recents list like kinds/contexts do: a
// namespace only exists inside its own cluster, so a global list would show
// dead rows from other contexts (see State's doc comment).
type PerContext struct {
	Namespace        string   `json:"namespace,omitempty"`
	Kind             string   `json:"kind,omitempty"`
	Filter           string   `json:"filter,omitempty"`
	RecentNamespaces []string `json:"recentNamespaces,omitempty"`
}

// State is the persisted document. RecentKinds/RecentContexts are
// most-recent-first — index 0 is always the current entry (only a completed
// switch pushes to the front), index 1 the one before it. Each palette
// scope's alt-tab grammar (opening the palette pre-selects index 1, so a
// bare open+enter toggles back to it — see tui.mostRecentOther) reads off
// that ordering. Kinds and contexts exist across every cluster, so their
// recents are one global list, filtered to what the current context actually
// has at display time. Namespaces don't share that property (a namespace is
// scoped to one cluster), so their recents live per-context instead — see
// PerContext.RecentNamespaces.
type State struct {
	Version        int                   `json:"version"`
	RecentKinds    []string              `json:"recentKinds,omitempty"`
	RecentContexts []string              `json:"recentContexts,omitempty"`
	PerContext     map[string]PerContext `json:"perContext,omitempty"`
}

// Path returns the state file location: $XDG_STATE_HOME/kute/state.json,
// or ~/.local/state/kute/state.json when XDG_STATE_HOME is unset.
func Path() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "kute", "state.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".local", "state", "kute", "state.json")
}

// Load reads and returns the state file at Path(). Missing/corrupt files
// yield a fresh zero value; an unrecognized newer Version is discarded
// (never partially interpreted) rather than risking a misread. Neither case
// is an error — callers get a usable State unconditionally.
func Load() State {
	return loadFrom(Path())
}

func loadFrom(path string) State {
	data, err := os.ReadFile(path)
	if err != nil {
		return zero()
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return zero()
	}
	switch {
	case s.Version == CurrentVersion:
		return normalize(s)
	case s.Version < CurrentVersion:
		return normalize(migrate(s))
	default:
		return zero()
	}
}

func zero() State {
	return State{Version: CurrentVersion, PerContext: map[string]PerContext{}}
}

func normalize(s State) State {
	if s.PerContext == nil {
		s.PerContext = map[string]PerContext{}
	}
	s.RecentKinds = capRecent(s.RecentKinds)
	s.RecentContexts = capRecent(s.RecentContexts)
	for name, pc := range s.PerContext {
		pc.RecentNamespaces = capRecent(pc.RecentNamespaces)
		s.PerContext[name] = pc
	}
	return s
}

func capRecent(items []string) []string {
	if len(items) > MaxRecent {
		return items[:MaxRecent]
	}
	return items
}

// migrate upgrades s to CurrentVersion. Schema v1 is the only version so
// far; this is the hook the next schema bump extends (add a case per old
// version, never delete the ability to read one further back than needed).
func migrate(s State) State {
	s.Version = CurrentVersion
	return s
}

// Save atomically writes s to Path() (temp file + rename, so a crash mid
// write never corrupts the previous state), stamping Version.
func (s State) Save() error {
	return s.saveTo(Path())
}

func (s State) saveTo(path string) error {
	s.Version = CurrentVersion
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".state-*.json.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

// PushRecent prepends value to items (most-recent-first), removing any
// existing occurrence and capping the result at MaxRecent. Empty values are
// ignored.
func PushRecent(items []string, value string) []string {
	if value == "" {
		return items
	}
	out := make([]string, 0, MaxRecent)
	out = append(out, value)
	for _, it := range items {
		if it == value {
			continue
		}
		out = append(out, it)
		if len(out) >= MaxRecent {
			break
		}
	}
	return out
}
