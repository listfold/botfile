package project

import (
	"reflect"
	"testing"

	"codeberg.org/botfile/botfile/internal/agent"
	"codeberg.org/botfile/botfile/internal/core"
	"codeberg.org/botfile/botfile/internal/reconcile"
)

// codingSource is a scanned "team" source with one plugin holding a skill and an
// instruction, used across the projection tests.
func codingSource() Source {
	return Source{
		Name: "team",
		Root: "/src/team",
		Plugins: []core.Plugin{{
			Name: "coding",
			Components: []core.Component{
				{Kind: core.KindSkill, Name: "go-style"},
				{Kind: core.KindInstruction, Name: "style"},
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
func roots() agent.Roots {
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
		{Target: "/home/u/.claude/rules/style.md", Dest: "/src/team/coding/instructions/style.md", SourceName: "team"},
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
	// copilot-vscode has no vendor spec yet: each matched component for it is an
	// explicit unsupported problem, while claude-code still gets its links.
	cfg := cfgWith(core.Selection{
		SourceName: "team", PluginName: core.Wildcard, ComponentID: core.Wildcard,
		Agents: []core.AgentID{core.AgentClaudeCode, core.AgentCopilotVSCode},
	})
	res := Project(cfg, []Source{codingSource()}, agent.Default(), roots())
	if len(res.Links) != 2 {
		t.Fatalf("claude-code links = %+v, want 2", res.Links)
	}
	if len(res.Problems) != 2 {
		t.Fatalf("want 2 unsupported problems for copilot-vscode, got %+v", res.Problems)
	}
	for _, p := range res.Problems {
		if p.Kind != ProblemUnsupported || p.Agent != core.AgentCopilotVSCode {
			t.Fatalf("unexpected problem %+v", p)
		}
	}
}

func TestProjectTwoInstructionsToOneSingletonAreAmbiguous(t *testing.T) {
	t.Parallel()
	// A source with two instructions, wildcard-selected to a singleton agent,
	// projects two links at the one fixed target. That is the same-source
	// many-to-one that reconcile reports as an ambiguous target (manifesto 35):
	// precedence chooses between sources, not within one, so botfile does not pick.
	src := Source{
		Name: "team", Root: "/src/team",
		Plugins: []core.Plugin{{
			Name: "coding",
			Components: []core.Component{
				{Kind: core.KindInstruction, Name: "go-style"},
				{Kind: core.KindInstruction, Name: "review"},
			},
		}},
	}
	cfg := cfgWith(core.Selection{
		SourceName: "team", PluginName: core.Wildcard, ComponentID: core.Wildcard,
		Agents: []core.AgentID{core.AgentCodexCLI},
	})
	res := Project(cfg, []Source{src}, agent.Default(), roots())
	n := 0
	for _, l := range res.Links {
		if l.Target == "/home/u/.codex/AGENTS.md" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("want two links contending for ~/.codex/AGENTS.md, got %d: %+v", n, res.Links)
	}
	plan := reconcile.Reconcile(res.Links, reconcile.World{}, reconcile.Options{
		Roots: []reconcile.Root{{Name: "team", Path: "/src/team"}},
	})
	found := false
	for _, p := range plan.Problems {
		if p.Kind == reconcile.ProblemAmbiguousTarget && p.Target == "/home/u/.codex/AGENTS.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want an ambiguous-target problem for the singleton, got %+v", plan.Problems)
	}
}

func TestProjectTwoCrushInstructionsAreAmbiguous(t *testing.T) {
	t.Parallel()
	// crush's instruction is a LeafFixed singleton (~/.config/crush/CRUSH.md), so it
	// obeys the same cardinality rule: two instructions from one source contend for
	// the one file and reconcile reports an ambiguous target.
	src := Source{
		Name: "team", Root: "/src/team",
		Plugins: []core.Plugin{{
			Name: "coding",
			Components: []core.Component{
				{Kind: core.KindInstruction, Name: "go-style"},
				{Kind: core.KindInstruction, Name: "review"},
			},
		}},
	}
	cfg := cfgWith(core.Selection{
		SourceName: "team", PluginName: core.Wildcard, ComponentID: core.Wildcard,
		Agents: []core.AgentID{core.AgentCrush},
	})
	res := Project(cfg, []Source{src}, agent.Default(), roots())
	plan := reconcile.Reconcile(res.Links, reconcile.World{}, reconcile.Options{
		Roots: []reconcile.Root{{Name: "team", Path: "/src/team"}},
	})
	found := false
	for _, p := range plan.Problems {
		if p.Kind == reconcile.ProblemAmbiguousTarget && p.Target == "/home/u/.config/crush/CRUSH.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want an ambiguous-target problem for crush's CRUSH.md, got %+v", plan.Problems)
	}
}

func TestProjectCodexSkillAndSingletonInstruction(t *testing.T) {
	t.Parallel()
	// codex-cli installs skills under the shared ~/.agents/skills and its single
	// instruction into the fixed file ~/.codex/AGENTS.md (a different root). Both
	// land as links; nothing is unsupported.
	cfg := cfgWith(core.Selection{
		SourceName: "team", PluginName: core.Wildcard, ComponentID: core.Wildcard,
		Agents: []core.AgentID{core.AgentCodexCLI},
	})
	res := Project(cfg, []Source{codingSource()}, agent.Default(), roots())
	if len(res.Problems) != 0 {
		t.Fatalf("want no problems, got %+v", res.Problems)
	}
	var skill, instr *reconcile.LinkSpec
	for i := range res.Links {
		switch res.Links[i].Target {
		case "/home/u/.agents/skills/go-style":
			skill = &res.Links[i]
		case "/home/u/.codex/AGENTS.md":
			instr = &res.Links[i]
		}
	}
	if skill == nil {
		t.Errorf("missing codex skill link under ~/.agents/skills: %+v", res.Links)
	}
	if instr == nil || instr.Dest != "/src/team/coding/instructions/style.md" {
		t.Errorf("missing codex instruction link to ~/.codex/AGENTS.md: %+v", res.Links)
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

func TestProjectSharedSkillNamespaceNotice(t *testing.T) {
	t.Parallel()
	// Scoping a skill to copilot-cli alone still installs it into the shared
	// ~/.agents/skills, which codex-cli, crush, opencode, and pi.dev also read. The
	// projection must say so (manifesto 49), so the user is not misled into
	// thinking the other readers are excluded.
	cfg := cfgWith(core.Selection{
		SourceName: "team", PluginName: core.Wildcard, ComponentID: "skill/go-style",
		Agents: []core.AgentID{core.AgentCopilotCLI},
	})
	res := Project(cfg, []Source{codingSource()}, agent.Default(), roots())
	if len(res.Links) != 1 || res.Links[0].Target != "/home/u/.agents/skills/go-style" {
		t.Fatalf("links = %+v, want the shared skill install", res.Links)
	}
	if len(res.Notices) != 1 {
		t.Fatalf("want one shared-skill-namespace notice, got %+v", res.Notices)
	}
	n := res.Notices[0]
	if n.Kind != NoticeSharedSkillNamespace || n.Namespace != "/home/u/.agents/skills" {
		t.Fatalf("notice = %+v, want shared-skill-namespace at /home/u/.agents/skills", n)
	}
	if len(n.Selected) != 1 || n.Selected[0] != core.AgentCopilotCLI {
		t.Errorf("notice.Selected = %v, want [copilot-cli]", n.Selected)
	}
	wantReaches := []core.AgentID{core.AgentCodexCLI, core.AgentCrush, core.AgentOpenCode, core.AgentPiDev}
	if !reflect.DeepEqual(n.AlsoReaches, wantReaches) {
		t.Errorf("notice.AlsoReaches = %v, want %v", n.AlsoReaches, wantReaches)
	}
}

func TestProjectNoticesCarrySelectionIdentity(t *testing.T) {
	t.Parallel()
	// Two copilot-only skill selections from the same source into the same shared
	// namespace must produce two distinct, individually attributable notices, not
	// two notices distinguishable only by slice position.
	twoSkill := Source{
		Name: "team", Root: "/src/team",
		Plugins: []core.Plugin{{
			Name: "coding",
			Components: []core.Component{
				{Kind: core.KindSkill, Name: "go-style"},
				{Kind: core.KindSkill, Name: "deploy"},
			},
		}},
	}
	cfg := cfgWith(
		core.Selection{SourceName: "team", PluginName: "coding", ComponentID: "skill/go-style", Agents: []core.AgentID{core.AgentCopilotCLI}},
		core.Selection{SourceName: "team", PluginName: "coding", ComponentID: "skill/deploy", Agents: []core.AgentID{core.AgentCopilotCLI}},
	)
	res := Project(cfg, []Source{twoSkill}, agent.Default(), roots())
	if len(res.Notices) != 2 {
		t.Fatalf("want 2 notices, got %+v", res.Notices)
	}
	// Sorted by ComponentID tie-breaker: deploy before go-style.
	if res.Notices[0].ComponentID != "skill/deploy" || res.Notices[1].ComponentID != "skill/go-style" {
		t.Fatalf("notices not attributable by selection: %+v", res.Notices)
	}
	for _, n := range res.Notices {
		if n.PluginName != "coding" {
			t.Errorf("notice missing plugin identity: %+v", n)
		}
	}
}

func TestProjectWholePoolNoNotice(t *testing.T) {
	t.Parallel()
	// Naming the whole shared pool is not a surprise: no notice.
	cfg := cfgWith(core.Selection{
		SourceName: "team", PluginName: core.Wildcard, ComponentID: "skill/go-style",
		Agents: []core.AgentID{core.AgentCodexCLI, core.AgentCopilotCLI, core.AgentCrush, core.AgentOpenCode, core.AgentPiDev},
	})
	res := Project(cfg, []Source{codingSource()}, agent.Default(), roots())
	if len(res.Notices) != 0 {
		t.Fatalf("naming the whole pool must not notice, got %+v", res.Notices)
	}
}

func TestProjectIsolatedAgentNoNotice(t *testing.T) {
	t.Parallel()
	// claude-code's skills dir is isolated, so a claude-only skill selection has
	// no shared-namespace surprise.
	cfg := cfgWith(core.Selection{
		SourceName: "team", PluginName: core.Wildcard, ComponentID: "skill/go-style",
		Agents: []core.AgentID{core.AgentClaudeCode},
	})
	res := Project(cfg, []Source{codingSource()}, agent.Default(), roots())
	if len(res.Notices) != 0 {
		t.Fatalf("isolated agent must not notice, got %+v", res.Notices)
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
