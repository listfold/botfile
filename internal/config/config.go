// Package config loads botfile's declared desired state from an XDG-compliant
// config.toml (manifesto 37) and maps it into the pure core domain model
// (manifesto 38). This is the I/O boundary for configuration: the core package
// stays pure, this package reads the file and resolves the environment.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"codeberg.org/botfile/botfile/internal/core"
)

// FileName is the fixed name of botfile's configuration file (manifesto 37).
const FileName = "config.toml"

// DefaultPath resolves the config.toml location per manifesto 37:
// $XDG_CONFIG_HOME/botfile/config.toml, falling back to ~/.config/botfile per
// the XDG Base Directory default when XDG_CONFIG_HOME is unset or relative.
func DefaultPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(base) {
		// XDG requires an absolute path; otherwise the default applies.
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory for config path: %w", err)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "botfile", FileName), nil
}

// wireConfig mirrors the on-disk config.toml shape. It exists only to decode
// TOML; it is mapped into the validated core.Config and then discarded.
type wireConfig struct {
	Sources    []wireSource    `toml:"sources"`
	Selections []wireSelection `toml:"selections"`
}

type wireSource struct {
	Name     string `toml:"name"`
	Location string `toml:"location"`
}

type wireSelection struct {
	Source    string   `toml:"source"`
	Plugin    string   `toml:"plugin"`
	Component string   `toml:"component"`
	Agents    []string `toml:"agents"`
}

// Load reads and validates the configuration at path. A missing file is an
// error here; callers that treat "no config yet" as a valid empty state should
// check with os.IsNotExist before deciding.
func Load(path string) (core.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return core.Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	return Parse(data)
}

// Parse decodes config bytes and maps them into a validated core.Config. It is
// the pure-ish seam Load builds on (it touches no filesystem), which keeps
// decoding and validation independently testable.
func Parse(data []byte) (core.Config, error) {
	var wire wireConfig
	meta, err := toml.Decode(string(data), &wire)
	if err != nil {
		return core.Config{}, fmt.Errorf("decode config: %w", err)
	}
	// Reject unknown keys rather than ignore them: because plugin and component
	// default to the wildcard, a silently dropped typo (for example
	// "components" for "component") would widen a selection to everything
	// instead of failing (manifesto 39). Surface it before defaulting.
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, k := range undecoded {
			keys = append(keys, k.String())
		}
		return core.Config{}, fmt.Errorf("decode config: unknown key(s): %s", strings.Join(keys, ", "))
	}

	cfg := core.Config{
		Sources:    make([]core.Source, 0, len(wire.Sources)),
		Selections: make([]core.Selection, 0, len(wire.Selections)),
	}
	for _, s := range wire.Sources {
		cfg.Sources = append(cfg.Sources, core.Source{
			Name:     s.Name,
			Location: s.Location,
		})
	}
	for _, sel := range wire.Selections {
		cfg.Selections = append(cfg.Selections, mapSelection(sel))
	}

	if err := cfg.Validate(); err != nil {
		return core.Config{}, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}

// mapSelection maps a wire selection into a core.Selection, applying the
// wildcard defaults: an omitted plugin or component targets all of them
// (manifesto 39), which is the common, ergonomic case.
func mapSelection(sel wireSelection) core.Selection {
	plugin := sel.Plugin
	if plugin == "" {
		plugin = core.Wildcard
	}
	component := sel.Component
	if component == "" {
		component = core.Wildcard
	}
	agents := make([]core.AgentID, 0, len(sel.Agents))
	for _, a := range sel.Agents {
		agents = append(agents, core.AgentID(a))
	}
	return core.Selection{
		SourceName:  sel.Source,
		PluginName:  plugin,
		ComponentID: component,
		Agents:      agents,
	}
}
