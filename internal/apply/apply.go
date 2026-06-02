// Package apply executes a reconcile plan's operations against the filesystem
// port, turning the pure plan into real symlinks. It is the effect interpreter
// for the planner (reviews/patterns.md: side effects live in a thin interpreter,
// not the planner), and it is the only consumer of fsport besides the world
// reader.
//
// Apply is a saga: each operation records a compensating undo, and the first
// failure rolls the whole batch back, so a run is all-or-nothing.
//
// It is a faithful interpreter, not a repairer: a destructive op (replace,
// remove) is applied only while its precondition still holds, namely that the
// target is a symlink whose destination still equals the plan's observed
// OldDest. If the world drifted (the target vanished, became a non-symlink, or
// now points elsewhere), Apply reports a drift error and leaves the path
// untouched, so the runtime re-plans rather than mutating stale state. This is
// how it honors manifesto 33: botfile only ever removes or replaces the exact
// symlink it created.
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
	// Precondition: the exact managed symlink the planner saw must still be here.
	switch {
	case !entry.Exists:
		return nil, fmt.Errorf("replace %s: drifted, expected a botfile symlink to %s but the path is absent; re-plan", op.Target, op.OldDest)
	case !entry.IsSymlink:
		return nil, fmt.Errorf("replace %s: refusing to replace a non-symlink (botfile owns only symlinks)", op.Target)
	case !sameDest(entry.Dest, op.OldDest):
		return nil, fmt.Errorf("replace %s: drifted, points to %s but the plan expected %s; re-plan", op.Target, entry.Dest, op.OldDest)
	}

	if err := fsys.Remove(op.Target); err != nil {
		return nil, err
	}
	if err := fsys.Symlink(op.Dest, op.Target); err != nil {
		_ = fsys.Symlink(op.OldDest, op.Target) // best-effort restore of the prior symlink
		return nil, err
	}
	return func() error {
		if err := fsys.Remove(op.Target); err != nil {
			return err
		}
		return fsys.Symlink(op.OldDest, op.Target)
	}, nil
}

func applyRemove(fsys fsport.FS, op reconcile.Op) (func() error, error) {
	entry, err := fsys.Lstat(op.Target)
	if err != nil {
		return nil, err
	}
	switch {
	case !entry.Exists:
		// Already gone: the desired end state holds, nothing to do or undo.
		return func() error { return nil }, nil
	case !entry.IsSymlink:
		return nil, fmt.Errorf("remove %s: refusing to remove a non-symlink (botfile owns only symlinks)", op.Target)
	case !sameDest(entry.Dest, op.OldDest):
		return nil, fmt.Errorf("remove %s: drifted, points to %s but the plan expected %s; re-plan", op.Target, entry.Dest, op.OldDest)
	}
	if err := fsys.Remove(op.Target); err != nil {
		return nil, err
	}
	return func() error { return fsys.Symlink(op.OldDest, op.Target) }, nil
}

// sameDest compares two symlink destinations with the same cleaned-path
// semantics reconcile uses, so an equivalent spelling is not mistaken for drift.
func sameDest(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}
