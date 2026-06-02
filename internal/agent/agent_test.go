package agent

import (
	"testing"

	"codeberg.org/botfile/botfile/internal/core"
)

func noEnv(string) string { return "" }

func TestDefaultClaudeCodeTargets(t *testing.T) {
	t.Parallel()
	ag, ok := Default().Lookup(core.AgentClaudeCode)
	if !ok {
		t.Fatal("claude-code must be in the default matrix")
	}
	root := ag.Root("/home/u", noEnv)
	if root != "/home/u/.claude" {
		t.Fatalf("root = %q, want /home/u/.claude", root)
	}

	skill, ok := ag.Target(root, core.KindSkill, "go-style")
	if !ok || skill != "/home/u/.claude/skills/go-style" {
		t.Errorf("skill target = %q,%v, want /home/u/.claude/skills/go-style", skill, ok)
	}
	mem, ok := ag.Target(root, core.KindMemory, "style")
	if !ok || mem != "/home/u/.claude/rules/style.md" {
		t.Errorf("memory target = %q,%v, want /home/u/.claude/rules/style.md", mem, ok)
	}
}

func TestRootHonorsEnvOverride(t *testing.T) {
	t.Parallel()
	ag, _ := Default().Lookup(core.AgentClaudeCode)
	getenv := func(k string) string {
		if k == "CLAUDE_CONFIG_DIR" {
			return "/custom/claude"
		}
		return ""
	}
	root := ag.Root("/home/u", getenv)
	if root != "/custom/claude" {
		t.Fatalf("root = %q, want /custom/claude (CLAUDE_CONFIG_DIR)", root)
	}
	skill, _ := ag.Target(root, core.KindSkill, "go-style")
	if skill != "/custom/claude/skills/go-style" {
		t.Errorf("skill target = %q, want under the override root", skill)
	}
}

func TestResolveRoots(t *testing.T) {
	t.Parallel()
	roots := Default().ResolveRoots("/home/u", noEnv)
	if roots[core.AgentClaudeCode] != "/home/u/.claude" {
		t.Errorf("resolved root = %q, want /home/u/.claude", roots[core.AgentClaudeCode])
	}
}

func TestSupportsAndUnsupported(t *testing.T) {
	t.Parallel()
	ag, _ := Default().Lookup(core.AgentClaudeCode)
	if !ag.Supports(core.KindSkill) || !ag.Supports(core.KindMemory) {
		t.Error("claude-code should support skill and memory")
	}
	if _, ok := ag.Target("/root", core.Kind("hook"), "x"); ok {
		t.Error("an unspecified kind must report unsupported")
	}
}

func TestCodexAndCopilotSkillsOnly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id       core.AgentID
		wantRoot string
	}{
		// Under shared-first, codex and copilot both install to the cross-agent
		// ~/.agents root (copilot also reads ~/.copilot, but we prefer the shared one).
		{core.AgentCodexCLI, "/home/u/.agents"},
		{core.AgentCopilotCLI, "/home/u/.agents"},
	}
	for _, tc := range cases {
		ag, ok := Default().Lookup(tc.id)
		if !ok {
			t.Fatalf("%s must be in the default matrix", tc.id)
		}
		root := ag.Root("/home/u", noEnv)
		if root != tc.wantRoot {
			t.Errorf("%s root = %q, want %q", tc.id, root, tc.wantRoot)
		}
		skill, ok := ag.Target(root, core.KindSkill, "deploy")
		if !ok || skill != tc.wantRoot+"/skills/deploy" {
			t.Errorf("%s skill target = %q,%v, want %s/skills/deploy", tc.id, skill, ok, tc.wantRoot)
		}
		// Memory stays unsupported for codex and copilot (manifesto 18).
		if ag.Supports(core.KindMemory) {
			t.Errorf("%s must not support memory (manifesto 18)", tc.id)
		}
	}
}

func TestCodexHomeDoesNotMoveSkills(t *testing.T) {
	t.Parallel()
	// CODEX_HOME relocates ~/.codex state but not skill discovery (which is under
	// ~/.agents), so it must not change the resolved root.
	ag, _ := Default().Lookup(core.AgentCodexCLI)
	getenv := func(k string) string {
		if k == "CODEX_HOME" {
			return "/custom/codex"
		}
		return ""
	}
	if root := ag.Root("/home/u", getenv); root != "/home/u/.agents" {
		t.Fatalf("root = %q, want /home/u/.agents (CODEX_HOME must not move skills)", root)
	}
}

func TestStillAbsentAgents(t *testing.T) {
	t.Parallel()
	// Agents without a confirmed vendor spec must stay out of the matrix, so a
	// selection targeting them is reported unsupported rather than guessed.
	for _, id := range []core.AgentID{core.AgentOpenCode, core.AgentCopilotVSCode, core.AgentPiDev} {
		if _, ok := Default().Lookup(id); ok {
			t.Errorf("%s has no confirmed vendor spec and must not be in the default matrix", id)
		}
	}
}

func TestNewAgentValidation(t *testing.T) {
	t.Parallel()
	good := Spec{
		ID:   core.AgentClaudeCode,
		Base: Base{HomeRelative: []string{".claude"}},
		Rules: map[core.Kind]InstallRule{
			core.KindSkill: {Tier: Tier1, Segments: []string{"skills"}, Shape: LeafDir},
		},
	}
	if _, err := NewAgent(good); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}

	tests := []struct {
		name string
		mut  func(s *Spec)
	}{
		{"unknown agent", func(s *Spec) { s.ID = "acme-bot" }},
		{"empty base", func(s *Spec) { s.Base.HomeRelative = nil }},
		{"no rules", func(s *Spec) { s.Rules = map[core.Kind]InstallRule{} }},
		{"unknown kind", func(s *Spec) {
			s.Rules = map[core.Kind]InstallRule{"hook": {Tier: Tier1, Segments: []string{"hooks"}, Shape: LeafDir}}
		}},
		{"empty segments", func(s *Spec) {
			s.Rules = map[core.Kind]InstallRule{core.KindSkill: {Tier: Tier1, Segments: nil, Shape: LeafDir}}
		}},
		{"dir with ext", func(s *Spec) {
			s.Rules = map[core.Kind]InstallRule{core.KindSkill: {Tier: Tier1, Segments: []string{"skills"}, Shape: LeafDir, Ext: ".md"}}
		}},
		{"file without ext", func(s *Spec) {
			s.Rules = map[core.Kind]InstallRule{core.KindMemory: {Tier: Tier1, Segments: []string{"rules"}, Shape: LeafFile}}
		}},
		{"bad tier", func(s *Spec) {
			s.Rules = map[core.Kind]InstallRule{core.KindSkill: {Tier: 9, Segments: []string{"skills"}, Shape: LeafDir}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := good
			tt.mut(&s)
			if _, err := NewAgent(s); err == nil {
				t.Errorf("%s: want error, got nil", tt.name)
			}
		})
	}
}

func TestNewSetRejectsDuplicate(t *testing.T) {
	t.Parallel()
	spec := Spec{
		ID:   core.AgentClaudeCode,
		Base: Base{HomeRelative: []string{".claude"}},
		Rules: map[core.Kind]InstallRule{
			core.KindSkill: {Tier: Tier1, Segments: []string{"skills"}, Shape: LeafDir},
		},
	}
	if _, err := NewSet(spec, spec); err == nil {
		t.Error("duplicate agent id: want error, got nil")
	}
}
