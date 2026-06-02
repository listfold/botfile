package core

import "fmt"

// Selection declares which components are registered with which agents
// (manifesto 11). It is the literal struct of manifesto 39, realized with a
// validated AgentID slice.
//
// PluginName is a concrete plugin name or Wildcard for every plugin in the
// source. ComponentID is "<kind>/<name>" or Wildcard for every component in the
// matched plugins (manifesto 39).
type Selection struct {
	SourceName  string    // matches Source.Name
	PluginName  string    // a plugin name, or Wildcard for all plugins in the source
	ComponentID string    // "<kind>/<name>", or Wildcard for all in the plugin
	Agents      []AgentID // agent IDs this selection targets
}

// MatchesAllPlugins reports whether the selection targets every plugin in its
// source rather than one named plugin.
func (s Selection) MatchesAllPlugins() bool {
	return s.PluginName == Wildcard
}

// MatchesAllComponents reports whether the selection targets every component in
// the matched plugins rather than one named component.
func (s Selection) MatchesAllComponents() bool {
	return s.ComponentID == Wildcard
}

// Validate checks a selection in isolation. The cross-check that SourceName
// refers to a declared source lives in Config.Validate, which can see the whole
// configuration.
func (s Selection) Validate() error {
	if err := ValidateName("selection source", s.SourceName); err != nil {
		return err
	}
	if s.PluginName != Wildcard {
		if err := ValidateName("selection plugin", s.PluginName); err != nil {
			return err
		}
	}
	if _, _, err := ParseComponentID(s.ComponentID); err != nil {
		return fmt.Errorf("selection on source %q: %w", s.SourceName, err)
	}
	if len(s.Agents) == 0 {
		return fmt.Errorf("selection on source %q targets no agents", s.SourceName)
	}
	seen := make(map[AgentID]bool, len(s.Agents))
	for _, agent := range s.Agents {
		if !IsKnownAgent(agent) {
			return fmt.Errorf("selection on source %q targets unknown agent %q", s.SourceName, agent)
		}
		if seen[agent] {
			return fmt.Errorf("selection on source %q lists agent %q more than once", s.SourceName, agent)
		}
		seen[agent] = true
	}
	return nil
}
