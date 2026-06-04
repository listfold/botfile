// Command botfile is the entrypoint: it resolves the environment (config path,
// home, agent matrix and roots), seeds the pure reducer, drives it through the
// effect interpreter, and renders the final run state. It is the cli layer; all
// orchestration lives in the pure reducer (internal/runtime) and all I/O in the
// interpreter's ports (internal/interp).
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codeberg.org/botfile/botfile/internal/adopt"
	"codeberg.org/botfile/botfile/internal/agent"
	"codeberg.org/botfile/botfile/internal/config"
	"codeberg.org/botfile/botfile/internal/core"
	"codeberg.org/botfile/botfile/internal/interp"
	"codeberg.org/botfile/botfile/internal/project"
	"codeberg.org/botfile/botfile/internal/reconcile"
	"codeberg.org/botfile/botfile/internal/runtime"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run parses the verb against the command contract, executes it, and returns
// the process exit code:
//
//	0 success (plan produced, or sync applied / nothing to do)
//	1 blocked (a problem or conflict prevented apply)
//	2 failed  (an effect or usage error)
func run(args []string) int {
	if len(args) == 0 {
		usage(os.Stderr)
		return 2
	}
	if a := args[0]; a == "-h" || a == "--help" || a == "help" {
		usage(os.Stdout)
		return 0
	}
	inv, err := parse(args)
	if err != nil {
		return usageErr(err)
	}

	var req adopt.Request
	if inv.Cmd.Mode == runtime.ModeAdopt {
		if req, err = adoptRequest(inv); err != nil {
			return usageErr(err)
		}
	}

	e, code := resolveEnv()
	if code != 0 {
		return code
	}
	model, cmd := runtime.Init(inv.Cmd.Mode, e.configPath, e.home, e.agents, e.roots)
	if inv.Cmd.Mode == runtime.ModeAdopt {
		req.Path = resolvePath(req.Path, e.home)
		model.Adopt = req
	}
	model = interp.OSDeps(e.home).Run(model, cmd)
	return render(os.Stdout, model)
}

// usageErr reports a usage-level failure (bad verb, flag, or argument) followed
// by the command summary, and returns the usage exit code.
func usageErr(err error) int {
	fmt.Fprintf(os.Stderr, "botfile: %v\n\n", err)
	usage(os.Stderr)
	return 2
}

// env is the environment the interpreter and reducer need, resolved once at the
// boundary.
type env struct {
	configPath string
	home       string
	agents     agent.Set
	roots      agent.Roots
}

// resolveEnv reads the config path, home, agent matrix, and resolved roots, or
// reports a usage-style failure.
func resolveEnv() (env, int) {
	configPath, err := config.DefaultPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "botfile: %v\n", err)
		return env{}, 2
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "botfile: resolve home: %v\n", err)
		return env{}, 2
	}
	agents := agent.Default()
	return env{configPath, home, agents, agents.ResolveRoots(home, os.Getenv)}, 0
}

// resolvePath expands a leading ~ and makes the path absolute (relative to the
// working directory, where a path argument is naturally rooted).
func resolvePath(path, home string) string {
	switch {
	case path == "~":
		path = home
	case strings.HasPrefix(path, "~/"):
		path = filepath.Join(home, path[2:])
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// render writes a full summary of the run, mapping every outcome the model
// preserves to a distinct line, and returns the exit code. An effect failure is
// terminal and reported on its own; otherwise the operations, informational
// outcomes, and blocking issues are each shown, then a summary line.
func render(w io.Writer, m runtime.Model) int {
	if m.Phase == runtime.PhaseFailed {
		fmt.Fprintf(w, "failed during %s: %v\n", m.FailedStage, m.Err)
		return 2
	}
	if m.Mode == runtime.ModeStatus {
		return renderStatus(w, m)
	}
	if m.Mode == runtime.ModeAdopt {
		return renderAdopt(w, m)
	}

	renderOps(w, m)
	renderInfo(w, m)
	renderIssues(w, m)

	switch m.Phase {
	case runtime.PhaseBlocked:
		fmt.Fprintf(w, "\nblocked: resolve the %d issue(s) above, then run `botfile sync`\n", len(m.Blockers))
		return 1
	case runtime.PhaseDone:
		if m.Mode == runtime.ModeSync {
			fmt.Fprintf(w, "\nsynced: %d operation(s) applied\n", len(m.Plan.Ops))
			return 0
		}
		// Plan mode is read-only, but a plan with blockers would not apply: say
		// so and exit non-zero, so `botfile plan && botfile sync` does not chain
		// into a sync that blocks.
		if len(m.Blockers) > 0 {
			fmt.Fprintf(w, "\nplan: %d operation(s), but %d issue(s) would block sync (resolve them first)\n", len(m.Plan.Ops), len(m.Blockers))
			return 1
		}
		fmt.Fprintf(w, "\nplan: %d operation(s) (run `botfile sync` to apply)\n", len(m.Plan.Ops))
		return 0
	default:
		fmt.Fprintf(w, "incomplete run (phase %d)\n", m.Phase)
		return 2
	}
}

// renderOps lists the planned filesystem operations.
func renderOps(w io.Writer, m runtime.Model) {
	for _, op := range m.Plan.Ops {
		if op.Kind == reconcile.OpRemove {
			fmt.Fprintf(w, "  remove   %s\n", op.Target)
		} else {
			fmt.Fprintf(w, "  %-8s %s -> %s\n", op.Kind, op.Target, op.Dest)
		}
	}
}

// renderInfo shows non-blocking outcomes: shared-namespace notices, precedence
// shadows, and components skipped because an agent does not support them
// (expected partial coverage, the one projection problem that does not block).
func renderInfo(w io.Writer, m runtime.Model) {
	for _, n := range m.Projection.Notices {
		fmt.Fprintf(w, "  note     skills for %v also reach %v via %s\n", n.Selected, n.AlsoReaches, n.Namespace)
	}
	for _, s := range m.Plan.Shadows {
		fmt.Fprintf(w, "  shadowed %s: %s overridden by %s\n", s.Target, s.SourceName, s.WonBy)
	}
	for _, p := range m.Projection.Problems {
		if p.Kind == project.ProblemUnsupported {
			fmt.Fprintf(w, "  skipped  %s on %s (%s)\n", p.Component, p.Agent, p.Detail)
		}
	}
}

// renderIssues lists every blocking issue with its kind and reference, the
// reasons a sync would not (or did not) apply.
func renderIssues(w io.Writer, m runtime.Model) {
	for _, b := range m.Blockers {
		fmt.Fprintf(w, "  ! %-18s %s: %s\n", b.Kind, b.Ref, b.Detail)
	}
}

// renderStatus shows the current state overview: what botfile manages and has in
// place, what is out of sync (a sync would change or is blocked), and what is
// unmanaged and adoptable. It is read-only and always exits 0.
func renderStatus(w io.Writer, m runtime.Model) int {
	changing := changingTargets(m.Plan)
	seen := make(map[string]bool)
	var managed []string
	for _, l := range m.Projection.Links {
		if changing[l.Target] || seen[l.Target] {
			continue
		}
		seen[l.Target] = true
		managed = append(managed, l.Target)
	}
	sort.Strings(managed)

	if len(managed) > 0 {
		fmt.Fprintf(w, "managed (%d)\n", len(managed))
		for _, t := range managed {
			fmt.Fprintf(w, "  %s\n", t)
		}
	}

	outOfSync := len(m.Plan.Ops) + len(m.Blockers)
	if outOfSync > 0 {
		fmt.Fprintf(w, "out of sync (%d)\n", outOfSync)
		renderOps(w, m)
		renderIssues(w, m)
	}

	// Non-blocking notes: shared-namespace notices, precedence shadows, and
	// components skipped because an agent does not support them. Skipped is
	// counted in the summary too, so a selection that cannot install is never
	// hidden behind "0 out of sync".
	skipped := 0
	for _, p := range m.Projection.Problems {
		if p.Kind == project.ProblemUnsupported {
			skipped++
		}
	}
	if notes := len(m.Projection.Notices) + len(m.Plan.Shadows) + skipped; notes > 0 {
		fmt.Fprintf(w, "notes (%d)\n", notes)
		renderInfo(w, m)
	}

	if len(m.Unmanaged) > 0 {
		fmt.Fprintf(w, "adoptable (%d)\n", len(m.Unmanaged))
		for _, u := range m.Unmanaged {
			fmt.Fprintf(w, "  %-22s %-16s %s\n", joinAgents(u.Agents), u.Ref(), u.Path)
		}
	}

	fmt.Fprintf(w, "\n%d managed, %d out of sync, %d skipped, %d adoptable\n", len(managed), outOfSync, skipped, len(m.Unmanaged))
	return 0
}

// joinAgents renders a list of agent IDs as a comma-separated string.
func joinAgents(ids []core.AgentID) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = string(id)
	}
	return strings.Join(parts, ",")
}

// renderAdopt reports the outcome of an adopt run: a typed problem (blocked, no
// effect ran), or the steps applied.
func renderAdopt(w io.Writer, m runtime.Model) int {
	if m.Phase == runtime.PhaseBlocked && m.AdoptProblem != nil {
		fmt.Fprintf(w, "cannot adopt: %s\n", m.AdoptProblem.Detail)
		return 1
	}
	if m.Phase == runtime.PhaseDone {
		p := m.AdoptPlan
		fmt.Fprintf(w, "  move   %s -> %s\n", p.From, p.To)
		fmt.Fprintf(w, "  link   %s -> %s\n", p.From, p.To)
		if p.AddSelection != nil {
			fmt.Fprintf(w, "  select %s for %s\n", p.AddSelection.ComponentID, joinAgents(p.AddSelection.Agents))
		}
		fmt.Fprintf(w, "\nadopted %s/%s into source %q (plugin %q)\n", p.Kind, p.Name, m.Adopt.SourceName, m.Adopt.PluginName)
		return 0
	}
	fmt.Fprintf(w, "incomplete adopt (phase %d)\n", m.Phase)
	return 2
}

// changingTargets is the set of target paths a sync would touch (an op) or that
// are blocked (a conflict or plan problem), so status can tell what is correctly
// in place from what is not.
func changingTargets(p reconcile.Plan) map[string]bool {
	s := make(map[string]bool)
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
