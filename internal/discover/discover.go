// Package discover finds components installed in an agent's namespace that
// botfile does not manage: real (non-symlink) skills and instructions an agent or
// the user created in place. These are candidates for adoption (manifesto 36),
// the read side of the create-then-manage loop.
//
// A botfile-managed component is always a symlink into a source, so any real
// component-shaped entry in a managed namespace is by definition unmanaged.
// Discovery therefore classifies by shape (a directory with SKILL.md is a skill,
// a <name>.md file is an instruction) and ignores every symlink, botfile's own
// and the user's alike.
package discover

import (
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"codeberg.org/botfile/botfile/internal/core"
	"codeberg.org/botfile/botfile/internal/fsport"
	"codeberg.org/botfile/botfile/internal/source"
)

// Namespace is one surface to scan for unmanaged components, with every agent
// that reads it. A directory shared by several agents (for example
// ~/.agents/skills) is one Namespace with several Agents, so it is scanned once
// but its components are attributed to all of them.
//
// When File is empty the Dir is scanned, every entry a candidate. When File is
// set the surface is a single fixed file at Dir/File (a singleton like
// ~/.codex/AGENTS.md): only that one entry is considered, never the rest of Dir,
// which holds unrelated user files botfile must not read as adoptable (manifesto
// 33).
type Namespace struct {
	Agents []core.AgentID
	Kind   core.Kind
	Dir    string // absolute path, for example ~/.claude/skills or ~/.codex
	File   string // when set, the only entry to consider under Dir (for example AGENTS.md)
	Ext    string // the install leaf extension for a drop-in instruction dir (".md", ".instructions.md")
}

// Unmanaged is an adoptable component found in an agent namespace, attributed to
// every agent that reads the directory it was found in.
type Unmanaged struct {
	Agents []core.AgentID
	Kind   core.Kind
	Name   string
	Path   string // absolute path of the component in the agent namespace
}

// Ref renders the component as "<kind>/<name>".
func (u Unmanaged) Ref() string { return string(u.Kind) + "/" + u.Name }

// Find scans each namespace and returns the unmanaged components, sorted by
// path. A namespace whose directory or fixed file does not exist is skipped.
func Find(fsys fsport.FS, namespaces []Namespace) ([]Unmanaged, error) {
	var found []Unmanaged
	for _, ns := range namespaces {
		// A fixed-file surface considers exactly one entry, never the rest of its
		// directory (manifesto 33).
		names := []string{ns.File}
		if ns.File == "" {
			entries, err := fsys.ReadDir(ns.Dir)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return nil, err
			}
			names = entries
		}
		for _, name := range names {
			path := filepath.Join(ns.Dir, name)
			entry, err := fsys.Lstat(path)
			if err != nil {
				return nil, err
			}
			if !entry.Exists || entry.IsSymlink {
				continue // symlinks (botfile's or the user's) are never adoptable
			}
			if u, ok := classify(fsys, ns, name, path, entry); ok {
				found = append(found, u)
			}
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Path < found[j].Path })
	return found, nil
}

// classify decides whether a real entry is an adoptable component of the
// namespace's kind, applying the same shape rules as the source scanner.
func classify(fsys fsport.FS, ns Namespace, name, path string, entry fsport.Entry) (Unmanaged, bool) {
	switch ns.Kind {
	case core.KindSkill:
		if !entry.IsDir {
			return Unmanaged{}, false // a skill is a directory (manifesto 48)
		}
		manifest, err := fsys.Lstat(filepath.Join(path, source.ManifestFile))
		if err != nil || !manifest.IsRegular {
			return Unmanaged{}, false // needs a regular SKILL.md file
		}
		if core.ValidateName("skill name", name) != nil {
			return Unmanaged{}, false
		}
		return Unmanaged{Agents: ns.Agents, Kind: core.KindSkill, Name: name, Path: path}, true

	case core.KindInstruction:
		if !entry.IsRegular {
			return Unmanaged{}, false // an instruction is a regular file (manifesto 48)
		}
		// A singleton (fixed-file) instruction takes its identity from its agent,
		// not its filename: the fixed filename is an agent install-path detail, not
		// a portable identity, and several agents share the AGENTS.md basename
		// (copilot-cli's is copilot-instructions.md), so the filename does not
		// distinguish them and naming several "AGENTS" would collide when adopted
		// into one plugin. The agent is the distinguishing, stable name
		// (~/.codex/AGENTS.md -> instruction/codex-cli).
		if ns.File != "" {
			if len(ns.Agents) == 0 {
				return Unmanaged{}, false
			}
			return Unmanaged{Agents: ns.Agents, Kind: core.KindInstruction, Name: string(ns.Agents[0]), Path: path}, true
		}
		// A drop-in instruction's name is its leaf with the agent's install
		// extension removed (claude-code ".md", copilot-vscode ".instructions.md").
		// The extension comes from the namespace, not a hardcoded ".md", so a
		// compound leaf like foo.instructions.md adopts as instruction/foo.
		if ns.Ext == "" || !strings.HasSuffix(name, ns.Ext) {
			return Unmanaged{}, false
		}
		iname := strings.TrimSuffix(name, ns.Ext)
		if core.ValidateName("instruction name", iname) != nil {
			return Unmanaged{}, false
		}
		return Unmanaged{Agents: ns.Agents, Kind: core.KindInstruction, Name: iname, Path: path}, true

	default:
		return Unmanaged{}, false
	}
}
