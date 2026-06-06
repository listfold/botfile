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
	return Build("/home/u/.config/botfile/config.toml", sampleCommands)
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
		id, skills, instructions string
	}{
		{"claude-code", "~/.claude/skills/<name>/", "~/.claude/rules/<name>.md"},
		{"codex-cli", "~/.agents/skills/<name>/", "~/.codex/AGENTS.md"},
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
