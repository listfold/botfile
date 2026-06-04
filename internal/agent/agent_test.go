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
	instr, ok := ag.Target(root, core.KindInstruction, "style")
	if !ok || instr != "/home/u/.claude/rules/style.md" {
		t.Errorf("instruction target = %q,%v, want /home/u/.claude/rules/style.md", instr, ok)
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
	if root, ok := roots.For(core.AgentClaudeCode, core.KindSkill); !ok || root != "/home/u/.claude" {
		t.Errorf("resolved skill root = %q,%v, want /home/u/.claude", root, ok)
	}
	if root, ok := roots.For(core.AgentClaudeCode, core.KindInstruction); !ok || root != "/home/u/.claude" {
		t.Errorf("resolved instruction root = %q,%v, want /home/u/.claude", root, ok)
	}
}

func TestResolveRootsPerKindOverride(t *testing.T) {
	t.Parallel()
	// A rule's Base override resolves that kind under a different root than the
	// agent's default, and honors the override's own env variable.
	set, err := NewSet(Spec{
		ID:   core.AgentCodexCLI,
		Base: Base{HomeRelative: []string{".agents"}},
		Rules: map[core.Kind]InstallRule{
			core.KindSkill: {Tier: Tier1, Segments: []string{"skills"}, Shape: LeafDir},
			core.KindInstruction: {
				Tier: Tier1, Base: &Base{HomeRelative: []string{".codex"}, EnvOverride: "CODEX_HOME"},
				Segments: []string{"."}, Shape: LeafFile, Ext: ".md",
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSet: %v", err)
	}
	roots := set.ResolveRoots("/home/u", noEnv)
	if root, _ := roots.For(core.AgentCodexCLI, core.KindSkill); root != "/home/u/.agents" {
		t.Errorf("skill root = %q, want /home/u/.agents (agent default)", root)
	}
	if root, _ := roots.For(core.AgentCodexCLI, core.KindInstruction); root != "/home/u/.codex" {
		t.Errorf("instruction root = %q, want /home/u/.codex (per-kind override)", root)
	}
	// The override's own env variable wins when set.
	withEnv := set.ResolveRoots("/home/u", func(k string) string {
		if k == "CODEX_HOME" {
			return "/custom/codex"
		}
		return ""
	})
	if root, _ := withEnv.For(core.AgentCodexCLI, core.KindInstruction); root != "/custom/codex" {
		t.Errorf("instruction root with CODEX_HOME = %q, want /custom/codex", root)
	}
	// ...and it does not move the skill root, which has no override.
	if root, _ := withEnv.For(core.AgentCodexCLI, core.KindSkill); root != "/home/u/.agents" {
		t.Errorf("skill root with CODEX_HOME = %q, want /home/u/.agents (unmoved)", root)
	}
}

func TestSupportsAndUnsupported(t *testing.T) {
	t.Parallel()
	ag, _ := Default().Lookup(core.AgentClaudeCode)
	if !ag.Supports(core.KindSkill) || !ag.Supports(core.KindInstruction) {
		t.Error("claude-code should support skill and instruction")
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
	}
}

func TestSingletonInstructionTargets(t *testing.T) {
	t.Parallel()
	// The singleton-file agents support instructions as one fixed file under their
	// own root (distinct from their skills root, slice 2). The component name is
	// ignored: every instruction maps to the agent's one file.
	roots := Default().ResolveRoots("/home/u", noEnv)
	cases := []struct {
		id       core.AgentID
		wantFile string
	}{
		{core.AgentCodexCLI, "/home/u/.codex/AGENTS.md"},
		{core.AgentOpenCode, "/home/u/.config/opencode/AGENTS.md"},
		{core.AgentPiDev, "/home/u/.pi/agent/AGENTS.md"},
		{core.AgentCopilotCLI, "/home/u/.copilot/copilot-instructions.md"},
		{core.AgentCrush, "/home/u/.config/crush/CRUSH.md"},
	}
	for _, tc := range cases {
		ag, _ := Default().Lookup(tc.id)
		root, ok := roots.For(tc.id, core.KindInstruction)
		if !ok {
			t.Errorf("%s: no resolved instruction root", tc.id)
			continue
		}
		// Two different component names both resolve to the one fixed file.
		for _, name := range []string{"go-style", "preferences"} {
			got, ok := ag.Target(root, core.KindInstruction, name)
			if !ok || got != tc.wantFile {
				t.Errorf("%s instruction target for %q = %q,%v, want %s", tc.id, name, got, ok, tc.wantFile)
			}
		}
	}
}

func TestSingletonInstructionRootHonorsEnv(t *testing.T) {
	t.Parallel()
	// CODEX_HOME / COPILOT_HOME move the singleton instruction root (which is the
	// agent's state dir), not the cross-agent skills root.
	getenv := func(k string) string {
		switch k {
		case "CODEX_HOME":
			return "/custom/codex"
		case "COPILOT_HOME":
			return "/custom/copilot"
		}
		return ""
	}
	roots := Default().ResolveRoots("/home/u", getenv)
	codex, _ := Default().Lookup(core.AgentCodexCLI)
	cRoot, _ := roots.For(core.AgentCodexCLI, core.KindInstruction)
	if got, _ := codex.Target(cRoot, core.KindInstruction, "x"); got != "/custom/codex/AGENTS.md" {
		t.Errorf("codex instruction with CODEX_HOME = %q, want /custom/codex/AGENTS.md", got)
	}
	if root, _ := roots.For(core.AgentCodexCLI, core.KindSkill); root != "/home/u/.agents" {
		t.Errorf("codex skill root with CODEX_HOME = %q, want /home/u/.agents (unmoved)", root)
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
	for _, id := range []core.AgentID{core.AgentCopilotVSCode} {
		if _, ok := Default().Lookup(id); ok {
			t.Errorf("%s has no confirmed vendor spec and must not be in the default matrix", id)
		}
	}
}

func TestSharedSkillsPool(t *testing.T) {
	t.Parallel()
	// The agents that read the cross-agent ~/.agents/skills drop-in must all
	// resolve skills to the same directory, so the projection sees one shared pool
	// (one symlink reaches every reader). claude-code stays isolated.
	roots := Default().ResolveRoots("/home/u", noEnv)
	for _, id := range []core.AgentID{core.AgentCodexCLI, core.AgentCopilotCLI, core.AgentCrush, core.AgentOpenCode, core.AgentPiDev} {
		ag, ok := Default().Lookup(id)
		if !ok {
			t.Fatalf("%s missing from the default matrix", id)
		}
		root, _ := roots.For(id, core.KindSkill)
		dir, ok := ag.Namespace(root, core.KindSkill)
		if !ok || dir != "/home/u/.agents/skills" {
			t.Errorf("%s skill namespace = %q (ok %v), want /home/u/.agents/skills", id, dir, ok)
		}
	}
	// claude-code reads only its own dir, so it is not in the shared pool.
	cc, _ := Default().Lookup(core.AgentClaudeCode)
	ccRoot, _ := roots.For(core.AgentClaudeCode, core.KindSkill)
	if dir, _ := cc.Namespace(ccRoot, core.KindSkill); dir != "/home/u/.claude/skills" {
		t.Errorf("claude-code skill namespace = %q, want /home/u/.claude/skills (isolated)", dir)
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
			s.Rules = map[core.Kind]InstallRule{core.KindInstruction: {Tier: Tier1, Segments: []string{"rules"}, Shape: LeafFile}}
		}},
		{"fixed without filename", func(s *Spec) {
			s.Rules = map[core.Kind]InstallRule{core.KindInstruction: {Tier: Tier1, Shape: LeafFixed}}
		}},
		{"fixed with ext", func(s *Spec) {
			s.Rules = map[core.Kind]InstallRule{core.KindInstruction: {Tier: Tier1, Shape: LeafFixed, Filename: "AGENTS.md", Ext: ".md"}}
		}},
		{"file with filename", func(s *Spec) {
			s.Rules = map[core.Kind]InstallRule{core.KindInstruction: {Tier: Tier1, Segments: []string{"rules"}, Shape: LeafFile, Ext: ".md", Filename: "AGENTS.md"}}
		}},
		{"base override empty", func(s *Spec) {
			s.Rules = map[core.Kind]InstallRule{core.KindSkill: {Tier: Tier1, Base: &Base{}, Segments: []string{"skills"}, Shape: LeafDir}}
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
