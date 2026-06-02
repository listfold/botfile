package fsport

import (
	"errors"
	"path/filepath"
	"testing"
)

// newBackend builds a fresh FS plus an absolute base directory that already
// exists, so each backend is exercised through the same conformance suite.
type newBackend func(t *testing.T) (FS, string)

func TestConformance_OS(t *testing.T) {
	runConformance(t, func(t *testing.T) (FS, string) {
		return OS{}, t.TempDir()
	})
}

func TestConformance_Mem(t *testing.T) {
	runConformance(t, func(t *testing.T) (FS, string) {
		m := NewMem()
		base := "/base"
		if err := m.MkdirAll(base); err != nil {
			t.Fatalf("seed base: %v", err)
		}
		return m, base
	})
}

func runConformance(t *testing.T, nb newBackend) {
	t.Helper()

	t.Run("lstat absent is a non-error absent entry", func(t *testing.T) {
		fsys, base := nb(t)
		e, err := fsys.Lstat(filepath.Join(base, "nope"))
		if err != nil {
			t.Fatalf("Lstat absent: unexpected error %v", err)
		}
		if e.Exists {
			t.Fatalf("absent entry reported as existing: %+v", e)
		}
	})

	t.Run("mkdirall then lstat is a non-symlink dir", func(t *testing.T) {
		fsys, base := nb(t)
		dir := filepath.Join(base, "a", "b")
		if err := fsys.MkdirAll(dir); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		e, err := fsys.Lstat(dir)
		if err != nil || !e.Exists || e.IsSymlink {
			t.Fatalf("dir lstat = %+v, err %v; want existing non-symlink", e, err)
		}
	})

	t.Run("symlink round-trips through lstat", func(t *testing.T) {
		fsys, base := nb(t)
		if err := fsys.MkdirAll(filepath.Join(base, "d")); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		link := filepath.Join(base, "d", "link")
		dest := filepath.Join(base, "target")
		if err := fsys.Symlink(dest, link); err != nil {
			t.Fatalf("Symlink: %v", err)
		}
		e, err := fsys.Lstat(link)
		if err != nil || !e.IsSymlink || e.Dest != dest {
			t.Fatalf("symlink lstat = %+v, err %v; want symlink to %s", e, err, dest)
		}
	})

	t.Run("symlink over an existing entry errors", func(t *testing.T) {
		fsys, base := nb(t)
		link := filepath.Join(base, "link")
		if err := fsys.Symlink(filepath.Join(base, "x"), link); err != nil {
			t.Fatalf("first Symlink: %v", err)
		}
		if err := fsys.Symlink(filepath.Join(base, "y"), link); err == nil {
			t.Fatal("Symlink over existing entry: want error, got nil")
		}
	})

	t.Run("symlink into a missing dir errors until created", func(t *testing.T) {
		fsys, base := nb(t)
		link := filepath.Join(base, "missing", "link")
		if err := fsys.Symlink(filepath.Join(base, "x"), link); err == nil {
			t.Fatal("Symlink into missing dir: want error, got nil")
		}
		if err := fsys.MkdirAll(filepath.Join(base, "missing")); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := fsys.Symlink(filepath.Join(base, "x"), link); err != nil {
			t.Fatalf("Symlink after MkdirAll: %v", err)
		}
	})

	t.Run("remove deletes a symlink", func(t *testing.T) {
		fsys, base := nb(t)
		link := filepath.Join(base, "link")
		if err := fsys.Symlink(filepath.Join(base, "x"), link); err != nil {
			t.Fatalf("Symlink: %v", err)
		}
		if err := fsys.Remove(link); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		e, _ := fsys.Lstat(link)
		if e.Exists {
			t.Fatal("removed symlink still exists")
		}
	})

	t.Run("remove absent errors", func(t *testing.T) {
		fsys, base := nb(t)
		if err := fsys.Remove(filepath.Join(base, "nope")); err == nil {
			t.Fatal("Remove absent: want error, got nil")
		}
	})

	t.Run("relative paths are rejected", func(t *testing.T) {
		fsys, _ := nb(t)
		rel := "relative/path"
		if _, err := fsys.Lstat(rel); !errors.Is(err, ErrNotAbsolute) {
			t.Errorf("Lstat rel err = %v, want ErrNotAbsolute", err)
		}
		if err := fsys.Symlink("x", rel); !errors.Is(err, ErrNotAbsolute) {
			t.Errorf("Symlink rel err = %v, want ErrNotAbsolute", err)
		}
		if err := fsys.Remove(rel); !errors.Is(err, ErrNotAbsolute) {
			t.Errorf("Remove rel err = %v, want ErrNotAbsolute", err)
		}
		if err := fsys.MkdirAll(rel); !errors.Is(err, ErrNotAbsolute) {
			t.Errorf("MkdirAll rel err = %v, want ErrNotAbsolute", err)
		}
	})
}
