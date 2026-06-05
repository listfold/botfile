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
	"strings"

	"codeberg.org/botfile/botfile/internal/adopt"
	"codeberg.org/botfile/botfile/internal/agent"
	"codeberg.org/botfile/botfile/internal/config"
	"codeberg.org/botfile/botfile/internal/interp"
	"codeberg.org/botfile/botfile/internal/output"
	"codeberg.org/botfile/botfile/internal/runtime"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// version is botfile's release version. It is overridden at release time via
// -ldflags "-X main.version=<tag>" (GoReleaser); the baked default tracks the
// current development version.
var version = "v0.1.0"

// versionLine is the one-line version string `botfile version` and `--version`
// print.
func versionLine() string { return "botfile " + version }

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
	switch args[0] {
	case "-h", "--help", "help":
		usage(os.Stdout)
		return 0
	case "--version", "version":
		fmt.Fprintln(os.Stdout, versionLine())
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
	return emit(os.Stdout, model, inv.Flags["format"])
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

// emit classifies the run into a presentation Report and writes it in the chosen
// format ("text" or "json"), returning the report's exit code. All outcome logic
// lives in internal/output, the single source shared by both renderers, so the
// exit code is identical regardless of format. Writes are best-effort: as in the
// text path, a write failure (a closed pipe, say) does not change the run's
// outcome code, so Report.ExitCode stays authoritative for both formats.
func emit(w io.Writer, m runtime.Model, format string) int {
	r := output.ReportFromModel(m)
	if format == "json" {
		_ = output.RenderJSON(w, r)
	} else {
		output.RenderText(w, r)
	}
	return r.ExitCode
}

// render writes the text form. It is the text entrypoint the render tests use.
func render(w io.Writer, m runtime.Model) int {
	return emit(w, m, "text")
}
