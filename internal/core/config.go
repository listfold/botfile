package core

import "fmt"

// Config is botfile's declared desired state (manifesto 38): the set of sources
// and the selections that map them onto agents. It is a pure value; loading it
// from config.toml is the config package's job (manifesto 37). Reconciliation
// computes a plan from this desired state and observed filesystem state
// (manifesto 3).
type Config struct {
	Sources    []Source
	Selections []Selection
}

// Validate checks the whole configuration: each source and selection in
// isolation, plus the cross-cutting invariants only the aggregate can see
// (unique source names, and every selection referencing a declared source).
func (c Config) Validate() error {
	byName := make(map[string]bool, len(c.Sources))
	for _, src := range c.Sources {
		if err := src.Validate(); err != nil {
			return err
		}
		if byName[src.Name] {
			return fmt.Errorf("source name %q is declared more than once", src.Name)
		}
		byName[src.Name] = true
	}
	for _, sel := range c.Selections {
		if err := sel.Validate(); err != nil {
			return err
		}
		if !byName[sel.SourceName] {
			return fmt.Errorf("selection references source %q, which is not declared", sel.SourceName)
		}
	}
	return nil
}
