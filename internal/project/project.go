// Package project is botfile's projection: the pure join that wires the
// configuration's selections, the scanned sources, and the agent vendor matrix
// into the desired links the planner consumes. For each selection it expands the
// matched components (manifesto 39 wildcards) across the targeted agents,
// computing each component's install target from the matrix and its source
// destination from the source grammar (manifesto 46-48).
//
// Like the planner it is total and pure (home is passed in, not read) and uses
// the explicit-outcome-algebra pattern: a selection that cannot produce a link
// yields a typed Problem (unknown source, no match, unsupported agent/kind)
// rather than being silently dropped.
package project

import (
	"path/filepath"
	"sort"

	"codeberg.org/botfile/botfile/internal/agent"
	"codeberg.org/botfile/botfile/internal/core"
	"codeberg.org/botfile/botfile/internal/reconcile"
	"codeberg.org/botfile/botfile/internal/source"
)

// Source is a scanned source ready for projection: its name, absolute root path,
// and the plugins the scanner found.
type Source struct {
	Name    string
	Root    string
	Plugins []core.Plugin
}

// ProblemKind classifies why a selection produced no link for some component or
// agent. It is the projection's outcome branch for inputs that did not become a
// LinkSpec, distinct from the planner's later Problems about the links.
type ProblemKind int

const (
	// ProblemUnknownSource: a selection names a source that was not provided
	// (for example its scan failed or it is absent).
	ProblemUnknownSource ProblemKind = iota
	// ProblemEmptySelection: a selection's plugin/component selector matched no
	// component in the source (commonly a typo).
	ProblemEmptySelection
	// ProblemUnsupported: a targeted agent has no vendor spec for the component's
	// kind, so botfile cannot install it (manifesto 24, 25).
	ProblemUnsupported
)

// String renders a ProblemKind as a stable, human-readable token.
func (k ProblemKind) String() string {
	switch k {
	case ProblemUnknownSource:
		return "unknown-source"
	case ProblemEmptySelection:
		return "empty-selection"
	case ProblemUnsupported:
		return "unsupported"
	default:
		return "unknown-problem"
	}
}

// Problem is a projection outcome: a selection that did not yield a link, with
// the context needed to report it.
type Problem struct {
	Kind       ProblemKind
	SourceName string
	Agent      core.AgentID
	Component  string // "<kind>/<name>" when the problem concerns one component
	Detail     string
}

// Result is the projection output: the desired links for the planner plus the
// problems that prevented some selections from producing links. Both are sorted
// for determinism.
type Result struct {
	Links    []reconcile.LinkSpec
	Problems []Problem
}

// Project maps cfg's selections over the scanned sources and the agent matrix,
// producing desired links plus problems. It is pure: home is passed in.
func Project(cfg core.Config, sources []Source, agents agent.Set, home string) Result {
	byName := make(map[string]Source, len(sources))
	for _, s := range sources {
		byName[s.Name] = s
	}

	var res Result
	for _, sel := range cfg.Selections {
		src, ok := byName[sel.SourceName]
		if !ok {
			res.Problems = append(res.Problems, Problem{
				Kind: ProblemUnknownSource, SourceName: sel.SourceName,
				Detail: "selection references a source that was not scanned",
			})
			continue
		}

		matched := matchComponents(sel, src)
		if len(matched) == 0 {
			res.Problems = append(res.Problems, Problem{
				Kind: ProblemEmptySelection, SourceName: sel.SourceName,
				Detail: "selection matched no component in the source",
			})
			continue
		}

		for _, m := range matched {
			for _, agentID := range sel.Agents {
				ag, ok := agents.Lookup(agentID)
				if !ok {
					res.Problems = append(res.Problems, Problem{
						Kind: ProblemUnsupported, SourceName: src.Name, Agent: agentID,
						Component: m.comp.Ref().String(),
						Detail:    "agent has no vendor spec in the matrix",
					})
					continue
				}
				target, supported := ag.Target(home, m.comp.Kind, m.comp.Name)
				if !supported {
					res.Problems = append(res.Problems, Problem{
						Kind: ProblemUnsupported, SourceName: src.Name, Agent: agentID,
						Component: m.comp.Ref().String(),
						Detail:    "agent does not support this component kind",
					})
					continue
				}
				res.Links = append(res.Links, reconcile.LinkSpec{
					Target:     target,
					Dest:       destPath(src.Root, m.plugin, m.comp),
					SourceName: src.Name,
				})
			}
		}
	}

	sortResult(&res)
	return res
}

// match is one matched (plugin, component) pair within a source.
type match struct {
	plugin string
	comp   core.Component
}

// matchComponents expands a selection's plugin and component selectors over a
// source's scanned plugins (manifesto 39 wildcards).
func matchComponents(sel core.Selection, src Source) []match {
	var out []match
	for _, p := range src.Plugins {
		if !sel.MatchesAllPlugins() && p.Name != sel.PluginName {
			continue
		}
		for _, c := range p.Components {
			if componentMatches(sel, c) {
				out = append(out, match{plugin: p.Name, comp: c})
			}
		}
	}
	return out
}

// componentMatches reports whether a component satisfies a selection's component
// selector. The selector is "*" (all) or a "<kind>/<name>" id, already validated
// by Config.Validate, so a parse failure here is defensive only.
func componentMatches(sel core.Selection, c core.Component) bool {
	if sel.MatchesAllComponents() {
		return true
	}
	ref, _, err := core.ParseComponentID(sel.ComponentID)
	if err != nil {
		return false
	}
	return c.Kind == ref.Kind && c.Name == ref.Name
}

// destPath builds a component's absolute path inside its source from the source
// grammar: <root>/<plugin>/<kindDir>/<leaf> (manifesto 46-48).
func destPath(root, plugin string, c core.Component) string {
	dir, _ := source.DirForKind(c.Kind)
	return filepath.Join(root, plugin, dir, source.ComponentLeaf(c))
}

// sortResult orders links and problems deterministically.
func sortResult(res *Result) {
	sort.Slice(res.Links, func(i, j int) bool {
		if res.Links[i].Target != res.Links[j].Target {
			return res.Links[i].Target < res.Links[j].Target
		}
		if res.Links[i].Dest != res.Links[j].Dest {
			return res.Links[i].Dest < res.Links[j].Dest
		}
		return res.Links[i].SourceName < res.Links[j].SourceName
	})
	sort.Slice(res.Problems, func(i, j int) bool {
		a, b := res.Problems[i], res.Problems[j]
		if a.SourceName != b.SourceName {
			return a.SourceName < b.SourceName
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Component != b.Component {
			return a.Component < b.Component
		}
		return a.Agent < b.Agent
	})
}
