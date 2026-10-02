// Package config reads and writes the kute user config file
// (~/.config/kute/config.yaml). Its one MVP field, prodContexts, is the
// sole source of PROD status (mvp-plan.md §Decisions already made #2) — a
// deliberate deviation from the design handoff's kubeconfig-annotation
// approach, and never a name heuristic. SetProd is the one write path (7a's
// ctrl+p mark/unmark-prod key); everything else only ever reads it.
package config

import (
	"os"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"
)

// Config is the parsed ~/.config/kute/config.yaml.
type Config struct {
	ProdContexts []string `yaml:"prodContexts,omitempty"`
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

// Load reads Path(). A missing or unparsable file yields the zero value
// (nothing is prod) rather than an error — an absent config file must never
// block startup.
func Load() Config {
	return loadFrom(Path())
}

func loadFrom(path string) Config {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Config{}
	}
	return c
}

// UpdateCheckEnabled reports whether the ambient release-feed check (28a)
// should run at all — true unless update.check is explicitly set to false.
func (c Config) UpdateCheckEnabled() bool {
	return c.Update.Check == nil || *c.Update.Check
}

// IsProd reports whether contextName is listed under prodContexts.
func (c Config) IsProd(contextName string) bool {
	return slices.Contains(c.ProdContexts, contextName)
}

// SetProd adds or removes contextName from ProdContexts and persists the
// result to Path() — the write-side counterpart to IsProd, backing 7a's
// ctrl+p mark/unmark-prod key (docs/design README.md §7a). A no-op (no
// write) when the context's status already matches prod. Every other kute
// session reads the same file, so this is the one place that status can
// change short of hand-editing the YAML.
func (c *Config) SetProd(contextName string, prod bool) error {
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
