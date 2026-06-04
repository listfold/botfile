package adopt

import (
	"testing"

	"codeberg.org/botfile/botfile/internal/agent"
	"codeberg.org/botfile/botfile/internal/core"
	"codeberg.org/botfile/botfile/internal/discover"
	"codeberg.org/botfile/botfile/internal/project"
)

// matrix is the default agent set, the singleton-cardinality preflight's source
// of truth for which agents read a kind as one fixed file.
func matrix() agent.Set { return agent.Default() }

func skillFound() []discover.Unmanaged {
	return []discover.Unmanaged{{
		Agents: []core.AgentID{core.AgentClaudeCode},
		Kind:   core.KindSkill, Name: "bark-pro",
		Path: "/home/u/.claude/skills/bark-pro",
	}}
}

func personalSources(plugins ...core.Plugin) []project.Source {
	return []project.Source{{Name: "personal", Root: "/src/personal", Plugins: plugins}}
}

func personalCfg(sels ...core.Selection) core.Config {
	return core.Config{
		Sources:    []core.Source{{Name: "personal", Location: "/src/personal"}},
		Selections: sels,
	}
}

func TestComputeValidAddsSelection(t *testing.T) {
	t.Parallel()
	req := Request{Path: "/home/u/.claude/skills/bark-pro", SourceName: "personal", PluginName: "mine"}
	plan, prob := Compute(req, personalCfg(), personalSources(), skillFound(), matrix())
	if prob != nil {
		t.Fatalf("unexpected problem: %+v", prob)
	}
	if plan.From != "/home/u/.claude/skills/bark-pro" || plan.To != "/src/personal/mine/skills/bark-pro" {
		t.Fatalf("from/to = %q -> %q", plan.From, plan.To)
	}
	sel := plan.AddSelection
	if sel == nil || sel.SourceName != "personal" || sel.PluginName != "mine" || sel.ComponentID != "skill/bark-pro" {
		t.Fatalf("AddSelection = %+v", sel)
	}
	if len(sel.Agents) != 1 || sel.Agents[0] != core.AgentClaudeCode {
		t.Fatalf("selection agents = %v, want [claude-code]", sel.Agents)
	}
}

func TestComputeCoveredNeedsNoSelection(t *testing.T) {
	t.Parallel()
	req := Request{Path: "/home/u/.claude/skills/bark-pro", SourceName: "personal", PluginName: "mine"}
	cfg := personalCfg(core.Selection{
		SourceName: "personal", PluginName: core.Wildcard, ComponentID: core.Wildcard,
		Agents: []core.AgentID{core.AgentClaudeCode},
	})
	plan, prob := Compute(req, cfg, personalSources(), skillFound(), matrix())
	if prob != nil {
		t.Fatalf("problem: %+v", prob)
	}
	if plan.AddSelection != nil {
		t.Fatalf("a wildcard selection already covers it; AddSelection = %+v", plan.AddSelection)
	}
}

func TestComputeInstructionDestination(t *testing.T) {
	t.Parallel()
	found := []discover.Unmanaged{{
		Agents: []core.AgentID{core.AgentClaudeCode},
		Kind:   core.KindInstruction, Name: "prefs",
		Path: "/home/u/.claude/rules/prefs.md",
	}}
	req := Request{Path: "/home/u/.claude/rules/prefs.md", SourceName: "personal", PluginName: "mine"}
	plan, prob := Compute(req, personalCfg(), personalSources(), found, matrix())
	if prob != nil {
		t.Fatalf("problem: %+v", prob)
	}
	if plan.To != "/src/personal/mine/instructions/prefs.md" {
		t.Fatalf("instruction To = %q, want /src/personal/mine/instructions/prefs.md", plan.To)
	}
}

// singletonFound is a codex-cli AGENTS.md discovered for adoption: a LeafFixed
// instruction named for its agent (slice 4).
func singletonFound() []discover.Unmanaged {
	return []discover.Unmanaged{{
		Agents: []core.AgentID{core.AgentCodexCLI},
		Kind:   core.KindInstruction, Name: "codex-cli",
		Path: "/home/u/.codex/AGENTS.md",
	}}
}

func TestComputeSingletonCleanAdoptSucceeds(t *testing.T) {
	t.Parallel()
	// A broad wildcard selects codex-cli, but the source holds no other
	// instruction, so the singleton's fixed file gets exactly one destination.
	cfg := personalCfg(core.Selection{
		SourceName: "personal", PluginName: core.Wildcard, ComponentID: core.Wildcard,
		Agents: []core.AgentID{core.AgentCodexCLI},
	})
	req := Request{Path: "/home/u/.codex/AGENTS.md", SourceName: "personal", PluginName: "mine"}
	plan, prob := Compute(req, cfg, personalSources(), singletonFound(), matrix())
	if prob != nil {
		t.Fatalf("clean singleton adopt should succeed: %+v", prob)
	}
	if plan.AddSelection != nil {
		t.Fatalf("the wildcard already covers it; AddSelection = %+v", plan.AddSelection)
	}
}

func TestComputeSingletonAmbiguousBlocks(t *testing.T) {
	t.Parallel()
	// The source already holds one instruction, and a broad wildcard selects
	// codex-cli. Adopting a second instruction for that singleton would route two
	// destinations to ~/.codex/AGENTS.md from one source: adopt must block.
	sources := personalSources(core.Plugin{
		Name:       "mine",
		Components: []core.Component{{Kind: core.KindInstruction, Name: "go-style"}},
	})
	cfg := personalCfg(core.Selection{
		SourceName: "personal", PluginName: core.Wildcard, ComponentID: core.Wildcard,
		Agents: []core.AgentID{core.AgentCodexCLI},
	})
	req := Request{Path: "/home/u/.codex/AGENTS.md", SourceName: "personal", PluginName: "mine"}
	_, prob := Compute(req, cfg, sources, singletonFound(), matrix())
	if prob == nil || prob.Kind != ProblemAmbiguousSingleton {
		t.Fatalf("ambiguous singleton: got %+v, want ProblemAmbiguousSingleton", prob)
	}
}

func TestComputeProblems(t *testing.T) {
	t.Parallel()
	base := Request{Path: "/home/u/.claude/skills/bark-pro", SourceName: "personal", PluginName: "mine"}

	notFound := Request{Path: "/nope", SourceName: "personal", PluginName: "mine"}
	if _, prob := Compute(notFound, personalCfg(), personalSources(), skillFound(), matrix()); prob == nil || prob.Kind != ProblemNotAdoptable {
		t.Errorf("not-adoptable: got %+v", prob)
	}

	ghost := base
	ghost.SourceName = "ghost"
	if _, prob := Compute(ghost, personalCfg(), personalSources(), skillFound(), matrix()); prob == nil || prob.Kind != ProblemUnknownSource {
		t.Errorf("unknown-source: got %+v", prob)
	}

	badPlugin := base
	badPlugin.PluginName = "bad name"
	if _, prob := Compute(badPlugin, personalCfg(), personalSources(), skillFound(), matrix()); prob == nil || prob.Kind != ProblemInvalidPlugin {
		t.Errorf("invalid-plugin: got %+v", prob)
	}

	collide := personalSources(core.Plugin{Name: "mine", Components: []core.Component{{Kind: core.KindSkill, Name: "bark-pro"}}})
	if _, prob := Compute(base, personalCfg(), collide, skillFound(), matrix()); prob == nil || prob.Kind != ProblemCollision {
		t.Errorf("collision: got %+v", prob)
	}
}
