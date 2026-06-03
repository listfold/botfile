// Package adopt holds botfile's pure adopt planner. Given a request to bring an
// unmanaged component under management, the configuration, the scanned sources,
// and the discovered components, it computes the steps to take (move the
// component into a source, symlink it back, and add a selection if one is
// needed) or a typed problem, all without I/O (manifesto 36, 42).
//
// The effect interpreter executes the resulting Plan as a saga, the same shape
// as apply: each step has a compensating undo and the run is all-or-nothing.
package adopt

import (
	"fmt"
	"path/filepath"

	"codeberg.org/botfile/botfile/internal/core"
	"codeberg.org/botfile/botfile/internal/discover"
	"codeberg.org/botfile/botfile/internal/project"
	"codeberg.org/botfile/botfile/internal/source"
)

// Request is what the user asked to adopt: the path of an unmanaged component in
// an agent's namespace, and the source and plugin to move it into.
type Request struct {
	Path       string
	SourceName string
	PluginName string
}

// Plan is the validated, computed adopt operation. Move the component From its
// place in the agent namespace To its destination in the source, then create a
// symlink at From pointing To it. AddSelection is the selection to add to the
// config so the symlink is kept, or nil when an existing selection already
// covers the component.
type Plan struct {
	Kind         core.Kind
	Name         string
	Agents       []core.AgentID
	From         string
	To           string
	AddSelection *core.Selection
}

// ProblemKind classifies why a request cannot be adopted.
type ProblemKind int

const (
	// ProblemNotAdoptable: the path is not an adoptable component (not found in
	// the scanned namespaces, already managed, or not component-shaped).
	ProblemNotAdoptable ProblemKind = iota
	// ProblemUnknownSource: the requested source is not declared/scanned.
	ProblemUnknownSource
	// ProblemInvalidPlugin: the requested plugin name is not a valid name.
	ProblemInvalidPlugin
	// ProblemCollision: the source/plugin already has a component of this
	// kind and name.
	ProblemCollision
)

// Problem is a typed reason a request cannot be adopted.
type Problem struct {
	Kind   ProblemKind
	Detail string
}

// Compute returns the adopt Plan for the request, or a Problem. It is pure.
func Compute(req Request, cfg core.Config, sources []project.Source, found []discover.Unmanaged) (Plan, *Problem) {
	comp := locate(req.Path, found)
	if comp == nil {
		return Plan{}, &Problem{ProblemNotAdoptable,
			fmt.Sprintf("%q is not an adoptable component (not found, already managed, or not in a scanned namespace)", req.Path)}
	}

	src := findSource(req.SourceName, sources)
	if src == nil {
		return Plan{}, &Problem{ProblemUnknownSource, fmt.Sprintf("unknown source %q", req.SourceName)}
	}

	if err := core.ValidateName("plugin name", req.PluginName); err != nil {
		return Plan{}, &Problem{ProblemInvalidPlugin, err.Error()}
	}

	kindDir, ok := source.DirForKind(comp.Kind)
	if !ok {
		return Plan{}, &Problem{ProblemNotAdoptable, fmt.Sprintf("no source layout for kind %q", comp.Kind)}
	}
	leaf := source.ComponentLeaf(core.Component{Kind: comp.Kind, Name: comp.Name})
	to := filepath.Join(src.Root, req.PluginName, kindDir, leaf)

	if hasComponent(*src, req.PluginName, comp.Kind, comp.Name) {
		return Plan{}, &Problem{ProblemCollision,
			fmt.Sprintf("source %q plugin %q already has %s", req.SourceName, req.PluginName, comp.Ref())}
	}

	add := neededSelection(req, *comp, cfg)

	return Plan{
		Kind: comp.Kind, Name: comp.Name, Agents: comp.Agents,
		From: comp.Path, To: to, AddSelection: add,
	}, nil
}

// locate finds the discovered component matching the requested path.
func locate(path string, found []discover.Unmanaged) *discover.Unmanaged {
	clean := filepath.Clean(path)
	for i := range found {
		if filepath.Clean(found[i].Path) == clean {
			return &found[i]
		}
	}
	return nil
}

func findSource(name string, sources []project.Source) *project.Source {
	for i := range sources {
		if sources[i].Name == name {
			return &sources[i]
		}
	}
	return nil
}

// hasComponent reports whether a scanned source's plugin already holds a
// component of the given kind and name.
func hasComponent(src project.Source, plugin string, kind core.Kind, name string) bool {
	for _, p := range src.Plugins {
		if p.Name != plugin {
			continue
		}
		for _, c := range p.Components {
			if c.Kind == kind && c.Name == name {
				return true
			}
		}
	}
	return false
}

// neededSelection returns a selection to add so the adopted component is kept by
// sync, or nil if existing selections already cover it for every agent that
// reads its namespace. When some agents are covered and others are not, the
// selection targets only the uncovered ones (the smallest entry).
func neededSelection(req Request, comp discover.Unmanaged, cfg core.Config) *core.Selection {
	var uncovered []core.AgentID
	for _, a := range comp.Agents {
		if !coveredBy(cfg.Selections, req.SourceName, req.PluginName, comp, a) {
			uncovered = append(uncovered, a)
		}
	}
	if len(uncovered) == 0 {
		return nil
	}
	return &core.Selection{
		SourceName:  req.SourceName,
		PluginName:  req.PluginName,
		ComponentID: comp.Ref(),
		Agents:      uncovered,
	}
}

// coveredBy reports whether any selection already selects this component, in this
// source and plugin, for the given agent.
func coveredBy(selections []core.Selection, srcName, pluginName string, comp discover.Unmanaged, a core.AgentID) bool {
	for _, sel := range selections {
		if sel.SourceName != srcName {
			continue
		}
		if !sel.MatchesAllPlugins() && sel.PluginName != pluginName {
			continue
		}
		if !sel.MatchesAllComponents() {
			ref, _, err := core.ParseComponentID(sel.ComponentID)
			if err != nil || ref.Kind != comp.Kind || ref.Name != comp.Name {
				continue
			}
		}
		for _, x := range sel.Agents {
			if x == a {
				return true
			}
		}
	}
	return false
}
