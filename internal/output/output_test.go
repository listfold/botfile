package output

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"codeberg.org/botfile/botfile/internal/adopt"
	"codeberg.org/botfile/botfile/internal/core"
	"codeberg.org/botfile/botfile/internal/discover"
	"codeberg.org/botfile/botfile/internal/project"
	"codeberg.org/botfile/botfile/internal/reconcile"
	"codeberg.org/botfile/botfile/internal/runtime"
)

func TestReportExitCodes(t *testing.T) {
	t.Parallel()
	oneBlocker := []runtime.Blocker{{Kind: runtime.BlockerPlanProblem, Cause: "ambiguous-target", Ref: "/t", Detail: "x"}}
	cases := []struct {
		name    string
		m       runtime.Model
		outcome string
		code    int
	}{
		{"plan clean", runtime.Model{Mode: runtime.ModePlan, Phase: runtime.PhaseDone}, "ok", 0},
		{"plan with blockers", runtime.Model{Mode: runtime.ModePlan, Phase: runtime.PhaseDone, Blockers: oneBlocker}, "blocked", 1},
		{"plan blocked", runtime.Model{Mode: runtime.ModePlan, Phase: runtime.PhaseBlocked, Blockers: oneBlocker}, "blocked", 1},
		{"sync done", runtime.Model{Mode: runtime.ModeSync, Phase: runtime.PhaseDone}, "ok", 0},
		{"sync blocked", runtime.Model{Mode: runtime.ModeSync, Phase: runtime.PhaseBlocked, Blockers: oneBlocker}, "blocked", 1},
		{"status", runtime.Model{Mode: runtime.ModeStatus, Phase: runtime.PhaseDone}, "ok", 0},
		{"adopt done", runtime.Model{Mode: runtime.ModeAdopt, Phase: runtime.PhaseDone}, "ok", 0},
		{"adopt blocked", runtime.Model{Mode: runtime.ModeAdopt, Phase: runtime.PhaseBlocked, AdoptProblem: &adopt.Problem{Detail: "collision"}}, "blocked", 1},
		{"failed", runtime.Model{Mode: runtime.ModeSync, Phase: runtime.PhaseFailed, FailedStage: "apply", Err: errors.New("boom")}, "failed", 2},
	}
	for _, tc := range cases {
		r := ReportFromModel(tc.m)
		if r.Outcome != tc.outcome || r.ExitCode != tc.code {
			t.Errorf("%s: outcome=%q code=%d, want %q/%d", tc.name, r.Outcome, r.ExitCode, tc.outcome, tc.code)
		}
	}
}

func TestReportClassifiesCategories(t *testing.T) {
	t.Parallel()
	m := runtime.Model{
		Mode: runtime.ModePlan, Phase: runtime.PhaseBlocked,
		Plan: reconcile.Plan{
			Ops:     []reconcile.Op{{Kind: reconcile.OpCreate, Target: "/t", Dest: "/d"}},
			Shadows: []reconcile.Shadow{{Target: "/s", SourceName: "a", WonBy: "b"}},
		},
		Blockers: []runtime.Blocker{{Kind: runtime.BlockerPlanProblem, Cause: "ambiguous-target", Ref: "/t", Detail: "two dests"}},
		Projection: project.Result{
			Notices:  []project.Notice{{Kind: project.NoticeSharedSkillNamespace, Selected: []core.AgentID{"codex-cli"}, AlsoReaches: []core.AgentID{"opencode"}, Namespace: "/ns"}},
			Problems: []project.Problem{{Kind: project.ProblemUnsupported, Component: "instruction/x", Agent: "codex-cli", Detail: "no support"}},
		},
	}
	r := ReportFromModel(m)

	if len(r.Ops) != 1 || r.Ops[0].Kind != "create" || r.Ops[0].Dest != "/d" {
		t.Fatalf("ops = %+v", r.Ops)
	}
	if len(r.Issues) != 1 || r.Issues[0].Kind != "plan-problem" || r.Issues[0].Cause != "ambiguous-target" {
		t.Fatalf("issues = %+v", r.Issues)
	}
	// Notes are notices, then shadows, then skipped, in that order.
	if len(r.Notes) != 3 || r.Notes[0].Kind != "notice" || r.Notes[1].Kind != "shadowed" || r.Notes[2].Kind != "skipped" {
		t.Fatalf("notes = %+v", r.Notes)
	}
	if r.Summary.Ops != 1 || r.Summary.Issues != 1 || r.Summary.Skipped != 1 || r.Summary.OutOfSync != 2 {
		t.Fatalf("summary = %+v", r.Summary)
	}
}

func TestReportStatusManagedSorted(t *testing.T) {
	t.Parallel()
	m := runtime.Model{
		Mode: runtime.ModeStatus, Phase: runtime.PhaseDone,
		Projection: project.Result{Links: []reconcile.LinkSpec{
			{Target: "/b"}, {Target: "/a"}, {Target: "/a"}, // duplicate collapses
		}},
		Unmanaged: []discover.Unmanaged{{Agents: []core.AgentID{core.AgentClaudeCode}, Kind: core.KindSkill, Name: "bark", Path: "/h/.claude/skills/bark"}},
	}
	r := ReportFromModel(m)
	if r.Status == nil || len(r.Status.Managed) != 2 || r.Status.Managed[0] != "/a" || r.Status.Managed[1] != "/b" {
		t.Fatalf("managed = %+v", r.Status)
	}
	if len(r.Status.Adoptable) != 1 || r.Status.Adoptable[0].Ref != "skill/bark" {
		t.Fatalf("adoptable = %+v", r.Status.Adoptable)
	}
	if r.Summary.Managed != 2 || r.Summary.Adoptable != 1 {
		t.Fatalf("summary = %+v", r.Summary)
	}
}

func TestReportAdoptSelect(t *testing.T) {
	t.Parallel()
	m := runtime.Model{
		Mode: runtime.ModeAdopt, Phase: runtime.PhaseDone,
		Adopt:     adopt.Request{SourceName: "personal", PluginName: "mine"},
		AdoptPlan: adopt.Plan{Kind: core.KindSkill, Name: "bark", From: "/f", To: "/t", AddSelection: &core.Selection{ComponentID: "skill/bark", Agents: []core.AgentID{core.AgentClaudeCode}}},
	}
	r := ReportFromModel(m)
	if r.Adopt == nil || r.Adopt.Select == nil || r.Adopt.Select.ComponentID != "skill/bark" {
		t.Fatalf("adopt = %+v", r.Adopt)
	}
	if r.Adopt.Move.From != "/f" || r.Adopt.Kind != "skill" || r.Adopt.Source != "personal" {
		t.Fatalf("adopt = %+v", r.Adopt)
	}
}

// TestRenderTextNoticeParity pins the load-bearing detail that the notice line
// formats the agent slices with %v (bracketed), identical to the historical
// renderer, by asserting against the shared copy template.
func TestRenderTextNoticeParity(t *testing.T) {
	t.Parallel()
	m := runtime.Model{
		Mode: runtime.ModePlan, Phase: runtime.PhaseDone,
		Projection: project.Result{Notices: []project.Notice{{
			Kind: project.NoticeSharedSkillNamespace, Selected: []core.AgentID{"codex-cli"},
			AlsoReaches: []core.AgentID{"opencode", "copilot-cli"}, Namespace: "/h/.agents/skills",
		}}},
	}
	var buf bytes.Buffer
	RenderText(&buf, ReportFromModel(m))
	want := fmt.Sprintf(lineNotice, []string{"codex-cli"}, []string{"opencode", "copilot-cli"}, "/h/.agents/skills")
	if !strings.Contains(buf.String(), want) {
		t.Fatalf("notice line missing\n got: %q\nwant substring: %q", buf.String(), want)
	}
}
