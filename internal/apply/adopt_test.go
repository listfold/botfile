package apply

import (
	"errors"
	"testing"

	"codeberg.org/botfile/botfile/internal/fsport"
)

func seedSkill(t *testing.T, fsys *fsport.Mem, dir string) {
	t.Helper()
	if err := fsys.MkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	fsys.AddFile(dir + "/SKILL.md")
}

func TestAdoptMovesLinksAndSelects(t *testing.T) {
	t.Parallel()
	fsys := fsport.NewMem()
	from := "/home/u/.claude/skills/bark"
	to := "/src/personal/mine/skills/bark"
	seedSkill(t, fsys, from)

	called := false
	addSel := func() (func() error, error) { called = true; return func() error { return nil }, nil }

	if err := Adopt(fsys, from, to, addSel); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if !called {
		t.Error("addSelection was not run")
	}
	// from is now a symlink to to.
	if e, _ := fsys.Lstat(from); !e.IsSymlink || e.Dest != to {
		t.Fatalf("from = %+v, want symlink to %s", e, to)
	}
	// the content moved into the source.
	if e, _ := fsys.Lstat(to + "/SKILL.md"); !e.IsRegular {
		t.Fatalf("SKILL.md not moved into the source")
	}
}

func TestAdoptRollsBackOnSelectionFailure(t *testing.T) {
	t.Parallel()
	fsys := fsport.NewMem()
	from := "/home/u/.claude/skills/bark"
	to := "/src/personal/mine/skills/bark"
	seedSkill(t, fsys, from)

	addSel := func() (func() error, error) { return nil, errors.New("config locked") }
	if err := Adopt(fsys, from, to, addSel); err == nil {
		t.Fatal("Adopt should fail when addSelection fails")
	}

	// All-or-nothing: from is the original directory again, to is gone.
	if e, _ := fsys.Lstat(from); e.IsSymlink || !e.IsDir {
		t.Fatalf("from not restored to a directory: %+v", e)
	}
	if e, _ := fsys.Lstat(from + "/SKILL.md"); !e.IsRegular {
		t.Fatalf("SKILL.md not restored under from")
	}
	if e, _ := fsys.Lstat(to); e.Exists {
		t.Fatalf("destination should be gone after rollback: %+v", e)
	}
}

func TestAdoptNilSelectionSkipsConfig(t *testing.T) {
	t.Parallel()
	fsys := fsport.NewMem()
	from := "/home/u/.claude/skills/bark"
	to := "/src/personal/mine/skills/bark"
	seedSkill(t, fsys, from)

	if err := Adopt(fsys, from, to, nil); err != nil {
		t.Fatalf("Adopt with no selection: %v", err)
	}
	if e, _ := fsys.Lstat(from); !e.IsSymlink {
		t.Fatalf("from = %+v, want symlink", e)
	}
}
