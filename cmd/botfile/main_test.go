package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"codeberg.org/botfile/botfile/internal/core"
	"codeberg.org/botfile/botfile/internal/discover"
	"codeberg.org/botfile/botfile/internal/project"
	"codeberg.org/botfile/botfile/internal/reconcile"
	"codeberg.org/botfile/botfile/internal/runtime"
)

func TestRenderSyncDoneShowsOpsAndInfo(t *testing.T) {
	m := runtime.Model{
		Mode:  runtime.ModeSync,
		Phase: runtime.PhaseDone,
		Plan: reconcile.Plan{
			Ops:     []reconcile.Op{{Kind: reconcile.OpCreate, Target: "/h/.claude/skills/go", Dest: "/src/skills/go"}},
			Shadows: []reconcile.Shadow{{Target: "/h/x", SourceName: "personal", WonBy: "team"}},
		},
		Projection: project.Result{
			Notices:  []project.Notice{{Selected: []core.AgentID{"copilot-cli"}, AlsoReaches: []core.AgentID{"codex-cli"}, Namespace: "/h/.agents/skills"}},
			Problems: []project.Problem{{Kind: project.ProblemUnsupported, Agent: "codex-cli", Component: "memory/x", Detail: "no memory support"}},
		},
	}
	var buf bytes.Buffer
	if code := render(&buf, m); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	out := buf.String()
	for _, want := range []string{"create", "/h/.claude/skills/go -> /src/skills/go", "note", "shadowed", "skipped", "memory/x", "synced: 1 operation"} {
		if !strings.Contains(out, want) {
			t.Errorf("render output missing %q\n%s", want, out)
		}
	}
}

func TestRenderBlockedShowsIssues(t *testing.T) {
	m := runtime.Model{
		Mode:  runtime.ModeSync,
		Phase: runtime.PhaseBlocked,
		Blockers: []runtime.Blocker{
			{Kind: runtime.BlockerConflict, Ref: "/h/.claude/skills/go", Detail: "a non-symlink file already exists at this path"},
		},
	}
	var buf bytes.Buffer
	if code := render(&buf, m); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	out := buf.String()
	for _, want := range []string{"conflict", "/h/.claude/skills/go", "non-symlink", "blocked:"} {
		if !strings.Contains(out, want) {
			t.Errorf("blocked render missing %q\n%s", want, out)
		}
	}
}

func TestRenderPlanWithBlockersDoesNotInviteSync(t *testing.T) {
	m := runtime.Model{
		Mode:  runtime.ModePlan,
		Phase: runtime.PhaseDone,
		Plan:  reconcile.Plan{Ops: []reconcile.Op{{Kind: reconcile.OpCreate, Target: "/h/x", Dest: "/s/x"}}},
		Blockers: []runtime.Blocker{
			{Kind: runtime.BlockerConflict, Ref: "/h/y", Detail: "a non-symlink file already exists at this path"},
		},
	}
	var buf bytes.Buffer
	code := render(&buf, m)
	if code != 1 {
		t.Fatalf("plan with blockers exit = %d, want 1", code)
	}
	out := buf.String()
	if strings.Contains(out, "run `botfile sync` to apply") {
		t.Errorf("plan with blockers must not invite sync:\n%s", out)
	}
	if !strings.Contains(out, "would block sync") {
		t.Errorf("plan with blockers should say it would block:\n%s", out)
	}
}

func TestRenderStatus(t *testing.T) {
	m := runtime.Model{
		Mode:  runtime.ModeStatus,
		Phase: runtime.PhaseDone,
		Projection: project.Result{
			Links: []reconcile.LinkSpec{
				{Target: "/h/.claude/skills/managed", Dest: "/s/managed", SourceName: "team"},
				{Target: "/h/.claude/skills/missing", Dest: "/s/missing", SourceName: "team"},
			},
			Notices:  []project.Notice{{Selected: []core.AgentID{"copilot-cli"}, AlsoReaches: []core.AgentID{"codex-cli"}, Namespace: "/h/.agents/skills"}},
			Problems: []project.Problem{{Kind: project.ProblemUnsupported, Agent: "codex-cli", Component: "memory/x", Detail: "no memory support"}},
		},
		Plan: reconcile.Plan{
			Ops:     []reconcile.Op{{Kind: reconcile.OpCreate, Target: "/h/.claude/skills/missing", Dest: "/s/missing"}},
			Shadows: []reconcile.Shadow{{Target: "/h/x", SourceName: "personal", WonBy: "team"}},
		},
		Unmanaged: []discover.Unmanaged{
			{Agents: []core.AgentID{core.AgentCodexCLI, core.AgentCopilotCLI}, Kind: core.KindSkill, Name: "bark-pro", Path: "/h/.agents/skills/bark-pro"},
		},
	}
	var buf bytes.Buffer
	if code := render(&buf, m); code != 0 {
		t.Fatalf("status exit = %d, want 0", code)
	}
	out := buf.String()
	for _, want := range []string{
		"managed (1)", "/h/.claude/skills/managed", // managed = the no-op link
		"out of sync (1)", "create", // missing = drift
		"notes", "note", "shadowed", "skipped", "memory/x", // non-blocking outcomes are shown
		"adoptable (1)", "skill/bark-pro", "codex-cli,copilot-cli", // both reading agents
		"1 managed, 1 out of sync, 1 skipped, 1 adoptable", // skipped surfaced in the summary
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "managed\n  /h/.claude/skills/missing") {
		t.Errorf("a drifting link must not be listed as managed\n%s", out)
	}
}

func TestRenderFailed(t *testing.T) {
	m := runtime.Model{Phase: runtime.PhaseFailed, FailedStage: "load-config", Err: errors.New("boom")}
	var buf bytes.Buffer
	if code := render(&buf, m); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(buf.String(), "failed during load-config: boom") {
		t.Errorf("failed render = %q", buf.String())
	}
}
