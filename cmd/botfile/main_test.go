package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"codeberg.org/botfile/botfile/internal/core"
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
