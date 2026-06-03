// Package interp is botfile's effect interpreter: the impure half of the Elm
// loop. It runs each Cmd the pure reducer emits against the real ports
// (config.Load, source.Scan, world.Read, apply.Apply) and feeds the resulting
// Msg back into runtime.Update, until the Model reaches a terminal phase. It
// holds no orchestration logic of its own; the state machine lives entirely in
// the pure reducer, and the interpreter is a faithful executor of already
// decided commands.
//
// Ports are injected so the loop is testable: a test can substitute an
// in-memory filesystem or a canned scanner, and the production path uses the
// os-backed defaults from OSDeps.
package interp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codeberg.org/botfile/botfile/internal/apply"
	"codeberg.org/botfile/botfile/internal/config"
	"codeberg.org/botfile/botfile/internal/core"
	"codeberg.org/botfile/botfile/internal/discover"
	"codeberg.org/botfile/botfile/internal/fsport"
	"codeberg.org/botfile/botfile/internal/project"
	"codeberg.org/botfile/botfile/internal/runtime"
	"codeberg.org/botfile/botfile/internal/source"
	"codeberg.org/botfile/botfile/internal/world"
)

// Deps are the ports the interpreter performs effects through. FS handles the
// world read and apply (symlink I/O); LoadConfig reads and validates the config;
// ScanSource scans one already-resolved absolute source path into its
// components; Home is used to expand a leading ~ in a source location.
type Deps struct {
	FS         fsport.FS
	LoadConfig func(path string) (core.Config, error)
	ScanSource func(absPath string) source.Result
	Home       string
}

// OSDeps returns the production interpreter, backed by the real filesystem, with
// home used for ~ expansion of source locations.
func OSDeps(home string) Deps {
	return Deps{
		FS:         fsport.OS{},
		LoadConfig: config.Load,
		ScanSource: scanLocal,
		Home:       home,
	}
}

// resolveLocation turns a configured source location into an absolute local path
// for scanning and as the planner root, or a typed problem when it cannot. A
// remote (git URL) location is rejected: botfile does not clone, by design (that
// is git's job, manifesto 29-30); a leading ~ is expanded against Home; a
// relative path is resolved against base
// (the config file's directory, not the process working directory), so a config
// like location = "./team" resolves the same regardless of where botfile is
// launched and never yields a relative destination the planner rejects as
// invalid-path.
func (d Deps) resolveLocation(base, location string) (string, *source.Problem) {
	if isRemote(location) {
		return location, &source.Problem{
			Kind: source.ProblemUnreadable, Path: location,
			Detail: "botfile does not clone repositories (that is git's job); clone it and set the source location to the local path",
		}
	}
	path := location
	switch {
	case path == "~":
		path = d.Home
	case strings.HasPrefix(path, "~/"):
		path = filepath.Join(d.Home, path[2:])
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return location, &source.Problem{
			Kind: source.ProblemUnreadable, Path: location,
			Detail: "cannot resolve source location: " + err.Error(),
		}
	}
	return abs, nil
}

// isRemote reports whether a location is a remote URL rather than a local path:
// a scheme like https:// or ssh://, or scp-like syntax (user@host:path).
func isRemote(location string) bool {
	if strings.Contains(location, "://") {
		return true
	}
	if i := strings.Index(location, "@"); i > 0 {
		rest := location[i+1:]
		if strings.Contains(rest, ":") &&
			!strings.HasPrefix(location, "/") &&
			!strings.HasPrefix(location, ".") &&
			!strings.HasPrefix(location, "~") {
			return true
		}
	}
	return false
}

// scanLocal scans a source at a local directory path. A missing or unreadable
// location surfaces as a source problem (ProblemUnreadable) inside the Result,
// not a hard error, so a broken source is reported, not fatal.
func scanLocal(location string) source.Result {
	return source.Scan(os.DirFS(location))
}

// Run drives the reducer to a terminal phase: it performs the current Cmd,
// feeds the resulting Msg into runtime.Update, and repeats until a CmdNone
// stops it. It returns the final Model for the caller to render.
func (d Deps) Run(model runtime.Model, cmd runtime.Cmd) runtime.Model {
	for {
		if _, done := cmd.(runtime.CmdNone); done {
			return model
		}
		msg := d.perform(cmd)
		model, cmd = runtime.Update(model, msg)
	}
}

// perform executes one command and returns the Msg describing its outcome. It
// never advances the Model; that is Update's job.
func (d Deps) perform(cmd runtime.Cmd) runtime.Msg {
	switch c := cmd.(type) {
	case runtime.CmdLoadConfig:
		cfg, err := d.LoadConfig(c.Path)
		if err != nil {
			return runtime.Failed{Stage: "load-config", Err: err}
		}
		return runtime.ConfigLoaded{Config: cfg}

	case runtime.CmdScanSources:
		sources := make([]project.Source, 0, len(c.Sources))
		var problems []runtime.ScanProblem
		for _, s := range c.Sources {
			root, prob := d.resolveLocation(c.BaseDir, s.Location)
			if prob != nil {
				problems = append(problems, runtime.ScanProblem{Source: s.Name, Problem: *prob})
				// Record the source with no plugins; an unresolved location
				// contributes nothing and blocks via the scan problem.
				sources = append(sources, project.Source{Name: s.Name, Root: root})
				continue
			}
			res := d.ScanSource(root)
			sources = append(sources, project.Source{Name: s.Name, Root: root, Plugins: res.Plugins})
			// Tag every problem with the source it came from, so adopt can block on
			// its own target source without an unrelated broken source stopping it.
			for _, p := range res.Problems {
				problems = append(problems, runtime.ScanProblem{Source: s.Name, Problem: p})
			}
		}
		return runtime.SourcesScanned{Sources: sources, Problems: problems}

	case runtime.CmdReadWorld:
		w, err := world.Read(d.FS, c.Targets, c.ManagedDirs)
		if err != nil {
			return runtime.Failed{Stage: "read-world", Err: err}
		}
		return runtime.WorldRead{World: w}

	case runtime.CmdApply:
		if err := apply.Apply(d.FS, c.Ops); err != nil {
			return runtime.Failed{Stage: "apply", Err: err}
		}
		return runtime.Applied{}

	case runtime.CmdDiscover:
		found, err := discover.Find(d.FS, c.Namespaces)
		if err != nil {
			return runtime.Failed{Stage: "discover", Err: err}
		}
		return runtime.Discovered{Unmanaged: found}

	case runtime.CmdApplyAdopt:
		var addSelection func() (func() error, error)
		if c.Plan.AddSelection != nil {
			sel := *c.Plan.AddSelection
			path := c.ConfigPath
			addSelection = func() (func() error, error) { return config.AddSelection(path, sel) }
		}
		if err := apply.Adopt(d.FS, c.Plan.From, c.Plan.To, addSelection); err != nil {
			return runtime.Failed{Stage: "adopt", Err: err}
		}
		return runtime.Applied{}

	default:
		return runtime.Failed{Stage: "interp", Err: fmt.Errorf("unknown command %T", cmd)}
	}
}
