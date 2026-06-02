// Package reconcile is botfile's pure planner. Given the desired set of symlinks
// and the observed filesystem state, it computes a Plan: the operations that
// would make the filesystem match the declaration, plus the conflicts that block
// it. It is a total function with no I/O, no clock, and no environment access
// (manifesto 42); the side-effecting ports that produce its inputs (the source
// scanner, the per-agent install-path projection, the World reader) and that
// apply its output live in other packages.
//
// This realizes Stow's two-phase discipline (manifesto 3): compute a plan, then
// apply it. The rules it encodes come straight from the manifesto:
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

// LinkSpec is one desired managed symlink: the link Target should point at Dest,
// contributed by the botfile source named SourceName (used for precedence, 35).
type LinkSpec struct {
	Target     string // absolute path of the symlink itself, in the agent's scanned directory
	Dest       string // absolute path it points at: the component inside the source
	SourceName string // the core.Source.Name that contributed this link
}

// EntryKind classifies what the World observed at a target path.
type EntryKind int

const (
	// Absent: nothing exists at the path.
	Absent EntryKind = iota
	// Symlink: a symlink exists; Entry.Dest holds its destination. Whether it is
	// botfile-managed is decided here, by testing Dest against the source roots.
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

// Root is a botfile source root: a name and the absolute directory botfile
// manages out of. The order of the Roots slice passed in Options is the source
// precedence order, highest precedence first (35).
type Root struct {
	Name string
	Path string
}

// Options carries the source roots, in precedence order (highest first). The
// roots serve two purposes: classifying which observed symlinks are
// botfile-managed (a symlink whose destination is under any root, per 33) and
// resolving precedence ties between desired links at the same target (35).
type Options struct {
	Roots []Root
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

// Conflict is a desired link that cannot be installed because something
// botfile does not own occupies the target path (35). botfile reports it and
// never clobbers.
type Conflict struct {
	Target     string
	Dest       string // the desired destination that could not be installed
	SourceName string // the source whose link is blocked
	Reason     string
}

// Shadow is a desired link that lost a precedence tie at its target: another
// source's link won the single slot (35). It is informational, surfaced so the
// user can see that a selection was overridden rather than silently dropped.
type Shadow struct {
	Target     string
	Dest       string
	SourceName string // the source whose link was shadowed
	WonBy      string // the source whose link won the target
}

// Plan is the result of reconciliation: the operations to apply, the conflicts
// blocking desired links, and the links shadowed by precedence. All slices are
// sorted deterministically (by target, then source) so the plan is reproducible.
type Plan struct {
	Ops       []Op
	Conflicts []Conflict
	Shadowed  []Shadow
}

// Reconcile computes the Plan that would make world match desired under opts.
// It is pure and total: equal inputs always yield an equal Plan.
func Reconcile(desired []LinkSpec, world World, opts Options) Plan {
	rootByName := make(map[string]string, len(opts.Roots))
	precedence := make(map[string]int, len(opts.Roots))
	for i, r := range opts.Roots {
		rootByName[r.Name] = filepath.Clean(r.Path)
		precedence[r.Name] = i // lower index = higher precedence
	}

	var plan Plan

	// Validate desired links before resolving winners: every link must point
	// into the root of the source that contributed it. A link whose destination
	// is outside its source root (a scanner or projection bug) becomes a conflict
	// and is never created. This keeps the planner idempotent: it never creates a
	// symlink that the next run would classify as unmanaged and report as a
	// conflict (33).
	valid := make([]LinkSpec, 0, len(desired))
	for _, l := range desired {
		root, ok := rootByName[l.SourceName]
		if !ok || !underRoot(filepath.Clean(l.Dest), root) {
			plan.Conflicts = append(plan.Conflicts, Conflict{
				Target: l.Target, Dest: l.Dest, SourceName: l.SourceName,
				Reason: "desired destination is not under the root of its source",
			})
			continue
		}
		valid = append(valid, l)
	}

	// Resolve precedence: at each target, the highest-precedence source wins the
	// single slot and lower-precedence links are shadowed (35). Links sharing the
	// highest precedence but disagreeing on destination are ambiguous and block
	// the target rather than installing an arbitrary winner.
	winners, shadowed, ambiguous := resolveWinners(valid, precedence)
	plan.Shadowed = shadowed
	plan.Conflicts = append(plan.Conflicts, ambiguous...)

	// Targets that are desired: decide create / replace / no-op / conflict.
	for target, want := range winners {
		entry := world.Entries[target]
		switch entry.Kind {
		case Absent:
			plan.Ops = append(plan.Ops, Op{Kind: OpCreate, Target: target, Dest: want.Dest})
		case Symlink:
			switch {
			case !opts.managed(entry.Dest):
				// A symlink botfile did not create: not ours, never clobber (33, 35).
				plan.Conflicts = append(plan.Conflicts, Conflict{
					Target: target, Dest: want.Dest, SourceName: want.SourceName,
					Reason: "a symlink not managed by botfile already exists at this path",
				})
			case sameLink(entry.Dest, want.Dest):
				// Already correct: no operation.
			default:
				plan.Ops = append(plan.Ops, Op{Kind: OpReplace, Target: target, Dest: want.Dest, OldDest: entry.Dest})
			}
		case Foreign:
			plan.Conflicts = append(plan.Conflicts, Conflict{
				Target: target, Dest: want.Dest, SourceName: want.SourceName,
				Reason: "a non-symlink file or directory already exists at this path",
			})
		}
	}

	// Targets that are observed but not desired: remove botfile's orphans only.
	for target, entry := range world.Entries {
		if _, isDesired := winners[target]; isDesired {
			continue
		}
		// Only a botfile-managed symlink is an orphan we remove; foreign entries
		// and foreign symlinks are left untouched (33).
		if entry.Kind == Symlink && opts.managed(entry.Dest) {
			plan.Ops = append(plan.Ops, Op{Kind: OpRemove, Target: target, OldDest: entry.Dest})
		}
	}

	// Sort the whole plan once so output is deterministic regardless of the order
	// ops and conflicts were discovered (creates/replaces before orphan removals).
	sortOps(plan.Ops)
	sortConflicts(plan.Conflicts)
	return plan
}

// sortOps orders operations by target (then kind, though a target carries at
// most one op) so a plan is reproducible.
func sortOps(ops []Op) {
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].Target != ops[j].Target {
			return ops[i].Target < ops[j].Target
		}
		return ops[i].Kind < ops[j].Kind
	})
}

// sortConflicts orders conflicts by target then source for reproducibility.
func sortConflicts(cs []Conflict) {
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].Target != cs[j].Target {
			return cs[i].Target < cs[j].Target
		}
		return cs[i].SourceName < cs[j].SourceName
	})
}

// resolveWinners groups desired links by target and picks the highest-precedence
// source at each. Lower-precedence links are returned as shadows (35). A source
// not present in the precedence map sorts after all known sources, so an unknown
// source can never silently outrank a declared one.
//
// Two safeguards keep an ambiguous source layout from silently installing an
// arbitrary link: exact duplicates (same source and destination) collapse to
// one, and links that share the highest precedence at a target but disagree on
// destination block the target with a conflict instead of choosing by spelling.
func resolveWinners(desired []LinkSpec, precedence map[string]int) (map[string]LinkSpec, []Shadow, []Conflict) {
	rank := func(name string) int {
		if i, ok := precedence[name]; ok {
			return i
		}
		return len(precedence) // unknown sources rank last
	}

	byTarget := make(map[string][]LinkSpec)
	for _, l := range desired {
		byTarget[l.Target] = append(byTarget[l.Target], l)
	}

	winners := make(map[string]LinkSpec, len(byTarget))
	var shadowed []Shadow
	var ambiguous []Conflict
	for target, links := range byTarget {
		// Collapse exact duplicates (same source and destination): a link
		// declared twice contributes nothing and is not a precedence override.
		type srcDest struct{ source, dest string }
		seen := make(map[srcDest]bool, len(links))
		uniq := make([]LinkSpec, 0, len(links))
		for _, l := range links {
			k := srcDest{l.SourceName, filepath.Clean(l.Dest)}
			if seen[k] {
				continue
			}
			seen[k] = true
			uniq = append(uniq, l)
		}

		// Stable order: precedence first, then source name, then dest, so the
		// winner and shadow ordering are deterministic.
		sort.Slice(uniq, func(i, j int) bool {
			ri, rj := rank(uniq[i].SourceName), rank(uniq[j].SourceName)
			if ri != rj {
				return ri < rj
			}
			if uniq[i].SourceName != uniq[j].SourceName {
				return uniq[i].SourceName < uniq[j].SourceName
			}
			return uniq[i].Dest < uniq[j].Dest
		})

		// If the links sharing the top precedence disagree on destination, the
		// target is ambiguous (for example one source contributing two different
		// destinations to the same path): block it rather than guess (35).
		topRank := rank(uniq[0].SourceName)
		topDests := make(map[string]bool)
		for _, l := range uniq {
			if rank(l.SourceName) == topRank {
				topDests[filepath.Clean(l.Dest)] = true
			}
		}
		if len(topDests) > 1 {
			ambiguous = append(ambiguous, Conflict{
				Target: target, Dest: uniq[0].Dest, SourceName: uniq[0].SourceName,
				Reason: "the highest-precedence source contributes more than one destination to this path",
			})
			continue
		}

		win := uniq[0]
		winners[target] = win
		for _, lost := range uniq[1:] {
			shadowed = append(shadowed, Shadow{
				Target: target, Dest: lost.Dest, SourceName: lost.SourceName, WonBy: win.SourceName,
			})
		}
	}

	sort.Slice(shadowed, func(i, j int) bool {
		if shadowed[i].Target != shadowed[j].Target {
			return shadowed[i].Target < shadowed[j].Target
		}
		return shadowed[i].SourceName < shadowed[j].SourceName
	})
	return winners, shadowed, ambiguous
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
