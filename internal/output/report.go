// Package output turns a finished run into one presentation document (Report),
// the single place where outcome classification, exit code, issue grouping, and
// summary counts are decided. RenderText and RenderJSON are then a boring walk
// over that Report, so the human and machine forms can never disagree. The
// package is pure (no I/O beyond the writer the renderers take) and json-tagged,
// so a future TUI can reuse ReportFromModel without scraping prose.
package output

import (
	"fmt"
	"sort"

	"codeberg.org/botfile/botfile/internal/core"
	"codeberg.org/botfile/botfile/internal/discover"
	"codeberg.org/botfile/botfile/internal/project"
	"codeberg.org/botfile/botfile/internal/reconcile"
	"codeberg.org/botfile/botfile/internal/runtime"
)

// SchemaVersion is the version of the Report shape consumers read. Bump it on an
// incompatible change.
const SchemaVersion = 1

// Report is the classified outcome of a run. Empty categories are omitted, so a
// clean run is small; Summary always carries the counts.
type Report struct {
	SchemaVersion int    `json:"schemaVersion"`
	Command       string `json:"command"` // plan | sync | status | adopt
	Phase         string `json:"phase"`   // done | blocked | failed | incomplete
	Outcome       string `json:"outcome"` // ok | blocked | failed
	ExitCode      int    `json:"exitCode"`

	Ops     []Op        `json:"ops,omitempty"`
	Issues  []Issue     `json:"issues,omitempty"`
	Notes   []Note      `json:"notes,omitempty"`
	Status  *StatusDTO  `json:"status,omitempty"`
	Adopt   *AdoptDTO   `json:"adopt,omitempty"`
	Failure *FailureDTO `json:"failure,omitempty"`
	Summary Summary     `json:"summary"`

	// incompletePhase carries the raw phase int for the "incomplete (phase N)"
	// text fallback only; it is never serialized.
	incompletePhase int
}

// Op is a planned or applied filesystem operation.
type Op struct {
	Kind   string `json:"kind"` // create | replace | remove
	Target string `json:"target"`
	Dest   string `json:"dest,omitempty"` // empty for remove
}

// Issue is one blocking reason, the aggregated form (from runtime.Blocker) of
// scan/projection/plan problems and conflicts. Kind is the coarse category;
// Cause is the specific sub-kind token.
type Issue struct {
	Kind   string `json:"kind"`
	Cause  string `json:"cause"`
	Ref    string `json:"ref"`
	Detail string `json:"detail"`
}

// Note is a non-blocking observation. Kind selects which fields are populated:
// "notice" (shared namespace), "shadowed" (precedence), or "skipped"
// (unsupported component).
type Note struct {
	Kind string `json:"kind"`

	Selected    []string `json:"selected,omitempty"`    // notice
	AlsoReaches []string `json:"alsoReaches,omitempty"` // notice
	Namespace   string   `json:"namespace,omitempty"`   // notice
	Selection   string   `json:"selection,omitempty"`   // notice: the selection that caused it

	Target string `json:"target,omitempty"` // shadowed
	Source string `json:"source,omitempty"` // shadowed
	WonBy  string `json:"wonBy,omitempty"`  // shadowed

	Component string `json:"component,omitempty"` // skipped
	Agent     string `json:"agent,omitempty"`     // skipped
	Detail    string `json:"detail,omitempty"`    // skipped
}

// StatusDTO holds what is specific to the status overview.
type StatusDTO struct {
	Managed   []string    `json:"managed"`             // targets already in place
	Adoptable []Adoptable `json:"adoptable,omitempty"` // unmanaged, adoptable components
}

// Adoptable is an unmanaged component discovered in an agent namespace.
type Adoptable struct {
	Agents []string `json:"agents"`
	Ref    string   `json:"ref"`
	Path   string   `json:"path"`
}

// AdoptDTO is the result of an adopt run: the applied steps and labels, or the
// blocking problem.
type AdoptDTO struct {
	Move   *Step  `json:"move,omitempty"`
	Link   *Step  `json:"link,omitempty"`
	Select *Sel   `json:"select,omitempty"` // nil when no selection was added
	Kind   string `json:"kind,omitempty"`
	Name   string `json:"name,omitempty"`
	Source string `json:"source,omitempty"`
	Plugin string `json:"plugin,omitempty"`

	Problem string `json:"problem,omitempty"` // set on the blocked branch
}

// Step is a from/to pair (a move or a link).
type Step struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Sel is a selection adopt added so a sync keeps the adopted component.
type Sel struct {
	ComponentID string   `json:"componentID"`
	Agents      []string `json:"agents"`
}

// FailureDTO reports an effect failure that aborted the run.
type FailureDTO struct {
	Stage   string `json:"stage"`
	Message string `json:"message"`
}

// Summary carries the counts a human sees on the final line, for consumers that
// want the totals without walking every array.
type Summary struct {
	Ops       int `json:"ops"`
	Issues    int `json:"issues"`
	Managed   int `json:"managed"`
	OutOfSync int `json:"outOfSync"`
	Skipped   int `json:"skipped"`
	Adoptable int `json:"adoptable"`
}

// ReportFromModel classifies a finished run into its Report. It is pure, and the
// single source of the process exit code (callers return Report.ExitCode). The
// structure mirrors the historical render dispatch exactly: a PhaseFailed
// short-circuit, then status, then adopt, then plan/sync.
func ReportFromModel(m runtime.Model) Report {
	r := Report{
		SchemaVersion:   SchemaVersion,
		Command:         commandToken(m.Mode),
		Phase:           phaseToken(m.Phase),
		incompletePhase: int(m.Phase),
	}

	if m.Phase == runtime.PhaseFailed {
		r.Outcome, r.ExitCode = "failed", 2
		r.Failure = &FailureDTO{Stage: m.FailedStage, Message: fmt.Sprintf("%v", m.Err)}
		return r
	}

	switch m.Mode {
	case runtime.ModeStatus:
		managed := managedTargets(m)
		r.Ops = opsFrom(m.Plan.Ops)
		r.Issues = issuesFrom(m.Blockers)
		r.Notes = notesFrom(m)
		r.Status = &StatusDTO{Managed: managed, Adoptable: adoptablesFrom(m.Unmanaged)}
		r.Outcome, r.ExitCode = "ok", 0 // a read-only overview always succeeds
		r.Summary = summaryOf(m, len(managed))
		return r

	case runtime.ModeAdopt:
		switch {
		case m.Phase == runtime.PhaseBlocked && m.AdoptProblem != nil:
			r.Adopt = &AdoptDTO{Problem: m.AdoptProblem.Detail}
			r.Outcome, r.ExitCode = "blocked", 1
		case m.Phase == runtime.PhaseDone:
			r.Adopt = adoptFrom(m)
			r.Outcome, r.ExitCode = "ok", 0
		default:
			r.Outcome, r.ExitCode = "failed", 2 // incomplete adopt
		}
		r.Summary = summaryOf(m, 0)
		return r

	default: // plan, sync
		r.Ops = opsFrom(m.Plan.Ops)
		r.Issues = issuesFrom(m.Blockers)
		r.Notes = notesFrom(m)
		switch {
		case m.Phase == runtime.PhaseBlocked:
			r.Outcome, r.ExitCode = "blocked", 1
		case m.Phase == runtime.PhaseDone && m.Mode != runtime.ModeSync && len(m.Blockers) > 0:
			r.Outcome, r.ExitCode = "blocked", 1 // a plan with blockers must not invite a sync
		case m.Phase == runtime.PhaseDone:
			r.Outcome, r.ExitCode = "ok", 0
		default:
			r.Outcome, r.ExitCode = "failed", 2 // incomplete run
		}
		r.Summary = summaryOf(m, 0)
		return r
	}
}

func summaryOf(m runtime.Model, managed int) Summary {
	return Summary{
		Ops:       len(m.Plan.Ops),
		Issues:    len(m.Blockers),
		Managed:   managed,
		OutOfSync: len(m.Plan.Ops) + len(m.Blockers),
		Skipped:   skippedCount(m),
		Adoptable: len(m.Unmanaged),
	}
}

func opsFrom(in []reconcile.Op) []Op {
	var out []Op
	for _, o := range in {
		out = append(out, Op{Kind: o.Kind.String(), Target: o.Target, Dest: o.Dest})
	}
	return out
}

func issuesFrom(in []runtime.Blocker) []Issue {
	var out []Issue
	for _, b := range in {
		out = append(out, Issue{Kind: b.Kind.String(), Cause: b.Cause, Ref: b.Ref, Detail: b.Detail})
	}
	return out
}

// notesFrom builds the non-blocking notes in the historical render order:
// notices, then shadows, then skipped. RenderText emits them in this order.
func notesFrom(m runtime.Model) []Note {
	var out []Note
	for _, n := range m.Projection.Notices {
		out = append(out, Note{
			Kind: "notice", Selected: agentStrings(n.Selected),
			AlsoReaches: agentStrings(n.AlsoReaches), Namespace: n.Namespace,
			Selection: selectionRef(n.SourceName, n.PluginName, n.ComponentID),
		})
	}
	for _, s := range m.Plan.Shadows {
		out = append(out, Note{Kind: "shadowed", Target: s.Target, Source: s.SourceName, WonBy: s.WonBy})
	}
	for _, p := range m.Projection.Problems {
		if p.Kind != project.ProblemUnsupported {
			continue
		}
		out = append(out, Note{Kind: "skipped", Component: p.Component, Agent: string(p.Agent), Detail: p.Detail})
	}
	return out
}

// selectionRef renders the selection that produced a notice as a compact
// source[/plugin[/component]] path, omitting wildcard selectors. Two notices
// can otherwise render identically (same agents, same namespace) when two
// selections scope skills to the same shared-pool subset; the ref keeps them
// distinguishable and points the user at the selection to edit.
func selectionRef(source, plugin, component string) string {
	hasPlugin := plugin != "" && plugin != "*"
	hasComponent := component != "" && component != "*"
	switch {
	case hasPlugin && hasComponent:
		return source + "/" + plugin + "/" + component
	case hasComponent:
		return source + "/*/" + component
	case hasPlugin:
		return source + "/" + plugin
	default:
		return source
	}
}

func adoptFrom(m runtime.Model) *AdoptDTO {
	p := m.AdoptPlan
	ad := &AdoptDTO{
		Move: &Step{From: p.From, To: p.To},
		Link: &Step{From: p.From, To: p.To},
		Kind: string(p.Kind), Name: p.Name,
		Source: m.Adopt.SourceName, Plugin: m.Adopt.PluginName,
	}
	if p.AddSelection != nil {
		ad.Select = &Sel{ComponentID: p.AddSelection.ComponentID, Agents: agentStrings(p.AddSelection.Agents)}
	}
	return ad
}

// managedTargets is the set of projected link targets a sync would not change:
// the components already in place. Sorted for stable output.
func managedTargets(m runtime.Model) []string {
	changing := changingTargets(m.Plan)
	seen := map[string]bool{}
	var managed []string
	for _, l := range m.Projection.Links {
		if changing[l.Target] || seen[l.Target] {
			continue
		}
		seen[l.Target] = true
		managed = append(managed, l.Target)
	}
	sort.Strings(managed)
	return managed
}

// changingTargets is the set of target paths a sync would touch (an op) or that
// are blocked (a conflict or plan problem).
func changingTargets(p reconcile.Plan) map[string]bool {
	s := map[string]bool{}
	for _, op := range p.Ops {
		s[op.Target] = true
	}
	for _, c := range p.Conflicts {
		s[c.Target] = true
	}
	for _, pr := range p.Problems {
		s[pr.Target] = true
	}
	return s
}

func adoptablesFrom(in []discover.Unmanaged) []Adoptable {
	var out []Adoptable
	for _, u := range in {
		out = append(out, Adoptable{Agents: agentStrings(u.Agents), Ref: u.Ref(), Path: u.Path})
	}
	return out
}

func skippedCount(m runtime.Model) int {
	n := 0
	for _, p := range m.Projection.Problems {
		if p.Kind == project.ProblemUnsupported {
			n++
		}
	}
	return n
}

func agentStrings(ids []core.AgentID) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = string(id)
	}
	return out
}

func commandToken(mode runtime.Mode) string {
	switch mode {
	case runtime.ModePlan:
		return "plan"
	case runtime.ModeSync:
		return "sync"
	case runtime.ModeStatus:
		return "status"
	case runtime.ModeAdopt:
		return "adopt"
	default:
		return "unknown"
	}
}

func phaseToken(p runtime.Phase) string {
	switch p {
	case runtime.PhaseDone:
		return "done"
	case runtime.PhaseBlocked:
		return "blocked"
	case runtime.PhaseFailed:
		return "failed"
	default:
		return "incomplete"
	}
}
