package runtime

import (
	"errors"
	"testing"

	"codeberg.org/botfile/botfile/internal/agent"
	"codeberg.org/botfile/botfile/internal/core"
	"codeberg.org/botfile/botfile/internal/project"
	"codeberg.org/botfile/botfile/internal/reconcile"
)

func noEnv(string) string { return "" }

func testConfig() core.Config {
	return core.Config{
		Sources: []core.Source{{Name: "team", Location: "/src/team"}},
		Selections: []core.Selection{{
			SourceName: "team", PluginName: core.Wildcard, ComponentID: core.Wildcard,
			Agents: []core.AgentID{core.AgentClaudeCode},
		}},
	}
}

func scannedTeam() project.Source {
	return project.Source{
		Name: "team",
		Root: "/src/team",
		Plugins: []core.Plugin{{
			Name:       "coding",
			Components: []core.Component{{Kind: core.KindSkill, Name: "go-style"}},
		}},
	}
}

func newModel(t *testing.T, mode Mode) (Model, Cmd) {
	t.Helper()
	agents := agent.Default()
	roots := agents.ResolveRoots("/home/u", noEnv)
	return Init(mode, "/cfg/config.toml", "/home/u", agents, roots)
}

func TestPlanRunReachesDoneWithAPlan(t *testing.T) {
	t.Parallel()
	m, cmd := newModel(t, ModePlan)
	if _, ok := cmd.(CmdLoadConfig); !ok || m.Phase != PhaseLoadingConfig {
		t.Fatalf("init = phase %v cmd %T, want LoadingConfig + CmdLoadConfig", m.Phase, cmd)
	}

	m, cmd = Update(m, ConfigLoaded{Config: testConfig()})
	if sc, ok := cmd.(CmdScanSources); !ok || len(sc.Sources) != 1 || m.Phase != PhaseScanning {
		t.Fatalf("after ConfigLoaded = phase %v cmd %T, want Scanning + CmdScanSources", m.Phase, cmd)
	}

	m, cmd = Update(m, SourcesScanned{Sources: []project.Source{scannedTeam()}})
	rw, ok := cmd.(CmdReadWorld)
	if !ok || m.Phase != PhaseReadingWorld {
		t.Fatalf("after SourcesScanned = phase %v cmd %T, want ReadingWorld + CmdReadWorld", m.Phase, cmd)
	}
	// Projection ran: one skill link to claude-code's skills dir.
	if len(m.Projection.Links) != 1 || m.Projection.Links[0].Target != "/home/u/.claude/skills/go-style" {
		t.Fatalf("projection links = %+v", m.Projection.Links)
	}
	if len(rw.Targets) != 1 || rw.Targets[0] != "/home/u/.claude/skills/go-style" {
		t.Fatalf("read-world targets = %v", rw.Targets)
	}
	// Managed dirs include claude's skill and memory namespaces (orphan scan).
	if !contains(rw.ManagedDirs, "/home/u/.claude/skills") || !contains(rw.ManagedDirs, "/home/u/.claude/rules") {
		t.Fatalf("managed dirs = %v, want claude skills + rules", rw.ManagedDirs)
	}

	m, cmd = Update(m, WorldRead{World: reconcile.World{Entries: map[string]reconcile.Entry{}}})
	if _, ok := cmd.(CmdNone); !ok || m.Phase != PhaseDone {
		t.Fatalf("plan run end = phase %v cmd %T, want Done + CmdNone", m.Phase, cmd)
	}
	if len(m.Plan.Ops) != 1 || m.Plan.Ops[0].Kind != reconcile.OpCreate {
		t.Fatalf("plan ops = %+v, want one create", m.Plan.Ops)
	}
	if !m.Done() {
		t.Fatal("model should be Done")
	}
}

func TestSyncRunAppliesThenDone(t *testing.T) {
	t.Parallel()
	m, _ := newModel(t, ModeSync)
	m, _ = Update(m, ConfigLoaded{Config: testConfig()})
	m, _ = Update(m, SourcesScanned{Sources: []project.Source{scannedTeam()}})
	m, cmd := Update(m, WorldRead{World: reconcile.World{Entries: map[string]reconcile.Entry{}}})

	ap, ok := cmd.(CmdApply)
	if !ok || m.Phase != PhaseApplying {
		t.Fatalf("sync after WorldRead = phase %v cmd %T, want Applying + CmdApply", m.Phase, cmd)
	}
	if len(ap.Ops) != 1 || ap.Ops[0].Kind != reconcile.OpCreate {
		t.Fatalf("apply ops = %+v, want one create", ap.Ops)
	}

	m, cmd = Update(m, Applied{})
	if _, ok := cmd.(CmdNone); !ok || m.Phase != PhaseDone {
		t.Fatalf("sync end = phase %v cmd %T, want Done + CmdNone", m.Phase, cmd)
	}
}

func TestFailedStops(t *testing.T) {
	t.Parallel()
	m, _ := newModel(t, ModeSync)
	boom := errors.New("disk on fire")
	m, cmd := Update(m, Failed{Stage: "scan", Err: boom})
	if _, ok := cmd.(CmdNone); !ok || m.Phase != PhaseFailed {
		t.Fatalf("after Failed = phase %v cmd %T, want Failed + CmdNone", m.Phase, cmd)
	}
	if m.FailedStage != "scan" || !errors.Is(m.Err, boom) {
		t.Fatalf("failure not recorded: stage %q err %v", m.FailedStage, m.Err)
	}
	if !m.Done() {
		t.Fatal("a failed model should be Done")
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
