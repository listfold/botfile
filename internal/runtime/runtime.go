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
	"path/filepath"
	"sort"

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
	Kind   BlockerKind
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
	Roots      map[core.AgentID]string

	// Accumulated state.
	Config       core.Config
	Sources      []project.Source
	ScanProblems []source.Problem
	Projection   project.Result
	Plan         reconcile.Plan
	Blockers     []Blocker
	Unmanaged    []discover.Unmanaged
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
	Problems []source.Problem
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

// CmdReadWorld asks the interpreter to observe Targets and scan ManagedDirs.
type CmdReadWorld struct {
	Targets     []string
	ManagedDirs []string
}

// CmdApply asks the interpreter to apply Ops through the filesystem port.
type CmdApply struct{ Ops []reconcile.Op }

// CmdDiscover asks the interpreter to scan Namespaces for unmanaged components.
type CmdDiscover struct{ Namespaces []discover.Namespace }

func (CmdNone) isCmd()        {}
func (CmdLoadConfig) isCmd()  {}
func (CmdScanSources) isCmd() {}
func (CmdReadWorld) isCmd()   {}
func (CmdApply) isCmd()       {}
func (CmdDiscover) isCmd()    {}

// Init builds the starting Model and first Cmd. The interpreter passes the
// values it resolved from the environment so Update stays pure.
func Init(mode Mode, configPath, home string, agents agent.Set, roots map[core.AgentID]string) (Model, Cmd) {
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
			// Pure: expand selections over the scanned sources and the matrix.
			m.Projection = project.Project(m.Config, m.Sources, m.Agents, m.Roots)
			m.Phase = PhaseReadingWorld
			return m, CmdReadWorld{
				Targets:     targetsOf(m.Projection.Links),
				ManagedDirs: managedDirs(m.Config, m.Agents, m.Roots),
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
			m.Phase = PhaseDone
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
		bs = append(bs, Blocker{Kind: BlockerScanProblem, Ref: p.Path, Detail: p.Detail})
	}
	for _, p := range m.Projection.Problems {
		if p.Kind == project.ProblemUnsupported {
			continue // expected partial coverage; its namespace is not scanned for orphans
		}
		ref := p.SourceName
		if p.Component != "" {
			ref = p.SourceName + ":" + p.Component
		}
		bs = append(bs, Blocker{Kind: BlockerProjectionProblem, Ref: ref, Detail: p.Detail})
	}
	for _, p := range m.Plan.Problems {
		bs = append(bs, Blocker{Kind: BlockerPlanProblem, Ref: p.Target, Detail: p.Detail})
	}
	for _, c := range m.Plan.Conflicts {
		bs = append(bs, Blocker{Kind: BlockerConflict, Ref: c.Target, Detail: c.Reason})
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

// managedDirs returns the namespace directories botfile may have installed into
// for the agents this config targets, so the world reader can find orphans
// (manifesto 33). It is every supported (agent, kind) namespace for each agent
// referenced by a selection.
func managedDirs(cfg core.Config, agents agent.Set, roots map[core.AgentID]string) []string {
	used := make(map[core.AgentID]bool)
	for _, sel := range cfg.Selections {
		for _, a := range sel.Agents {
			used[a] = true
		}
	}
	seen := make(map[string]bool)
	var dirs []string
	for id := range used {
		ag, ok := agents.Lookup(id)
		if !ok {
			continue
		}
		for _, kind := range ag.SupportedKinds() {
			if dir, ok := ag.Namespace(roots[id], kind); ok && !seen[dir] {
				seen[dir] = true
				dirs = append(dirs, dir)
			}
		}
	}
	sort.Strings(dirs)
	return dirs
}

// managedNamespaces returns the (agent, kind, directory) namespaces to scan for
// unmanaged components, one per directory: a directory shared by several agents
// (for example ~/.agents/skills) is scanned once, attributed to the
// lowest-sorted agent that reads it, since an unmanaged skill there is the same
// component whichever agent finds it.
func managedNamespaces(cfg core.Config, agents agent.Set, roots map[core.AgentID]string) []discover.Namespace {
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
	sort.Slice(used, func(i, j int) bool { return used[i] < used[j] })

	seenDir := make(map[string]bool)
	var ns []discover.Namespace
	for _, id := range used {
		ag, ok := agents.Lookup(id)
		if !ok {
			continue
		}
		for _, kind := range ag.SupportedKinds() {
			dir, ok := ag.Namespace(roots[id], kind)
			if !ok || seenDir[dir] {
				continue
			}
			seenDir[dir] = true
			ns = append(ns, discover.Namespace{Agent: id, Kind: kind, Dir: dir})
		}
	}
	return ns
}
