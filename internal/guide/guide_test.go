package guide

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"codeberg.org/botfile/botfile/internal/core"
)

// sampleCommands stands in for the cli's canonical command table.
var sampleCommands = []CommandDoc{
	{Name: "plan", Summary: "show what a sync would change", Usage: "botfile plan"},
	{Name: "sync", Summary: "reconcile your agents to match your config", Usage: "botfile sync"},
	{Name: "status", Summary: "show what is managed, out of sync, and adoptable", Usage: "botfile status"},
	{Name: "adopt", Summary: "bring an agent-created component under management", Usage: "botfile adopt <path> --into <source>/<plugin>"},
}

func build() Guide {
	// home "~", nil env: documentation-style default paths, no overrides.
	return Build("/home/u/.config/botfile/config.toml", "~", nil, sampleCommands)
}

func TestBuildHasModelTermsAndWorkflowOrder(t *testing.T) {
	t.Parallel()
	g := build()

	if g.SchemaVersion != SchemaVersion {
		t.Fatalf("schemaVersion = %d, want %d", g.SchemaVersion, SchemaVersion)
	}
	wantTerms := []string{"source", "plugin", "component", "selection"}
	if len(g.Model) != len(wantTerms) {
		t.Fatalf("model has %d terms, want %d", len(g.Model), len(wantTerms))
	}
	for i, name := range wantTerms {
		if g.Model[i].Name != name {
			t.Errorf("model[%d] = %q, want %q", i, g.Model[i].Name, name)
		}
		if g.Model[i].Definition == "" {
			t.Errorf("model term %q has no definition", name)
		}
	}

	// Scope must state the user-scope boundary, the kind difference, and the
	// selection hierarchy, the three facts an agent needs before selecting.
	if len(g.Scope) == 0 {
		t.Fatal("guide has no scope notes")
	}
	scope := strings.Join(g.Scope, " ")
	for _, want := range []string{"user scope", "fan out", "model-invoked", "user-invoked", "ambient", "source > plugin > component", "wildcard"} {
		if !strings.Contains(scope, want) {
			t.Errorf("scope notes missing %q", want)
		}
	}

	// The safe order must put the read-only steps and the confirm gate before
	// sync, and adopt last.
	order := make([]string, len(g.Workflow))
	for i, s := range g.Workflow {
		order[i] = s.Command
	}
	joined := strings.Join(order, " | ")
	statusAt := indexOfContains(order, "status")
	planAt := indexOfContains(order, "plan")
	confirmAt := indexOfContains(order, "confirm")
	syncAt := indexOfContains(order, "sync")
	adoptAt := indexOfContains(order, "adopt")
	if !(statusAt < planAt && planAt < confirmAt && confirmAt < syncAt && syncAt < adoptAt) {
		t.Errorf("workflow out of safe order: %s", joined)
	}
}

func TestAgentLocationsMatchMatrix(t *testing.T) {
	t.Parallel()
	g := build()

	if len(g.Agents) != len(core.KnownAgents) {
		t.Fatalf("guide lists %d agents, want %d", len(g.Agents), len(core.KnownAgents))
	}
	got := map[string]AgentDoc{}
	for _, a := range g.Agents {
		got[a.ID] = a
	}

	cases := []struct {
		id, skills, instructions, commands string
	}{
		{"claude-code", "~/.claude/skills/<name>/", "~/.claude/rules/<name>.md", "~/.claude/commands/<name>.md"},
		{"codex-cli", "~/.agents/skills/<name>/", "~/.codex/AGENTS.md", "~/.codex/prompts/<name>.md"},
		{"opencode", "~/.agents/skills/<name>/", "~/.config/opencode/AGENTS.md", "~/.config/opencode/commands/<name>.md"},
		{"pi.dev", "~/.agents/skills/<name>/", "~/.pi/agent/AGENTS.md", "~/.pi/agent/prompts/<name>.md"},
		{"copilot-cli", "~/.agents/skills/<name>/", "~/.copilot/copilot-instructions.md", ""},
		{"crush", "~/.agents/skills/<name>/", "~/.config/crush/CRUSH.md", ""},
	}
	for _, c := range cases {
		a, ok := got[c.id]
		if !ok {
			t.Fatalf("agent %q missing from guide", c.id)
		}
		if a.Skills != c.skills {
			t.Errorf("%s skills = %q, want %q", c.id, a.Skills, c.skills)
		}
		if a.Instructions != c.instructions {
			t.Errorf("%s instructions = %q, want %q", c.id, a.Instructions, c.instructions)
		}
		if a.Commands != c.commands {
			t.Errorf("%s commands = %q, want %q", c.id, a.Commands, c.commands)
		}
	}
}

func TestAgentLocationsHonorRootOverrides(t *testing.T) {
	t.Parallel()
	getenv := func(k string) string {
		switch k {
		case "CLAUDE_CONFIG_DIR":
			return "/work/claude"
		case "CODEX_HOME":
			return "/work/codex"
		case "COPILOT_HOME":
			return "/work/copilot"
		}
		return ""
	}
	g := Build("/cfg", "/home/u", getenv, sampleCommands)
	got := map[string]AgentDoc{}
	for _, a := range g.Agents {
		got[a.ID] = a
	}

	// claude-code's base override relocates its skills, instructions, and
	// commands alike.
	if a := got["claude-code"]; a.Skills != "/work/claude/skills/<name>/" || a.Instructions != "/work/claude/rules/<name>.md" || a.Commands != "/work/claude/commands/<name>.md" {
		t.Errorf("claude-code did not honor CLAUDE_CONFIG_DIR: %+v", a)
	}
	// codex-cli's instruction singleton honors CODEX_HOME; its skills stay in the
	// shared ~/.agents pool (home-rooted, so abbreviated).
	if a := got["codex-cli"]; a.Instructions != "/work/codex/AGENTS.md" {
		t.Errorf("codex-cli did not honor CODEX_HOME: %q", a.Instructions)
	}
	if a := got["codex-cli"]; a.Skills != "~/.agents/skills/<name>/" {
		t.Errorf("codex-cli skills should stay in the shared pool: %q", a.Skills)
	}
	// copilot-cli's instruction singleton honors COPILOT_HOME.
	if a := got["copilot-cli"]; a.Instructions != "/work/copilot/copilot-instructions.md" {
		t.Errorf("copilot-cli did not honor COPILOT_HOME: %q", a.Instructions)
	}
	// copilot-vscode's instruction root has no override, so COPILOT_HOME must not
	// move it: it stays under the home-rooted ~/.copilot, abbreviated.
	if a := got["copilot-vscode"]; a.Instructions != "~/.copilot/instructions/<name>.instructions.md" {
		t.Errorf("copilot-vscode must not honor COPILOT_HOME: %q", a.Instructions)
	}
}

func TestRenderTextHasAllSections(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	RenderText(&b, build())
	out := b.String()

	for _, want := range []string{
		"botfile:", "MODEL", "source", "selection",
		"CONFIG", "/home/u/.config/botfile/config.toml", "[[sources]]", "agents = [\"claude-code\"]",
		"SCOPE", "user scope",
		"WORKFLOW", "botfile status", "botfile plan", "botfile sync", "botfile adopt",
		"COMMANDS", "AGENTS", "claude-code", "~/.claude/skills/<name>/",
		"JSON", "--format json",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text guide missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderMarkdownStructure(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	RenderMarkdown(&b, build())
	out := b.String()

	for _, want := range []string{
		"# botfile", "## Model", "- **source**:", "## Config", "```toml",
		"## Scope", "user scope",
		"## Workflow", "## Commands", "| Command | Does |", "## Agents",
		"| Agent | Skills | Instructions |", "`claude-code`", "## JSON for agents",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown guide missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderJSONRoundTrips(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	if err := RenderJSON(&b, build()); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var g Guide
	if err := json.Unmarshal(b.Bytes(), &g); err != nil {
		t.Fatalf("guide JSON does not round-trip: %v", err)
	}
	if g.SchemaVersion != SchemaVersion {
		t.Errorf("schemaVersion = %d, want %d", g.SchemaVersion, SchemaVersion)
	}
	if len(g.Model) != 4 {
		t.Errorf("model terms = %d, want 4", len(g.Model))
	}
	if len(g.Scope) == 0 {
		t.Error("scope notes missing from JSON guide")
	}
	if len(g.Agents) != len(core.KnownAgents) {
		t.Errorf("agents = %d, want %d", len(g.Agents), len(core.KnownAgents))
	}
	if len(g.Commands) != len(sampleCommands) {
		t.Errorf("commands = %d, want %d", len(g.Commands), len(sampleCommands))
	}
}

// indexOfContains returns the first index whose element contains sub, or -1.
func indexOfContains(xs []string, sub string) int {
	for i, x := range xs {
		if strings.Contains(x, sub) {
			return i
		}
	}
	return -1
}
