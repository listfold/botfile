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

	"codeberg.org/botfile/botfile/internal/core"
)

// Tier is the support-rubric tier an (agent, kind) cell qualifies under
// (manifesto 22-25): tier 1 is native auto-discovery, tier 2 is a one-time
// settings or env-var registration. The projection treats both as supported; the
// distinction drives later registration work, not path computation.
type Tier int

const (
	Tier1 Tier = 1
	Tier2 Tier = 2
)

// LeafShape is how a component's installed entry is named in the agent's
// namespace: a directory named <name> (a skill) or a file <name><ext> (a
// memory), per manifesto 48.
type LeafShape int

const (
	LeafDir LeafShape = iota
	LeafFile
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
type InstallRule struct {
	Tier     Tier
	Segments []string // the kind's directory, relative to the agent config root
	Shape    LeafShape
	Ext      string // leaf extension when Shape is LeafFile (for example ".md")
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

// Root resolves the agent's config root under home, consulting getenv for the
// agent's override variable first (manifesto-agnostic vendor behavior, for
// example claude-code's CLAUDE_CONFIG_DIR). getenv is injected so the env read
// stays at the boundary; pass os.Getenv from the runtime, or a fake in tests. A
// nil getenv behaves as if no variable is set.
func (a Agent) Root(home string, getenv func(string) string) string {
	if a.base.EnvOverride != "" && getenv != nil {
		if v := getenv(a.base.EnvOverride); v != "" {
			return v
		}
	}
	parts := make([]string, 0, len(a.base.HomeRelative)+1)
	parts = append(parts, home)
	parts = append(parts, a.base.HomeRelative...)
	return filepath.Join(parts...)
}

// Target returns the absolute install path for a component of kind/name given
// the agent's already-resolved config root, and whether the agent supports that
// kind. The path is the kind's native per-kind directory under root plus the
// component leaf: a directory for a skill, a <name><ext> file for a memory
// (manifesto 48).
func (a Agent) Target(root string, kind core.Kind, name string) (string, bool) {
	rule, ok := a.rules[kind]
	if !ok {
		return "", false
	}
	leaf := name
	if rule.Shape == LeafFile {
		leaf = name + rule.Ext
	}
	parts := make([]string, 0, len(rule.Segments)+2)
	parts = append(parts, root)
	parts = append(parts, rule.Segments...)
	parts = append(parts, leaf)
	return filepath.Join(parts...), true
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

// ResolveRoots resolves every agent's config root under home, consulting getenv
// for overrides. The runtime calls this once (with os.Getenv) and passes the
// result to the pure projection, so projection never reads the environment.
func (s Set) ResolveRoots(home string, getenv func(string) string) map[core.AgentID]string {
	roots := make(map[core.AgentID]string, len(s.agents))
	for id, a := range s.agents {
		roots[id] = a.Root(home, getenv)
	}
	return roots
}

// NewAgent validates a Spec into an Agent. It rejects an unknown agent id, an
// empty config-root base, no rules, and any rule with an unknown kind, empty
// segments, an invalid tier, or a leaf shape inconsistent with its extension.
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
		if len(rule.Segments) == 0 {
			return Agent{}, fmt.Errorf("agent %q kind %q: empty segments", sp.ID, kind)
		}
		for _, seg := range rule.Segments {
			if seg == "" {
				return Agent{}, fmt.Errorf("agent %q kind %q: empty path segment", sp.ID, kind)
			}
		}
		if rule.Tier != Tier1 && rule.Tier != Tier2 {
			return Agent{}, fmt.Errorf("agent %q kind %q: invalid tier %d", sp.ID, kind, rule.Tier)
		}
		switch rule.Shape {
		case LeafDir:
			if rule.Ext != "" {
				return Agent{}, fmt.Errorf("agent %q kind %q: a directory leaf must not set an extension", sp.ID, kind)
			}
		case LeafFile:
			if rule.Ext == "" {
				return Agent{}, fmt.Errorf("agent %q kind %q: a file leaf requires an extension", sp.ID, kind)
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
// codex-cli, and copilot-cli. Memory is specified only for claude-code; codex
// and copilot memory remain unsupported because each would need a SessionStart
// hook, which botfile never does (manifesto 18, 25). opencode, copilot-vscode,
// and pi.dev are pending vendor confirmation.
func Default() Set {
	set, err := NewSet(
		Spec{
			ID:   core.AgentClaudeCode,
			Base: Base{HomeRelative: []string{".claude"}, EnvOverride: "CLAUDE_CONFIG_DIR"},
			Rules: map[core.Kind]InstallRule{
				// claude-code scans <root>/skills/<skill>/ (agentskills.io); tier 1
				// auto-discovery (manifesto 22).
				core.KindSkill: {Tier: Tier1, Segments: []string{"skills"}, Shape: LeafDir},
				// claude-code reads <root>/rules/<name>.md as part of init; tier 1
				// (manifesto 18, 22).
				core.KindMemory: {Tier: Tier1, Segments: []string{"rules"}, Shape: LeafFile, Ext: ".md"},
			},
		},
		Spec{
			// codex-cli discovers personal skills under ~/.codex/skills/ (CODEX_HOME
			// overrides ~/.codex), scanning the tree for SKILL.md by presence; tier 1.
			ID:   core.AgentCodexCLI,
			Base: Base{HomeRelative: []string{".codex"}, EnvOverride: "CODEX_HOME"},
			Rules: map[core.Kind]InstallRule{
				core.KindSkill: {Tier: Tier1, Segments: []string{"skills"}, Shape: LeafDir},
			},
		},
		Spec{
			// copilot-cli discovers personal skills under ~/.copilot/skills/, each a
			// directory with SKILL.md, found automatically; tier 1. No documented
			// home-override variable.
			ID:   core.AgentCopilotCLI,
			Base: Base{HomeRelative: []string{".copilot"}},
			Rules: map[core.Kind]InstallRule{
				core.KindSkill: {Tier: Tier1, Segments: []string{"skills"}, Shape: LeafDir},
			},
		},
	)
	if err != nil {
		panic("agent.Default: " + err.Error())
	}
	return set
}
