// Package config reads and writes the kute user config file
// (~/.config/kute/config.yaml). Its one MVP field, prodContexts, is the
// sole source of PROD status (mvp-plan.md §Decisions already made #2) — a
// deliberate deviation from the design handoff's kubeconfig-annotation
// approach, and never a name heuristic. SetProd is the one write path (7a's
// ctrl+p mark/unmark-prod key); everything else only ever reads it.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"
)

// Config is the parsed ~/.config/kute/config.yaml.
type Config struct {
	ProdContexts StringList `yaml:"prodContexts,omitempty"`
	// Theme overrides terminal-background detection: "dark" or "light".
	// Empty (or any other value) falls back to detection. A --theme flag
	// takes precedence over this — see decision #3 in mvp-plan.md.
	Theme string `yaml:"theme,omitempty"`
	// NodeShellImage overrides the debug-container image the node-shell
	// verb ('s' on a node) hands to kubectl debug — for clusters that can't
	// pull from Docker Hub. Empty falls back to kube.DefaultNodeShellImage.
	NodeShellImage string `yaml:"nodeShellImage,omitempty"`
	// Update holds 28a/28b's update-check toggle — relevant behind
	// egress-flagging proxies (docs/design README.md §28a).
	Update UpdateConfig `yaml:"update,omitempty"`

	// LoadErr is why the file on disk could not be read in full — nil for a
	// clean read and for a file that simply doesn't exist. Every field that
	// did decode is still populated; the UI shows LoadErr as a header chip
	// (tui.BuildConfigChip) and the composition root logs it to the
	// diagnostics sink, so a broken hand edit is never silent.
	LoadErr error `yaml:"-"`
	// prodUnknown is set when prodContexts itself couldn't be read (an
	// unreadable or syntactically broken file, or a prodContexts value that
	// is neither a string nor a list of strings). IsProd then answers true
	// for every context: a broken config must never quietly lower the
	// destructive-action tier (fail closed, CLAUDE.md's destructive-action
	// policy).
	prodUnknown bool
}

// StringList is a YAML list of strings that also accepts a lone scalar as a
// one-element list — `prodContexts: prod-eu` is the hand edit people
// actually make, and it means exactly what it looks like.
type StringList []string

// UnmarshalYAML implements yaml.Unmarshaler.
func (l *StringList) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		var s string
		if err := n.Decode(&s); err != nil {
			return err
		}
		*l = StringList{s}
		return nil
	}
	var list []string
	if err := n.Decode(&list); err != nil {
		return err
	}
	*l = list
	return nil
}

// UpdateConfig is Config's "update:" block.
type UpdateConfig struct {
	// Check disables the ambient release-feed GET entirely when explicitly
	// set to false. A nil pointer (the key absent from the file) means
	// enabled — see UpdateCheckEnabled.
	Check *bool `yaml:"check,omitempty"`
}

// Path returns ~/.config/kute/config.yaml.
func Path() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "kute", "config.yaml")
}

// Load reads Path(). A missing file yields the zero value (nothing is prod)
// — an absent config file must never block startup. An unreadable or
// partly unparsable one never blocks startup either, but it never fails
// open: every field that decodes is kept, the problem is carried on
// LoadErr, and if prodContexts itself is the casualty every context reads
// as PROD (see IsProd).
func Load() Config {
	return loadFrom(Path())
}

func loadFrom(path string) Config {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}
	}
	if err != nil {
		return Config{LoadErr: fmt.Errorf("read %s: %w", path, err), prodUnknown: true}
	}
	var c Config
	err = yaml.Unmarshal(data, &c)
	if err == nil {
		return c
	}
	var typeErr *yaml.TypeError
	if !errors.As(err, &typeErr) {
		// A syntax error: nothing in the file can be trusted.
		return Config{LoadErr: fmt.Errorf("parse %s: %w", path, err), prodUnknown: true}
	}
	// A type error: yaml.v3 has already decoded every field it could into
	// c. Whether prodContexts is among the casualties is answered by
	// decoding it alone — any error there (including one bad element of an
	// otherwise valid list, which yaml.v3 would silently drop) means the
	// list can't be trusted.
	c.LoadErr = fmt.Errorf("parse %s: %w", path, err)
	var prod struct {
		ProdContexts StringList `yaml:"prodContexts"`
	}
	if yaml.Unmarshal(data, &prod) != nil {
		c.prodUnknown = true
	}
	return c
}

// ProdUnknown reports whether prodContexts couldn't be read, so every
// context is being treated as PROD until the file is fixed.
func (c Config) ProdUnknown() bool {
	return c.prodUnknown
}

// UpdateCheckEnabled reports whether the ambient release-feed check (28a)
// should run at all — true unless update.check is explicitly set to false.
func (c Config) UpdateCheckEnabled() bool {
	return c.Update.Check == nil || *c.Update.Check
}

// IsProd reports whether contextName is listed under prodContexts — or,
// when prodContexts couldn't be read at all, true for every context.
func (c Config) IsProd(contextName string) bool {
	return c.prodUnknown || slices.Contains(c.ProdContexts, contextName)
}

// SetProd adds or removes contextName from ProdContexts and persists the
// result to Path() — the write-side counterpart to IsProd, backing 7a's
// ctrl+p mark/unmark-prod key (docs/design README.md §7a). A no-op (no
// write) when the context's status already matches prod. Every other kute
// session reads the same file, so this is the one place that status can
// change short of hand-editing the YAML.
//
// SetProd refuses (ErrUnparsable, nothing written) when the file failed to
// load: the in-memory Config is only what survived the parse, so writing it
// back would clobber the user's file with that remnant.
func (c *Config) SetProd(contextName string, prod bool) error {
	if c.LoadErr != nil {
		return fmt.Errorf("%w: %w", ErrUnparsable, c.LoadErr)
	}
	if prod == c.IsProd(contextName) {
		return nil
	}
	if prod {
		c.ProdContexts = append(c.ProdContexts, contextName)
	} else {
		c.ProdContexts = slices.DeleteFunc(c.ProdContexts, func(s string) bool { return s == contextName })
	}
	return c.save()
}

// ErrUnparsable is SetProd's refusal to overwrite a config file that didn't
// load cleanly.
var ErrUnparsable = errors.New("config.yaml has errors; fix it by hand before changing PROD contexts")

// save writes c to Path() as YAML, creating ~/.config/kute if it doesn't
// exist yet — SetProd may be the very first thing to persist any config for
// a user who never hand-wrote the file.
func (c Config) save() error {
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
