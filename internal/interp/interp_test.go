package interp

import (
	"os"
	"path/filepath"
	"testing"

	"codeberg.org/botfile/botfile/internal/adopt"
	"codeberg.org/botfile/botfile/internal/agent"
	"codeberg.org/botfile/botfile/internal/config"
	"codeberg.org/botfile/botfile/internal/core"
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

	// A source: one skill (directory + SKILL.md) and one instruction (a .md file).
	writeFile(t, filepath.Join(src, "coding", "skills", "go-style", "SKILL.md"), "# go style")
	writeFile(t, filepath.Join(src, "coding", "instructions", "style.md"), "be terse")

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

	final := OSDeps(home).Run(model, cmd)
	if final.Phase != runtime.PhaseDone {
		t.Fatalf("phase = %v (err %v, stage %q), want Done", final.Phase, final.Err, final.FailedStage)
	}

	assertSymlink(t, filepath.Join(home, ".claude", "skills", "go-style"))
	assertSymlink(t, filepath.Join(home, ".claude", "rules", "style.md"))
}

func TestEndToEndSyncIsIdempotent(t *testing.T) {
	model, cmd, home := setup(t, runtime.ModeSync)
	final := OSDeps(home).Run(model, cmd)
	if final.Phase != runtime.PhaseDone {
		t.Fatalf("first sync: phase %v err %v", final.Phase, final.Err)
	}
	if len(final.Plan.Ops) != 2 {
		t.Fatalf("first sync ops = %d, want 2 creates", len(final.Plan.Ops))
	}

	// Re-run against the now-populated home: the world already matches, so the
	// second plan is empty.
	model2, cmd2, _ := reinit(t, final)
	final2 := OSDeps(home).Run(model2, cmd2)
	if final2.Phase != runtime.PhaseDone {
		t.Fatalf("second sync: phase %v err %v", final2.Phase, final2.Err)
	}
	if len(final2.Plan.Ops) != 0 {
		t.Fatalf("second sync ops = %+v, want none (idempotent)", final2.Plan.Ops)
	}
}

func TestEndToEndPlanTouchesNothing(t *testing.T) {
	model, cmd, home := setup(t, runtime.ModePlan)
	final := OSDeps(home).Run(model, cmd)
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

func TestEndToEndStatusFindsUnmanaged(t *testing.T) {
	// After a sync installs the managed skill, an agent-created skill placed
	// directly in ~/.claude/skills must show up as adoptable in status.
	model, cmd, home := setup(t, runtime.ModeSync)
	if final := OSDeps(home).Run(model, cmd); final.Phase != runtime.PhaseDone {
		t.Fatalf("seed sync: phase %v err %v", final.Phase, final.Err)
	}

	// An agent writes a new skill in place.
	writeFile(t, filepath.Join(home, ".claude", "skills", "bark-pro", "SKILL.md"), "woof woof")

	sm, scmd := reinitStatus(t, home)
	final := OSDeps(home).Run(sm, scmd)
	if final.Phase != runtime.PhaseDone {
		t.Fatalf("status: phase %v err %v stage %q", final.Phase, final.Err, final.FailedStage)
	}
	if len(final.Unmanaged) != 1 || final.Unmanaged[0].Name != "bark-pro" {
		t.Fatalf("unmanaged = %+v, want the agent-created bark-pro", final.Unmanaged)
	}
}

// reinitStatus builds a status run for the home/config produced by setup.
func reinitStatus(t *testing.T, home string) (runtime.Model, runtime.Cmd) {
	t.Helper()
	configPath := filepath.Join(filepath.Dir(home), "config.toml")
	agents := agent.Default()
	roots := agents.ResolveRoots(home, func(string) string { return "" })
	return runtime.Init(runtime.ModeStatus, configPath, home, agents, roots)
}

func TestEndToEndAdopt(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src", "personal")
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	// An agent created a skill directly in claude-code's namespace.
	writeFile(t, filepath.Join(home, ".claude", "skills", "bark-pro", "SKILL.md"), "woof")

	// A config whose existing selection names claude-code (so its namespaces are
	// scanned) but does not cover bark-pro, so adopt must add a selection.
	configPath := filepath.Join(tmp, "config.toml")
	writeFile(t, configPath, ""+
		"[[sources]]\nname = \"personal\"\nlocation = \""+src+"\"\n\n"+
		"[[selections]]\nsource = \"personal\"\ncomponent = \"skill/placeholder\"\nagents = [\"claude-code\"]\n")

	agents := agent.Default()
	roots := agents.ResolveRoots(home, func(string) string { return "" })
	model, cmd := runtime.Init(runtime.ModeAdopt, configPath, home, agents, roots)
	model.Adopt = adopt.Request{
		Path:       filepath.Join(home, ".claude", "skills", "bark-pro"),
		SourceName: "personal", PluginName: "mine",
	}
	final := OSDeps(home).Run(model, cmd)
	if final.Phase != runtime.PhaseDone {
		t.Fatalf("phase = %v (err %v, stage %q, problem %+v)", final.Phase, final.Err, final.FailedStage, final.AdoptProblem)
	}

	// The skill moved into the source.
	if e, _ := (fsport.OS{}).Lstat(filepath.Join(src, "mine", "skills", "bark-pro", "SKILL.md")); !e.IsRegular {
		t.Fatal("skill was not moved into the source")
	}
	// The original is now a symlink into the source.
	if e, _ := (fsport.OS{}).Lstat(filepath.Join(home, ".claude", "skills", "bark-pro")); !e.IsSymlink {
		t.Fatal("the original was not replaced with a symlink")
	}
	// The config gained a selection for the adopted skill.
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sel := range cfg.Selections {
		if sel.SourceName == "personal" && sel.PluginName == "mine" && sel.ComponentID == "skill/bark-pro" {
			found = true
		}
	}
	if !found {
		t.Fatalf("config did not gain a selection for the adopted skill: %+v", cfg.Selections)
	}
}

func TestEndToEndAdoptSingletonInstruction(t *testing.T) {
	// Adopt a codex-cli singleton instruction (~/.codex/AGENTS.md, a fixed file on
	// its own root). The generic adopt flow must move it into the source as a
	// <name>.md, replace it with a back-symlink, and add a codex-cli selection so a
	// later sync maintains it.
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src", "personal")
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	// The user's hand-written codex instruction file.
	writeFile(t, filepath.Join(home, ".codex", "AGENTS.md"), "be terse and cite sources")
	// An unrelated file in the same directory must be left untouched.
	writeFile(t, filepath.Join(home, ".codex", "history.md"), "session log")

	configPath := filepath.Join(tmp, "config.toml")
	writeFile(t, configPath, "[[sources]]\nname = \"personal\"\nlocation = \""+src+"\"\n")

	agents := agent.Default()
	roots := agents.ResolveRoots(home, func(string) string { return "" })
	model, cmd := runtime.Init(runtime.ModeAdopt, configPath, home, agents, roots)
	model.Adopt = adopt.Request{
		Path:       filepath.Join(home, ".codex", "AGENTS.md"),
		SourceName: "personal", PluginName: "mine",
	}
	final := OSDeps(home).Run(model, cmd)
	if final.Phase != runtime.PhaseDone {
		t.Fatalf("phase = %v (err %v, stage %q, problem %+v)", final.Phase, final.Err, final.FailedStage, final.AdoptProblem)
	}

	// The instruction moved into the source (named for its agent, codex-cli),
	// content preserved.
	moved := filepath.Join(src, "mine", "instructions", "codex-cli.md")
	if e, _ := (fsport.OS{}).Lstat(moved); !e.IsRegular {
		t.Fatalf("instruction was not moved into the source at %s", moved)
	}
	if b, err := os.ReadFile(moved); err != nil || string(b) != "be terse and cite sources" {
		t.Fatalf("moved instruction content = %q, err %v", b, err)
	}
	// The original path is now a symlink into the source.
	if e, _ := (fsport.OS{}).Lstat(filepath.Join(home, ".codex", "AGENTS.md")); !e.IsSymlink {
		t.Fatal("the original AGENTS.md was not replaced with a symlink")
	}
	// The unrelated sibling is untouched.
	if e, _ := (fsport.OS{}).Lstat(filepath.Join(home, ".codex", "history.md")); !e.IsRegular {
		t.Fatal("an unrelated sibling file must not be touched by adopt")
	}
	// The config gained a codex-cli selection for the adopted instruction.
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sel := range cfg.Selections {
		if sel.SourceName == "personal" && sel.PluginName == "mine" && sel.ComponentID == "instruction/codex-cli" &&
			len(sel.Agents) == 1 && sel.Agents[0] == core.AgentCodexCLI {
			found = true
		}
	}
	if !found {
		t.Fatalf("config did not gain a codex-cli selection for the adopted instruction: %+v", cfg.Selections)
	}

	// Round-trip: a sync now maintains the adopted singleton. The back-symlink
	// already points at the source file, so the plan is empty (it propagates on a
	// fresh machine, and is a no-op here).
	sm, scmd := runtime.Init(runtime.ModeSync, configPath, home, agents, roots)
	sfinal := OSDeps(home).Run(sm, scmd)
	if sfinal.Phase != runtime.PhaseDone {
		t.Fatalf("post-adopt sync: phase %v err %v blockers %+v", sfinal.Phase, sfinal.Err, sfinal.Blockers)
	}
	if len(sfinal.Plan.Ops) != 0 {
		t.Fatalf("post-adopt sync must be a no-op, got %+v", sfinal.Plan.Ops)
	}
}

func TestEndToEndAdoptMultipleSingletonsSamePlugin(t *testing.T) {
	// Several agents on one device each have an AGENTS.md. Because a singleton is
	// named for its agent, not its filename, all of them adopt into the same plugin
	// without colliding (codex-cli.md, opencode.md), each with its own selection.
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src", "personal")
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".codex", "AGENTS.md"), "codex rules")
	writeFile(t, filepath.Join(home, ".config", "opencode", "AGENTS.md"), "opencode rules")

	configPath := filepath.Join(tmp, "config.toml")
	writeFile(t, configPath, "[[sources]]\nname = \"personal\"\nlocation = \""+src+"\"\n")

	agents := agent.Default()
	roots := agents.ResolveRoots(home, func(string) string { return "" })

	adoptOne := func(path string) {
		t.Helper()
		model, cmd := runtime.Init(runtime.ModeAdopt, configPath, home, agents, roots)
		model.Adopt = adopt.Request{Path: path, SourceName: "personal", PluginName: "mine"}
		final := OSDeps(home).Run(model, cmd)
		if final.Phase != runtime.PhaseDone {
			t.Fatalf("adopt %s: phase %v problem %+v", path, final.Phase, final.AdoptProblem)
		}
	}
	adoptOne(filepath.Join(home, ".codex", "AGENTS.md"))
	adoptOne(filepath.Join(home, ".config", "opencode", "AGENTS.md")) // must not collide

	for _, leaf := range []string{"codex-cli.md", "opencode.md"} {
		if e, _ := (fsport.OS{}).Lstat(filepath.Join(src, "mine", "instructions", leaf)); !e.IsRegular {
			t.Errorf("expected source instruction %s after adopting both singletons", leaf)
		}
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]core.AgentID{"instruction/codex-cli": core.AgentCodexCLI, "instruction/opencode": core.AgentOpenCode}
	got := map[string]core.AgentID{}
	for _, sel := range cfg.Selections {
		if len(sel.Agents) == 1 {
			got[sel.ComponentID] = sel.Agents[0]
		}
	}
	for id, ag := range want {
		if got[id] != ag {
			t.Errorf("selection %s = %v, want %v (selections: %+v)", id, got[id], ag, cfg.Selections)
		}
	}
}

func TestEndToEndStatusFindsSingletonInstruction(t *testing.T) {
	// status surfaces an agent-authored singleton instruction as adoptable, by its
	// fixed filename, without reporting unrelated files in the same directory.
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src", "personal")
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".codex", "AGENTS.md"), "global codex rules")
	writeFile(t, filepath.Join(home, ".codex", "config.toml"), "[settings]") // unrelated

	// codex is named by a selection (skills), so status scans its namespaces.
	configPath := filepath.Join(tmp, "config.toml")
	writeFile(t, configPath, ""+
		"[[sources]]\nname = \"personal\"\nlocation = \""+src+"\"\n\n"+
		"[[selections]]\nsource = \"personal\"\ncomponent = \"skill/x\"\nagents = [\"codex-cli\"]\n")

	agents := agent.Default()
	roots := agents.ResolveRoots(home, func(string) string { return "" })
	model, cmd := runtime.Init(runtime.ModeStatus, configPath, home, agents, roots)
	final := OSDeps(home).Run(model, cmd)
	if final.Phase != runtime.PhaseDone {
		t.Fatalf("status: phase %v err %v stage %q", final.Phase, final.Err, final.FailedStage)
	}
	if len(final.Unmanaged) != 1 || final.Unmanaged[0].Ref() != "instruction/codex-cli" ||
		final.Unmanaged[0].Path != filepath.Join(home, ".codex", "AGENTS.md") {
		t.Fatalf("unmanaged = %+v, want only the codex AGENTS.md singleton", final.Unmanaged)
	}
}

func TestEndToEndAdoptBootstrapsWithoutSelections(t *testing.T) {
	// A first adopt against a config that declares a source but has NO selections
	// yet must still work: discovery scans every supported agent namespace, not
	// just those a selection already names, so the agent-created skill is found and
	// the very first selection is written.
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src", "personal")
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".claude", "skills", "bark-pro", "SKILL.md"), "woof")

	configPath := filepath.Join(tmp, "config.toml")
	writeFile(t, configPath, "[[sources]]\nname = \"personal\"\nlocation = \""+src+"\"\n")

	agents := agent.Default()
	roots := agents.ResolveRoots(home, func(string) string { return "" })
	model, cmd := runtime.Init(runtime.ModeAdopt, configPath, home, agents, roots)
	model.Adopt = adopt.Request{
		Path:       filepath.Join(home, ".claude", "skills", "bark-pro"),
		SourceName: "personal", PluginName: "mine",
	}
	final := OSDeps(home).Run(model, cmd)
	if final.Phase != runtime.PhaseDone {
		t.Fatalf("phase = %v (err %v, stage %q, problem %+v)", final.Phase, final.Err, final.FailedStage, final.AdoptProblem)
	}
	if e, _ := (fsport.OS{}).Lstat(filepath.Join(src, "mine", "skills", "bark-pro", "SKILL.md")); !e.IsRegular {
		t.Fatal("skill was not moved into the source")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Selections) != 1 || cfg.Selections[0].ComponentID != "skill/bark-pro" {
		t.Fatalf("config did not gain the first selection: %+v", cfg.Selections)
	}
}

// reinit rebuilds the initial model+cmd reusing the prior run's seeded inputs, so
// a second sync runs against the filesystem the first one produced.
func reinit(t *testing.T, prev runtime.Model) (runtime.Model, runtime.Cmd, string) {
	t.Helper()
	model, cmd := runtime.Init(prev.Mode, prev.ConfigPath, prev.Home, prev.Agents, prev.Roots)
	return model, cmd, prev.Home
}

func TestResolveLocation(t *testing.T) {
	t.Parallel()
	d := Deps{Home: "/home/u"}
	const base = "/cfg"

	if got, prob := d.resolveLocation(base, "/abs/team"); prob != nil || got != "/abs/team" {
		t.Errorf("abs: got %q prob %v, want /abs/team", got, prob)
	}
	if got, prob := d.resolveLocation(base, "~/team"); prob != nil || got != "/home/u/team" {
		t.Errorf("tilde: got %q prob %v, want /home/u/team", got, prob)
	}
	// A relative location resolves against base (the config dir), NOT the CWD.
	if got, prob := d.resolveLocation(base, "./team"); prob != nil || got != "/cfg/team" {
		t.Errorf("relative ./team: got %q prob %v, want /cfg/team", got, prob)
	}
	if got, prob := d.resolveLocation(base, "team"); prob != nil || got != "/cfg/team" {
		t.Errorf("relative team: got %q prob %v, want /cfg/team", got, prob)
	}
	for _, url := range []string{"git@codeberg.org:botfile/team.git", "https://example.com/team.git", "ssh://host/team"} {
		if _, prob := d.resolveLocation(base, url); prob == nil {
			t.Errorf("remote %q: want an unsupported-source problem, got none", url)
		}
	}
}

func TestEndToEndSyncWithRelativeSourcePath(t *testing.T) {
	// A source location relative to the config file must resolve and sync, not
	// produce relative destinations the planner rejects as invalid-path.
	model, cmd, home := setupRelative(t)
	final := OSDeps(home).Run(model, cmd)
	if final.Phase != runtime.PhaseDone {
		t.Fatalf("phase = %v (err %v, stage %q, blockers %+v), want Done", final.Phase, final.Err, final.FailedStage, final.Blockers)
	}
	assertSymlink(t, filepath.Join(home, ".claude", "skills", "go-style"))
}

// setupRelative is like setup but writes the source location as a path relative
// to the config file's directory, so resolveLocation must join it to that base.
func setupRelative(t *testing.T) (runtime.Model, runtime.Cmd, string) {
	t.Helper()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src", "team")
	home := filepath.Join(tmp, "home")
	writeFile(t, filepath.Join(src, "coding", "skills", "go-style", "SKILL.md"), "# go style")

	configPath := filepath.Join(tmp, "config.toml")
	rel, err := filepath.Rel(filepath.Dir(configPath), src) // "src/team", relative to the config dir
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, configPath, ""+
		"[[sources]]\n"+
		"name = \"team\"\n"+
		"location = \""+rel+"\"\n\n"+
		"[[selections]]\n"+
		"source = \"team\"\n"+
		"agents = [\"claude-code\"]\n")

	agents := agent.Default()
	roots := agents.ResolveRoots(home, func(string) string { return "" })
	model, cmd := runtime.Init(runtime.ModeSync, configPath, home, agents, roots)
	return model, cmd, home
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
