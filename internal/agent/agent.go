// Package agent holds botfile's vendor matrix: which (agent, kind) cells are
// supported and how each component installs into an agent's native namespace
// (manifesto 16-25). It is expressed as data (the capability-matrix-as-data
// pattern, reviews/patterns.md), so support is a table lookup rather than
// scattered branching, and adding an agent or kind is adding an entry.
//
// The projection layer consumes this matrix as an injected value, not a global,
// so where the specs come from (the built-in Default now, or a future embedded
// or OS-specific file, or a config override) is a decision separate from how a
// target path is computed.
package agent

import (
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

// InstallRule is the vendor spec for one (agent, kind) cell: the rubric tier plus
// the path projection (the kind's directory relative to the user home, and how
// the component leaf is named).
type InstallRule struct {
	Tier     Tier
	Segments []string // the kind's directory, relative to the user home
	Shape    LeafShape
	Ext      string // leaf extension when Shape is LeafFile (for example ".md")
}

// Agent is one agent's supported kinds and their install rules.
type Agent struct {
	ID    core.AgentID
	rules map[core.Kind]InstallRule
}

// Supports reports whether the agent supports a component kind (manifesto 24).
func (a Agent) Supports(kind core.Kind) bool {
	_, ok := a.rules[kind]
	return ok
}

// Target returns the absolute install path for a component of kind/name in the
// agent's namespace under home, and whether the agent supports that kind. The
// path is the agent's native per-kind directory plus the component leaf: a
// directory for a skill, a <name><ext> file for a memory (manifesto 48).
func (a Agent) Target(home string, kind core.Kind, name string) (string, bool) {
	rule, ok := a.rules[kind]
	if !ok {
		return "", false
	}
	leaf := name
	if rule.Shape == LeafFile {
		leaf = name + rule.Ext
	}
	parts := make([]string, 0, len(rule.Segments)+2)
	parts = append(parts, home)
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

// Default is botfile's built-in vendor matrix.
//
// Only cells verified against an agent's real behavior are included; an
// unverified or unsupported (agent, kind) is intentionally absent, so the
// projection reports it as unsupported rather than installing to a guessed path
// (manifesto 24, 25). Today claude-code is specified; the other installed agents
// (codex-cli, copilot-cli) and the rest are pending vendor confirmation of their
// skill and memory directories before they earn an entry here.
func Default() Set {
	return Set{agents: map[core.AgentID]Agent{
		core.AgentClaudeCode: {
			ID: core.AgentClaudeCode,
			rules: map[core.Kind]InstallRule{
				// claude-code scans ~/.claude/skills/<skill>/ (agentskills.io); tier 1
				// auto-discovery (manifesto 22).
				core.KindSkill: {Tier: Tier1, Segments: []string{".claude", "skills"}, Shape: LeafDir},
				// claude-code reads ~/.claude/rules/<name>.md as part of init; tier 1
				// (manifesto 18, 22).
				core.KindMemory: {Tier: Tier1, Segments: []string{".claude", "rules"}, Shape: LeafFile, Ext: ".md"},
			},
		},
	}}
}
