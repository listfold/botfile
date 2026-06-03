package discover

import (
	"testing"

	"codeberg.org/botfile/botfile/internal/core"
	"codeberg.org/botfile/botfile/internal/fsport"
)

func TestFindUnmanagedSkillsAndMemories(t *testing.T) {
	t.Parallel()
	fsys := fsport.NewMem()
	skills := "/home/u/.claude/skills"
	rules := "/home/u/.claude/rules"
	if err := fsys.MkdirAll(skills); err != nil {
		t.Fatal(err)
	}
	if err := fsys.MkdirAll(rules); err != nil {
		t.Fatal(err)
	}

	// A real, agent-created skill: a directory with a SKILL.md file.
	mustMkdir(t, fsys, skills+"/bark-pro")
	fsys.AddFile(skills + "/bark-pro/SKILL.md")
	// A botfile-managed skill: a symlink into a source. Not adoptable.
	if err := fsys.Symlink("/src/team/p/skills/go-style", skills+"/go-style"); err != nil {
		t.Fatal(err)
	}
	// A skill-shaped dir missing its manifest: not adoptable.
	mustMkdir(t, fsys, skills+"/incomplete")
	// A real, agent-created memory file.
	fsys.AddFile(rules + "/preferences.md")
	// A botfile-managed memory: a symlink. Not adoptable.
	if err := fsys.Symlink("/src/team/p/memories/coding.md", rules+"/coding.md"); err != nil {
		t.Fatal(err)
	}
	// A non-component file in the memory namespace (no .md): ignored.
	fsys.AddFile(rules + "/notes.txt")

	got, err := Find(fsys, []Namespace{
		{Agents: []core.AgentID{core.AgentClaudeCode}, Kind: core.KindSkill, Dir: skills},
		{Agents: []core.AgentID{core.AgentClaudeCode}, Kind: core.KindMemory, Dir: rules},
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}

	want := []string{"memory/preferences", "skill/bark-pro"} // sorted by path: rules/ before skills/
	if len(got) != len(want) {
		t.Fatalf("found %d, want %d: %+v", len(got), len(want), got)
	}
	for i, u := range got {
		if u.Ref() != want[i] {
			t.Errorf("found[%d] = %s, want %s", i, u.Ref(), want[i])
		}
		if len(u.Agents) != 1 || u.Agents[0] != core.AgentClaudeCode {
			t.Errorf("found[%d] agents = %v, want [claude-code]", i, u.Agents)
		}
	}
}

func TestFindRejectsNonRegularFiles(t *testing.T) {
	t.Parallel()
	fsys := fsport.NewMem()
	skills := "/home/u/.claude/skills"
	rules := "/home/u/.claude/rules"
	mustMkdir(t, fsys, skills)
	mustMkdir(t, fsys, rules)

	// A skill directory whose SKILL.md is a special file (FIFO), not a regular
	// file: not a valid component.
	mustMkdir(t, fsys, skills+"/fifo-skill")
	fsys.AddSpecial(skills + "/fifo-skill/SKILL.md")
	// A memory namespace entry named like a memory but a special file.
	fsys.AddSpecial(rules + "/special.md")

	got, err := Find(fsys, []Namespace{
		{Agents: []core.AgentID{core.AgentClaudeCode}, Kind: core.KindSkill, Dir: skills},
		{Agents: []core.AgentID{core.AgentClaudeCode}, Kind: core.KindMemory, Dir: rules},
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("non-regular SKILL.md / .md must not be adoptable, got %+v", got)
	}
}

func TestFindSkipsMissingNamespace(t *testing.T) {
	t.Parallel()
	fsys := fsport.NewMem()
	got, err := Find(fsys, []Namespace{{Agents: []core.AgentID{core.AgentClaudeCode}, Kind: core.KindSkill, Dir: "/nope"}})
	if err != nil {
		t.Fatalf("a missing namespace must be skipped, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want none, got %+v", got)
	}
}

func mustMkdir(t *testing.T, fsys *fsport.Mem, dir string) {
	t.Helper()
	if err := fsys.MkdirAll(dir); err != nil {
		t.Fatal(err)
	}
}
