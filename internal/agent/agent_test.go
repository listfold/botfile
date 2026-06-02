package agent

import (
	"testing"

	"codeberg.org/botfile/botfile/internal/core"
)

func TestDefaultClaudeCodeTargets(t *testing.T) {
	t.Parallel()
	ag, ok := Default().Lookup(core.AgentClaudeCode)
	if !ok {
		t.Fatal("claude-code must be in the default matrix")
	}

	skill, ok := ag.Target("/home/u", core.KindSkill, "go-style")
	if !ok || skill != "/home/u/.claude/skills/go-style" {
		t.Errorf("skill target = %q,%v, want /home/u/.claude/skills/go-style", skill, ok)
	}
	mem, ok := ag.Target("/home/u", core.KindMemory, "style")
	if !ok || mem != "/home/u/.claude/rules/style.md" {
		t.Errorf("memory target = %q,%v, want /home/u/.claude/rules/style.md", mem, ok)
	}
}

func TestSupportsAndUnsupported(t *testing.T) {
	t.Parallel()
	ag, _ := Default().Lookup(core.AgentClaudeCode)
	if !ag.Supports(core.KindSkill) || !ag.Supports(core.KindMemory) {
		t.Error("claude-code should support skill and memory")
	}
	if _, ok := ag.Target("/home/u", core.Kind("hook"), "x"); ok {
		t.Error("an unspecified kind must report unsupported")
	}
}

func TestLookupUnknownAgent(t *testing.T) {
	t.Parallel()
	if _, ok := Default().Lookup(core.AgentCodexCLI); ok {
		t.Error("codex-cli has no vendor spec yet and must not be in the default matrix")
	}
}
