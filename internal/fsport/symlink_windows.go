//go:build windows

package fsport

import (
	"errors"
	"fmt"
	"syscall"
)

// errPrivilegeNotHeld is Windows ERROR_PRIVILEGE_NOT_HELD (1314), returned by
// CreateSymbolicLink when the process may not create symlinks: Developer Mode is
// off and the process is not elevated.
const errPrivilegeNotHeld = syscall.Errno(1314)

// symlinkResult annotates the Windows symlink-privilege failure with actionable
// guidance (manifesto 41), and passes any other error through unchanged.
func symlinkResult(err error) error {
	if err != nil && errors.Is(err, errPrivilegeNotHeld) {
		return fmt.Errorf("%w: creating a symlink needs permission on Windows; enable Developer Mode (Settings > Privacy & security > For developers) or run elevated", err)
	}
	return err
}
