package apply

import (
	"errors"
	"fmt"
	"path/filepath"

	"codeberg.org/botfile/botfile/internal/fsport"
)

// Adopt brings a component under management as a saga, the same shape as Apply:
// it moves the component from its place in the agent namespace (from) into the
// source (to), creates a symlink at from pointing to it, and, when addSelection
// is non-nil, runs it to record the selection. Any failure rolls back the steps
// already done, so the run is all-or-nothing.
//
// addSelection is injected (rather than apply touching config) so this stays a
// pure fsport saga plus one opaque, reversible step: it returns its own undo.
func Adopt(fsys fsport.FS, from, to string, addSelection func() (undo func() error, err error)) error {
	var undos []func() error

	// Record which destination directories MkdirAll will create, and push their
	// removal first so it runs last in rollback: by then the content has been
	// moved back out, so the created directories are empty and removable.
	created := missingAncestors(fsys, filepath.Dir(to))
	if err := fsys.MkdirAll(filepath.Dir(to)); err != nil {
		return fmt.Errorf("adopt: prepare destination: %w", err)
	}
	if len(created) > 0 {
		undos = append(undos, func() error { return removeEmptyDirs(fsys, created) })
	}

	// The planner already verified to is free (ProblemCollision). Rename adds a
	// best-effort (non-atomic) refusal of an observed existing destination, so a
	// race that slips past the preflight fails fast here instead of overwriting.
	if err := fsys.Rename(from, to); err != nil {
		return adoptFail(undos, fmt.Errorf("adopt: move %s: %w", from, err))
	}
	// Undo moves the content back; from has been vacated so this is collision-free.
	undos = append(undos, func() error { return fsys.Rename(to, from) })

	if err := fsys.Symlink(to, from); err != nil {
		return adoptFail(undos, fmt.Errorf("adopt: link %s: %w", from, err))
	}
	undos = append(undos, func() error { return fsys.Remove(from) })

	if addSelection != nil {
		undo, err := addSelection()
		if err != nil {
			return adoptFail(undos, fmt.Errorf("adopt: record selection: %w", err))
		}
		undos = append(undos, undo)
	}
	return nil
}

// adoptFail rolls back the steps done so far and reports the failure, noting a
// rollback failure if one occurs.
func adoptFail(undos []func() error, err error) error {
	if rb := rollback(undos); rb != nil {
		return fmt.Errorf("%w; rollback failed: %v", err, rb)
	}
	return err
}

// missingAncestors returns the directories at and above dir that do not yet
// exist, deepest first, the ones MkdirAll(dir) will create. It stops at the
// first existing ancestor, so pre-existing directories are never listed.
func missingAncestors(fsys fsport.FS, dir string) []string {
	var missing []string
	for d := dir; d != filepath.Dir(d); d = filepath.Dir(d) {
		e, err := fsys.Lstat(d)
		if err != nil || e.Exists {
			break
		}
		missing = append(missing, d)
	}
	return missing
}

// removeEmptyDirs removes the given directories in order (deepest first), each
// only if it is still an empty directory, so it never deletes a directory that
// has gained other content.
func removeEmptyDirs(fsys fsport.FS, dirs []string) error {
	var errs []error
	for _, d := range dirs {
		names, err := fsys.ReadDir(d)
		if err != nil || len(names) != 0 {
			continue // already gone, not a directory, or no longer empty
		}
		if err := fsys.Remove(d); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
