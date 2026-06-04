// Package agent holds botfile's vendor matrix: which (agent, kind) cells are
// supported and how each component installs into an agent's native namespace
// (manifesto 16-25). It is expressed as data (the capability-matrix-as-data
// pattern, reviews/patterns.md), so support is a table lookup rather than
// scattered branching, and adding an agent or kind is adding an entry.
//
// The matrix is split into two kinds of vendor knowledge:
//
//   - Each agent's config root: a default path relative to the user home plus an
//     optional environment variable that overrides it (for example claude-code
//     honors CLAUDE_CONFIG_DIR). Resolving a root reads the environment, which is
//     impure, so it is done at the boundary via ResolveRoots(home, getenv) and
//     the result is handed to the pure projection. The pure layer never reads
//     env (reviews/patterns.md: effects in interpreters).
//   - Each (agent, kind) install rule: where the kind's directory sits relative
//     to that root, and how the component leaf is named.
//
// The projection consumes the matrix as an injected value, so where the specs
// come from (the built-in Default, or a future embedded or OS-specific file, or
// a config override) is a decision separate from how a path is computed.
package agent

import (
	"fmt"
	"path/filepath"
	"sort"

	"codeberg.org/botfile/botfile/internal/core"
)

// Tier is the support-rubric tier an (agent, kind) cell qualifies under
// (manifesto 22-25, 51):
//   - Tier1: a native drop-in directory the harness scans by presence (skills/,
//     claude-code's rules/).
//   - Tier2: a native fixed file the harness reads by presence (an AGENTS.md /
//     copilot-instructions.md singleton). Same no-registration property as tier
//     1, different cardinality.
//   - Tier3: settings or env-var registration (a config write). Defined but used
//     by no rule today; instructions reach every agent through tier 1 or tier 2.
//
// All three are "supported" for projection; the tier records how the harness
// discovers the files, not how a path is computed.
type Tier int

const (
	Tier1 Tier = 1
	Tier2 Tier = 2
	Tier3 Tier = 3
)

// LeafShape is how a component's installed entry is named in the agent's
// namespace (manifesto 48):
//   - LeafDir: a directory named <name> (a skill).
//   - LeafFile: a file <name><ext> in a drop-in directory (a claude-code
//     instruction in rules/), one entry per component.
//   - LeafFixed: a single fixed file whose name is the agent's, not the
//     component's (an instruction an agent reads as one file: AGENTS.md,
//     CLAUDE.md, copilot-instructions.md). Many components can target it, so the
//     reconcile precedence and conflict rules (manifesto 35) decide the single
//     occupant; botfile creates the symlink where the path is free and reports a
//     conflict (never clobbers) where the user already has a file there.
type LeafShape int

const (
	LeafDir LeafShape = iota
	LeafFile
	LeafFixed
)

// Base is how an agent's config root is located: the default path relative to
// the user home, plus an optional environment variable that overrides it when
// set (for example claude-code's CLAUDE_CONFIG_DIR). Resolving it is done by
// Agent.Root with an injected env lookup, keeping the env read at the boundary.
type Base struct {
	HomeRelative []string // default root, relative to the user home
	EnvOverride  string   // env var whose value, when non-empty, is used as the root instead
}

// InstallRule is the vendor spec for one (agent, kind) cell: the rubric tier plus
// the path projection (the kind's directory relative to the agent's config root,
// and how the component leaf is named).
//
// Shared-first policy: a cell records the single namespace botfile *installs*
// into, not every namespace the agent *reads*. When an agent reads several native
// directories for a kind at user scope (crush, for example, reads ~/.agents/skills,
// ~/.claude/skills, ~/.config/crush/skills, and ~/.config/agents/skills for
// skills), botfile targets the shared cross-agent namespace (~/.agents/skills), so
// one symlink serves every reader, accepting the coarser visibility of manifesto
// 49. Modeling every directory an agent reads would be a separate concept (split
// this install rule from a read-namespaces set); it is not needed while install is
// shared-first.
type InstallRule struct {
	Tier Tier
	// Base optionally overrides the agent's default config root for this kind.
	// Most kinds live under the agent's one Base (nil here), but a kind whose
	// namespace sits under a different root than the agent's others sets its own
	// (for example codex's AGENTS.md lives under ~/.codex while its skills live
	// under the cross-agent ~/.agents). nil means "use the agent's Base".
	Base     *Base
	Segments []string // the kind's directory, relative to this kind's config root
	Shape    LeafShape
	Ext      string // leaf extension when Shape is LeafFile (for example ".md")
	Filename string // the fixed leaf name when Shape is LeafFixed (for example "AGENTS.md")
}

// Spec is the external, validated description of one agent used to build a Set
// via NewSet / NewAgent.
type Spec struct {
	ID    core.AgentID
	Base  Base
	Rules map[core.Kind]InstallRule
}

// Agent is one agent's resolved-at-runtime config root spec and its supported
// kinds with install rules. It is constructed only through NewAgent, so every
// Agent is validated.
type Agent struct {
	id    core.AgentID
	base  Base
	rules map[core.Kind]InstallRule
}

// ID returns the agent's identifier.
func (a Agent) ID() core.AgentID { return a.id }

// Supports reports whether the agent supports a component kind (manifesto 24).
func (a Agent) Supports(kind core.Kind) bool {
	_, ok := a.rules[kind]
	return ok
}

// SupportedKinds returns the agent's supported kinds, sorted for determinism.
func (a Agent) SupportedKinds() []core.Kind {
	kinds := make([]core.Kind, 0, len(a.rules))
	for k := range a.rules {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	return kinds
}

// resolveBase resolves one Base to an absolute path under home, consulting getenv
// for the override variable first (for example claude-code's CLAUDE_CONFIG_DIR).
// getenv is injected so the env read stays at the boundary; a nil getenv behaves
// as if no variable is set.
func resolveBase(base Base, home string, getenv func(string) string) string {
	if base.EnvOverride != "" && getenv != nil {
		if v := getenv(base.EnvOverride); v != "" {
			return v
		}
	}
	parts := make([]string, 0, len(base.HomeRelative)+1)
	parts = append(parts, home)
	parts = append(parts, base.HomeRelative...)
	return filepath.Join(parts...)
}

// Root resolves the agent's default config root under home (the Base shared by
// kinds that do not override it). getenv is injected; pass os.Getenv from the
// runtime, or a fake in tests.
func (a Agent) Root(home string, getenv func(string) string) string {
	return resolveBase(a.base, home, getenv)
}

// rootForKind resolves the config root for one kind: the rule's Base override if
// it sets one, otherwise the agent's default Base. The bool is false when the
// agent does not support the kind.
func (a Agent) rootForKind(kind core.Kind, home string, getenv func(string) string) (string, bool) {
	rule, ok := a.rules[kind]
	if !ok {
		return "", false
	}
	base := a.base
	if rule.Base != nil {
		base = *rule.Base
	}
	return resolveBase(base, home, getenv), true
}

// Namespace returns the absolute directory under root where the agent installs
// components of kind (the per-kind directory, without a component leaf), and
// whether the agent supports that kind. Two agents whose Namespace for a kind is
// the same directory share visibility of everything installed there: that is how
// the projection detects a shared skills pool (manifesto 35, 49).
func (a Agent) Namespace(root string, kind core.Kind) (string, bool) {
	rule, ok := a.rules[kind]
	if !ok {
		return "", false
	}
	parts := make([]string, 0, len(rule.Segments)+1)
	parts = append(parts, root)
	parts = append(parts, rule.Segments...)
	return filepath.Join(parts...), true
}

// Target returns the absolute install path for a component of kind/name given
// the agent's already-resolved config root, and whether the agent supports that
// kind. The path is the kind's per-kind directory under root plus the leaf: a
// directory named <name> for a skill, a <name><ext> file for a drop-in
// instruction, or the agent's fixed filename for a singleton instruction (which
// ignores the component name, since the agent reads one file; manifesto 48).
func (a Agent) Target(root string, kind core.Kind, name string) (string, bool) {
	rule, ok := a.rules[kind]
	if !ok {
		return "", false
	}
	leaf := name
	switch rule.Shape {
	case LeafFile:
		leaf = name + rule.Ext
	case LeafFixed:
		leaf = rule.Filename
	}
	parts := make([]string, 0, len(rule.Segments)+2)
	parts = append(parts, root)
	parts = append(parts, rule.Segments...)
	parts = append(parts, leaf)
	return filepath.Join(parts...), true
}

// FixedFile reports whether the agent installs a kind as one fixed file (a
// LeafFixed singleton like AGENTS.md) rather than as entries in a scanned
// directory, and the fixed filename if so. Callers use it to observe and discover
// only that one file, never the parent directory it sits in (manifesto 33): a
// singleton's namespace directory holds unrelated user files botfile must not
// touch.
func (a Agent) FixedFile(kind core.Kind) (string, bool) {
	rule, ok := a.rules[kind]
	if !ok || rule.Shape != LeafFixed {
		return "", false
	}
	return rule.Filename, true
}

// Set is a collection of agents keyed by ID: the matrix the projection consumes.
type Set struct {
	agents map[core.AgentID]Agent
}

// Lookup returns the agent for id, and whether it is in the set.
func (s Set) Lookup(id core.AgentID) (Agent, bool) {
	a, ok := s.agents[id]
	return a, ok
}

// IDs returns the agent identifiers in the set (unordered).
func (s Set) IDs() []core.AgentID {
	ids := make([]core.AgentID, 0, len(s.agents))
	for id := range s.agents {
		ids = append(ids, id)
	}
	return ids
}

// Roots holds each agent's resolved config root for each kind it supports. Most
// kinds share the agent's default Base, but a rule may override it (a kind whose
// namespace sits under a different root than the agent's others). The env read
// happens once, in ResolveRoots, so the pure layer only looks a root up.
type Roots struct {
	byKind map[core.AgentID]map[core.Kind]string
}

// For returns the resolved config root for an (agent, kind) and whether it is
// set (false when the agent does not support the kind, or is absent).
func (r Roots) For(id core.AgentID, kind core.Kind) (string, bool) {
	ks, ok := r.byKind[id]
	if !ok {
		return "", false
	}
	root, ok := ks[kind]
	return root, ok
}

// ResolveRoots resolves every agent's config root, per kind, under home,
// consulting getenv for overrides. The runtime calls this once (with os.Getenv)
// and passes the result to the pure projection, so projection never reads the
// environment.
func (s Set) ResolveRoots(home string, getenv func(string) string) Roots {
	byKind := make(map[core.AgentID]map[core.Kind]string, len(s.agents))
	for id, a := range s.agents {
		ks := make(map[core.Kind]string, len(a.rules))
		for kind := range a.rules {
			if root, ok := a.rootForKind(kind, home, getenv); ok {
				ks[kind] = root
			}
		}
		byKind[id] = ks
	}
	return Roots{byKind: byKind}
}

// NewAgent validates a Spec into an Agent. It rejects an unknown agent id, an
// empty config-root base, no rules, and any rule with an unknown kind, an invalid
// per-kind base override, empty segments, an invalid tier, or a leaf shape
// inconsistent with its extension.
func NewAgent(sp Spec) (Agent, error) {
	if !core.IsKnownAgent(sp.ID) {
		return Agent{}, fmt.Errorf("agent %q is not a known agent", sp.ID)
	}
	if len(sp.Base.HomeRelative) == 0 {
		return Agent{}, fmt.Errorf("agent %q: base home-relative path is empty", sp.ID)
	}
	for _, seg := range sp.Base.HomeRelative {
		if seg == "" {
			return Agent{}, fmt.Errorf("agent %q: base path has an empty segment", sp.ID)
		}
	}
	if len(sp.Rules) == 0 {
		return Agent{}, fmt.Errorf("agent %q: no install rules", sp.ID)
	}
	rules := make(map[core.Kind]InstallRule, len(sp.Rules))
	for kind, rule := range sp.Rules {
		if !core.IsKnownKind(kind) {
			return Agent{}, fmt.Errorf("agent %q: unknown kind %q", sp.ID, kind)
		}
		if rule.Base != nil {
			if len(rule.Base.HomeRelative) == 0 {
				return Agent{}, fmt.Errorf("agent %q kind %q: base override has an empty home-relative path", sp.ID, kind)
			}
			for _, seg := range rule.Base.HomeRelative {
				if seg == "" {
					return Agent{}, fmt.Errorf("agent %q kind %q: base override has an empty segment", sp.ID, kind)
				}
			}
		}
		// A singleton (LeafFixed) may live directly in its root, so it alone may
		// have no segments; the directory kinds always need one.
		if rule.Shape != LeafFixed && len(rule.Segments) == 0 {
			return Agent{}, fmt.Errorf("agent %q kind %q: empty segments", sp.ID, kind)
		}
		for _, seg := range rule.Segments {
			if seg == "" {
				return Agent{}, fmt.Errorf("agent %q kind %q: empty path segment", sp.ID, kind)
			}
		}
		if rule.Tier != Tier1 && rule.Tier != Tier2 && rule.Tier != Tier3 {
			return Agent{}, fmt.Errorf("agent %q kind %q: invalid tier %d", sp.ID, kind, rule.Tier)
		}
		switch rule.Shape {
		case LeafDir:
			if rule.Ext != "" || rule.Filename != "" {
				return Agent{}, fmt.Errorf("agent %q kind %q: a directory leaf must not set an extension or filename", sp.ID, kind)
			}
		case LeafFile:
			if rule.Ext == "" {
				return Agent{}, fmt.Errorf("agent %q kind %q: a file leaf requires an extension", sp.ID, kind)
			}
			if rule.Filename != "" {
				return Agent{}, fmt.Errorf("agent %q kind %q: a drop-in file leaf must not set a fixed filename", sp.ID, kind)
			}
		case LeafFixed:
			if rule.Filename == "" {
				return Agent{}, fmt.Errorf("agent %q kind %q: a fixed leaf requires a filename", sp.ID, kind)
			}
			if rule.Ext != "" {
				return Agent{}, fmt.Errorf("agent %q kind %q: a fixed leaf must not set an extension", sp.ID, kind)
			}
		default:
			return Agent{}, fmt.Errorf("agent %q kind %q: invalid leaf shape", sp.ID, kind)
		}
		rules[kind] = rule
	}
	return Agent{id: sp.ID, base: sp.Base, rules: rules}, nil
}

// NewSet validates specs into a Set, rejecting a duplicate agent id.
func NewSet(specs ...Spec) (Set, error) {
	agents := make(map[core.AgentID]Agent, len(specs))
	for _, sp := range specs {
		a, err := NewAgent(sp)
		if err != nil {
			return Set{}, err
		}
		if _, dup := agents[a.id]; dup {
			return Set{}, fmt.Errorf("agent %q declared more than once", a.id)
		}
		agents[a.id] = a
	}
	return Set{agents: agents}, nil
}

// Default is botfile's built-in vendor matrix.
//
// Only cells verified against an agent's real behavior are included; an
// unverified or unsupported (agent, kind) is intentionally absent, so the
// projection reports it as unsupported rather than installing to a guessed path
// (manifesto 24, 25). It is built through NewSet, so a mistake in the built-in
// data is a construction panic, not a silently broken matrix.
//
// Skills (tier 1 auto-discovery: one directory per skill, each with a SKILL.md,
// found by presence, manifesto 17, 22, 48) are specified for claude-code,
// codex-cli, copilot-cli, crush, opencode, and pi.dev. Five of those (codex-cli,
// copilot-cli, crush, opencode, pi.dev) read the cross-agent ~/.agents/skills
// drop-in, so botfile targets that shared directory for all of them: one symlink
// serves every reader, at the cost of coarse selection across the pool (manifesto
// 49; callouts/per-agent-skill-selection-needs-isolated-namespaces.md). claude-code does not read the
// shared dir (only ~/.claude/skills), so it stays isolated and keeps per-agent
// selection. opencode also reads ~/.config/opencode/skills and ~/.claude/skills,
// pi.dev also reads ~/.pi/agent/skills, and crush also reads ~/.claude/skills,
// ~/.config/crush/skills, and ~/.config/agents/skills; we install to the shared
// dir on purpose, to stay on the cross-agent convention.
//
// Instructions (manifesto 18) are specified for every agent, in one of two
// shapes. claude-code exposes a drop-in directory of one file per instruction
// (~/.claude/rules/, LeafFile, tier 1), so botfile fans out one symlink per
// instruction. The others read a single fixed file (codex-cli ~/.codex/AGENTS.md,
// opencode ~/.config/opencode/AGENTS.md, pi.dev ~/.pi/agent/AGENTS.md, copilot-cli
// ~/.copilot/copilot-instructions.md, crush ~/.config/crush/CRUSH.md): a LeafFixed
// singleton on its own root
// (slice 2's per-kind base). botfile installs into it like any other target and
// never clobbers; where a user-authored file already sits there the reconcile
// conflict rules (manifesto 35) report it and refuse to sync, and the user
// reconciles it out of band (typically by adopting their file, 50). copilot-vscode
// is pending vendor confirmation. See
// callouts/instructions-are-one-kind-distribute-or-adopt.md.
func Default() Set {
	set, err := NewSet(
		Spec{
			ID:   core.AgentClaudeCode,
			Base: Base{HomeRelative: []string{".claude"}, EnvOverride: "CLAUDE_CONFIG_DIR"},
			Rules: map[core.Kind]InstallRule{
				// claude-code scans <root>/skills/<skill>/ (agentskills.io); tier 1
				// auto-discovery (manifesto 22).
				core.KindSkill: {Tier: Tier1, Segments: []string{"skills"}, Shape: LeafDir},
				// claude-code reads <root>/rules/<name>.md as part of init: a drop-in
				// directory of one file per instruction; tier 1 (manifesto 18, 22).
				core.KindInstruction: {Tier: Tier1, Segments: []string{"rules"}, Shape: LeafFile, Ext: ".md"},
			},
		},
		Spec{
			// codex-cli discovers a user's personal skills under ~/.agents/skills/,
			// each a directory with SKILL.md, scanned by presence; tier 1. This is a
			// cross-agent location (copilot-cli also reads ~/.agents/skills), so a
			// skill installed here for codex is visible to other agents that scan it;
			// see the callouts. CODEX_HOME relocates ~/.codex state but not skill
			// discovery, so it is not an override here.
			// Source: developers.openai.com/codex/skills.
			ID:   core.AgentCodexCLI,
			Base: Base{HomeRelative: []string{".agents"}},
			Rules: map[core.Kind]InstallRule{
				core.KindSkill: {Tier: Tier1, Segments: []string{"skills"}, Shape: LeafDir},
				// codex reads one global instruction file at ~/.codex/AGENTS.md
				// (CODEX_HOME relocates ~/.codex). A different root than its skills
				// (slice 2), and a singleton: many source instructions cannot all land
				// here, so precedence/conflict (manifesto 35) picks the one occupant.
				// Source: developers.openai.com/codex/guides/agents-md.
				core.KindInstruction: {
					Tier: Tier2, Base: &Base{HomeRelative: []string{".codex"}, EnvOverride: "CODEX_HOME"},
					Shape: LeafFixed, Filename: "AGENTS.md",
				},
			},
		},
		Spec{
			// copilot-cli reads both ~/.copilot/skills and the cross-agent
			// ~/.agents/skills. Under the shared-first policy botfile installs to the
			// shared ~/.agents/skills (the same root codex uses), so one symlink
			// serves every agent that reads it. Consequence: a skill here reaches the
			// whole shared pool, so skill selection is coarse across it (see
			// callouts/per-agent-skill-selection-needs-isolated-namespaces.md). Tier 1. No documented
			// home-override variable.
			ID:   core.AgentCopilotCLI,
			Base: Base{HomeRelative: []string{".agents"}},
			Rules: map[core.Kind]InstallRule{
				core.KindSkill: {Tier: Tier1, Segments: []string{"skills"}, Shape: LeafDir},
				// copilot-cli auto-loads one home instruction file,
				// ~/.copilot/copilot-instructions.md (COPILOT_HOME relocates ~/.copilot).
				// The ~/.copilot/instructions/ directory is not auto-loaded (it needs
				// COPILOT_CUSTOM_INSTRUCTIONS_DIRS, the env-var registration this design
				// retired), so the singleton file is the only conformant surface.
				// Source: docs.github.com/copilot/.../copilot-cli/.../add-custom-instructions.
				core.KindInstruction: {
					Tier: Tier2, Base: &Base{HomeRelative: []string{".copilot"}, EnvOverride: "COPILOT_HOME"},
					Shape: LeafFixed, Filename: "copilot-instructions.md",
				},
			},
		},
		Spec{
			// crush scans several skill dirs, including the cross-agent
			// ~/.agents/skills drop-in (alongside ~/.claude/skills,
			// ~/.config/crush/skills, and ~/.config/agents/skills). botfile installs
			// to the shared ~/.agents/skills so one symlink serves crush and every
			// other agent that reads it; tier 1, found by presence.
			// Source: github.com/charmbracelet/crush (Agent Skills).
			ID:   core.AgentCrush,
			Base: Base{HomeRelative: []string{".agents"}},
			Rules: map[core.Kind]InstallRule{
				core.KindSkill: {Tier: Tier1, Segments: []string{"skills"}, Shape: LeafDir},
				// crush reads one user-level instruction file at
				// ~/.config/crush/CRUSH.md (its own, isolated; the generic
				// ~/.config/AGENTS.md cross-tool surface is deliberately not modeled
				// here). A singleton, like codex. Project-scoped context files
				// (./CRUSH.md, ./AGENTS.md) are out of scope: botfile is user-scope.
				// Source: github.com/charmbracelet/crush.
				core.KindInstruction: {
					Tier: Tier2, Base: &Base{HomeRelative: []string{".config", "crush"}},
					Shape: LeafFixed, Filename: "CRUSH.md",
				},
			},
		},
		Spec{
			// opencode scans several skill dirs, including the cross-agent
			// ~/.agents/skills/<skill>/SKILL.md drop-in (alongside
			// ~/.config/opencode/skills and ~/.claude/skills). botfile installs to
			// the shared ~/.agents/skills so one symlink serves opencode and every
			// other agent that reads it; tier 1, found by presence. No documented
			// home-override variable for skill discovery.
			// Source: opencode.ai/docs/skills.
			ID:   core.AgentOpenCode,
			Base: Base{HomeRelative: []string{".agents"}},
			Rules: map[core.Kind]InstallRule{
				core.KindSkill: {Tier: Tier1, Segments: []string{"skills"}, Shape: LeafDir},
				// opencode reads one global instruction file at
				// ~/.config/opencode/AGENTS.md (it also falls back to ~/.claude/CLAUDE.md;
				// we target its own canonical path). A singleton, like codex.
				// Source: opencode.ai/docs/rules.
				core.KindInstruction: {
					Tier: Tier2, Base: &Base{HomeRelative: []string{".config", "opencode"}},
					Shape: LeafFixed, Filename: "AGENTS.md",
				},
			},
		},
		Spec{
			// pi.dev scans both its own ~/.pi/agent/skills and the cross-agent
			// ~/.agents/skills (in the shared dir only directories with a SKILL.md
			// are skills; root .md files are ignored). botfile targets the shared
			// dir to stay on the cross-agent convention; tier 1, found by presence.
			// Source: pi.dev/docs/latest/skills.
			ID:   core.AgentPiDev,
			Base: Base{HomeRelative: []string{".agents"}},
			Rules: map[core.Kind]InstallRule{
				core.KindSkill: {Tier: Tier1, Segments: []string{"skills"}, Shape: LeafDir},
				// pi.dev reads one global instruction file at ~/.pi/agent/AGENTS.md.
				// A singleton, like codex.
				// Source: pi.dev/docs/latest (pi loads ~/.pi/agent/AGENTS.md globally).
				core.KindInstruction: {
					Tier: Tier2, Base: &Base{HomeRelative: []string{".pi", "agent"}},
					Shape: LeafFixed, Filename: "AGENTS.md",
				},
			},
		},
	)
	if err != nil {
		panic("agent.Default: " + err.Error())
	}
	return set
}
