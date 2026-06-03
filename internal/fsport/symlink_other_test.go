//go:build !windows

package fsport

import (
	"errors"
	"testing"
)

func TestSymlinkResultPassesThrough(t *testing.T) {
	t.Parallel()
	if symlinkResult(nil) != nil {
		t.Error("nil should pass through")
	}
	boom := errors.New("boom")
	if symlinkResult(boom) != boom {
		t.Error("a non-nil error should pass through unchanged off Windows")
	}
}
