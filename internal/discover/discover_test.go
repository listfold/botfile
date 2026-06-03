package discover

import (
	"testing"

	"codeberg.org/botfile/botfile/internal/core"
	"codeberg.org/botfile/botfile/internal/fsport"
)

func TestFindFixedFileConsidersOnlyTheSingleton(t *testing.T) {
	t.Parallel()
	// A fixed-file namespace (a singleton like ~/.codex/AGENTS.md) must consider
	// only that one entry, never the rest of its directory, which holds unrelated
	// user files botfile must not report as adoptable (manifesto 33).
	fsys := fsport.NewMem()
	dir := "/home/u/.codex"
	if err := fsys.MkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	fsys.AddFile(dir + "/AGENTS.md") // the agent-authored singleton: adoptable
	fsys.AddFile(dir + "/notes.md")  // an unrelated user file: must be ignored
	fsys.AddFile(dir + "/config.toml")

	got, err := Find(fsys, []Namespace{
		{Agents: []core.AgentID{core.AgentCodexCLI}, Kind: core.KindInstruction, Dir: dir, File: "AGENTS.md"},
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 1 || got[0].Path != dir+"/AGENTS.md" || got[0].Ref() != "instruction/AGENTS" {
		t.Fatalf("found = %+v, want only instruction/AGENTS at the singleton path", got)
	}
}

func TestFindFixedFileSkipsForeignOrMissing(t *testing.T) {
	t.Parallel()
	// A managed (symlink) singleton is not adoptable; a missing one yields nothing.
	fsys := fsport.NewMem()
	dir := "/home/u/.codex"
	if err := fsys.MkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := fsys.Symlink("/src/team/p/instructions/global", dir+"/AGENTS.md"); err != nil {
		t.Fatal(err)
	}
	ns := []Namespace{{Agents: []core.AgentID{core.AgentCodexCLI}, Kind: core.KindInstruction, Dir: dir, File: "AGENTS.md"}}
	got, err := Find(fsys, ns)
	if err != nil || len(got) != 0 {
		t.Fatalf("a symlinked singleton must not be adoptable: got %+v, err %v", got, err)
	}
	// And a missing one (no AGENTS.md) is simply skipped.
	got, err = Find(fsport.NewMem(), ns)
	if err != nil || len(got) != 0 {
		t.Fatalf("a missing singleton must yield nothing: got %+v, err %v", got, err)
	}
}

func TestFindUnmanagedSkillsAndInstructions(t *testing.T) {
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
	// A real, agent-created instruction file.
	fsys.AddFile(rules + "/preferences.md")
	// A botfile-managed instruction: a symlink. Not adoptable.
	if err := fsys.Symlink("/src/team/p/instructions/coding.md", rules+"/coding.md"); err != nil {
		t.Fatal(err)
	}
	// A non-component file in the instruction namespace (no .md): ignored.
	fsys.AddFile(rules + "/notes.txt")

	got, err := Find(fsys, []Namespace{
		{Agents: []core.AgentID{core.AgentClaudeCode}, Kind: core.KindSkill, Dir: skills},
		{Agents: []core.AgentID{core.AgentClaudeCode}, Kind: core.KindInstruction, Dir: rules},
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}

	want := []string{"instruction/preferences", "skill/bark-pro"} // sorted by path: rules/ before skills/
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
	// An instruction namespace entry named like an instruction but a special file.
	fsys.AddSpecial(rules + "/special.md")

	got, err := Find(fsys, []Namespace{
		{Agents: []core.AgentID{core.AgentClaudeCode}, Kind: core.KindSkill, Dir: skills},
		{Agents: []core.AgentID{core.AgentClaudeCode}, Kind: core.KindInstruction, Dir: rules},
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
