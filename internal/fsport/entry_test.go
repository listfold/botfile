package fsport

import (
	"os"
	"path/filepath"
	"testing"
)

// Entry's IsRegular cannot be exercised through the FS interface (it has no file
// creation), so these tests create files out of band per backend.

func TestEntryKindsMem(t *testing.T) {
	t.Parallel()
	m := NewMem()
	m.AddFile("/a/regular")
	m.AddSpecial("/a/fifo")

	reg, _ := m.Lstat("/a/regular")
	if !reg.IsRegular || reg.IsDir || reg.IsSymlink {
		t.Errorf("regular file = %+v, want IsRegular only", reg)
	}
	sp, _ := m.Lstat("/a/fifo")
	if sp.IsRegular || sp.IsDir || sp.IsSymlink || !sp.Exists {
		t.Errorf("special file = %+v, want exists but neither regular/dir/symlink", sp)
	}
}

func TestEntryKindsOS(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := OS{}.Lstat(file)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if !e.IsRegular || e.IsDir || e.IsSymlink {
		t.Errorf("regular file = %+v, want IsRegular only", e)
	}
}
