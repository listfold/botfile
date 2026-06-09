#!/bin/sh
# Shared project checks, run by BOTH .githooks/pre-commit and `just check` so the
# hook and the recipe cannot drift. These mirror what a CI job would run, kept
# local and immediate.
set -e

# Formatting: gofmt -l lists files that are not gofmt-clean. It exits 0 even when
# it lists files, so we must treat any output as failure (not the exit code).
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
	echo "check: these files are not gofmt-clean:" >&2
	echo "$unformatted" >&2
	echo "fix with: gofmt -w ." >&2
	exit 1
fi

go vet ./...
go build ./...
go test ./...

# Typecheck for Windows so its build-tagged files stay compilable from any host,
# even without a Windows machine. go vet typechecks _test.go files too (the
# windows-only symlink diagnostic test), which GOOS=windows go build would skip.
GOOS=windows GOARCH=amd64 go vet ./...

echo "check: gofmt, vet, build, test, windows typecheck all clean"
