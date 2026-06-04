// Package runtime is botfile's Elm-style reducer: the pure state machine that
// sequences a plan or sync run (manifesto 42). It is split exactly as the Elm
// architecture prescribes:
//
//   - Model is immutable run state.
//   - Msg is the closed algebra of events that advance it.
//   - Cmd is a description of a side effect to perform next, never the effect
//     itself.
//   - Update is a pure, total function (Model, Msg) -> (Model, Cmd).
//
// The effect interpreter (a separate package) seeds the initial Model with the
// values it resolved from the environment (config path, home, the agent matrix,
// the resolved agent roots), runs each Cmd against the real ports
// (config.Load, source.Scan, world.Read, apply.Apply), and feeds the resulting
// Msg back into Update until the run reaches a terminal phase. Update itself
// reads no clock, no env, and no filesystem; the pure transforms it performs
// (project, reconcile) are themselves pure.
package runtime

import (
	"fmt"
	"path/filepath"
	"sort"

	"codeberg.org/botfile/botfile/internal/adopt"
	"codeberg.org/botfile/botfile/internal/agent"
	"codeberg.org/botfile/botfile/internal/core"
	"codeberg.org/botfile/botfile/internal/discover"
	"codeberg.org/botfile/botfile/internal/project"
	"codeberg.org/botfile/botfile/internal/reconcile"
	"codeberg.org/botfile/botfile/internal/source"
)

// Mode is what the run is for: compute a plan (read-only) or apply it.
type Mode int

const (
	ModePlan Mode = iota
	ModeSync
	ModeStatus
	ModeAdopt
)

// Phase is where the run is in its lifecycle.
type Phase int

const (
	PhaseLoadingConfig Phase = iota
	PhaseScanning
	PhaseReadingWorld
	PhaseDiscovering
	PhaseApplying
	PhaseDone
	// PhaseBlocked is terminal: the reducer refused to apply because the plan was
	// built from a broken desired model or the observed world blocks it. It is
	// distinct from PhaseFailed, which is an effect failure, not a model/world
	// outcome.
	PhaseBlocked
	PhaseFailed
)

// BlockerKind classifies why a sync was refused before any effect ran.
type BlockerKind int

const (
	// BlockerScanProblem: a source component was malformed or unreadable and
	// dropped from the desired set, leaving the model incomplete.
	BlockerScanProblem BlockerKind = iota
	// BlockerProjectionProblem: a selection matched nothing, named an unscanned
	// source, or hit a layout gap, also leaving the model incomplete. Unsupported
	// (agent, kind) is excluded; see blockers.
	BlockerProjectionProblem
	// BlockerPlanProblem: the desired model is malformed (a reconcile Problem),
	// so the plan must not be applied (reviews/patterns.md).
	BlockerPlanProblem
	// BlockerConflict: an entry botfile does not own occupies a target path, so
	// the desired state cannot be realized there (manifesto 35).
	BlockerConflict
)

// String renders a BlockerKind as a stable, human-readable token.
func (k BlockerKind) String() string {
	switch k {
	case BlockerScanProblem:
		return "scan-problem"
	case BlockerProjectionProblem:
		return "projection-problem"
	case BlockerPlanProblem:
		return "plan-problem"
	case BlockerConflict:
		return "conflict"
	default:
		return "unknown-blocker"
	}
}

// Blocker is a machine-detectable reason the reducer did not apply.
//
// The dividing line is whether the desired model is trustworthy enough to act
// on, in particular to perform destructive orphan removal: orphan cleanup
// removes a managed symlink not in the desired set, so a desired set made
// incomplete by a problem (a typo'd selection, a malformed source, an unreadable
// directory) would delete a correct, previously-installed link. Such problems
// block. The one exception is an unsupported (agent, kind): its namespace is
// never scanned for orphans, it is expected partial coverage, and it is
// persistent, so it is reported but never blocks.
type Blocker struct {
	Kind BlockerKind
	// Cause is the specific sub-kind token behind the coarse Kind (for example
	// "ambiguous-target" or "skill-missing-manifest"), so a machine consumer can
	// branch on the precise reason that Kind groups. Conflicts have no sub-kind
	// enum yet, so theirs is the coarse "conflict".
	Cause  string
	Ref    string // the path, source, or component the blocker concerns
	Detail string
}

// Model is the immutable state of a run. The inputs block is seeded by the
// interpreter at Init; the rest accumulates as Msgs arrive. Update returns a new
// Model rather than mutating in place.
type Model struct {
	Mode  Mode
	Phase Phase

	// Inputs, resolved by the interpreter (Update never reads the environment).
	ConfigPath string
	Home       string
	Agents     agent.Set
	Roots      agent.Roots
	Adopt      adopt.Request // the request, in ModeAdopt; zero otherwise

	// Accumulated state.
	Config       core.Config
	Sources      []project.Source
	ScanProblems []ScanProblem
	Projection   project.Result
	Plan         reconcile.Plan
	Blockers     []Blocker
	Unmanaged    []discover.Unmanaged
	AdoptPlan    adopt.Plan     // the computed adopt steps, in ModeAdopt
	AdoptProblem *adopt.Problem // why a request could not be adopted, if blocked
	Err          error
	FailedStage  string
}

// Msg is the closed algebra of events that advance a run.
type Msg interface{ isMsg() }

// ConfigLoaded delivers the loaded configuration.
type ConfigLoaded struct{ Config core.Config }

// SourcesScanned delivers the scanned sources and any source-grammar problems.
type SourcesScanned struct {
	Sources  []project.Source
	Problems []ScanProblem
}

// ScanProblem is a source-grammar problem tagged with the configured source it
// came from. The tag lets a consumer block on the right scope: sync and status
// block on any source problem (the desired model is incomplete), while adopt
// blocks only on problems in its own target source, so an unrelated broken
// source does not stop an otherwise-valid adopt.
type ScanProblem struct {
	Source  string
	Problem source.Problem
}

// WorldRead delivers the observed filesystem state.
type WorldRead struct{ World reconcile.World }

// Discovered delivers the unmanaged, adoptable components found in the agents'
// namespaces (status mode).
type Discovered struct{ Unmanaged []discover.Unmanaged }

// Applied signals that the plan's operations were applied successfully.
type Applied struct{}

// Failed signals that a step's side effect failed; Stage names the step.
type Failed struct {
	Stage string
	Err   error
}

func (ConfigLoaded) isMsg()   {}
func (SourcesScanned) isMsg() {}
func (WorldRead) isMsg()      {}
func (Discovered) isMsg()     {}
func (Applied) isMsg()        {}
func (Failed) isMsg()         {}

// Cmd describes a side effect for the interpreter to perform. It carries the
// data the effect needs; it never performs the effect.
type Cmd interface{ isCmd() }

// CmdNone is the absence of a next effect (a terminal phase).
type CmdNone struct{}

// CmdLoadConfig asks the interpreter to load and validate the config at Path.
type CmdLoadConfig struct{ Path string }

// CmdScanSources asks the interpreter to scan each source's location. BaseDir is
// the directory a relative source location resolves against (the config file's
// directory), so resolution does not depend on the process working directory.
type CmdScanSources struct {
	Sources []core.Source
	BaseDir string
}

// CmdReadWorld asks the interpreter to observe Targets, scan ManagedDirs, and
// observe each ManagedFile (singleton targets observed one path at a time, never
// by scanning their parent directory).
type CmdReadWorld struct {
	Targets      []string
	ManagedDirs  []string
	ManagedFiles []string
}

// CmdApply asks the interpreter to apply Ops through the filesystem port.
type CmdApply struct{ Ops []reconcile.Op }

// CmdDiscover asks the interpreter to scan Namespaces for unmanaged components.
type CmdDiscover struct{ Namespaces []discover.Namespace }

// CmdApplyAdopt asks the interpreter to execute an adopt plan (move, link, and
// record the selection in the config at ConfigPath) as a saga.
type CmdApplyAdopt struct {
	Plan       adopt.Plan
	ConfigPath string
}

func (CmdNone) isCmd()        {}
func (CmdLoadConfig) isCmd()  {}
func (CmdScanSources) isCmd() {}
func (CmdReadWorld) isCmd()   {}
func (CmdApply) isCmd()       {}
func (CmdDiscover) isCmd()    {}
func (CmdApplyAdopt) isCmd()  {}

// Init builds the starting Model and first Cmd. The interpreter passes the
// values it resolved from the environment so Update stays pure.
func Init(mode Mode, configPath, home string, agents agent.Set, roots agent.Roots) (Model, Cmd) {
	m := Model{
		Mode:       mode,
		Phase:      PhaseLoadingConfig,
		ConfigPath: configPath,
		Home:       home,
		Agents:     agents,
		Roots:      roots,
	}
	return m, CmdLoadConfig{Path: configPath}
}

// Update is the pure reducer: it advances the Model by one Msg and returns the
// next Cmd. It enforces the phase model: terminal phases ignore further
// messages, a Failed message ends any non-terminal phase, and otherwise only the
// message a phase expects advances it (a stale or out-of-order message is
// ignored). The pure transforms (project, reconcile) happen here, inline,
// because they need no I/O; only the steps that touch the world become Cmds.
func Update(m Model, msg Msg) (Model, Cmd) {
	if m.Done() {
		return m, CmdNone{} // terminal phases are terminal
	}
	if f, ok := msg.(Failed); ok {
		m.Phase = PhaseFailed
		m.FailedStage = f.Stage
		m.Err = f.Err
		return m, CmdNone{}
	}

	switch m.Phase {
	case PhaseLoadingConfig:
		if cl, ok := msg.(ConfigLoaded); ok {
			m.Config = cl.Config
			m.Phase = PhaseScanning
			// Relative source locations resolve against the config's directory
			// (filepath.Dir is a pure string op, no I/O).
			return m, CmdScanSources{Sources: m.Config.Sources, BaseDir: filepath.Dir(m.ConfigPath)}
		}

	case PhaseScanning:
		if ss, ok := msg.(SourcesScanned); ok {
			m.Sources = ss.Sources
			m.ScanProblems = ss.Problems
			if m.Mode == ModeAdopt {
				// adopt's collision preflight trusts the scanned contents of the
				// target source, so a source that did not scan cleanly cannot be
				// adopted into: refuse here rather than risk a blind move whose
				// collision check missed a skipped entry. Only the target source
				// blocks; an unrelated broken source is irrelevant to this adopt.
				if sp := scanProblemForSource(m.ScanProblems, m.Adopt.SourceName); sp != nil {
					m.AdoptProblem = &adopt.Problem{
						Kind:   adopt.ProblemSourceUnscannable,
						Detail: fmt.Sprintf("source %q did not scan cleanly (%s: %s)", m.Adopt.SourceName, sp.Problem.Path, sp.Problem.Detail),
					}
					m.Phase = PhaseBlocked
					return m, CmdNone{}
				}
				// adopt needs the scanned sources (roots and collision data) and the
				// discovered components to validate the request; it skips the
				// reconcile pipeline. Discovery scans every supported agent namespace
				// (not just those a selection already names), so a first adopt can
				// bootstrap a config that has sources but no selections yet.
				m.Phase = PhaseDiscovering
				return m, CmdDiscover{Namespaces: allNamespaces(m.Agents, m.Roots)}
			}
			// Pure: expand selections over the scanned sources and the matrix.
			m.Projection = project.Project(m.Config, m.Sources, m.Agents, m.Roots)
			m.Phase = PhaseReadingWorld
			dirs, files := managedSurfaces(m.Config, m.Agents, m.Roots)
			return m, CmdReadWorld{
				Targets:      targetsOf(m.Projection.Links),
				ManagedDirs:  dirs,
				ManagedFiles: files,
			}
		}

	case PhaseReadingWorld:
		if wr, ok := msg.(WorldRead); ok {
			// Pure: compute the plan, then the blockers that decide whether to apply.
			m.Plan = reconcile.Reconcile(m.Projection.Links, wr.World, reconcileOpts(m.Sources))
			m.Blockers = blockers(m)
			switch m.Mode {
			case ModeStatus:
				// Read-only overview: also find the unmanaged, adoptable components.
				m.Phase = PhaseDiscovering
				return m, CmdDiscover{Namespaces: managedNamespaces(m.Config, m.Agents, m.Roots)}
			case ModePlan:
				m.Phase = PhaseDone // read-only: report the plan and its blockers
				return m, CmdNone{}
			default: // ModeSync
				if len(m.Blockers) > 0 {
					m.Phase = PhaseBlocked // refuse to apply a broken or blocked plan
					return m, CmdNone{}
				}
				m.Phase = PhaseApplying
				return m, CmdApply{Ops: m.Plan.Ops}
			}
		}

	case PhaseDiscovering:
		if d, ok := msg.(Discovered); ok {
			m.Unmanaged = d.Unmanaged
			if m.Mode == ModeAdopt {
				// Pure: compute the adopt plan from the request, sources, and the
				// discovered components, or a problem if it cannot be adopted.
				plan, prob := adopt.Compute(m.Adopt, m.Config, m.Sources, m.Unmanaged, m.Agents)
				if prob != nil {
					m.AdoptProblem = prob
					m.Phase = PhaseBlocked // a request problem, not an effect failure
					return m, CmdNone{}
				}
				m.AdoptPlan = plan
				m.Phase = PhaseApplying
				return m, CmdApplyAdopt{Plan: plan, ConfigPath: m.ConfigPath}
			}
			m.Phase = PhaseDone // status: report what was found
			return m, CmdNone{}
		}

	case PhaseApplying:
		if _, ok := msg.(Applied); ok {
			m.Phase = PhaseDone
			return m, CmdNone{}
		}
	}

	// Stale or out-of-order (phase, message): ignore, leaving the Model unchanged.
	return m, CmdNone{}
}

// Done reports whether the run reached a terminal phase.
func (m Model) Done() bool {
	return m.Phase == PhaseDone || m.Phase == PhaseBlocked || m.Phase == PhaseFailed
}

// blockers enumerates the machine-detectable reasons not to apply: any problem
// that leaves the desired model incomplete (so orphan removal would be unsafe) or
// an observed conflict. Scan problems and projection problems both leave the
// model incomplete, except an unsupported (agent, kind), which is expected
// partial coverage and never blocks. The slices it reads are already sorted, so
// the result is deterministic.
func blockers(m Model) []Blocker {
	var bs []Blocker
	for _, p := range m.ScanProblems {
		bs = append(bs, Blocker{Kind: BlockerScanProblem, Cause: p.Problem.Kind.String(), Ref: p.Problem.Path, Detail: p.Problem.Detail})
	}
	for _, p := range m.Projection.Problems {
		if p.Kind == project.ProblemUnsupported {
			continue // expected partial coverage; its namespace is not scanned for orphans
		}
		ref := p.SourceName
		if p.Component != "" {
			ref = p.SourceName + ":" + p.Component
		}
		bs = append(bs, Blocker{Kind: BlockerProjectionProblem, Cause: p.Kind.String(), Ref: ref, Detail: p.Detail})
	}
	for _, p := range m.Plan.Problems {
		bs = append(bs, Blocker{Kind: BlockerPlanProblem, Cause: p.Kind.String(), Ref: p.Target, Detail: p.Detail})
	}
	for _, c := range m.Plan.Conflicts {
		bs = append(bs, Blocker{Kind: BlockerConflict, Cause: BlockerConflict.String(), Ref: c.Target, Detail: c.Reason})
	}
	return bs
}

// reconcileOpts builds the planner options from the scanned sources, preserving
// their order so precedence is config declaration order, first declared wins
// (manifesto 35).
func reconcileOpts(sources []project.Source) reconcile.Options {
	roots := make([]reconcile.Root, len(sources))
	for i, s := range sources {
		roots[i] = reconcile.Root{Name: s.Name, Path: s.Root}
	}
	return reconcile.Options{Roots: roots}
}

// targetsOf returns the unique, sorted target paths of the desired links.
func targetsOf(links []reconcile.LinkSpec) []string {
	seen := make(map[string]bool, len(links))
	var out []string
	for _, l := range links {
		if !seen[l.Target] {
			seen[l.Target] = true
			out = append(out, l.Target)
		}
	}
	sort.Strings(out)
	return out
}

// managedSurfaces returns the surfaces botfile may have installed into for the
// agents this config targets, so the world reader can find orphans (manifesto
// 33), split by shape: dirs are namespace directories to scan, files are exact
// singleton targets to observe one at a time (never scanning their parent, which
// holds unrelated user files). It covers every supported (agent, kind) for each
// agent referenced by a selection.
func managedSurfaces(cfg core.Config, agents agent.Set, roots agent.Roots) (dirs, files []string) {
	used := make(map[core.AgentID]bool)
	for _, sel := range cfg.Selections {
		for _, a := range sel.Agents {
			used[a] = true
		}
	}
	seenDir := make(map[string]bool)
	seenFile := make(map[string]bool)
	for id := range used {
		ag, ok := agents.Lookup(id)
		if !ok {
			continue
		}
		for _, kind := range ag.SupportedKinds() {
			root, ok := roots.For(id, kind)
			if !ok {
				continue
			}
			if _, fixed := ag.FixedFile(kind); fixed {
				if f, ok := ag.Target(root, kind, ""); ok && !seenFile[f] {
					seenFile[f] = true
					files = append(files, f)
				}
				continue
			}
			if dir, ok := ag.Namespace(root, kind); ok && !seenDir[dir] {
				seenDir[dir] = true
				dirs = append(dirs, dir)
			}
		}
	}
	sort.Strings(dirs)
	sort.Strings(files)
	return dirs, files
}

// scanProblemForSource returns the first scan problem attributed to the named
// source, or nil if that source scanned cleanly. It lets adopt block on its own
// target source without being stopped by an unrelated broken source.
func scanProblemForSource(problems []ScanProblem, source string) *ScanProblem {
	for i := range problems {
		if problems[i].Source == source {
			return &problems[i]
		}
	}
	return nil
}

// managedNamespaces returns the namespaces to scan for unmanaged components in a
// config-scoped run (status orphan discovery): only the agents some selection
// already names. A first sync has nothing to manage, so scanning every agent
// would surface noise the user never opted into.
func managedNamespaces(cfg core.Config, agents agent.Set, roots agent.Roots) []discover.Namespace {
	usedSet := make(map[core.AgentID]bool)
	for _, sel := range cfg.Selections {
		for _, a := range sel.Agents {
			usedSet[a] = true
		}
	}
	used := make([]core.AgentID, 0, len(usedSet))
	for id := range usedSet {
		used = append(used, id)
	}
	return namespacesFor(used, agents, roots)
}

// allNamespaces returns the namespaces to scan across every agent in the matrix,
// regardless of the current selections. adopt uses this so a first adopt can
// bootstrap a config that has sources but no selections yet (the agent that
// created the component need not already be selected).
func allNamespaces(agents agent.Set, roots agent.Roots) []discover.Namespace {
	return namespacesFor(agents.IDs(), agents, roots)
}

// namespacesFor returns the namespaces to scan for the given agent ids, one per
// (kind, directory). A directory shared by several agents (for example
// ~/.agents/skills) is scanned once but carries every agent that reads it, so a
// component found there is attributed to all of them, not just one.
func namespacesFor(ids []core.AgentID, agents agent.Set, roots agent.Roots) []discover.Namespace {
	used := append([]core.AgentID(nil), ids...) // copy: do not reorder the caller's slice
	sort.Slice(used, func(i, j int) bool { return used[i] < used[j] })

	// Key by (kind, directory, file): a custom matrix could map two kinds to the
	// same directory, and a fixed-file surface is distinct from a directory scan at
	// the same dir. Agents that share a key are accumulated onto one namespace. A
	// fixed-file kind (a singleton like AGENTS.md) carries its filename so
	// discovery reads only that one entry, never the rest of its directory
	// (manifesto 33).
	type key struct {
		kind core.Kind
		dir  string
		file string
	}
	byKey := make(map[key]int) // (kind, dir, file) -> index into ns
	var ns []discover.Namespace
	for _, id := range used {
		ag, ok := agents.Lookup(id)
		if !ok {
			continue
		}
		for _, kind := range ag.SupportedKinds() {
			root, ok := roots.For(id, kind)
			if !ok {
				continue
			}
			dir, ok := ag.Namespace(root, kind)
			if !ok {
				continue
			}
			file, _ := ag.FixedFile(kind) // "" for a directory surface
			k := key{kind, dir, file}
			if i, exists := byKey[k]; exists {
				ns[i].Agents = append(ns[i].Agents, id)
				continue
			}
			byKey[k] = len(ns)
			ns = append(ns, discover.Namespace{Agents: []core.AgentID{id}, Kind: kind, Dir: dir, File: file})
		}
	}
	sort.Slice(ns, func(i, j int) bool {
		if ns[i].Dir != ns[j].Dir {
			return ns[i].Dir < ns[j].Dir
		}
		if ns[i].Kind != ns[j].Kind {
			return ns[i].Kind < ns[j].Kind
		}
		// File is part of the dedupe key, so it must also order: two fixed-file
		// surfaces of the same kind in one dir (a custom matrix) stay deterministic.
		return ns[i].File < ns[j].File
	})
	return ns
}
