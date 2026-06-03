package adopt

import (
	"testing"

	"codeberg.org/botfile/botfile/internal/core"
	"codeberg.org/botfile/botfile/internal/discover"
	"codeberg.org/botfile/botfile/internal/project"
)

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
	plan, prob := Compute(req, personalCfg(), personalSources(), skillFound())
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
	plan, prob := Compute(req, cfg, personalSources(), skillFound())
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
	plan, prob := Compute(req, personalCfg(), personalSources(), found)
	if prob != nil {
		t.Fatalf("problem: %+v", prob)
	}
	if plan.To != "/src/personal/mine/instructions/prefs.md" {
		t.Fatalf("instruction To = %q, want /src/personal/mine/instructions/prefs.md", plan.To)
	}
}

func TestComputeProblems(t *testing.T) {
	t.Parallel()
	base := Request{Path: "/home/u/.claude/skills/bark-pro", SourceName: "personal", PluginName: "mine"}

	notFound := Request{Path: "/nope", SourceName: "personal", PluginName: "mine"}
	if _, prob := Compute(notFound, personalCfg(), personalSources(), skillFound()); prob == nil || prob.Kind != ProblemNotAdoptable {
		t.Errorf("not-adoptable: got %+v", prob)
	}

	ghost := base
	ghost.SourceName = "ghost"
	if _, prob := Compute(ghost, personalCfg(), personalSources(), skillFound()); prob == nil || prob.Kind != ProblemUnknownSource {
		t.Errorf("unknown-source: got %+v", prob)
	}

	badPlugin := base
	badPlugin.PluginName = "bad name"
	if _, prob := Compute(badPlugin, personalCfg(), personalSources(), skillFound()); prob == nil || prob.Kind != ProblemInvalidPlugin {
		t.Errorf("invalid-plugin: got %+v", prob)
	}

	collide := personalSources(core.Plugin{Name: "mine", Components: []core.Component{{Kind: core.KindSkill, Name: "bark-pro"}}})
	if _, prob := Compute(base, personalCfg(), collide, skillFound()); prob == nil || prob.Kind != ProblemCollision {
		t.Errorf("collision: got %+v", prob)
	}
}
