// Package apply executes a reconcile plan's operations against the filesystem
// port, turning the pure plan into real symlinks. It is the effect interpreter
// for the planner (reviews/patterns.md: side effects live in a thin interpreter,
// not the planner), and it is the only consumer of fsport besides the world
// reader.
//
// Apply is a saga: each operation records a compensating undo, and the first
// failure rolls the whole batch back, so a run is all-or-nothing. It honors
// manifesto 33 by refusing to remove or replace anything that is not a symlink,
// so it never clobbers a file botfile does not own, even if the world drifted
// since the plan was computed.
package apply

import (
	"errors"
	"fmt"
	"path/filepath"

	"codeberg.org/botfile/botfile/internal/fsport"
	"codeberg.org/botfile/botfile/internal/reconcile"
)

// Apply executes ops in order. On success every op is applied and Apply returns
// nil. On the first failure it rolls back the operations already applied (in
// reverse) and returns an error describing the failure and, if rollback itself
// failed, that too. Apply consumes only the plan's Ops; conflicts, shadows, and
// problems are the runtime's to gate on before calling Apply.
func Apply(fsys fsport.FS, ops []reconcile.Op) error {
	undos := make([]func() error, 0, len(ops))
	for _, op := range ops {
		undo, err := applyOp(fsys, op)
		if err != nil {
			if rbErr := rollback(undos); rbErr != nil {
				return fmt.Errorf("apply %s %s: %w; rollback failed: %v", op.Kind, op.Target, err, rbErr)
			}
			return fmt.Errorf("apply %s %s: %w (rolled back %d applied op(s))", op.Kind, op.Target, err, len(undos))
		}
		undos = append(undos, undo)
	}
	return nil
}

// rollback runs the undo stack in reverse, joining any errors.
func rollback(undos []func() error) error {
	var errs []error
	for i := len(undos) - 1; i >= 0; i-- {
		if err := undos[i](); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// applyOp performs one operation and returns a compensating undo that reverses
// exactly what it did.
func applyOp(fsys fsport.FS, op reconcile.Op) (func() error, error) {
	switch op.Kind {
	case reconcile.OpCreate:
		return applyCreate(fsys, op)
	case reconcile.OpReplace:
		return applyReplace(fsys, op)
	case reconcile.OpRemove:
		return applyRemove(fsys, op)
	default:
		return nil, fmt.Errorf("unknown op kind %d", op.Kind)
	}
}

func applyCreate(fsys fsport.FS, op reconcile.Op) (func() error, error) {
	if err := fsys.MkdirAll(filepath.Dir(op.Target)); err != nil {
		return nil, err
	}
	if err := fsys.Symlink(op.Dest, op.Target); err != nil {
		return nil, err
	}
	return func() error { return fsys.Remove(op.Target) }, nil
}

func applyReplace(fsys fsport.FS, op reconcile.Op) (func() error, error) {
	entry, err := fsys.Lstat(op.Target)
	if err != nil {
		return nil, err
	}
	if entry.Exists && !entry.IsSymlink {
		return nil, fmt.Errorf("refusing to replace non-symlink at %s (botfile owns only symlinks)", op.Target)
	}

	hadOld := entry.Exists
	oldDest := entry.Dest
	if hadOld {
		if err := fsys.Remove(op.Target); err != nil {
			return nil, err
		}
	} else if err := fsys.MkdirAll(filepath.Dir(op.Target)); err != nil {
		// The managed symlink vanished since planning; treat as a create.
		return nil, err
	}

	if err := fsys.Symlink(op.Dest, op.Target); err != nil {
		// Best-effort restore of the prior symlink before reporting.
		if hadOld {
			_ = fsys.Symlink(oldDest, op.Target)
		}
		return nil, err
	}

	return func() error {
		if err := fsys.Remove(op.Target); err != nil {
			return err
		}
		if hadOld {
			return fsys.Symlink(oldDest, op.Target)
		}
		return nil
	}, nil
}

func applyRemove(fsys fsport.FS, op reconcile.Op) (func() error, error) {
	entry, err := fsys.Lstat(op.Target)
	if err != nil {
		return nil, err
	}
	if !entry.Exists {
		// Already gone: the desired state holds, nothing to undo.
		return func() error { return nil }, nil
	}
	if !entry.IsSymlink {
		return nil, fmt.Errorf("refusing to remove non-symlink at %s (botfile owns only symlinks)", op.Target)
	}
	oldDest := entry.Dest
	if err := fsys.Remove(op.Target); err != nil {
		return nil, err
	}
	return func() error { return fsys.Symlink(oldDest, op.Target) }, nil
}
