package runtime

import (
	"errors"
	"testing"

	"codeberg.org/botfile/botfile/internal/adopt"
	"codeberg.org/botfile/botfile/internal/agent"
	"codeberg.org/botfile/botfile/internal/core"
	"codeberg.org/botfile/botfile/internal/discover"
	"codeberg.org/botfile/botfile/internal/project"
	"codeberg.org/botfile/botfile/internal/reconcile"
	"codeberg.org/botfile/botfile/internal/source"
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

func TestSyncBlocksOnConflict(t *testing.T) {
	t.Parallel()
	m, _ := newModel(t, ModeSync)
	m, _ = Update(m, ConfigLoaded{Config: testConfig()})
	m, _ = Update(m, SourcesScanned{Sources: []project.Source{scannedTeam()}})
	// A foreign file sits where the skill would install: a conflict.
	world := reconcile.World{Entries: map[string]reconcile.Entry{
		"/home/u/.claude/skills/go-style": {Kind: reconcile.Foreign},
	}}
	m, cmd := Update(m, WorldRead{World: world})
	if _, ok := cmd.(CmdNone); !ok || m.Phase != PhaseBlocked {
		t.Fatalf("conflicting sync = phase %v cmd %T, want Blocked + CmdNone (no apply)", m.Phase, cmd)
	}
	if len(m.Blockers) != 1 || m.Blockers[0].Kind != BlockerConflict || m.Blockers[0].Ref != "/home/u/.claude/skills/go-style" {
		t.Fatalf("blockers = %+v, want one conflict at the skill target", m.Blockers)
	}
	if !m.Done() {
		t.Fatal("a blocked model is terminal")
	}
}

func TestBlockersClassifyAllIncompleteModelCauses(t *testing.T) {
	t.Parallel()
	m := Model{
		ScanProblems: []source.Problem{{Kind: source.ProblemSkillMissingManifest, Path: "p/skills/x", Detail: "no SKILL.md"}},
		Projection: project.Result{Problems: []project.Problem{
			{Kind: project.ProblemEmptySelection, SourceName: "team", Detail: "matched nothing"},
			{Kind: project.ProblemUnsupported, SourceName: "team", Agent: core.AgentCodexCLI, Component: "memory/x", Detail: "unsupported"},
		}},
		Plan: reconcile.Plan{
			Problems:  []reconcile.Problem{{Kind: reconcile.ProblemAmbiguousTarget, Target: "/t/x", Detail: "ambiguous"}},
			Conflicts: []reconcile.Conflict{{Target: "/t/y", Reason: "foreign file"}},
		},
	}
	bs := blockers(m)
	// scan + projection(empty, not unsupported) + plan + conflict = 4; unsupported excluded.
	if len(bs) != 4 {
		t.Fatalf("blockers = %+v, want 4 (unsupported excluded)", bs)
	}
	kinds := map[BlockerKind]bool{}
	for _, b := range bs {
		kinds[b.Kind] = true
	}
	for _, want := range []BlockerKind{BlockerScanProblem, BlockerProjectionProblem, BlockerPlanProblem, BlockerConflict} {
		if !kinds[want] {
			t.Errorf("missing blocker kind %v", want)
		}
	}
}

func TestSyncBlocksOnProjectionTypoSparingExistingLink(t *testing.T) {
	t.Parallel()
	// A typo'd selector matches nothing (ProblemEmptySelection) and produces no
	// desired link. An existing managed symlink would look like an orphan; sync
	// must NOT apply (and so must not remove it) while the model is incomplete.
	cfg := core.Config{
		Sources: []core.Source{{Name: "team", Location: "/src/team"}},
		Selections: []core.Selection{{
			SourceName: "team", PluginName: core.Wildcard, ComponentID: "skill/go-styel", // typo
			Agents: []core.AgentID{core.AgentClaudeCode},
		}},
	}
	m, _ := newModel(t, ModeSync)
	m, _ = Update(m, ConfigLoaded{Config: cfg})
	m, _ = Update(m, SourcesScanned{Sources: []project.Source{scannedTeam()}})
	// The correct link is already installed; with the typo it is now an orphan
	// candidate in the world.
	world := reconcile.World{Entries: map[string]reconcile.Entry{
		"/home/u/.claude/skills/go-style": {Kind: reconcile.Symlink, Dest: "/src/team/coding/skills/go-style"},
	}}
	m, cmd := Update(m, WorldRead{World: world})
	if _, ok := cmd.(CmdApply); ok {
		t.Fatalf("sync emitted CmdApply despite a projection problem: %T", cmd)
	}
	if m.Phase != PhaseBlocked {
		t.Fatalf("phase = %v, want Blocked", m.Phase)
	}
	if len(m.Blockers) == 0 || m.Blockers[0].Kind != BlockerProjectionProblem {
		t.Fatalf("blockers = %+v, want a projection-problem blocker", m.Blockers)
	}
}

func TestSyncBlocksOnScanProblem(t *testing.T) {
	t.Parallel()
	m, _ := newModel(t, ModeSync)
	m, _ = Update(m, ConfigLoaded{Config: testConfig()})
	m, _ = Update(m, SourcesScanned{
		Sources:  []project.Source{scannedTeam()},
		Problems: []source.Problem{{Kind: source.ProblemSkillMissingManifest, Path: "coding/skills/broken", Detail: "no SKILL.md"}},
	})
	m, cmd := Update(m, WorldRead{World: reconcile.World{Entries: map[string]reconcile.Entry{}}})
	if _, ok := cmd.(CmdApply); ok {
		t.Fatalf("sync emitted CmdApply despite a scan problem: %T", cmd)
	}
	if m.Phase != PhaseBlocked || len(m.Blockers) != 1 || m.Blockers[0].Kind != BlockerScanProblem {
		t.Fatalf("phase %v blockers %+v, want Blocked with a scan-problem blocker", m.Phase, m.Blockers)
	}
}

func TestSyncProceedsWhenOnlyUnsupported(t *testing.T) {
	t.Parallel()
	// A config that selects everything for claude and codex: codex memory is
	// unsupported (a projection problem) but that is expected partial coverage and
	// must NOT block the clean installs.
	cfg := core.Config{
		Sources: []core.Source{{Name: "team", Location: "/src/team"}},
		Selections: []core.Selection{{
			SourceName: "team", PluginName: core.Wildcard, ComponentID: core.Wildcard,
			Agents: []core.AgentID{core.AgentClaudeCode, core.AgentCodexCLI},
		}},
	}
	scanned := project.Source{
		Name: "team", Root: "/src/team",
		Plugins: []core.Plugin{{
			Name: "coding",
			Components: []core.Component{
				{Kind: core.KindSkill, Name: "go-style"},
				{Kind: core.KindMemory, Name: "style"},
			},
		}},
	}
	m, _ := newModel(t, ModeSync)
	m, _ = Update(m, ConfigLoaded{Config: cfg})
	m, _ = Update(m, SourcesScanned{Sources: []project.Source{scanned}})
	m, cmd := Update(m, WorldRead{World: reconcile.World{Entries: map[string]reconcile.Entry{}}})

	if _, ok := cmd.(CmdApply); !ok || m.Phase != PhaseApplying {
		t.Fatalf("unsupported-only sync = phase %v cmd %T, want Applying + CmdApply", m.Phase, cmd)
	}
	if len(m.Blockers) != 0 {
		t.Fatalf("unsupported must not block, got blockers %+v", m.Blockers)
	}
	// The unsupported problem is still recorded for reporting.
	foundUnsupported := false
	for _, p := range m.Projection.Problems {
		if p.Kind == project.ProblemUnsupported {
			foundUnsupported = true
		}
	}
	if !foundUnsupported {
		t.Fatal("expected the codex memory unsupported problem to be recorded")
	}
}

func TestTerminalPhasesAreTerminal(t *testing.T) {
	t.Parallel()
	// A Done model ignores further messages.
	done := Model{Phase: PhaseDone}
	if m, cmd := Update(done, ConfigLoaded{Config: testConfig()}); m.Phase != PhaseDone {
		t.Errorf("Done + ConfigLoaded advanced to %v (cmd %T); must stay Done", m.Phase, cmd)
	}
	// A Failed model ignores further messages.
	failed := Model{Phase: PhaseFailed}
	if m, _ := Update(failed, WorldRead{}); m.Phase != PhaseFailed {
		t.Errorf("Failed + WorldRead advanced to %v; must stay Failed", m.Phase)
	}
	// A Blocked model ignores further messages.
	blocked := Model{Phase: PhaseBlocked}
	if m, _ := Update(blocked, Applied{}); m.Phase != PhaseBlocked {
		t.Errorf("Blocked + Applied advanced to %v; must stay Blocked", m.Phase)
	}
}

func TestOutOfOrderMessageIsIgnored(t *testing.T) {
	t.Parallel()
	// An Applied message during Scanning is stale and must not complete the run.
	m, _ := newModel(t, ModeSync)
	m, _ = Update(m, ConfigLoaded{Config: testConfig()}) // now PhaseScanning
	got, cmd := Update(m, Applied{})
	if got.Phase != PhaseScanning {
		t.Fatalf("stale Applied advanced Scanning to %v (cmd %T)", got.Phase, cmd)
	}
}

func TestStatusRunDiscoversThenDone(t *testing.T) {
	t.Parallel()
	m, _ := newModel(t, ModeStatus)
	m, _ = Update(m, ConfigLoaded{Config: testConfig()})
	m, _ = Update(m, SourcesScanned{Sources: []project.Source{scannedTeam()}})

	// After reconcile, status mode asks the interpreter to discover unmanaged
	// components rather than applying.
	m, cmd := Update(m, WorldRead{World: reconcile.World{Entries: map[string]reconcile.Entry{}}})
	disc, ok := cmd.(CmdDiscover)
	if !ok || m.Phase != PhaseDiscovering {
		t.Fatalf("status after WorldRead = phase %v cmd %T, want Discovering + CmdDiscover", m.Phase, cmd)
	}
	// claude-code is the only targeted agent: skills and rules namespaces.
	if len(disc.Namespaces) != 2 {
		t.Fatalf("namespaces = %+v, want claude skills + rules", disc.Namespaces)
	}

	found := []discover.Unmanaged{{Agents: []core.AgentID{core.AgentClaudeCode}, Kind: core.KindSkill, Name: "bark-pro", Path: "/home/u/.claude/skills/bark-pro"}}
	m, cmd = Update(m, Discovered{Unmanaged: found})
	if _, ok := cmd.(CmdNone); !ok || m.Phase != PhaseDone {
		t.Fatalf("status end = phase %v cmd %T, want Done + CmdNone", m.Phase, cmd)
	}
	if len(m.Unmanaged) != 1 || m.Unmanaged[0].Name != "bark-pro" {
		t.Fatalf("unmanaged = %+v, want bark-pro", m.Unmanaged)
	}
}

func TestManagedNamespacesDedupesSharedDir(t *testing.T) {
	t.Parallel()
	// codex and copilot both read ~/.agents/skills; it must appear once.
	cfg := core.Config{
		Sources: []core.Source{{Name: "team", Location: "/src/team"}},
		Selections: []core.Selection{{
			SourceName: "team", PluginName: core.Wildcard, ComponentID: core.Wildcard,
			Agents: []core.AgentID{core.AgentCodexCLI, core.AgentCopilotCLI},
		}},
	}
	agents := agent.Default()
	roots := agents.ResolveRoots("/home/u", noEnv)
	ns := managedNamespaces(cfg, agents, roots)

	var shared *discover.Namespace
	count := 0
	for i := range ns {
		if ns[i].Dir == "/home/u/.agents/skills" {
			count++
			shared = &ns[i]
		}
	}
	if count != 1 {
		t.Fatalf("~/.agents/skills appears %d times, want 1: %+v", count, ns)
	}
	// Deduped to one namespace, but both readers preserved.
	if len(shared.Agents) != 2 || shared.Agents[0] != core.AgentCodexCLI || shared.Agents[1] != core.AgentCopilotCLI {
		t.Fatalf("shared dir agents = %v, want [codex-cli copilot-cli]", shared.Agents)
	}
}

func TestAdoptRunComputesAndApplies(t *testing.T) {
	t.Parallel()
	m, _ := newModel(t, ModeAdopt)
	m.Adopt = adopt.Request{Path: "/home/u/.claude/skills/bark", SourceName: "team", PluginName: "mine"}
	m, _ = Update(m, ConfigLoaded{Config: testConfig()})

	// adopt skips the reconcile pipeline: scan then discover.
	m, cmd := Update(m, SourcesScanned{Sources: []project.Source{scannedTeam()}})
	if _, ok := cmd.(CmdDiscover); !ok || m.Phase != PhaseDiscovering {
		t.Fatalf("adopt after scan = phase %v cmd %T, want Discovering + CmdDiscover", m.Phase, cmd)
	}

	found := []discover.Unmanaged{{Agents: []core.AgentID{core.AgentClaudeCode}, Kind: core.KindSkill, Name: "bark", Path: "/home/u/.claude/skills/bark"}}
	m, cmd = Update(m, Discovered{Unmanaged: found})
	ca, ok := cmd.(CmdApplyAdopt)
	if !ok || m.Phase != PhaseApplying {
		t.Fatalf("adopt compute = phase %v cmd %T, want Applying + CmdApplyAdopt", m.Phase, cmd)
	}
	if ca.Plan.From != "/home/u/.claude/skills/bark" || ca.Plan.To != "/src/team/mine/skills/bark" {
		t.Fatalf("adopt plan = %+v", ca.Plan)
	}

	m, cmd = Update(m, Applied{})
	if _, ok := cmd.(CmdNone); !ok || m.Phase != PhaseDone {
		t.Fatalf("adopt end = phase %v cmd %T, want Done", m.Phase, cmd)
	}
}

func TestAdoptRunBlocksOnProblem(t *testing.T) {
	t.Parallel()
	m, _ := newModel(t, ModeAdopt)
	m.Adopt = adopt.Request{Path: "/nope", SourceName: "team", PluginName: "mine"}
	m, _ = Update(m, ConfigLoaded{Config: testConfig()})
	m, _ = Update(m, SourcesScanned{Sources: []project.Source{scannedTeam()}})

	// Nothing matches the requested path: a not-adoptable problem, blocked, no effect.
	m, cmd := Update(m, Discovered{Unmanaged: nil})
	if _, ok := cmd.(CmdNone); !ok || m.Phase != PhaseBlocked {
		t.Fatalf("adopt of an undiscovered path = phase %v cmd %T, want Blocked + CmdNone", m.Phase, cmd)
	}
	if m.AdoptProblem == nil || m.AdoptProblem.Kind != adopt.ProblemNotAdoptable {
		t.Fatalf("AdoptProblem = %+v, want not-adoptable", m.AdoptProblem)
	}
}

func TestManagedNamespacesKeysByKindNotJustDir(t *testing.T) {
	t.Parallel()
	// A custom matrix that maps both skill and memory to the same directory must
	// yield two distinct namespaces, not one merged by directory.
	set, err := agent.NewSet(agent.Spec{
		ID:   core.AgentOpenCode,
		Base: agent.Base{HomeRelative: []string{".oc"}},
		Rules: map[core.Kind]agent.InstallRule{
			core.KindSkill:  {Tier: agent.Tier1, Segments: []string{"shared"}, Shape: agent.LeafDir},
			core.KindMemory: {Tier: agent.Tier1, Segments: []string{"shared"}, Shape: agent.LeafFile, Ext: ".md"},
		},
	})
	if err != nil {
		t.Fatalf("NewSet: %v", err)
	}
	cfg := core.Config{
		Sources: []core.Source{{Name: "team", Location: "/src/team"}},
		Selections: []core.Selection{{
			SourceName: "team", PluginName: core.Wildcard, ComponentID: core.Wildcard,
			Agents: []core.AgentID{core.AgentOpenCode},
		}},
	}
	ns := managedNamespaces(cfg, set, set.ResolveRoots("/home/u", noEnv))

	if len(ns) != 2 {
		t.Fatalf("namespaces = %+v, want 2 (skill and memory at the same dir)", ns)
	}
	// Same directory, distinct kinds, sorted by kind (memory < skill).
	if ns[0].Dir != "/home/u/.oc/shared" || ns[1].Dir != "/home/u/.oc/shared" {
		t.Fatalf("dirs = %q, %q, want both /home/u/.oc/shared", ns[0].Dir, ns[1].Dir)
	}
	if ns[0].Kind != core.KindMemory || ns[1].Kind != core.KindSkill {
		t.Fatalf("kinds = %q, %q, want memory then skill", ns[0].Kind, ns[1].Kind)
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
