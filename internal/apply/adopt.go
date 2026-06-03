package apply

import (
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

	if err := fsys.MkdirAll(filepath.Dir(to)); err != nil {
		return fmt.Errorf("adopt: prepare destination: %w", err)
	}
	if err := fsys.Rename(from, to); err != nil {
		return fmt.Errorf("adopt: move %s: %w", from, err)
	}
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
