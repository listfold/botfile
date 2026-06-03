// Package fsport is botfile's filesystem boundary: the one place symlink I/O
// happens. It is a small interface (FS) with a real os-backed implementation and
// an in-memory implementation, both held to the same conformance suite (the
// port-laws pattern, reviews/patterns.md), so the applier can be tested against
// the in-memory backend and trusted against the real filesystem.
//
// The port deals only in what the applier and the (future) world reader need:
// inspecting an entry without following symlinks, creating and removing
// symlinks, and materializing a scan directory. Every method requires an
// absolute path; a relative path is ErrNotAbsolute. Lstat reports a missing path
// as a non-error absent entry, so callers branch on Entry.Exists, not on error
// shape.
package fsport

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrNotAbsolute is returned by every method given a non-absolute path.
var ErrNotAbsolute = errors.New("path must be absolute")

// Entry describes what exists at a path, observed without following symlinks
// (an lstat). A missing path is Entry{Exists: false} with a nil error. IsDir and
// IsRegular are mutually exclusive and only meaningful for a non-symlink that
// Exists; a special file (FIFO, socket, device) has both false.
type Entry struct {
	Exists    bool
	IsSymlink bool
	IsDir     bool   // a directory (and not a symlink)
	IsRegular bool   // a regular file (and not a symlink, directory, or special file)
	Dest      string // the symlink's destination when IsSymlink
}

// FS is the filesystem port. Implementations must satisfy the conformance suite
// in fsport_test.go.
type FS interface {
	// Lstat reports the entry at path without following a final symlink. A
	// missing path yields Entry{Exists: false} and a nil error.
	Lstat(path string) (Entry, error)
	// Symlink creates a symlink at link pointing to dest. Both link and dest must
	// be absolute. It errors if link already exists or its parent directory does
	// not.
	Symlink(dest, link string) error
	// Remove removes the symlink (or empty directory) at path. It errors if path
	// does not exist.
	Remove(path string) error
	// MkdirAll creates dir and any missing parents. It is a no-op if dir already
	// exists as a directory, and errors if a path component exists as a non-dir.
	MkdirAll(dir string) error
	// ReadDir returns the names of the entries directly in dir (not recursive),
	// sorted. It errors if dir does not exist or is not a directory; a missing
	// dir is fs.ErrNotExist so callers can skip an unmaterialized namespace.
	ReadDir(dir string) ([]string, error)
}

// OS is the real filesystem backend.
type OS struct{}

// Lstat implements FS over os.Lstat / os.Readlink.
func (OS) Lstat(path string) (Entry, error) {
	if !filepath.IsAbs(path) {
		return Entry{}, notAbs(path)
	}
	fi, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Entry{Exists: false}, nil
		}
		return Entry{}, err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		dest, err := os.Readlink(path)
		if err != nil {
			return Entry{}, err
		}
		return Entry{Exists: true, IsSymlink: true, Dest: dest}, nil
	}
	return Entry{Exists: true, IsDir: fi.IsDir(), IsRegular: fi.Mode().IsRegular()}, nil
}

// Symlink implements FS over os.Symlink. Both the link and its destination must
// be absolute: botfile normalizes every desired destination to an absolute path
// before planning, so a relative dest here is a malformed op, not a feature.
func (OS) Symlink(dest, link string) error {
	if !filepath.IsAbs(link) {
		return notAbs(link)
	}
	if !filepath.IsAbs(dest) {
		return notAbs(dest)
	}
	// symlinkResult annotates the Windows "Developer Mode off" failure with
	// guidance (manifesto 41) and is a no-op elsewhere.
	return symlinkResult(os.Symlink(dest, link))
}

// Remove implements FS over os.Remove.
func (OS) Remove(path string) error {
	if !filepath.IsAbs(path) {
		return notAbs(path)
	}
	return os.Remove(path)
}

// MkdirAll implements FS over os.MkdirAll.
func (OS) MkdirAll(dir string) error {
	if !filepath.IsAbs(dir) {
		return notAbs(dir)
	}
	return os.MkdirAll(dir, 0o755)
}

// ReadDir implements FS over os.ReadDir (which returns entries sorted by name).
func (OS) ReadDir(dir string) ([]string, error) {
	if !filepath.IsAbs(dir) {
		return nil, notAbs(dir)
	}
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(des))
	for _, de := range des {
		names = append(names, de.Name())
	}
	return names, nil
}

func notAbs(path string) error {
	return fmt.Errorf("%q: %w", path, ErrNotAbsolute)
}
