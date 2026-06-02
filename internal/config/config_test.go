package config

import (
	"path/filepath"
	"testing"

	"codeberg.org/botfile/botfile/internal/core"
)

func TestParseValid(t *testing.T) {
	t.Parallel()
	const src = `
[[sources]]
name = "team"
location = "git@codeberg.org:botfile/team.git"

[[sources]]
name = "personal"
location = "~/src/personal-botfiles"

[[selections]]
source = "team"
plugin = "*"
component = "*"
agents = ["claude-code", "codex-cli"]

[[selections]]
source = "personal"
component = "skill/secret"
agents = ["claude-code"]
`
	cfg, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if len(cfg.Sources) != 2 {
		t.Fatalf("got %d sources, want 2", len(cfg.Sources))
	}
	if len(cfg.Selections) != 2 {
		t.Fatalf("got %d selections, want 2", len(cfg.Selections))
	}

	// Omitted plugin defaults to the wildcard (manifesto 39).
	second := cfg.Selections[1]
	if second.PluginName != core.Wildcard {
		t.Errorf("omitted plugin = %q, want wildcard %q", second.PluginName, core.Wildcard)
	}
	if second.ComponentID != "skill/secret" {
		t.Errorf("component = %q, want %q", second.ComponentID, "skill/secret")
	}
	if len(second.Agents) != 1 || second.Agents[0] != core.AgentClaudeCode {
		t.Errorf("agents = %v, want [claude-code]", second.Agents)
	}
}

func TestParseInvalid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
	}{
		{"unknown agent", `
[[sources]]
name = "team"
location = "/srv/team"
[[selections]]
source = "team"
agents = ["acme-bot"]
`},
		{"dangling source", `
[[selections]]
source = "ghost"
agents = ["claude-code"]
`},
		{"empty source location", `
[[sources]]
name = "team"
location = ""
`},
		{"malformed toml", `[[sources]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Parse([]byte(tt.src)); err == nil {
				t.Errorf("%s: want error, got nil", tt.name)
			}
		})
	}
}

func TestDefaultPathXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/xdg")
	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if want := filepath.Join("/custom/xdg", "botfile", "config.toml"); got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestDefaultPathFallback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/tester")
	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if want := filepath.Join("/home/tester", ".config", "botfile", "config.toml"); got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}
