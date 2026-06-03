//go:build !windows

package fsport

// symlinkResult is a no-op on platforms where creating a symlink needs no
// special privilege. The Windows build annotates the privilege failure instead.
func symlinkResult(err error) error { return err }
