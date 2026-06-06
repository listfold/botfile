package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"codeberg.org/botfile/botfile/internal/adopt"
	"codeberg.org/botfile/botfile/internal/core"
	"codeberg.org/botfile/botfile/internal/discover"
	"codeberg.org/botfile/botfile/internal/output"
	"codeberg.org/botfile/botfile/internal/project"
	"codeberg.org/botfile/botfile/internal/reconcile"
	"codeberg.org/botfile/botfile/internal/runtime"
)

// errWriter fails every write, standing in for a closed pipe.
type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("pipe closed") }

// TestGuideDispatch covers the config-free help/guide surfaces: the right exit
// codes, format selection (including the markdown default for `guide` and the
// text default for `help`), the `agent` topic alias, valid JSON, and rejection
// of a bad format or stray argument. None of these load config.
func TestGuideDispatch(t *testing.T) {
	t.Parallel()

	var b bytes.Buffer
	if code := help(&b, nil); code != 0 {
		t.Fatalf("help exit = %d, want 0", code)
	}
	if out := b.String(); !strings.Contains(out, "MODEL") || !strings.Contains(out, "WORKFLOW") {
		t.Errorf("help text missing sections:\n%s", out)
	}

	b.Reset()
	if code := guideCmd(&b, nil); code != 0 {
		t.Fatalf("guide exit = %d, want 0", code)
	}
	if out := b.String(); !strings.Contains(out, "# botfile") || !strings.Contains(out, "## Model") {
		t.Errorf("guide default is not markdown:\n%s", out)
	}

	b.Reset()
	if code := help(&b, []string{"agent", "--format", "markdown"}); code != 0 {
		t.Fatalf("help agent exit = %d, want 0", code)
	}
	if out := b.String(); !strings.Contains(out, "## Model") {
		t.Errorf("help agent --format markdown not markdown:\n%s", out)
	}

	b.Reset()
	if code := emitGuide(&b, []string{"--format", "json"}, "text"); code != 0 {
		t.Fatalf("guide json exit = %d, want 0", code)
	}
	var doc struct {
		SchemaVersion int `json:"schemaVersion"`
	}
	if err := json.Unmarshal(b.Bytes(), &doc); err != nil {
		t.Fatalf("guide json invalid: %v\n%s", err, b.String())
	}
	if doc.SchemaVersion == 0 {
		t.Error("guide json missing schemaVersion")
	}

	if code := emitGuide(&bytes.Buffer{}, []string{"--format", "yaml"}, "text"); code != 2 {
		t.Errorf("bad format exit = %d, want 2", code)
	}
	if code := emitGuide(&bytes.Buffer{}, []string{"bogus"}, "text"); code != 2 {
		t.Errorf("stray arg exit = %d, want 2", code)
	}
}

// TestEmitWriteErrorKeepsExitCode pins that a write failure does not change the
// exit code, and does so identically for both renderers: Report.ExitCode stays
// authoritative regardless of format.
func TestEmitWriteErrorKeepsExitCode(t *testing.T) {
	t.Parallel()
	m := runtime.Model{Mode: runtime.ModePlan, Phase: runtime.PhaseBlocked, Blockers: []runtime.Blocker{{Kind: runtime.BlockerConflict, Cause: "conflict", Ref: "/t", Detail: "x"}}}
	want := output.ReportFromModel(m).ExitCode // blocked -> 1
	if c := emit(errWriter{}, m, "text"); c != want {
		t.Errorf("text exit on write error = %d, want %d", c, want)
	}
	if c := emit(errWriter{}, m, "json"); c != want {
		t.Errorf("json exit on write error = %d, want %d", c, want)
	}
}

// TestEmitFormatParity checks that the text and JSON renderers agree on the exit
// code for every outcome, and that the JSON form is always valid and carries the
// same code in its body.
func TestEmitFormatParity(t *testing.T) {
	t.Parallel()
	models := []runtime.Model{
		{Mode: runtime.ModePlan, Phase: runtime.PhaseDone, Plan: reconcile.Plan{Ops: []reconcile.Op{{Kind: reconcile.OpCreate, Target: "/t", Dest: "/d"}}}},
		{Mode: runtime.ModePlan, Phase: runtime.PhaseDone, Blockers: []runtime.Blocker{{Kind: runtime.BlockerConflict, Cause: "conflict", Ref: "/t", Detail: "x"}}},
		{Mode: runtime.ModeSync, Phase: runtime.PhaseBlocked, Blockers: []runtime.Blocker{{Kind: runtime.BlockerConflict, Cause: "conflict", Ref: "/t", Detail: "x"}}},
		{Mode: runtime.ModeStatus, Phase: runtime.PhaseDone},
		{Mode: runtime.ModeAdopt, Phase: runtime.PhaseBlocked, AdoptProblem: &adopt.Problem{Detail: "collision"}},
		{Mode: runtime.ModeSync, Phase: runtime.PhaseFailed, FailedStage: "apply", Err: errors.New("boom")},
	}
	for _, m := range models {
		var tb, jb bytes.Buffer
		tc := emit(&tb, m, "text")
		jc := emit(&jb, m, "json")
		if tc != jc {
			t.Errorf("%v/%v: text exit %d != json exit %d", m.Mode, m.Phase, tc, jc)
		}
		var doc struct {
			ExitCode int `json:"exitCode"`
		}
		if err := json.Unmarshal(jb.Bytes(), &doc); err != nil {
			t.Errorf("%v/%v: invalid json: %v\n%s", m.Mode, m.Phase, err, jb.String())
			continue
		}
		if doc.ExitCode != jc {
			t.Errorf("%v/%v: json body exitCode %d != returned %d", m.Mode, m.Phase, doc.ExitCode, jc)
		}
	}
}

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
			Problems: []project.Problem{{Kind: project.ProblemUnsupported, Agent: "codex-cli", Component: "instruction/x", Detail: "no instruction support"}},
		},
	}
	var buf bytes.Buffer
	if code := render(&buf, m); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	out := buf.String()
	for _, want := range []string{"create", "/h/.claude/skills/go -> /src/skills/go", "note", "shadowed", "skipped", "instruction/x", "synced: 1 operation"} {
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
			Problems: []project.Problem{{Kind: project.ProblemUnsupported, Agent: "codex-cli", Component: "instruction/x", Detail: "no instruction support"}},
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
		"notes", "note", "shadowed", "skipped", "instruction/x", // non-blocking outcomes are shown
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

func TestParseAdopt(t *testing.T) {
	t.Parallel()
	req, err := parseAdopt([]string{"/p/bark", "--into", "personal/mine"})
	if err != nil || req.Path != "/p/bark" || req.SourceName != "personal" || req.PluginName != "mine" {
		t.Fatalf("parseAdopt = %+v, %v", req, err)
	}
	if r, _ := parseAdopt([]string{"/p/bark", "--into=personal/mine"}); r.SourceName != "personal" || r.PluginName != "mine" {
		t.Errorf("--into= form = %+v", r)
	}
	bad := [][]string{
		{},                             // no path, no into
		{"/p"},                         // missing --into
		{"--into", "personal/mine"},    // missing path
		{"/p", "--into", "noplugin"},   // not source/plugin
		{"/p", "--into", "/mine"},      // empty source
		{"/p", "--into", "personal/"},  // empty plugin
		{"/p", "--bogus"},              // unknown flag
		{"/p", "/q", "--into", "s/pl"}, // extra argument
	}
	for _, args := range bad {
		if _, err := parseAdopt(args); err == nil {
			t.Errorf("parseAdopt(%v): want error, got nil", args)
		}
	}
}

func TestRenderAdopt(t *testing.T) {
	t.Parallel()
	// Success.
	done := runtime.Model{
		Mode: runtime.ModeAdopt, Phase: runtime.PhaseDone,
		Adopt:     adopt.Request{SourceName: "personal", PluginName: "mine"},
		AdoptPlan: adopt.Plan{Kind: core.KindSkill, Name: "bark", From: "/h/.claude/skills/bark", To: "/s/mine/skills/bark", AddSelection: &core.Selection{ComponentID: "skill/bark", Agents: []core.AgentID{core.AgentClaudeCode}}},
	}
	var buf bytes.Buffer
	if code := render(&buf, done); code != 0 {
		t.Fatalf("adopt done exit = %d, want 0", code)
	}
	for _, want := range []string{"move", "link", "select", "adopted skill/bark", "personal", "mine"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("adopt render missing %q\n%s", want, buf.String())
		}
	}
	// Blocked by a problem.
	blocked := runtime.Model{Mode: runtime.ModeAdopt, Phase: runtime.PhaseBlocked, AdoptProblem: &adopt.Problem{Kind: adopt.ProblemCollision, Detail: "already has skill/bark"}}
	buf.Reset()
	if code := render(&buf, blocked); code != 1 {
		t.Fatalf("adopt blocked exit = %d, want 1", code)
	}
	if !strings.Contains(buf.String(), "cannot adopt: already has skill/bark") {
		t.Errorf("adopt blocked render = %q", buf.String())
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
