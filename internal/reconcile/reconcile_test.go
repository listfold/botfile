package reconcile

import (
	"reflect"
	"testing"
)

var defaultRoots = []Root{
	{Name: "team", Path: "/src/team"},
	{Name: "personal", Path: "/src/personal"},
}

func opts() Options { return Options{Roots: defaultRoots} }

func TestCreateWhenAbsent(t *testing.T) {
	t.Parallel()
	desired := []LinkSpec{{Target: "/home/u/.claude/skills/go", Dest: "/src/team/p/skills/go", SourceName: "team"}}
	plan := Reconcile(desired, World{Entries: map[string]Entry{}}, opts())
	want := []Op{{Kind: OpCreate, Target: "/home/u/.claude/skills/go", Dest: "/src/team/p/skills/go"}}
	if !reflect.DeepEqual(plan.Ops, want) {
		t.Fatalf("ops = %+v, want %+v", plan.Ops, want)
	}
	if len(plan.Conflicts) != 0 || len(plan.Shadows) != 0 || len(plan.Problems) != 0 {
		t.Fatalf("unexpected non-op outcomes: %+v", plan)
	}
}

func TestNoOpWhenAlreadyCorrect(t *testing.T) {
	t.Parallel()
	desired := []LinkSpec{{Target: "/t/go", Dest: "/src/team/p/skills/go", SourceName: "team"}}
	world := World{Entries: map[string]Entry{
		"/t/go": {Kind: Symlink, Dest: "/src/team/p/skills/go"},
	}}
	plan := Reconcile(desired, world, opts())
	if len(plan.Ops) != 0 {
		t.Fatalf("expected no ops, got %+v", plan.Ops)
	}
}

func TestReplaceWhenManagedLinkWrong(t *testing.T) {
	t.Parallel()
	desired := []LinkSpec{{Target: "/t/go", Dest: "/src/team/p/skills/go", SourceName: "team"}}
	world := World{Entries: map[string]Entry{
		"/t/go": {Kind: Symlink, Dest: "/src/personal/old/skills/go"}, // managed (under a root) but stale
	}}
	plan := Reconcile(desired, world, opts())
	want := []Op{{Kind: OpReplace, Target: "/t/go", Dest: "/src/team/p/skills/go", OldDest: "/src/personal/old/skills/go"}}
	if !reflect.DeepEqual(plan.Ops, want) {
		t.Fatalf("ops = %+v, want %+v", plan.Ops, want)
	}
}

func TestOrphanRemoval(t *testing.T) {
	t.Parallel()
	world := World{Entries: map[string]Entry{
		"/t/gone": {Kind: Symlink, Dest: "/src/team/p/skills/gone"},
	}}
	plan := Reconcile(nil, world, opts())
	want := []Op{{Kind: OpRemove, Target: "/t/gone", OldDest: "/src/team/p/skills/gone"}}
	if !reflect.DeepEqual(plan.Ops, want) {
		t.Fatalf("ops = %+v, want %+v", plan.Ops, want)
	}
}

func TestForeignFileIsConflictNeverClobbered(t *testing.T) {
	t.Parallel()
	desired := []LinkSpec{{Target: "/t/go", Dest: "/src/team/p/skills/go", SourceName: "team"}}
	world := World{Entries: map[string]Entry{
		"/t/go": {Kind: Foreign},
	}}
	plan := Reconcile(desired, world, opts())
	if len(plan.Ops) != 0 {
		t.Fatalf("a foreign file must never be clobbered, got ops %+v", plan.Ops)
	}
	if len(plan.Conflicts) != 1 || plan.Conflicts[0].Target != "/t/go" {
		t.Fatalf("expected one conflict at /t/go, got %+v", plan.Conflicts)
	}
}

func TestForeignSymlinkIsConflict(t *testing.T) {
	t.Parallel()
	desired := []LinkSpec{{Target: "/t/go", Dest: "/src/team/p/skills/go", SourceName: "team"}}
	world := World{Entries: map[string]Entry{
		"/t/go": {Kind: Symlink, Dest: "/somewhere/else/go"}, // not under any root => not ours
	}}
	plan := Reconcile(desired, world, opts())
	if len(plan.Ops) != 0 {
		t.Fatalf("a foreign symlink must not be clobbered, got ops %+v", plan.Ops)
	}
	if len(plan.Conflicts) != 1 {
		t.Fatalf("expected one conflict, got %+v", plan.Conflicts)
	}
}

func TestForeignSymlinkNotDesiredIsLeftAlone(t *testing.T) {
	t.Parallel()
	world := World{Entries: map[string]Entry{
		"/t/mine": {Kind: Symlink, Dest: "/somewhere/else"}, // user's own symlink, not desired
	}}
	plan := Reconcile(nil, world, opts())
	if len(plan.Ops) != 0 || len(plan.Conflicts) != 0 {
		t.Fatalf("foreign, undesired symlink must be left alone, got %+v", plan)
	}
}

func TestPrecedenceWinnerAndShadow(t *testing.T) {
	t.Parallel()
	desired := []LinkSpec{
		{Target: "/t/go", Dest: "/src/personal/p/skills/go", SourceName: "personal"},
		{Target: "/t/go", Dest: "/src/team/p/skills/go", SourceName: "team"}, // team has higher precedence
	}
	plan := Reconcile(desired, World{Entries: map[string]Entry{}}, opts())
	want := []Op{{Kind: OpCreate, Target: "/t/go", Dest: "/src/team/p/skills/go"}}
	if !reflect.DeepEqual(plan.Ops, want) {
		t.Fatalf("winner ops = %+v, want %+v", plan.Ops, want)
	}
	if len(plan.Shadows) != 1 {
		t.Fatalf("expected one shadow, got %+v", plan.Shadows)
	}
	s := plan.Shadows[0]
	if s.SourceName != "personal" || s.WonBy != "team" || s.Target != "/t/go" {
		t.Fatalf("shadow = %+v, want personal shadowed by team at /t/go", s)
	}
}

func TestDuplicateSameLinkIsNotShadow(t *testing.T) {
	t.Parallel()
	// The exact same link declared twice contributes nothing extra and is not a
	// precedence override.
	desired := []LinkSpec{
		{Target: "/t/go", Dest: "/src/team/p/skills/go", SourceName: "team"},
		{Target: "/t/go", Dest: "/src/team/p/skills/go", SourceName: "team"},
	}
	plan := Reconcile(desired, World{Entries: map[string]Entry{}}, opts())
	if len(plan.Shadows) != 0 {
		t.Fatalf("identical duplicate must not shadow, got %+v", plan.Shadows)
	}
	if len(plan.Ops) != 1 {
		t.Fatalf("expected one create, got %+v", plan.Ops)
	}
}

func TestDestOutsideRootIsProblemNotCreate(t *testing.T) {
	t.Parallel()
	// A desired link whose destination is not under its source root is an invalid
	// desired model (a scanner/projection bug): a Problem, never a Conflict and
	// never a create (manifesto 33).
	desired := []LinkSpec{{Target: "/t/go", Dest: "/elsewhere/go", SourceName: "team"}}
	plan := Reconcile(desired, World{Entries: map[string]Entry{}}, opts())
	if len(plan.Ops) != 0 || len(plan.Conflicts) != 0 {
		t.Fatalf("out-of-root dest must not create or conflict, got %+v", plan)
	}
	if len(plan.Problems) != 1 || plan.Problems[0].Kind != ProblemDestOutsideRoot {
		t.Fatalf("expected one ProblemDestOutsideRoot, got %+v", plan.Problems)
	}
}

func TestUnknownSourceLinkIsProblem(t *testing.T) {
	t.Parallel()
	// A link from a source with no configured root cannot be validated, so it is
	// a Problem (unknown source), not a Conflict and not an op.
	desired := []LinkSpec{{Target: "/t/go", Dest: "/src/ghost/go", SourceName: "ghost"}}
	plan := Reconcile(desired, World{Entries: map[string]Entry{}}, opts())
	if len(plan.Ops) != 0 || len(plan.Conflicts) != 0 {
		t.Fatalf("unknown-source link must not create or conflict, got %+v", plan)
	}
	if len(plan.Problems) != 1 || plan.Problems[0].Kind != ProblemUnknownSource {
		t.Fatalf("expected one ProblemUnknownSource, got %+v", plan.Problems)
	}
}

func TestSameSourceDifferentDestIsAmbiguousProblem(t *testing.T) {
	t.Parallel()
	// One source contributing two different destinations to the same target is
	// ambiguous: a Problem that blocks the target, not a Shadow and not an op (35).
	desired := []LinkSpec{
		{Target: "/t/go", Dest: "/src/team/a/skills/go", SourceName: "team"},
		{Target: "/t/go", Dest: "/src/team/b/skills/go", SourceName: "team"},
	}
	plan := Reconcile(desired, World{Entries: map[string]Entry{}}, opts())
	if len(plan.Ops) != 0 {
		t.Fatalf("ambiguous same-source target must not install, got ops %+v", plan.Ops)
	}
	if len(plan.Shadows) != 0 {
		t.Fatalf("a blocked target must not also shadow, got %+v", plan.Shadows)
	}
	if len(plan.Problems) != 1 || plan.Problems[0].Kind != ProblemAmbiguousTarget {
		t.Fatalf("expected one ProblemAmbiguousTarget, got %+v", plan.Problems)
	}
}

func TestLocalizedProblemStillPlansRest(t *testing.T) {
	t.Parallel()
	// A Problem on one target must not suppress a clean plan for another: the
	// planner stays non-judgmental and localizes the problem.
	desired := []LinkSpec{
		{Target: "/t/bad", Dest: "/elsewhere/x", SourceName: "team"},          // ProblemDestOutsideRoot
		{Target: "/t/good", Dest: "/src/team/p/skills/x", SourceName: "team"}, // clean create
	}
	plan := Reconcile(desired, World{Entries: map[string]Entry{}}, opts())
	if len(plan.Problems) != 1 {
		t.Fatalf("expected one problem, got %+v", plan.Problems)
	}
	if len(plan.Ops) != 1 || plan.Ops[0].Target != "/t/good" || plan.Ops[0].Kind != OpCreate {
		t.Fatalf("expected a clean create for /t/good alongside the problem, got %+v", plan.Ops)
	}
}

func TestGlobalOpOrderingAcrossCreateAndRemove(t *testing.T) {
	t.Parallel()
	// A create for a high target plus an orphan removal for a low target must
	// come back globally sorted by target, not in discovery order.
	desired := []LinkSpec{{Target: "/t/z", Dest: "/src/team/p/skills/z", SourceName: "team"}}
	world := World{Entries: map[string]Entry{
		"/t/a": {Kind: Symlink, Dest: "/src/team/p/skills/a"}, // orphan, gets removed
	}}
	plan := Reconcile(desired, world, opts())
	if len(plan.Ops) != 2 {
		t.Fatalf("expected 2 ops, got %+v", plan.Ops)
	}
	if plan.Ops[0].Target != "/t/a" || plan.Ops[0].Kind != OpRemove {
		t.Errorf("first op = %+v, want remove /t/a", plan.Ops[0])
	}
	if plan.Ops[1].Target != "/t/z" || plan.Ops[1].Kind != OpCreate {
		t.Errorf("second op = %+v, want create /t/z", plan.Ops[1])
	}
}

func TestUnderRootSegmentBoundary(t *testing.T) {
	t.Parallel()
	// A symlink into "/src/team-archive" must not count as managed by root
	// "/src/team": prefix matching has to respect segment boundaries.
	o := Options{Roots: []Root{{Name: "team", Path: "/src/team"}}}
	if o.managed("/src/team-archive/x") {
		t.Error("/src/team-archive must not be under /src/team")
	}
	if !o.managed("/src/team/x") {
		t.Error("/src/team/x must be under /src/team")
	}
}
