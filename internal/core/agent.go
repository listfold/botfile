// Package core holds botfile's pure domain model: validated value types for
// agents, components, sources, plugins, and selections, plus the configuration
// aggregate that declares desired state (manifesto 38). Nothing in this package
// performs I/O, reads the clock, or touches the environment; it is the pure
// heart that the Elm-style reducer and effect interpreter build on (manifesto
// 42).
package core

// AgentID identifies an agent (harness + model, manifesto 6) that botfile can
// target. It is a named string so selections and the capability matrix can be
// strongly typed while staying faithful to the "agent IDs" shape of the
// Selection struct (manifesto 39).
type AgentID string

// The agents botfile recognizes (manifesto 16). Support for a given component
// kind on a given agent is a separate concern, decided by the support rubric
// (manifesto 22-25); membership here only means the ID is a real agent botfile
// knows how to name.
const (
	AgentClaudeCode    AgentID = "claude-code"
	AgentCodexCLI      AgentID = "codex-cli"
	AgentCopilotCLI    AgentID = "copilot-cli"
	AgentCopilotVSCode AgentID = "copilot-vscode"
	AgentCrush         AgentID = "crush"
	AgentOpenCode      AgentID = "opencode"
	AgentPiDev         AgentID = "pi.dev"
)

// KnownAgents is the canonical, ordered set of agent IDs botfile recognizes
// (manifesto 16). It is the single source for validation and for any surface
// that needs to enumerate agents.
var KnownAgents = []AgentID{
	AgentClaudeCode,
	AgentCodexCLI,
	AgentCopilotCLI,
	AgentCopilotVSCode,
	AgentCrush,
	AgentOpenCode,
	AgentPiDev,
}

// IsKnownAgent reports whether id names an agent botfile recognizes.
func IsKnownAgent(id AgentID) bool {
	for _, known := range KnownAgents {
		if known == id {
			return true
		}
	}
	return false
}
