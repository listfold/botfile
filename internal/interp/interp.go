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

	"codeberg.org/botfile/botfile/internal/apply"
	"codeberg.org/botfile/botfile/internal/config"
	"codeberg.org/botfile/botfile/internal/core"
	"codeberg.org/botfile/botfile/internal/fsport"
	"codeberg.org/botfile/botfile/internal/project"
	"codeberg.org/botfile/botfile/internal/runtime"
	"codeberg.org/botfile/botfile/internal/source"
	"codeberg.org/botfile/botfile/internal/world"
)

// Deps are the ports the interpreter performs effects through. FS handles the
// world read and apply (symlink I/O); LoadConfig reads and validates the config;
// ScanSource scans one source location into its components.
type Deps struct {
	FS         fsport.FS
	LoadConfig func(path string) (core.Config, error)
	ScanSource func(location string) source.Result
}

// OSDeps returns the production interpreter, backed by the real filesystem.
func OSDeps() Deps {
	return Deps{
		FS:         fsport.OS{},
		LoadConfig: config.Load,
		ScanSource: scanLocal,
	}
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
		var problems []source.Problem
		for _, s := range c.Sources {
			res := d.ScanSource(s.Location)
			sources = append(sources, project.Source{
				Name:    s.Name,
				Root:    s.Location,
				Plugins: res.Plugins,
			})
			problems = append(problems, res.Problems...)
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

	default:
		return runtime.Failed{Stage: "interp", Err: fmt.Errorf("unknown command %T", cmd)}
	}
}
