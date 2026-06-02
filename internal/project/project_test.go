package project

import (
	"reflect"
	"testing"

	"codeberg.org/botfile/botfile/internal/agent"
	"codeberg.org/botfile/botfile/internal/core"
	"codeberg.org/botfile/botfile/internal/reconcile"
)

// codingSource is a scanned "team" source with one plugin holding a skill and a
// memory, used across the projection tests.
func codingSource() Source {
	return Source{
		Name: "team",
		Root: "/src/team",
		Plugins: []core.Plugin{{
			Name: "coding",
			Components: []core.Component{
				{Kind: core.KindSkill, Name: "go-style"},
				{Kind: core.KindMemory, Name: "style"},
			},
		}},
	}
}

func cfgWith(sels ...core.Selection) core.Config {
	return core.Config{
		Sources:    []core.Source{{Name: "team", Location: "/src/team"}},
		Selections: sels,
	}
}

// roots resolves the default matrix's agent roots under a fixed home with no env
// overrides, matching the pre-resolved roots the runtime would hand projection.
func roots() map[core.AgentID]string {
	return agent.Default().ResolveRoots("/home/u", func(string) string { return "" })
}

func TestProjectWildcardToClaudeCode(t *testing.T) {
	t.Parallel()
	cfg := cfgWith(core.Selection{
		SourceName: "team", PluginName: core.Wildcard, ComponentID: core.Wildcard,
		Agents: []core.AgentID{core.AgentClaudeCode},
	})
	res := Project(cfg, []Source{codingSource()}, agent.Default(), roots())

	if len(res.Problems) != 0 {
		t.Fatalf("unexpected problems: %+v", res.Problems)
	}
	want := []reconcile.LinkSpec{
		// Sorted by target: rules/ before skills/.
		{Target: "/home/u/.claude/rules/style.md", Dest: "/src/team/coding/memories/style.md", SourceName: "team"},
		{Target: "/home/u/.claude/skills/go-style", Dest: "/src/team/coding/skills/go-style", SourceName: "team"},
	}
	if !reflect.DeepEqual(res.Links, want) {
		t.Fatalf("links = %+v\nwant %+v", res.Links, want)
	}
}

func TestProjectSpecificComponent(t *testing.T) {
	t.Parallel()
	cfg := cfgWith(core.Selection{
		SourceName: "team", PluginName: "coding", ComponentID: "skill/go-style",
		Agents: []core.AgentID{core.AgentClaudeCode},
	})
	res := Project(cfg, []Source{codingSource()}, agent.Default(), roots())
	if len(res.Links) != 1 || res.Links[0].Dest != "/src/team/coding/skills/go-style" {
		t.Fatalf("links = %+v, want only the go-style skill", res.Links)
	}
}

func TestProjectUnsupportedAgentIsProblem(t *testing.T) {
	t.Parallel()
	// opencode has no vendor spec yet: each matched component for it is an
	// explicit unsupported problem, while claude-code still gets its links.
	cfg := cfgWith(core.Selection{
		SourceName: "team", PluginName: core.Wildcard, ComponentID: core.Wildcard,
		Agents: []core.AgentID{core.AgentClaudeCode, core.AgentOpenCode},
	})
	res := Project(cfg, []Source{codingSource()}, agent.Default(), roots())
	if len(res.Links) != 2 {
		t.Fatalf("claude-code links = %+v, want 2", res.Links)
	}
	if len(res.Problems) != 2 {
		t.Fatalf("want 2 unsupported problems for opencode, got %+v", res.Problems)
	}
	for _, p := range res.Problems {
		if p.Kind != ProblemUnsupported || p.Agent != core.AgentOpenCode {
			t.Fatalf("unexpected problem %+v", p)
		}
	}
}

func TestProjectCodexSkillYesMemoryNo(t *testing.T) {
	t.Parallel()
	// codex-cli supports skills (tier 1) but not memory (manifesto 18): the skill
	// installs, the memory is an explicit unsupported problem.
	cfg := cfgWith(core.Selection{
		SourceName: "team", PluginName: core.Wildcard, ComponentID: core.Wildcard,
		Agents: []core.AgentID{core.AgentCodexCLI},
	})
	res := Project(cfg, []Source{codingSource()}, agent.Default(), roots())
	if len(res.Links) != 1 || res.Links[0].Target != "/home/u/.codex/skills/go-style" {
		t.Fatalf("links = %+v, want only the codex skill under ~/.codex/skills", res.Links)
	}
	if len(res.Problems) != 1 || res.Problems[0].Kind != ProblemUnsupported || res.Problems[0].Component != "memory/style" {
		t.Fatalf("want one unsupported problem for memory/style, got %+v", res.Problems)
	}
}

func TestProjectEmptySelectionIsProblem(t *testing.T) {
	t.Parallel()
	cfg := cfgWith(core.Selection{
		SourceName: "team", PluginName: core.Wildcard, ComponentID: "skill/missing",
		Agents: []core.AgentID{core.AgentClaudeCode},
	})
	res := Project(cfg, []Source{codingSource()}, agent.Default(), roots())
	if len(res.Links) != 0 {
		t.Fatalf("a non-matching selection must produce no links, got %+v", res.Links)
	}
	if len(res.Problems) != 1 || res.Problems[0].Kind != ProblemEmptySelection {
		t.Fatalf("expected one empty-selection problem, got %+v", res.Problems)
	}
}

func TestProjectUnknownSourceIsProblem(t *testing.T) {
	t.Parallel()
	cfg := cfgWith(core.Selection{
		SourceName: "ghost", PluginName: core.Wildcard, ComponentID: core.Wildcard,
		Agents: []core.AgentID{core.AgentClaudeCode},
	})
	res := Project(cfg, []Source{codingSource()}, agent.Default(), roots())
	if len(res.Links) != 0 {
		t.Fatalf("a selection on an unscanned source must produce no links, got %+v", res.Links)
	}
	if len(res.Problems) != 1 || res.Problems[0].Kind != ProblemUnknownSource {
		t.Fatalf("expected one unknown-source problem, got %+v", res.Problems)
	}
}

func TestProjectPluginFilter(t *testing.T) {
	t.Parallel()
	// A named plugin that does not exist matches nothing.
	cfg := cfgWith(core.Selection{
		SourceName: "team", PluginName: "nope", ComponentID: core.Wildcard,
		Agents: []core.AgentID{core.AgentClaudeCode},
	})
	res := Project(cfg, []Source{codingSource()}, agent.Default(), roots())
	if len(res.Links) != 0 || len(res.Problems) != 1 || res.Problems[0].Kind != ProblemEmptySelection {
		t.Fatalf("plugin filter miss should be one empty-selection problem, got %+v", res)
	}
}
