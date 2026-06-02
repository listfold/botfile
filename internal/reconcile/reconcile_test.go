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
