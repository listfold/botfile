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
	"sort"

	"codeberg.org/botfile/botfile/internal/agent"
	"codeberg.org/botfile/botfile/internal/core"
	"codeberg.org/botfile/botfile/internal/project"
	"codeberg.org/botfile/botfile/internal/reconcile"
	"codeberg.org/botfile/botfile/internal/source"
)

// Mode is what the run is for: compute a plan (read-only) or apply it.
type Mode int

const (
	ModePlan Mode = iota
	ModeSync
)

// Phase is where the run is in its lifecycle.
type Phase int

const (
	PhaseLoadingConfig Phase = iota
	PhaseScanning
	PhaseReadingWorld
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
	// BlockerPlanProblem: the desired model is malformed (a reconcile Problem),
	// so the plan must not be applied (reviews/patterns.md).
	BlockerPlanProblem BlockerKind = iota
	// BlockerConflict: an entry botfile does not own occupies a target path, so
	// the desired state cannot be realized there (manifesto 35).
	BlockerConflict
)

// Blocker is a machine-detectable reason the reducer did not apply. Scan and
// projection problems are deliberately not blockers: an unsupported (agent,
// kind) or a stray source file is expected partial coverage, reported on the
// Model but not a reason to halt every clean install.
type Blocker struct {
	Kind   BlockerKind
	Target string
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
func (Applied) isMsg()        {}
func (Failed) isMsg()         {}

// Cmd describes a side effect for the interpreter to perform. It carries the
// data the effect needs; it never performs the effect.
type Cmd interface{ isCmd() }

// CmdNone is the absence of a next effect (a terminal phase).
type CmdNone struct{}

// CmdLoadConfig asks the interpreter to load and validate the config at Path.
type CmdLoadConfig struct{ Path string }

// CmdScanSources asks the interpreter to scan each source's location.
type CmdScanSources struct{ Sources []core.Source }

// CmdReadWorld asks the interpreter to observe Targets and scan ManagedDirs.
type CmdReadWorld struct {
	Targets     []string
	ManagedDirs []string
}

// CmdApply asks the interpreter to apply Ops through the filesystem port.
type CmdApply struct{ Ops []reconcile.Op }

func (CmdNone) isCmd()        {}
func (CmdLoadConfig) isCmd()  {}
func (CmdScanSources) isCmd() {}
func (CmdReadWorld) isCmd()   {}
func (CmdApply) isCmd()       {}

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
			return m, CmdScanSources{Sources: m.Config.Sources}
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
			m.Blockers = blockers(m.Plan)
			if m.Mode == ModePlan {
				m.Phase = PhaseDone // read-only: report the plan and its blockers
				return m, CmdNone{}
			}
			if len(m.Blockers) > 0 {
				m.Phase = PhaseBlocked // refuse to apply a broken or blocked plan
				return m, CmdNone{}
			}
			m.Phase = PhaseApplying
			return m, CmdApply{Ops: m.Plan.Ops}
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

// blockers enumerates the machine-detectable reasons not to apply a plan: a
// malformed desired model (reconcile Problems) and an unmanaged entry occupying
// a managed path (Conflicts). They derive from already-sorted slices, so the
// result is deterministic.
func blockers(plan reconcile.Plan) []Blocker {
	bs := make([]Blocker, 0, len(plan.Problems)+len(plan.Conflicts))
	for _, p := range plan.Problems {
		bs = append(bs, Blocker{Kind: BlockerPlanProblem, Target: p.Target, Detail: p.Detail})
	}
	for _, c := range plan.Conflicts {
		bs = append(bs, Blocker{Kind: BlockerConflict, Target: c.Target, Detail: c.Reason})
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
