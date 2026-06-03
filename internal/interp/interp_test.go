package interp

import (
	"os"
	"path/filepath"
	"testing"

	"codeberg.org/botfile/botfile/internal/agent"
	"codeberg.org/botfile/botfile/internal/fsport"
	"codeberg.org/botfile/botfile/internal/runtime"
)

// writeFile creates parent dirs and writes a file.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setup builds a real source tree, a config, and a home under tmp, and returns
// the seeded model + first cmd plus the resolved home.
func setup(t *testing.T, mode runtime.Mode) (runtime.Model, runtime.Cmd, string) {
	t.Helper()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src", "team")
	home := filepath.Join(tmp, "home")

	// A source: one skill (directory + SKILL.md) and one memory (a .md file).
	writeFile(t, filepath.Join(src, "coding", "skills", "go-style", "SKILL.md"), "# go style")
	writeFile(t, filepath.Join(src, "coding", "memories", "style.md"), "be terse")

	configPath := filepath.Join(tmp, "config.toml")
	writeFile(t, configPath, ""+
		"[[sources]]\n"+
		"name = \"team\"\n"+
		"location = \""+src+"\"\n\n"+
		"[[selections]]\n"+
		"source = \"team\"\n"+
		"agents = [\"claude-code\"]\n")

	agents := agent.Default()
	roots := agents.ResolveRoots(home, func(string) string { return "" })
	model, cmd := runtime.Init(mode, configPath, home, agents, roots)
	return model, cmd, home
}

func TestEndToEndSyncCreatesSymlinks(t *testing.T) {
	model, cmd, home := setup(t, runtime.ModeSync)

	final := OSDeps().Run(model, cmd)
	if final.Phase != runtime.PhaseDone {
		t.Fatalf("phase = %v (err %v, stage %q), want Done", final.Phase, final.Err, final.FailedStage)
	}

	assertSymlink(t, filepath.Join(home, ".claude", "skills", "go-style"))
	assertSymlink(t, filepath.Join(home, ".claude", "rules", "style.md"))
}

func TestEndToEndSyncIsIdempotent(t *testing.T) {
	model, cmd, _ := setup(t, runtime.ModeSync)
	final := OSDeps().Run(model, cmd)
	if final.Phase != runtime.PhaseDone {
		t.Fatalf("first sync: phase %v err %v", final.Phase, final.Err)
	}
	if len(final.Plan.Ops) != 2 {
		t.Fatalf("first sync ops = %d, want 2 creates", len(final.Plan.Ops))
	}

	// Re-run against the now-populated home: the world already matches, so the
	// second plan is empty.
	model2, cmd2, _ := reinit(t, final)
	final2 := OSDeps().Run(model2, cmd2)
	if final2.Phase != runtime.PhaseDone {
		t.Fatalf("second sync: phase %v err %v", final2.Phase, final2.Err)
	}
	if len(final2.Plan.Ops) != 0 {
		t.Fatalf("second sync ops = %+v, want none (idempotent)", final2.Plan.Ops)
	}
}

func TestEndToEndPlanTouchesNothing(t *testing.T) {
	model, cmd, home := setup(t, runtime.ModePlan)
	final := OSDeps().Run(model, cmd)
	if final.Phase != runtime.PhaseDone {
		t.Fatalf("plan: phase %v err %v", final.Phase, final.Err)
	}
	if len(final.Plan.Ops) != 2 {
		t.Fatalf("plan ops = %d, want 2", len(final.Plan.Ops))
	}
	// Plan is read-only: nothing was created.
	if e, _ := (fsport.OS{}).Lstat(filepath.Join(home, ".claude", "skills", "go-style")); e.Exists {
		t.Fatal("plan must not create anything on disk")
	}
}

// reinit rebuilds the initial model+cmd reusing the prior run's seeded inputs, so
// a second sync runs against the filesystem the first one produced.
func reinit(t *testing.T, prev runtime.Model) (runtime.Model, runtime.Cmd, string) {
	t.Helper()
	model, cmd := runtime.Init(prev.Mode, prev.ConfigPath, prev.Home, prev.Agents, prev.Roots)
	return model, cmd, prev.Home
}

func assertSymlink(t *testing.T, path string) {
	t.Helper()
	e, err := (fsport.OS{}).Lstat(path)
	if err != nil {
		t.Fatalf("Lstat %s: %v", path, err)
	}
	if !e.IsSymlink {
		t.Fatalf("%s is not a symlink: %+v", path, e)
	}
}
