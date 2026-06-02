package apply

import (
	"testing"

	"codeberg.org/botfile/botfile/internal/fsport"
	"codeberg.org/botfile/botfile/internal/reconcile"
)

func mustAbsent(t *testing.T, fsys fsport.FS, path string) {
	t.Helper()
	e, err := fsys.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat %s: %v", path, err)
	}
	if e.Exists {
		t.Fatalf("%s exists, want absent: %+v", path, e)
	}
}

func mustSymlink(t *testing.T, fsys fsport.FS, path, dest string) {
	t.Helper()
	e, err := fsys.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat %s: %v", path, err)
	}
	if !e.IsSymlink || e.Dest != dest {
		t.Fatalf("%s = %+v, want symlink to %s", path, e, dest)
	}
}

func TestApplyCreate(t *testing.T) {
	t.Parallel()
	fsys := fsport.NewMem()
	ops := []reconcile.Op{
		{Kind: reconcile.OpCreate, Target: "/home/u/.claude/skills/go", Dest: "/src/team/coding/skills/go"},
	}
	if err := Apply(fsys, ops); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	mustSymlink(t, fsys, "/home/u/.claude/skills/go", "/src/team/coding/skills/go")
}

func TestApplyReplace(t *testing.T) {
	t.Parallel()
	fsys := fsport.NewMem()
	// Existing managed symlink pointing at a stale dest.
	if err := Apply(fsys, []reconcile.Op{{Kind: reconcile.OpCreate, Target: "/t/go", Dest: "/src/old/go"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ops := []reconcile.Op{
		{Kind: reconcile.OpReplace, Target: "/t/go", Dest: "/src/new/go", OldDest: "/src/old/go"},
	}
	if err := Apply(fsys, ops); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	mustSymlink(t, fsys, "/t/go", "/src/new/go")
}

func TestApplyRemove(t *testing.T) {
	t.Parallel()
	fsys := fsport.NewMem()
	if err := Apply(fsys, []reconcile.Op{{Kind: reconcile.OpCreate, Target: "/t/go", Dest: "/src/go"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ops := []reconcile.Op{{Kind: reconcile.OpRemove, Target: "/t/go", OldDest: "/src/go"}}
	if err := Apply(fsys, ops); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	mustAbsent(t, fsys, "/t/go")
}

func TestApplyRemoveAbsentIsNoOp(t *testing.T) {
	t.Parallel()
	fsys := fsport.NewMem()
	ops := []reconcile.Op{{Kind: reconcile.OpRemove, Target: "/t/gone", OldDest: "/src/gone"}}
	if err := Apply(fsys, ops); err != nil {
		t.Fatalf("removing an already-absent target should succeed, got %v", err)
	}
}

func TestApplyRefusesNonSymlink(t *testing.T) {
	t.Parallel()
	fsys := fsport.NewMem()
	fsys.AddFile("/t/userfile") // a file botfile does not own
	ops := []reconcile.Op{{Kind: reconcile.OpRemove, Target: "/t/userfile", OldDest: "/whatever"}}
	if err := Apply(fsys, ops); err == nil {
		t.Fatal("Apply must refuse to remove a non-symlink, got nil error")
	}
	// The user's file is untouched.
	e, _ := fsys.Lstat("/t/userfile")
	if !e.Exists || e.IsSymlink {
		t.Fatalf("user file was altered: %+v", e)
	}
}

func TestApplyReplaceRefusesDrift(t *testing.T) {
	t.Parallel()
	fsys := fsport.NewMem()
	// A managed symlink that now points somewhere other than the plan's OldDest
	// (the world drifted, e.g. the user re-pointed it).
	if err := Apply(fsys, []reconcile.Op{{Kind: reconcile.OpCreate, Target: "/t/go", Dest: "/src/current/go"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ops := []reconcile.Op{
		{Kind: reconcile.OpReplace, Target: "/t/go", Dest: "/src/new/go", OldDest: "/src/STALE/go"},
	}
	if err := Apply(fsys, ops); err == nil {
		t.Fatal("replace must refuse a symlink whose dest differs from the plan's OldDest")
	}
	// Untouched: still the current symlink, not the new one.
	mustSymlink(t, fsys, "/t/go", "/src/current/go")
}

func TestApplyRemoveRefusesDrift(t *testing.T) {
	t.Parallel()
	fsys := fsport.NewMem()
	if err := Apply(fsys, []reconcile.Op{{Kind: reconcile.OpCreate, Target: "/t/go", Dest: "/src/current/go"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ops := []reconcile.Op{{Kind: reconcile.OpRemove, Target: "/t/go", OldDest: "/src/STALE/go"}}
	if err := Apply(fsys, ops); err == nil {
		t.Fatal("remove must refuse a symlink whose dest differs from the plan's OldDest")
	}
	mustSymlink(t, fsys, "/t/go", "/src/current/go")
}

func TestApplyRollsBackOnFailure(t *testing.T) {
	t.Parallel()
	fsys := fsport.NewMem()
	fsys.AddFile("/t/c") // a non-symlink that will make the third op fail
	ops := []reconcile.Op{
		{Kind: reconcile.OpCreate, Target: "/t/a/x", Dest: "/src/a"},
		{Kind: reconcile.OpCreate, Target: "/t/b/y", Dest: "/src/b"},
		{Kind: reconcile.OpReplace, Target: "/t/c", Dest: "/src/c"}, // fails: /t/c is not a symlink
	}
	if err := Apply(fsys, ops); err == nil {
		t.Fatal("Apply should fail on the non-symlink replace")
	}
	// All-or-nothing: the two successful creates were rolled back.
	mustAbsent(t, fsys, "/t/a/x")
	mustAbsent(t, fsys, "/t/b/y")
	// The pre-existing file is untouched.
	if e, _ := fsys.Lstat("/t/c"); !e.Exists || e.IsSymlink {
		t.Fatalf("/t/c was altered by a failed+rolled-back run: %+v", e)
	}
}

func TestApplyIsIdempotentAcrossRuns(t *testing.T) {
	t.Parallel()
	fsys := fsport.NewMem()
	ops := []reconcile.Op{{Kind: reconcile.OpCreate, Target: "/t/go", Dest: "/src/go"}}
	if err := Apply(fsys, ops); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	// Re-applying a create whose target already exists fails (the symlink is
	// there); a real run re-plans first, which would yield a no-op. This asserts
	// the create op itself is not silently destructive.
	if err := Apply(fsys, ops); err == nil {
		t.Fatal("re-creating an existing symlink should error rather than clobber")
	}
	mustSymlink(t, fsys, "/t/go", "/src/go")
}
