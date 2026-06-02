// Package reconcile is botfile's pure planner. Given the raw desired links and
// the observed filesystem state, it computes a Plan: the operations that would
// make the filesystem match the declaration, the conflicts blocking it, the
// links shadowed by precedence, and the problems in the desired model itself. It
// is a total function with no I/O, no clock, and no environment access
// (manifesto 42); the side-effecting ports that produce its inputs (the source
// scanner, the per-agent install-path projection, the World reader) and that
// apply its output live in other packages.
//
// It is structured as a pure pipeline, one concern per stage, no stage relying
// on the append order of another (see reviews/patterns.md, "normalize-then-plan"
// and "explicit outcome algebra"):
//
//	raw []LinkSpec
//	  -> prepare  : parse each link into a validated DesiredLink, or a Problem
//	  -> resolve  : group by target, dedup, pick the precedence winner; Shadows + Problems
//	  -> planOps  : compare winners to the World; Ops + Conflicts (the only stage that reads the World)
//	  -> sortPlan : one deterministic sort per output slice
//
// The rules it encodes come straight from the manifesto:
//
//   - botfile owns only the symlink it creates (33): a symlink is botfile's iff
//     its destination lies under a known source root. A foreign symlink or a
//     real file is never touched.
//   - botfile imposes no ordering among siblings (34): the plan is sorted only
//     for determinism, never to influence how a host loads the directory.
//   - source precedence resolves a single winner when two sources contribute the
//     same target path (35); an unmanaged non-symlink already there is a conflict
//     botfile reports and never clobbers.
package reconcile

import (
	"path/filepath"
	"sort"
)

// LinkSpec is the raw, unvalidated desired link produced by the projection
// layer: a symlink that should live at Target and point at Dest, contributed by
// the source named SourceName. It is raw because SourceName is a loose string
// and Dest is unconstrained; prepare turns each one into a validated DesiredLink
// or a Problem.
type LinkSpec struct {
	Target     string // absolute path of the symlink itself, in the agent's scanned directory
	Dest       string // absolute path it points at: the component inside the source
	SourceName string // the core.Source.Name that contributed this link
}

// Root is a botfile source root: a name and the absolute directory botfile
// manages out of. The order of Options.Roots is the source precedence order,
// highest precedence first (manifesto 35).
type Root struct {
	Name string
	Path string
}

// Options carries the source roots in precedence order (highest first). The
// roots serve three purposes: validating that a desired link points into its own
// source (prepare), resolving precedence between sources (resolve), and
// classifying which observed symlinks are botfile-managed (planOps, per 33).
type Options struct {
	Roots []Root
}

// EntryKind classifies what the World observed at a target path.
type EntryKind int

const (
	// Absent: nothing exists at the path.
	Absent EntryKind = iota
	// Symlink: a symlink exists; Entry.Dest holds its destination. Whether it is
	// botfile-managed is decided in planOps, by testing Dest against the roots.
	Symlink
	// Foreign: a real file or directory (not a symlink) exists. Never botfile's.
	Foreign
)

// Entry is the observed state at a single target path.
type Entry struct {
	Kind EntryKind
	Dest string // destination of an observed Symlink; empty otherwise
}

// World is the observed filesystem state, keyed by absolute target path. It is
// produced by an I/O port (a later slice); reconcile only reads it.
type World struct {
	Entries map[string]Entry
}

// DesiredLink is a validated desired link: by construction its Dest lies under
// Source.Path, so "a destination outside its source root" is unrepresentable in
// the core (parse, don't validate). It is produced only by newDesiredLink.
type DesiredLink struct {
	Target string
	Dest   string
	Source Root
	rank   int // precedence index of Source (lower = higher precedence)
}

// newDesiredLink is the single gate that turns a raw target/dest plus a known
// source root into a validated DesiredLink. It fails (ok == false) when the
// destination does not lie under the source root, which is the only way an
// invalid dest is kept out of the core.
func newDesiredLink(target, dest string, source Root, rank int) (DesiredLink, bool) {
	if !underRoot(filepath.Clean(dest), filepath.Clean(source.Path)) {
		return DesiredLink{}, false
	}
	return DesiredLink{Target: target, Dest: dest, Source: source, rank: rank}, true
}

// OpKind is the kind of filesystem operation a Plan prescribes.
type OpKind int

const (
	// OpCreate: create a new managed symlink where nothing exists.
	OpCreate OpKind = iota
	// OpReplace: a managed symlink exists but points at the wrong destination;
	// retarget it. OldDest carries the existing destination for undo.
	OpReplace
	// OpRemove: a managed symlink is no longer desired (an orphan); remove it.
	// OldDest carries the destination being removed for undo.
	OpRemove
)

// Op is a single planned filesystem operation. The applier turns it into a real
// symlink mutation; the undo stack uses OldDest to reverse it.
type Op struct {
	Kind    OpKind
	Target  string
	Dest    string // new destination for OpCreate and OpReplace
	OldDest string // prior destination for OpReplace and OpRemove
}

// Conflict is a valid desire blocked by observed filesystem state botfile does
// not own (manifesto 35). It is distinct from a Problem: the desire is sound,
// the filesystem is in the way, and the user can resolve it (move the file).
// botfile reports it and never clobbers.
type Conflict struct {
	Target     string
	Dest       string // the desired destination that could not be installed
	SourceName string // the source whose link is blocked
	Reason     string
}

// Shadow is a valid desire overridden at its target by a higher-precedence
// source: the other source won the single slot (manifesto 35). It is
// informational, surfaced so an overridden selection is visible, not silent.
type Shadow struct {
	Target     string
	Dest       string
	SourceName string // the source whose link was shadowed
	WonBy      string // the source whose link won the target
}

// ProblemKind classifies a defect in the desired model itself: an input bug from
// the scanner, projection, or config, not a filesystem condition. This is the
// branch of the outcome algebra that Conflict must never absorb.
type ProblemKind int

const (
	// ProblemUnknownSource: a link names a source with no configured root.
	ProblemUnknownSource ProblemKind = iota
	// ProblemDestOutsideRoot: a link's destination is not under its source root.
	ProblemDestOutsideRoot
	// ProblemAmbiguousTarget: the highest-precedence source at a target offers
	// more than one destination, so precedence cannot pick a single winner.
	ProblemAmbiguousTarget
)

// String renders a ProblemKind as a stable, human-readable token.
func (k ProblemKind) String() string {
	switch k {
	case ProblemUnknownSource:
		return "unknown-source"
	case ProblemDestOutsideRoot:
		return "dest-outside-root"
	case ProblemAmbiguousTarget:
		return "ambiguous-target"
	default:
		return "unknown-problem"
	}
}

// Problem is an invalid desired-model condition (an input bug). It is reported
// so the runtime can refuse to apply a plan built from a broken model, distinct
// from a Conflict (which is reported and may be skipped). The pure planner stays
// non-judgmental: it localizes a Problem to its target and still plans the rest;
// whether any Problem blocks the whole apply is a policy the runtime decides.
type Problem struct {
	Kind       ProblemKind
	Target     string
	Dest       string
	SourceName string
	Detail     string
}

// Plan is the result of reconciliation. Ops is the only thing the interpreter
// applies; Conflicts, Shadows, and Problems are the three explicit outcome
// branches for inputs that did not become operations. All slices are sorted
// deterministically so an equal input always yields an equal Plan.
type Plan struct {
	Ops       []Op
	Conflicts []Conflict
	Shadows   []Shadow
	Problems  []Problem
}

// Reconcile computes the Plan that would make world match raw under opts. It is
// pure and total: equal inputs always yield an equal Plan.
func Reconcile(raw []LinkSpec, world World, opts Options) Plan {
	prepared, prepareProblems := prepare(raw, opts)
	winners, shadows, resolveProblems := resolve(prepared)
	ops, conflicts := planOps(winners, world, opts)

	plan := Plan{
		Ops:       ops,
		Conflicts: conflicts,
		Shadows:   shadows,
		Problems:  append(prepareProblems, resolveProblems...),
	}
	sortPlan(&plan)
	return plan
}

// prepare validates each raw link into a DesiredLink tied to its source root, or
// records a Problem. A link whose source has no configured root, or whose
// destination is not under that root, is an invalid desired model (a scanner or
// projection bug), not a filesystem condition (manifesto 33). Keeping that out
// of the planner makes Reconcile idempotent: it never creates a symlink the next
// run would classify as unmanaged.
func prepare(raw []LinkSpec, opts Options) ([]DesiredLink, []Problem) {
	rank := make(map[string]int, len(opts.Roots))
	root := make(map[string]Root, len(opts.Roots))
	for i, r := range opts.Roots {
		rank[r.Name] = i
		root[r.Name] = r
	}

	links := make([]DesiredLink, 0, len(raw))
	var problems []Problem
	for _, l := range raw {
		r, ok := root[l.SourceName]
		if !ok {
			problems = append(problems, Problem{
				Kind: ProblemUnknownSource, Target: l.Target, Dest: l.Dest, SourceName: l.SourceName,
				Detail: "source has no configured root",
			})
			continue
		}
		dl, ok := newDesiredLink(l.Target, l.Dest, r, rank[l.SourceName])
		if !ok {
			problems = append(problems, Problem{
				Kind: ProblemDestOutsideRoot, Target: l.Target, Dest: l.Dest, SourceName: l.SourceName,
				Detail: "destination is not under the root of its source",
			})
			continue
		}
		links = append(links, dl)
	}
	return links, problems
}

// resolve groups validated links by target, collapses exact duplicates, and
// picks the highest-precedence winner at each target; lower-precedence links
// become Shadows (manifesto 35). Precedence applies strictly between sources: if
// the links sharing the top precedence disagree on destination (for example one
// source contributing two destinations to one path), the target is ambiguous and
// yields a Problem with no winner, rather than a winner chosen by spelling.
func resolve(links []DesiredLink) (map[string]DesiredLink, []Shadow, []Problem) {
	byTarget := make(map[string][]DesiredLink)
	for _, l := range links {
		byTarget[l.Target] = append(byTarget[l.Target], l)
	}

	winners := make(map[string]DesiredLink, len(byTarget))
	var shadows []Shadow
	var problems []Problem

	for target, group := range byTarget {
		uniq := dedupLinks(group)

		// Stable order: precedence first, then source name, then dest, so the
		// winner and the shadow ordering are deterministic.
		sort.Slice(uniq, func(i, j int) bool {
			if uniq[i].rank != uniq[j].rank {
				return uniq[i].rank < uniq[j].rank
			}
			if uniq[i].Source.Name != uniq[j].Source.Name {
				return uniq[i].Source.Name < uniq[j].Source.Name
			}
			return uniq[i].Dest < uniq[j].Dest
		})

		// If the links sharing the top precedence disagree on destination, the
		// target is ambiguous: report a Problem and install nothing here (35).
		topRank := uniq[0].rank
		topDests := make(map[string]bool)
		for _, l := range uniq {
			if l.rank == topRank {
				topDests[filepath.Clean(l.Dest)] = true
			}
		}
		if len(topDests) > 1 {
			problems = append(problems, Problem{
				Kind: ProblemAmbiguousTarget, Target: target, Dest: uniq[0].Dest, SourceName: uniq[0].Source.Name,
				Detail: "the highest-precedence source contributes more than one destination to this path",
			})
			continue
		}

		win := uniq[0]
		winners[target] = win
		for _, lost := range uniq[1:] {
			shadows = append(shadows, Shadow{
				Target: target, Dest: lost.Dest, SourceName: lost.Source.Name, WonBy: win.Source.Name,
			})
		}
	}

	return winners, shadows, problems
}

// dedupLinks collapses exact duplicates (same source and destination): a link
// declared twice contributes nothing and is not a precedence override.
func dedupLinks(group []DesiredLink) []DesiredLink {
	type srcDest struct{ source, dest string }
	seen := make(map[srcDest]bool, len(group))
	uniq := make([]DesiredLink, 0, len(group))
	for _, l := range group {
		k := srcDest{l.Source.Name, filepath.Clean(l.Dest)}
		if seen[k] {
			continue
		}
		seen[k] = true
		uniq = append(uniq, l)
	}
	return uniq
}

// planOps is the only stage that reads the World. For each desired winner it
// decides create / replace / no-op or a Conflict against observed state; for
// each observed-but-undesired target it removes a botfile-managed orphan and
// leaves everything else untouched (manifesto 33-35).
func planOps(winners map[string]DesiredLink, world World, opts Options) ([]Op, []Conflict) {
	var ops []Op
	var conflicts []Conflict

	for target, want := range winners {
		entry := world.Entries[target]
		switch entry.Kind {
		case Absent:
			ops = append(ops, Op{Kind: OpCreate, Target: target, Dest: want.Dest})
		case Symlink:
			switch {
			case !opts.managed(entry.Dest):
				// A symlink botfile did not create: not ours, never clobber (33, 35).
				conflicts = append(conflicts, Conflict{
					Target: target, Dest: want.Dest, SourceName: want.Source.Name,
					Reason: "a symlink not managed by botfile already exists at this path",
				})
			case sameLink(entry.Dest, want.Dest):
				// Already correct: no operation.
			default:
				ops = append(ops, Op{Kind: OpReplace, Target: target, Dest: want.Dest, OldDest: entry.Dest})
			}
		case Foreign:
			conflicts = append(conflicts, Conflict{
				Target: target, Dest: want.Dest, SourceName: want.Source.Name,
				Reason: "a non-symlink file or directory already exists at this path",
			})
		}
	}

	for target, entry := range world.Entries {
		if _, isDesired := winners[target]; isDesired {
			continue
		}
		// Only a botfile-managed symlink is an orphan we remove; foreign entries
		// and foreign symlinks are left untouched (33).
		if entry.Kind == Symlink && opts.managed(entry.Dest) {
			ops = append(ops, Op{Kind: OpRemove, Target: target, OldDest: entry.Dest})
		}
	}

	return ops, conflicts
}

// sortPlan sorts every output slice once, deterministically, so no stage relies
// on append order for final output (manifesto 34: ordering here is for
// reproducibility, never to influence host load order).
func sortPlan(p *Plan) {
	sort.Slice(p.Ops, func(i, j int) bool {
		if p.Ops[i].Target != p.Ops[j].Target {
			return p.Ops[i].Target < p.Ops[j].Target
		}
		return p.Ops[i].Kind < p.Ops[j].Kind
	})
	sort.Slice(p.Conflicts, func(i, j int) bool {
		if p.Conflicts[i].Target != p.Conflicts[j].Target {
			return p.Conflicts[i].Target < p.Conflicts[j].Target
		}
		return p.Conflicts[i].SourceName < p.Conflicts[j].SourceName
	})
	sort.Slice(p.Shadows, func(i, j int) bool {
		if p.Shadows[i].Target != p.Shadows[j].Target {
			return p.Shadows[i].Target < p.Shadows[j].Target
		}
		return p.Shadows[i].SourceName < p.Shadows[j].SourceName
	})
	sort.Slice(p.Problems, func(i, j int) bool {
		if p.Problems[i].Target != p.Problems[j].Target {
			return p.Problems[i].Target < p.Problems[j].Target
		}
		if p.Problems[i].Kind != p.Problems[j].Kind {
			return p.Problems[i].Kind < p.Problems[j].Kind
		}
		return p.Problems[i].SourceName < p.Problems[j].SourceName
	})
}

// managed reports whether dest lies under one of the source roots, which is how
// reconcile decides a symlink is botfile's own (33).
func (o Options) managed(dest string) bool {
	clean := filepath.Clean(dest)
	for _, r := range o.Roots {
		if underRoot(clean, filepath.Clean(r.Path)) {
			return true
		}
	}
	return false
}

// underRoot reports whether path equals root or sits beneath it, comparing whole
// path segments so that "/srv/foo-bar" is not considered under "/srv/foo".
func underRoot(path, root string) bool {
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	// rel must not climb out of root.
	return rel != ".." && !hasDotDotPrefix(rel)
}

func hasDotDotPrefix(rel string) bool {
	return len(rel) >= 3 && rel[0] == '.' && rel[1] == '.' && (rel[2] == filepath.Separator || rel[2] == '/')
}

// sameLink compares two symlink destinations for equality after cleaning, so
// equivalent spellings of the same path are treated as already-correct.
func sameLink(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}
