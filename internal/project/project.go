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
	// ProblemNoLayout: the source grammar has no directory mapping for a
	// component's kind, so its source destination cannot be built. This guards
	// against a matrix supporting a kind the source layout cannot represent.
	ProblemNoLayout
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
	case ProblemNoLayout:
		return "no-layout"
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

// NoticeKind classifies a non-blocking projection notice: the link is produced,
// but the user should know something about its effect.
type NoticeKind int

const (
	// NoticeSharedSkillNamespace: a selection scoped skills to a subset of a
	// shared skills directory, so other agents that read that directory will also
	// see the skills. Reported so a scope botfile cannot enforce never lies
	// silently (manifesto 49).
	NoticeSharedSkillNamespace NoticeKind = iota
)

// String renders a NoticeKind as a stable, human-readable token.
func (k NoticeKind) String() string {
	switch k {
	case NoticeSharedSkillNamespace:
		return "shared-skill-namespace"
	default:
		return "unknown-notice"
	}
}

// Notice is a non-blocking projection outcome: the install happens, but its
// effect is broader than the selection literally named, and the user is told so.
// It carries the selection's selectors (PluginName, ComponentID) so a consumer
// can point the user at the exact selection that caused the broader reach, and
// can tell two notices apart without relying on slice position.
type Notice struct {
	Kind        NoticeKind
	SourceName  string
	PluginName  string         // the selection's plugin selector ("*" for all)
	ComponentID string         // the selection's component selector ("*" for all)
	Namespace   string         // the shared directory the skills install into
	Selected    []core.AgentID // agents the selection named that share this namespace
	AlsoReaches []core.AgentID // other agents that read the namespace and will also see the skills
	Detail      string
}

// Result is the projection output: the desired links for the planner, the
// problems that prevented some selections from producing links, and the notices
// about installs whose reach exceeds what a selection named. All are sorted for
// determinism.
type Result struct {
	Links    []reconcile.LinkSpec
	Problems []Problem
	Notices  []Notice
}

// Project maps cfg's selections over the scanned sources and the agent matrix,
// producing desired links plus problems. It is pure: the agents' config roots
// are resolved by the caller (agent.Set.ResolveRoots) and passed in as roots, so
// projection never reads the environment.
func Project(cfg core.Config, sources []Source, agents agent.Set, roots agent.Roots) Result {
	byName := make(map[string]Source, len(sources))
	for _, s := range sources {
		byName[s.Name] = s
	}

	// skillNS maps each skill-supporting agent to its skill directory, so a
	// selection that scopes skills to a subset of a shared directory can be
	// flagged (manifesto 49).
	skillNS := skillNamespaces(agents, roots)

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
				root, ok := roots.For(agentID, m.comp.Kind)
				if !ok || root == "" {
					res.Problems = append(res.Problems, Problem{
						Kind: ProblemUnsupported, SourceName: src.Name, Agent: agentID,
						Component: m.comp.Ref().String(),
						Detail:    "no resolved config root for this agent and kind",
					})
					continue
				}
				target, supported := ag.Target(root, m.comp.Kind, m.comp.Name)
				if !supported {
					res.Problems = append(res.Problems, Problem{
						Kind: ProblemUnsupported, SourceName: src.Name, Agent: agentID,
						Component: m.comp.Ref().String(),
						Detail:    "agent does not support this component kind",
					})
					continue
				}
				dest, ok := destPath(src.Root, m.plugin, m.comp)
				if !ok {
					res.Problems = append(res.Problems, Problem{
						Kind: ProblemNoLayout, SourceName: src.Name, Agent: agentID,
						Component: m.comp.Ref().String(),
						Detail:    "source grammar has no directory for this component kind",
					})
					continue
				}
				res.Links = append(res.Links, reconcile.LinkSpec{
					Target:     target,
					Dest:       dest,
					SourceName: src.Name,
				})
			}
		}

		// If this selection installs skills into a shared directory that agents
		// it did not name also read, say so (manifesto 49): a scope botfile cannot
		// enforce must never pass silently.
		if matchesAnySkill(matched) {
			res.Notices = append(res.Notices, skillNotices(sel, src.Name, skillNS)...)
		}
	}

	sortResult(&res)
	return res
}

// skillNamespaces maps each agent that supports skills to its skill directory.
func skillNamespaces(agents agent.Set, roots agent.Roots) map[core.AgentID]string {
	ns := make(map[core.AgentID]string)
	for _, id := range agents.IDs() {
		ag, ok := agents.Lookup(id)
		if !ok {
			continue
		}
		root, ok := roots.For(id, core.KindSkill)
		if !ok || root == "" {
			continue
		}
		if dir, ok := ag.Namespace(root, core.KindSkill); ok {
			ns[id] = dir
		}
	}
	return ns
}

// matchesAnySkill reports whether any matched component is a skill.
func matchesAnySkill(matched []match) bool {
	for _, m := range matched {
		if m.comp.Kind == core.KindSkill {
			return true
		}
	}
	return false
}

// skillNotices flags each shared skill directory this selection scopes to a
// subset of: for every directory a named skill-agent installs into, any other
// skill-agent that reads the same directory but was not named is an "also
// reaches" surprise (manifesto 49).
func skillNotices(sel core.Selection, sourceName string, skillNS map[core.AgentID]string) []Notice {
	reachByDir := make(map[string][]core.AgentID)
	for id, dir := range skillNS {
		reachByDir[dir] = append(reachByDir[dir], id)
	}

	var notices []Notice
	seenDir := make(map[string]bool)
	for _, a := range sel.Agents {
		dir, ok := skillNS[a]
		if !ok || seenDir[dir] {
			continue
		}
		seenDir[dir] = true

		named := make(map[core.AgentID]bool)
		for _, b := range sel.Agents {
			if skillNS[b] == dir {
				named[b] = true
			}
		}

		var selected, surprise []core.AgentID
		for _, id := range reachByDir[dir] {
			if named[id] {
				selected = append(selected, id)
			} else {
				surprise = append(surprise, id)
			}
		}
		if len(surprise) == 0 {
			continue
		}
		sortAgents(selected)
		sortAgents(surprise)
		notices = append(notices, Notice{
			Kind: NoticeSharedSkillNamespace, SourceName: sourceName,
			PluginName: sel.PluginName, ComponentID: sel.ComponentID, Namespace: dir,
			Selected: selected, AlsoReaches: surprise,
			Detail: "skills scoped to these agents install into a shared directory other agents also read",
		})
	}
	return notices
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
// grammar: <root>/<plugin>/<kindDir>/<leaf> (manifesto 46-48). It returns false
// when the source layout has no directory for the component's kind.
func destPath(sourceRoot, plugin string, c core.Component) (string, bool) {
	dir, ok := source.DirForKind(c.Kind)
	if !ok {
		return "", false
	}
	return filepath.Join(sourceRoot, plugin, dir, source.ComponentLeaf(c)), true
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
	sort.Slice(res.Notices, func(i, j int) bool {
		a, b := res.Notices[i], res.Notices[j]
		if a.SourceName != b.SourceName {
			return a.SourceName < b.SourceName
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.PluginName != b.PluginName {
			return a.PluginName < b.PluginName
		}
		return a.ComponentID < b.ComponentID
	})
}

// sortAgents sorts a slice of agent IDs in place, for deterministic output.
func sortAgents(ids []core.AgentID) {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
}
