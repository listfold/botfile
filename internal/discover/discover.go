// Package discover finds components installed in an agent's namespace that
// botfile does not manage: real (non-symlink) skills and memories an agent or
// the user created in place. These are candidates for adoption (manifesto 36),
// the read side of the create-then-manage loop.
//
// A botfile-managed component is always a symlink into a source, so any real
// component-shaped entry in a managed namespace is by definition unmanaged.
// Discovery therefore classifies by shape (a directory with SKILL.md is a skill,
// a <name>.md file is a memory) and ignores every symlink, botfile's own and the
// user's alike.
package discover

import (
	"errors"
	"io/fs"
	"path/filepath"
	"sort"

	"codeberg.org/botfile/botfile/internal/core"
	"codeberg.org/botfile/botfile/internal/fsport"
	"codeberg.org/botfile/botfile/internal/source"
)

// Namespace is one (agent, kind) directory to scan for unmanaged components.
type Namespace struct {
	Agent core.AgentID
	Kind  core.Kind
	Dir   string // absolute path, for example ~/.claude/skills
}

// Unmanaged is an adoptable component found in an agent's namespace.
type Unmanaged struct {
	Agent core.AgentID
	Kind  core.Kind
	Name  string
	Path  string // absolute path of the component in the agent namespace
}

// Ref renders the component as "<kind>/<name>".
func (u Unmanaged) Ref() string { return string(u.Kind) + "/" + u.Name }

// Find scans each namespace and returns the unmanaged components, sorted by
// path. A namespace directory that does not exist is skipped.
func Find(fsys fsport.FS, namespaces []Namespace) ([]Unmanaged, error) {
	var found []Unmanaged
	for _, ns := range namespaces {
		names, err := fsys.ReadDir(ns.Dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
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
		if err != nil || !manifest.Exists || manifest.IsDir || manifest.IsSymlink {
			return Unmanaged{}, false // needs a real SKILL.md file
		}
		if core.ValidateName("skill name", name) != nil {
			return Unmanaged{}, false
		}
		return Unmanaged{Agent: ns.Agent, Kind: core.KindSkill, Name: name, Path: path}, true

	case core.KindMemory:
		if entry.IsDir {
			return Unmanaged{}, false // a memory is a file (manifesto 48)
		}
		mname, ok := source.MemoryName(name)
		if !ok || core.ValidateName("memory name", mname) != nil {
			return Unmanaged{}, false
		}
		return Unmanaged{Agent: ns.Agent, Kind: core.KindMemory, Name: mname, Path: path}, true

	default:
		return Unmanaged{}, false
	}
}
