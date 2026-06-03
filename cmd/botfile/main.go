// Command botfile is the entrypoint: it resolves the environment (config path,
// home, agent matrix and roots), seeds the pure reducer, drives it through the
// effect interpreter, and renders the final run state. It is the cli layer; all
// orchestration lives in the pure reducer (internal/runtime) and all I/O in the
// interpreter's ports (internal/interp).
package main

import (
	"fmt"
	"os"

	"codeberg.org/botfile/botfile/internal/agent"
	"codeberg.org/botfile/botfile/internal/config"
	"codeberg.org/botfile/botfile/internal/interp"
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
	model = interp.OSDeps().Run(model, cmd)

	return render(os.Stdout, model)
}

func usage() {
	fmt.Fprint(os.Stderr, `botfile manages your agents' config and context, like dotfiles.

usage:
  botfile plan    show what a sync would change, without touching anything
  botfile sync    reconcile your agents to match your config
`)
}

// render writes a human-readable summary of the run and returns the exit code.
func render(w *os.File, m runtime.Model) int {
	switch m.Phase {
	case runtime.PhaseFailed:
		fmt.Fprintf(w, "failed during %s: %v\n", m.FailedStage, m.Err)
		return 2
	}

	renderPlan(w, m)

	switch m.Phase {
	case runtime.PhaseBlocked:
		fmt.Fprintf(w, "\nblocked: %d issue(s) must be resolved before sync\n", len(m.Blockers))
		for _, b := range m.Blockers {
			fmt.Fprintf(w, "  - %s\n", b.Detail)
		}
		return 1
	case runtime.PhaseDone:
		if m.Mode == runtime.ModeSync {
			fmt.Fprintf(w, "\nsynced: %d operation(s) applied\n", len(m.Plan.Ops))
		} else {
			fmt.Fprintf(w, "\nplan: %d operation(s) (run `botfile sync` to apply)\n", len(m.Plan.Ops))
		}
		return 0
	default:
		// A non-terminal phase here means the loop exited early; treat as failure.
		fmt.Fprintf(w, "incomplete run (phase %d)\n", m.Phase)
		return 2
	}
}

// renderPlan prints the plan's operations and the non-blocking outcomes.
func renderPlan(w *os.File, m runtime.Model) {
	for _, op := range m.Plan.Ops {
		if op.Kind == reconcile.OpRemove {
			fmt.Fprintf(w, "  remove  %s\n", op.Target)
		} else {
			fmt.Fprintf(w, "  %-7s %s -> %s\n", op.Kind, op.Target, op.Dest)
		}
	}
	for _, c := range m.Plan.Conflicts {
		fmt.Fprintf(w, "  conflict %s (%s)\n", c.Target, c.Reason)
	}
	for _, p := range m.Projection.Problems {
		fmt.Fprintf(w, "  skipped %s (%s)\n", p.Component, p.Detail)
	}
	for _, n := range m.Projection.Notices {
		fmt.Fprintf(w, "  note: skills for %s also reach %v via %s\n", n.Selected, n.AlsoReaches, n.Namespace)
	}
}
