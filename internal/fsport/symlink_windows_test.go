//go:build windows

package fsport

import (
	"errors"
	"strings"
	"testing"
)

func TestSymlinkResultAnnotatesPrivilegeError(t *testing.T) {
	t.Parallel()
	got := symlinkResult(errPrivilegeNotHeld)
	if got == nil || !strings.Contains(got.Error(), "Developer Mode") {
		t.Fatalf("privilege error not annotated: %v", got)
	}
	// The original error is still unwrappable for callers that check it.
	if !errors.Is(got, errPrivilegeNotHeld) {
		t.Error("annotated error should still wrap the privilege errno")
	}

	other := errors.New("disk full")
	if symlinkResult(other) != other {
		t.Error("a non-privilege error should pass through unchanged")
	}
}
