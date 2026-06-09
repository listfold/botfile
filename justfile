# justfile — build, test, and release recipes for botfile.
#
# Requires: just (https://just.systems) and go 1.23+. The release recipes also
# need curl, jq, and a Codeberg API token in $CODEBERG_TOKEN (a token with
# repository write scope).
#
# Cut a release by running `just release <tag>` against a v* tag (the tag must
# already exist locally). A .githooks/pre-push hook to run this automatically on
# a v* tag push is added in a later step; until then, release is a manual step.

owner := "botfile"
repo := "botfile"
bin := "botfile"
dist := "dist"
api := "https://codeberg.org/api/v1/repos/" + owner + "/" + repo

# List available recipes.
default:
    @just --list

# Run the same checks as the pre-commit hook (shared scripts/check.sh).
check:
    scripts/check.sh

# Build a local binary (./{{bin}}); pass a version string to stamp it.
# quote() wraps the whole ldflags value so an arbitrary version cannot inject shell.
build version="dev":
    go build -ldflags {{ quote("-s -w -X main.version=" + version) }} -o {{ bin }} ./cmd/{{ bin }}

# Install into the Go bin directory via `go install`.
install:
    go install ./cmd/{{ bin }}

# Cross-compile a working-tree SNAPSHOT into ./{{dist}} (version is just a label).
# The label is passed as a quoted positional arg; build-matrix.sh reads it as "$1".
build-all version="dev":
    rm -rf {{ dist }}
    scripts/build-matrix.sh {{ quote(version) }} {{ dist }}

# Build a pushed v* tag's source and publish its assets to the Codeberg release.
# All validation and shell handling live in scripts/release.sh, which receives
# the tag as a single quoted positional argument (no raw interpolation here).
release tag:
    scripts/release.sh {{ quote(tag) }}

# Remove build artifacts.
clean:
    rm -rf {{ dist }} {{ bin }}
