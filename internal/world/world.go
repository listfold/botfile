// Package world reads observed filesystem state into a reconcile.World, the
// other half of the planner's input alongside the projected desired links. It is
// the read-side counterpart of the apply package: both go through the fsport
// boundary, and neither lives in the pure planner.
//
// It observes two things:
//
//   - Each desired target, so the planner can tell an absent target (create)
//     from an existing managed symlink (no-op or replace) from a foreign file
//     (conflict, manifesto 35).
//   - Every symlink already present in the managed namespace directories, so a
//     symlink botfile created but no longer desires is seen and removed as an
//     orphan (manifesto 33). Non-symlink entries in those directories are
//     user-owned and deliberately not recorded; the planner leaves them alone.
//   - Each managed fixed file (a singleton target like ~/.codex/AGENTS.md), one
//     exact path at a time. A singleton lives in a directory full of unrelated
//     user files, so botfile observes only that file, never scanning its parent
//     (manifesto 33). Recording it lets an orphaned, no-longer-desired singleton
//     symlink be removed without touching its siblings.
//
// The world reader records raw entries only; deciding which symlinks are
// botfile-managed (destination under a source root) is the planner's job, so the
// reader stays a dumb observer.
package world

import (
	"errors"
	"io/fs"
	"path/filepath"

	"codeberg.org/botfile/botfile/internal/fsport"
	"codeberg.org/botfile/botfile/internal/reconcile"
)

// Read observes the filesystem through fsys into a reconcile.World.
// desiredTargets are the absolute target paths the projection produced;
// managedDirs are the absolute namespace directories to scan for existing
// symlinks (orphan candidates); managedFiles are exact singleton paths to observe
// one at a time, never scanning their parent. A managedDir or managedFile that
// does not exist yet is skipped.
func Read(fsys fsport.FS, desiredTargets, managedDirs, managedFiles []string) (reconcile.World, error) {
	entries := make(map[string]reconcile.Entry)

	// Observe each desired target. A foreign file at a desired target must be
	// recorded so the planner can report a conflict; an absent one need not be,
	// but recording it keeps the World explicit.
	for _, t := range desiredTargets {
		clean := filepath.Clean(t)
		if _, ok := entries[clean]; ok {
			continue
		}
		e, err := fsys.Lstat(clean)
		if err != nil {
			return reconcile.World{}, err
		}
		entries[clean] = toEntry(e)
	}

	// Observe each managed fixed file directly. Only a symlink is an orphan
	// candidate; a user-owned file at the path is left for the planner to ignore
	// (or to report as a conflict when it is also a desired target).
	for _, f := range managedFiles {
		clean := filepath.Clean(f)
		if _, ok := entries[clean]; ok {
			continue
		}
		e, err := fsys.Lstat(clean)
		if err != nil {
			return reconcile.World{}, err
		}
		if e.IsSymlink {
			entries[clean] = reconcile.Entry{Kind: reconcile.Symlink, Dest: e.Dest}
		}
	}

	// Scan the managed namespace directories for existing symlinks not already
	// observed: these are the orphan candidates.
	for _, dir := range managedDirs {
		names, err := fsys.ReadDir(dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // namespace not materialized yet
			}
			return reconcile.World{}, err
		}
		for _, name := range names {
			path := filepath.Join(dir, name)
			if _, ok := entries[path]; ok {
				continue
			}
			e, err := fsys.Lstat(path)
			if err != nil {
				return reconcile.World{}, err
			}
			// Only symlinks are orphan candidates; a user-owned file or directory
			// in a managed namespace is left for the planner to ignore.
			if e.IsSymlink {
				entries[path] = reconcile.Entry{Kind: reconcile.Symlink, Dest: e.Dest}
			}
		}
	}

	return reconcile.World{Entries: entries}, nil
}

// toEntry maps an fsport observation to a reconcile entry.
func toEntry(e fsport.Entry) reconcile.Entry {
	switch {
	case !e.Exists:
		return reconcile.Entry{Kind: reconcile.Absent}
	case e.IsSymlink:
		return reconcile.Entry{Kind: reconcile.Symlink, Dest: e.Dest}
	default:
		return reconcile.Entry{Kind: reconcile.Foreign}
	}
}
