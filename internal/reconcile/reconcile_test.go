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
	desired := []LinkSpec{{Target: "/home/u/.claude/skills/go", Dest: "/src/team/p/skill/go", SourceName: "team"}}
	plan := Reconcile(desired, World{Entries: map[string]Entry{}}, opts())
	want := []Op{{Kind: OpCreate, Target: "/home/u/.claude/skills/go", Dest: "/src/team/p/skill/go"}}
	if !reflect.DeepEqual(plan.Ops, want) {
		t.Fatalf("ops = %+v, want %+v", plan.Ops, want)
	}
	if len(plan.Conflicts) != 0 || len(plan.Shadowed) != 0 {
		t.Fatalf("unexpected conflicts/shadows: %+v %+v", plan.Conflicts, plan.Shadowed)
	}
}

func TestNoOpWhenAlreadyCorrect(t *testing.T) {
	t.Parallel()
	desired := []LinkSpec{{Target: "/t/go", Dest: "/src/team/p/skill/go", SourceName: "team"}}
	world := World{Entries: map[string]Entry{
		"/t/go": {Kind: Symlink, Dest: "/src/team/p/skill/go"},
	}}
	plan := Reconcile(desired, world, opts())
	if len(plan.Ops) != 0 {
		t.Fatalf("expected no ops, got %+v", plan.Ops)
	}
}

func TestReplaceWhenManagedLinkWrong(t *testing.T) {
	t.Parallel()
	desired := []LinkSpec{{Target: "/t/go", Dest: "/src/team/p/skill/go", SourceName: "team"}}
	world := World{Entries: map[string]Entry{
		"/t/go": {Kind: Symlink, Dest: "/src/personal/old/skill/go"}, // managed (under a root) but stale
	}}
	plan := Reconcile(desired, world, opts())
	want := []Op{{Kind: OpReplace, Target: "/t/go", Dest: "/src/team/p/skill/go", OldDest: "/src/personal/old/skill/go"}}
	if !reflect.DeepEqual(plan.Ops, want) {
		t.Fatalf("ops = %+v, want %+v", plan.Ops, want)
	}
}

func TestOrphanRemoval(t *testing.T) {
	t.Parallel()
	world := World{Entries: map[string]Entry{
		"/t/gone": {Kind: Symlink, Dest: "/src/team/p/skill/gone"},
	}}
	plan := Reconcile(nil, world, opts())
	want := []Op{{Kind: OpRemove, Target: "/t/gone", OldDest: "/src/team/p/skill/gone"}}
	if !reflect.DeepEqual(plan.Ops, want) {
		t.Fatalf("ops = %+v, want %+v", plan.Ops, want)
	}
}

func TestForeignFileIsConflictNeverClobbered(t *testing.T) {
	t.Parallel()
	desired := []LinkSpec{{Target: "/t/go", Dest: "/src/team/p/skill/go", SourceName: "team"}}
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
	desired := []LinkSpec{{Target: "/t/go", Dest: "/src/team/p/skill/go", SourceName: "team"}}
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
		t.Fatalf("foreign, undesired symlink must be left alone, got %+v %+v", plan.Ops, plan.Conflicts)
	}
}

func TestPrecedenceWinnerAndShadow(t *testing.T) {
	t.Parallel()
	desired := []LinkSpec{
		{Target: "/t/go", Dest: "/src/personal/p/skill/go", SourceName: "personal"},
		{Target: "/t/go", Dest: "/src/team/p/skill/go", SourceName: "team"}, // team has higher precedence
	}
	plan := Reconcile(desired, World{Entries: map[string]Entry{}}, opts())
	want := []Op{{Kind: OpCreate, Target: "/t/go", Dest: "/src/team/p/skill/go"}}
	if !reflect.DeepEqual(plan.Ops, want) {
		t.Fatalf("winner ops = %+v, want %+v", plan.Ops, want)
	}
	if len(plan.Shadowed) != 1 {
		t.Fatalf("expected one shadow, got %+v", plan.Shadowed)
	}
	s := plan.Shadowed[0]
	if s.SourceName != "personal" || s.WonBy != "team" || s.Target != "/t/go" {
		t.Fatalf("shadow = %+v, want personal shadowed by team at /t/go", s)
	}
}

func TestDuplicateSameLinkIsNotShadow(t *testing.T) {
	t.Parallel()
	// The exact same link declared twice contributes nothing extra and is not
	// a precedence override.
	desired := []LinkSpec{
		{Target: "/t/go", Dest: "/src/team/p/skill/go", SourceName: "team"},
		{Target: "/t/go", Dest: "/src/team/p/skill/go", SourceName: "team"},
	}
	plan := Reconcile(desired, World{Entries: map[string]Entry{}}, opts())
	if len(plan.Shadowed) != 0 {
		t.Fatalf("identical duplicate must not shadow, got %+v", plan.Shadowed)
	}
	if len(plan.Ops) != 1 {
		t.Fatalf("expected one create, got %+v", plan.Ops)
	}
}

func TestDeterministicOrdering(t *testing.T) {
	t.Parallel()
	desired := []LinkSpec{
		{Target: "/t/c", Dest: "/src/team/p/skill/c", SourceName: "team"},
		{Target: "/t/a", Dest: "/src/team/p/skill/a", SourceName: "team"},
		{Target: "/t/b", Dest: "/src/team/p/skill/b", SourceName: "team"},
	}
	plan := Reconcile(desired, World{Entries: map[string]Entry{}}, opts())
	got := []string{plan.Ops[0].Target, plan.Ops[1].Target, plan.Ops[2].Target}
	want := []string{"/t/a", "/t/b", "/t/c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ops not sorted by target: got %v, want %v", got, want)
	}
}

func TestDestOutsideRootIsConflictNotCreate(t *testing.T) {
	t.Parallel()
	// A desired link whose destination is not under its source root is a planner
	// input bug; it must become a conflict, never a create that the next run
	// would see as foreign (manifesto 33).
	desired := []LinkSpec{{Target: "/t/go", Dest: "/elsewhere/go", SourceName: "team"}}
	plan := Reconcile(desired, World{Entries: map[string]Entry{}}, opts())
	if len(plan.Ops) != 0 {
		t.Fatalf("out-of-root dest must not create, got ops %+v", plan.Ops)
	}
	if len(plan.Conflicts) != 1 || plan.Conflicts[0].Target != "/t/go" {
		t.Fatalf("expected one conflict at /t/go, got %+v", plan.Conflicts)
	}
}

func TestUnknownSourceLinkIsConflict(t *testing.T) {
	t.Parallel()
	// A link from a source with no configured root cannot be classified as
	// managed, so it is rejected rather than created.
	desired := []LinkSpec{{Target: "/t/go", Dest: "/src/ghost/go", SourceName: "ghost"}}
	plan := Reconcile(desired, World{Entries: map[string]Entry{}}, opts())
	if len(plan.Ops) != 0 || len(plan.Conflicts) != 1 {
		t.Fatalf("unknown-source link must be a conflict with no ops, got ops=%+v conflicts=%+v", plan.Ops, plan.Conflicts)
	}
}

func TestSameSourceDifferentDestIsConflict(t *testing.T) {
	t.Parallel()
	// One source contributing two different destinations to the same target is
	// ambiguous: block the target instead of picking one by spelling (35).
	desired := []LinkSpec{
		{Target: "/t/go", Dest: "/src/team/a/skill/go", SourceName: "team"},
		{Target: "/t/go", Dest: "/src/team/b/skill/go", SourceName: "team"},
	}
	plan := Reconcile(desired, World{Entries: map[string]Entry{}}, opts())
	if len(plan.Ops) != 0 {
		t.Fatalf("ambiguous same-source target must not install, got ops %+v", plan.Ops)
	}
	if len(plan.Conflicts) != 1 || plan.Conflicts[0].Target != "/t/go" {
		t.Fatalf("expected one conflict at /t/go, got %+v", plan.Conflicts)
	}
	if len(plan.Shadowed) != 0 {
		t.Fatalf("a blocked target must not also shadow, got %+v", plan.Shadowed)
	}
}

func TestGlobalOpOrderingAcrossCreateAndRemove(t *testing.T) {
	t.Parallel()
	// A create for a high target plus an orphan removal for a low target must
	// come back globally sorted by target, not in discovery order.
	desired := []LinkSpec{{Target: "/t/z", Dest: "/src/team/p/skill/z", SourceName: "team"}}
	world := World{Entries: map[string]Entry{
		"/t/a": {Kind: Symlink, Dest: "/src/team/p/skill/a"}, // orphan, gets removed
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
