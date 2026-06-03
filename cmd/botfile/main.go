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

	"codeberg.org/botfile/botfile/internal/agent"
	"codeberg.org/botfile/botfile/internal/config"
	"codeberg.org/botfile/botfile/internal/interp"
	"codeberg.org/botfile/botfile/internal/project"
	"codeberg.org/botfile/botfile/internal/reconcile"
	"codeberg.org/botfile/botfile/internal/runtime"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run parses the verb, executes it, and returns the process exit code:
//
//	0 success (plan produced, or sync applied / nothing to do)
//	1 blocked (a problem or conflict prevented apply)
//	2 failed  (an effect or usage error)
func run(args []string) int {
	if len(args) != 1 {
		usage()
		return 2
	}

	var mode runtime.Mode
	switch args[0] {
	case "plan":
		mode = runtime.ModePlan
	case "sync":
		mode = runtime.ModeSync
	case "-h", "--help", "help":
		usage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "botfile: unknown command %q\n\n", args[0])
		usage()
		return 2
	}

	configPath, err := config.DefaultPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "botfile: %v\n", err)
		return 2
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "botfile: resolve home: %v\n", err)
		return 2
	}

	agents := agent.Default()
	roots := agents.ResolveRoots(home, os.Getenv)

	model, cmd := runtime.Init(mode, configPath, home, agents, roots)
	model = interp.OSDeps(home).Run(model, cmd)

	return render(os.Stdout, model)
}

func usage() {
	fmt.Fprint(os.Stderr, `botfile manages your agents' config and context, like dotfiles.

usage:
  botfile plan    show what a sync would change, without touching anything
  botfile sync    reconcile your agents to match your config
`)
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
