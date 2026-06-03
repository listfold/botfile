package world

import (
	"testing"

	"codeberg.org/botfile/botfile/internal/fsport"
	"codeberg.org/botfile/botfile/internal/reconcile"
)

func TestReadClassifiesTargetsAndFindsOrphans(t *testing.T) {
	t.Parallel()
	fsys := fsport.NewMem()
	dir := "/home/u/.claude/skills"
	if err := fsys.MkdirAll(dir); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// An existing managed symlink not in the desired set: an orphan candidate.
	if err := fsys.Symlink("/src/team/coding/skills/orphan", dir+"/orphan"); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	// A user-owned file living in the managed namespace.
	fsys.AddFile(dir + "/userskill")

	desired := []string{dir + "/want", dir + "/userskill"} // want absent; userskill foreign
	w, err := Read(fsys, desired, []string{dir}, nil)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if got := w.Entries[dir+"/want"]; got.Kind != reconcile.Absent {
		t.Errorf("want target = %+v, want Absent", got)
	}
	if got := w.Entries[dir+"/userskill"]; got.Kind != reconcile.Foreign {
		t.Errorf("userskill = %+v, want Foreign", got)
	}
	orphan := w.Entries[dir+"/orphan"]
	if orphan.Kind != reconcile.Symlink || orphan.Dest != "/src/team/coding/skills/orphan" {
		t.Errorf("orphan = %+v, want managed Symlink", orphan)
	}
	// The user file is not recorded as an orphan candidate (only the explicit
	// desired-target Lstat recorded it, as Foreign, which it should not double).
	if len(w.Entries) != 3 {
		t.Fatalf("entries = %v, want exactly want/userskill/orphan", w.Entries)
	}
}

func TestReadManagedFileObservesOnlyTheSingleton(t *testing.T) {
	t.Parallel()
	// A singleton lives in a directory full of unrelated user files and symlinks.
	// The world reader observes only the exact managed file, never scanning the
	// parent, so a sibling symlink is not picked up as an orphan candidate.
	fsys := fsport.NewMem()
	dir := "/home/u/.codex"
	if err := fsys.MkdirAll(dir); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// The managed singleton: a botfile symlink no longer desired (an orphan).
	if err := fsys.Symlink("/src/team/coding/instructions/global", dir+"/AGENTS.md"); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	// An unrelated symlink sitting in the same directory: must NOT be observed.
	if err := fsys.Symlink("/src/team/whatever", dir+"/sibling-link"); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	fsys.AddFile(dir + "/history.md") // an unrelated user file

	w, err := Read(fsys, nil, nil, []string{dir + "/AGENTS.md"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := w.Entries[dir+"/AGENTS.md"]; got.Kind != reconcile.Symlink {
		t.Errorf("AGENTS.md = %+v, want managed Symlink", got)
	}
	if len(w.Entries) != 1 {
		t.Fatalf("entries = %v, want only AGENTS.md (the sibling link must be untouched)", w.Entries)
	}
}

func TestReadManagedFileIgnoresForeignSingleton(t *testing.T) {
	t.Parallel()
	// A user-authored file at the singleton path is not a botfile symlink, so it is
	// not recorded as an orphan candidate (the planner reports a conflict only if it
	// is also a desired target).
	fsys := fsport.NewMem()
	dir := "/home/u/.codex"
	if err := fsys.MkdirAll(dir); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	fsys.AddFile(dir + "/AGENTS.md")

	w, err := Read(fsys, nil, nil, []string{dir + "/AGENTS.md"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(w.Entries) != 0 {
		t.Fatalf("entries = %v, want empty (a foreign singleton is not an orphan)", w.Entries)
	}
}

func TestReadSkipsMissingManagedDir(t *testing.T) {
	t.Parallel()
	fsys := fsport.NewMem()
	w, err := Read(fsys, nil, []string{"/no/such/dir"}, []string{"/no/such/file.md"})
	if err != nil {
		t.Fatalf("Read must skip a missing managed dir, got %v", err)
	}
	if len(w.Entries) != 0 {
		t.Fatalf("entries = %v, want empty", w.Entries)
	}
}

// TestReadFeedsReconcileOrphanRemoval shows the world reader and planner compose:
// an orphan symlink in a managed dir, with nothing desired, plans a removal.
func TestReadFeedsReconcileOrphanRemoval(t *testing.T) {
	t.Parallel()
	fsys := fsport.NewMem()
	dir := "/home/u/.agents/skills"
	if err := fsys.MkdirAll(dir); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := fsys.Symlink("/src/team/coding/skills/old", dir+"/old"); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	w, err := Read(fsys, nil, []string{dir}, nil)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	plan := reconcile.Reconcile(nil, w, reconcile.Options{
		Roots: []reconcile.Root{{Name: "team", Path: "/src/team"}},
	})
	if len(plan.Ops) != 1 || plan.Ops[0].Kind != reconcile.OpRemove || plan.Ops[0].Target != dir+"/old" {
		t.Fatalf("plan = %+v, want one OpRemove of the orphan", plan.Ops)
	}
}
