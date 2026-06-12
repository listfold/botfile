package source

import (
	"io/fs"
	"path"
	"sort"

	"codeberg.org/botfile/botfile/internal/core"
)

// ProblemKind classifies a defect found while enforcing the source grammar
// (manifesto 46-48). Like the planner's Problem, it is an explicit outcome: a
// malformed entry is reported, not silently skipped, so the author can see why a
// component was not picked up.
type ProblemKind int

const (
	// ProblemUnreadable: a directory could not be read.
	ProblemUnreadable ProblemKind = iota
	// ProblemInvalidName: a plugin or component name breaks the shared name rule
	// (core.ValidateName): whitespace, a separator, or the wildcard token.
	ProblemInvalidName
	// ProblemUnknownKindDir: a directory under a plugin is not a recognized kind
	// directory (manifesto 47: skills/, instructions/, commands/).
	ProblemUnknownKindDir
	// ProblemStraySkillFile: a non-directory entry sits directly under skills/,
	// where a skill must be a directory (manifesto 48).
	ProblemStraySkillFile
	// ProblemSkillMissingManifest: a skill directory has no SKILL.md (manifesto
	// 17, 48).
	ProblemSkillMissingManifest
	// ProblemInstructionNotMarkdown: an entry under instructions/ is not a regular
	// .md file (manifesto 48).
	ProblemInstructionNotMarkdown
	// ProblemHiddenComponent: a hidden (dotfile) entry sits directly under a kind
	// directory, where entries are component candidates, so it is reported rather
	// than silently skipped.
	ProblemHiddenComponent
	// ProblemCommandNotMarkdown: an entry under commands/ is not a regular .md
	// file (manifesto 48).
	ProblemCommandNotMarkdown
)

// String renders a ProblemKind as a stable, human-readable token.
func (k ProblemKind) String() string {
	switch k {
	case ProblemUnreadable:
		return "unreadable"
	case ProblemInvalidName:
		return "invalid-name"
	case ProblemUnknownKindDir:
		return "unknown-kind-dir"
	case ProblemStraySkillFile:
		return "stray-skill-file"
	case ProblemSkillMissingManifest:
		return "skill-missing-manifest"
	case ProblemInstructionNotMarkdown:
		return "instruction-not-markdown"
	case ProblemHiddenComponent:
		return "hidden-component"
	case ProblemCommandNotMarkdown:
		return "command-not-markdown"
	default:
		return "unknown-problem"
	}
}

// Problem is a malformed entry found during a scan, located by its path relative
// to the source root.
type Problem struct {
	Kind   ProblemKind
	Path   string // slash-separated path within the source where the problem was found
	Detail string
}

// Result is a scanned source: its plugins in deterministic order, plus the
// problems found enforcing the grammar. A caller decides whether problems block
// use; the scanner itself stays non-judgmental.
type Result struct {
	Plugins  []core.Plugin
	Problems []Problem
}

// Scan reads a source tree through fsys (rooted at the source) and returns its
// plugins and any grammar problems. It never touches paths outside fsys and
// performs no mutation.
func Scan(fsys fs.FS) Result {
	var res Result

	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		res.Problems = append(res.Problems, Problem{Kind: ProblemUnreadable, Path: ".", Detail: err.Error()})
		return res
	}

	for _, e := range entries {
		if isHidden(e.Name()) {
			continue
		}
		// The first level is always a plugin (manifesto 46). Non-directory
		// entries at the source root are repo furniture (README, LICENSE) and are
		// ignored, since components may never sit at the root.
		if !e.IsDir() {
			continue
		}
		scanPlugin(fsys, e.Name(), &res)
	}

	sortResult(&res)
	return res
}

// scanPlugin scans one plugin directory, appending its plugin (with components)
// and any problems to res.
func scanPlugin(fsys fs.FS, name string, res *Result) {
	if err := core.ValidateName("plugin name", name); err != nil {
		res.Problems = append(res.Problems, Problem{Kind: ProblemInvalidName, Path: name, Detail: err.Error()})
		return
	}

	entries, err := fs.ReadDir(fsys, name)
	if err != nil {
		res.Problems = append(res.Problems, Problem{Kind: ProblemUnreadable, Path: name, Detail: err.Error()})
		return
	}

	plugin := core.Plugin{Name: name}
	for _, e := range entries {
		if isHidden(e.Name()) {
			continue
		}
		// The second level is always a kind directory (manifesto 46-47).
		// Non-directory entries under a plugin are ignored as furniture; an
		// unrecognized directory is flagged, since it looks like a kind grouping
		// the author intended.
		if !e.IsDir() {
			continue
		}
		kind, ok := kindForDir[e.Name()]
		if !ok {
			res.Problems = append(res.Problems, Problem{
				Kind: ProblemUnknownKindDir, Path: path.Join(name, e.Name()),
				Detail: "not a recognized kind directory (expected skills/, instructions/, or commands/)",
			})
			continue
		}
		scanKind(fsys, name, e.Name(), kind, &plugin, res)
	}

	res.Plugins = append(res.Plugins, plugin)
}

// scanKind scans one kind directory under a plugin, appending its components to
// plugin and any problems to res.
func scanKind(fsys fs.FS, pluginName, kindDir string, kind core.Kind, plugin *core.Plugin, res *Result) {
	base := path.Join(pluginName, kindDir)
	entries, err := fs.ReadDir(fsys, base)
	if err != nil {
		res.Problems = append(res.Problems, Problem{Kind: ProblemUnreadable, Path: base, Detail: err.Error()})
		return
	}

	for _, e := range entries {
		// Inside a kind directory every entry is a component candidate, so a
		// hidden entry is reported rather than silently skipped (the dotfile
		// furniture exception applies only at the source and plugin levels).
		if isHidden(e.Name()) {
			res.Problems = append(res.Problems, Problem{
				Kind: ProblemHiddenComponent, Path: path.Join(base, e.Name()),
				Detail: "a hidden entry under a kind directory is not a valid component",
			})
			continue
		}
		switch kind {
		case core.KindSkill:
			scanSkill(fsys, base, e, plugin, res)
		case core.KindInstruction, core.KindCommand:
			scanMarkdownComponent(base, e, kind, plugin, res)
		}
	}
}

// scanSkill validates one entry under skills/: it must be a directory containing
// SKILL.md (manifesto 48).
func scanSkill(fsys fs.FS, base string, e fs.DirEntry, plugin *core.Plugin, res *Result) {
	entryPath := path.Join(base, e.Name())
	if !e.IsDir() {
		res.Problems = append(res.Problems, Problem{
			Kind: ProblemStraySkillFile, Path: entryPath,
			Detail: "a skill must be a directory containing " + ManifestFile,
		})
		return
	}
	comp := core.Component{Kind: core.KindSkill, Name: e.Name()}
	if err := comp.Validate(); err != nil {
		res.Problems = append(res.Problems, Problem{Kind: ProblemInvalidName, Path: entryPath, Detail: err.Error()})
		return
	}
	// The manifest must be a regular file, not a directory named SKILL.md or any
	// other special entry (manifesto 48). fs.Stat resolves a symlink to its
	// target, so a manifest that is a symlink to a regular file is accepted.
	info, err := fs.Stat(fsys, path.Join(entryPath, ManifestFile))
	if err != nil || !info.Mode().IsRegular() {
		res.Problems = append(res.Problems, Problem{
			Kind: ProblemSkillMissingManifest, Path: entryPath,
			Detail: ManifestFile + " is missing or is not a regular file",
		})
		return
	}
	plugin.Components = append(plugin.Components, comp)
}

// scanMarkdownComponent validates one entry under instructions/ or commands/:
// either kind is a single <name>.md file (manifesto 48).
func scanMarkdownComponent(base string, e fs.DirEntry, kind core.Kind, plugin *core.Plugin, res *Result) {
	entryPath := path.Join(base, e.Name())
	problem, noun := ProblemInstructionNotMarkdown, "an instruction"
	if kind == core.KindCommand {
		problem, noun = ProblemCommandNotMarkdown, "a command"
	}
	// The component must be a regular .md file: not a directory, symlink, or
	// other special entry (manifesto 48). DirEntry.Type does not follow symlinks,
	// so a symlink entry is correctly rejected as non-regular.
	name, ok := InstructionName(e.Name())
	if !e.Type().IsRegular() || !ok {
		res.Problems = append(res.Problems, Problem{
			Kind: problem, Path: entryPath,
			Detail: noun + " must be a regular " + instructionExt + " file",
		})
		return
	}
	comp := core.Component{Kind: kind, Name: name}
	if err := comp.Validate(); err != nil {
		res.Problems = append(res.Problems, Problem{Kind: ProblemInvalidName, Path: entryPath, Detail: err.Error()})
		return
	}
	plugin.Components = append(plugin.Components, comp)
}

// isHidden reports whether a directory entry is a dotfile, which the scanner
// ignores at every level (.git, .gitignore, .DS_Store, and so on).
func isHidden(name string) bool {
	return len(name) > 0 && name[0] == '.'
}

// sortResult orders plugins, their components, and problems deterministically so
// an equal tree always yields an equal Result (reviews/patterns.md,
// determinism).
func sortResult(res *Result) {
	sort.Slice(res.Plugins, func(i, j int) bool {
		return res.Plugins[i].Name < res.Plugins[j].Name
	})
	for i := range res.Plugins {
		comps := res.Plugins[i].Components
		sort.Slice(comps, func(a, b int) bool {
			if comps[a].Kind != comps[b].Kind {
				return comps[a].Kind < comps[b].Kind
			}
			return comps[a].Name < comps[b].Name
		})
	}
	sort.Slice(res.Problems, func(i, j int) bool {
		if res.Problems[i].Path != res.Problems[j].Path {
			return res.Problems[i].Path < res.Problems[j].Path
		}
		return res.Problems[i].Kind < res.Problems[j].Kind
	})
}
