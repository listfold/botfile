package fsport

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
)

// Mem is an in-memory FS for tests: a flat map of absolute paths to nodes. It
// models symlinks, directories, and opaque non-symlink entries, which is enough
// to exercise the applier and the conformance suite without touching disk.
type Mem struct {
	nodes map[string]memNode
}

type memKind int

const (
	memSymlink memKind = iota
	memDir
	memFile
	memSpecial // a non-regular, non-dir, non-symlink entry (FIFO, socket, device)
)

type memNode struct {
	kind memKind
	dest string // for memSymlink
}

// NewMem returns an empty in-memory filesystem.
func NewMem() *Mem {
	return &Mem{nodes: make(map[string]memNode)}
}

// AddFile seeds a regular non-symlink file (one botfile does not own), for tests
// that exercise the "never clobber a non-symlink" guard.
func (m *Mem) AddFile(path string) {
	m.nodes[filepath.Clean(path)] = memNode{kind: memFile}
}

// AddSpecial seeds a non-regular special file (FIFO, socket, device), for tests
// that exercise the "must be a regular file" rule.
func (m *Mem) AddSpecial(path string) {
	m.nodes[filepath.Clean(path)] = memNode{kind: memSpecial}
}

// Lstat implements FS.
func (m *Mem) Lstat(path string) (Entry, error) {
	if !filepath.IsAbs(path) {
		return Entry{}, notAbs(path)
	}
	n, ok := m.nodes[filepath.Clean(path)]
	if !ok {
		return Entry{Exists: false}, nil
	}
	if n.kind == memSymlink {
		return Entry{Exists: true, IsSymlink: true, Dest: n.dest}, nil
	}
	return Entry{Exists: true, IsDir: n.kind == memDir, IsRegular: n.kind == memFile}, nil
}

// Symlink implements FS.
func (m *Mem) Symlink(dest, link string) error {
	if !filepath.IsAbs(link) {
		return notAbs(link)
	}
	if !filepath.IsAbs(dest) {
		return notAbs(dest)
	}
	p := filepath.Clean(link)
	if _, ok := m.nodes[p]; ok {
		return &fs.PathError{Op: "symlink", Path: p, Err: fs.ErrExist}
	}
	if !m.isDir(filepath.Dir(p)) {
		return &fs.PathError{Op: "symlink", Path: p, Err: fs.ErrNotExist}
	}
	m.nodes[p] = memNode{kind: memSymlink, dest: dest}
	return nil
}

// Remove implements FS.
func (m *Mem) Remove(path string) error {
	if !filepath.IsAbs(path) {
		return notAbs(path)
	}
	p := filepath.Clean(path)
	n, ok := m.nodes[p]
	if !ok {
		return &fs.PathError{Op: "remove", Path: p, Err: fs.ErrNotExist}
	}
	if n.kind == memDir && m.hasChildren(p) {
		return &fs.PathError{Op: "remove", Path: p, Err: errors.New("directory not empty")}
	}
	delete(m.nodes, p)
	return nil
}

// MkdirAll implements FS.
func (m *Mem) MkdirAll(dir string) error {
	if !filepath.IsAbs(dir) {
		return notAbs(dir)
	}
	p := filepath.Clean(dir)
	// Collect components from p up to (not including) the filesystem root.
	var parts []string
	for cur := p; cur != filepath.Dir(cur); cur = filepath.Dir(cur) {
		parts = append(parts, cur)
	}
	// Create from the top down.
	for i := len(parts) - 1; i >= 0; i-- {
		cur := parts[i]
		if n, ok := m.nodes[cur]; ok {
			if n.kind != memDir {
				return &fs.PathError{Op: "mkdir", Path: cur, Err: fmt.Errorf("not a directory")}
			}
			continue
		}
		m.nodes[cur] = memNode{kind: memDir}
	}
	return nil
}

// ReadDir implements FS.
func (m *Mem) ReadDir(dir string) ([]string, error) {
	if !filepath.IsAbs(dir) {
		return nil, notAbs(dir)
	}
	p := filepath.Clean(dir)
	if !m.isDir(p) {
		return nil, &fs.PathError{Op: "readdir", Path: p, Err: fs.ErrNotExist}
	}
	var names []string
	for k := range m.nodes {
		if k != p && filepath.Dir(k) == p {
			names = append(names, filepath.Base(k))
		}
	}
	sort.Strings(names)
	return names, nil
}

// isDir reports whether p is a directory: the filesystem root (where Dir(p)==p)
// is always a directory; otherwise a node must exist and be a memDir.
func (m *Mem) isDir(p string) bool {
	if p == filepath.Dir(p) {
		return true
	}
	n, ok := m.nodes[p]
	return ok && n.kind == memDir
}

func (m *Mem) hasChildren(p string) bool {
	for k := range m.nodes {
		if k != p && filepath.Dir(k) == p {
			return true
		}
	}
	return false
}
