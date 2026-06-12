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
	"codeberg.org/botfile/botfile/internal/guide"
	"codeberg.org/botfile/botfile/internal/interp"
	"codeberg.org/botfile/botfile/internal/output"
	"codeberg.org/botfile/botfile/internal/runtime"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// version is the release tag stamped at build time via
// -ldflags "-X main.version=<tag>" (scripts/build-matrix.sh). The baked
// default is deliberately not a release tag: a `go install` source build must
// identify as dev, so `botfile upgrade` refuses to replace it (it could be
// ahead of the latest release).
var version = "dev"

// versionLine is the one-line version string `botfile version` and `--version`
// print.
func versionLine() string { return "botfile " + version }

// help handles `botfile help [agent]`: the operator guide rendered for a human,
// in text. `help agent` is an explicit alias for the same agent-facing guide.
// Both accept --format text|markdown|json and load no config, so help works on a
// machine with no botfile config at all.
func help(w io.Writer, rest []string) int { return emitGuide(w, rest, "text") }

// guideCmd handles `botfile guide`: the agent-oriented operator guide. It
// defaults to markdown (the richest agent-legible form) and accepts
// --format text|markdown|json. Config-free, like help.
func guideCmd(w io.Writer, rest []string) int { return emitGuide(w, rest, "markdown") }

// emitGuide parses an optional topic and --format, builds the guide from the
// canonical command table and agent matrix, and renders it in the chosen format.
// It never loads config; the config path is shown for reference only, with a
// conventional fallback when the environment cannot be resolved.
func emitGuide(w io.Writer, rest []string, defaultFormat string) int {
	format, ok := guideFormat(rest, defaultFormat)
	if !ok {
		return 2
	}
	// Resolve install locations exactly as a real run does, honoring agent root
	// overrides (CLAUDE_CONFIG_DIR, CODEX_HOME, COPILOT_HOME) via os.Getenv. An
	// unresolvable home falls back to "~" so the guide still renders, with
	// documentation-style default paths.
	home, err := os.UserHomeDir()
	if err != nil {
		home = "~"
	}
	g := guide.Build(displayConfigPath(), home, os.Getenv, commandDocs())
	switch format {
	case "json":
		_ = guide.RenderJSON(w, g)
	case "markdown":
		guide.RenderMarkdown(w, g)
	default:
		guide.RenderText(w, g)
	}
	return 0
}

// guideFormat scans rest for an optional "agent" topic and a --format value,
// returning the chosen format (defaulting to def) and whether parsing succeeded.
// An unknown token or a format outside text|markdown|json prints a usage-style
// error to stderr and returns false.
func guideFormat(rest []string, def string) (string, bool) {
	format := def
	for i := 0; i < len(rest); i++ {
		tok := rest[i]
		switch {
		case tok == "agent":
			// an accepted topic alias for the agent guide; nothing to consume
		case strings.HasPrefix(tok, "--format="):
			format = strings.TrimPrefix(tok, "--format=")
		case tok == "--format":
			i++
			if i >= len(rest) {
				fmt.Fprintln(os.Stderr, "botfile: flag \"--format\" needs a value")
				return "", false
			}
			format = rest[i]
		default:
			fmt.Fprintf(os.Stderr, "botfile: unexpected argument %q\n", tok)
			return "", false
		}
	}
	switch format {
	case "text", "markdown", "json":
		return format, true
	default:
		fmt.Fprintf(os.Stderr, "botfile: --format must be one of text|markdown|json, got %q\n", format)
		return "", false
	}
}

// displayConfigPath returns the config location to show in the guide. The guide
// must render even when the environment is unresolvable, so a DefaultPath error
// falls back to the conventional path rather than failing.
func displayConfigPath() string {
	if p, err := config.DefaultPath(); err == nil {
		return p
	}
	return "~/.config/botfile/config.toml"
}

// commandDocs adapts the canonical command table into guide command docs, so the
// guide's command list cannot drift from what the parser actually accepts. The
// meta verbs dispatched before the contract (guide, version) are appended here,
// the one place they are documented, so the guide describes its own entry point.
func commandDocs() []guide.CommandDoc {
	docs := make([]guide.CommandDoc, 0, len(commands)+2)
	for _, c := range commands {
		docs = append(docs, guide.CommandDoc{Name: c.Name, Summary: c.Summary, Usage: invocationLine(c)})
	}
	docs = append(docs,
		guide.CommandDoc{Name: "guide", Summary: "print this guide (text, markdown, or json)", Usage: "botfile guide"},
		guide.CommandDoc{Name: "version", Summary: "print the version", Usage: "botfile version"},
		guide.CommandDoc{Name: "upgrade", Summary: "replace this binary with the latest release, checksum-verified (--check: report only)", Usage: "botfile upgrade [--check]"},
	)
	return docs
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
	switch args[0] {
	case "-h", "--help", "help":
		return help(os.Stdout, args[1:])
	case "guide":
		return guideCmd(os.Stdout, args[1:])
	case "--version", "version":
		fmt.Fprintln(os.Stdout, versionLine())
		return 0
	case "upgrade":
		return upgradeCmd(os.Stdout, args[1:], osUpgradeDeps())
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
