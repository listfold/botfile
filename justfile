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
build version="dev":
    go build -ldflags "-s -w -X main.version={{ version }}" -o {{ bin }} ./cmd/{{ bin }}

# Install into the Go bin directory via `go install`.
install:
    go install ./cmd/{{ bin }}

# Cross-compile a working-tree SNAPSHOT into ./{{dist}} (version is just a label).
build-all version="dev":
    rm -rf {{ dist }}
    scripts/build-matrix.sh {{ version }} {{ dist }}

# (internal) Cross-compile the TAGGED source into ./{{dist}}, stamped with <tag>.
# Builds from an isolated export of the tag so artifacts match the tag exactly,
# regardless of working-tree state or which commit is checked out.
_build-release tag:
    #!/usr/bin/env sh
    set -eu
    git rev-parse --verify "{{ tag }}^{commit}" >/dev/null
    work=$(mktemp -d)
    trap 'rm -rf "$work"' EXIT
    git archive --format=tar "{{ tag }}" | tar -x -C "$work"
    rm -rf "{{ dist }}"
    mkdir -p "{{ dist }}"
    dist_abs=$(cd "{{ dist }}" && pwd)
    scripts_abs=$(cd scripts && pwd)
    ( cd "$work" && "$scripts_abs/build-matrix.sh" "{{ tag }}" "$dist_abs" )

# Build the tagged source for <tag> and publish the assets to the Codeberg release.
release tag: (_build-release tag)
    #!/usr/bin/env sh
    set -eu
    : "${CODEBERG_TOKEN:?set CODEBERG_TOKEN to a Codeberg API token with repository write scope}"
    tag="{{ tag }}"
    api="{{ api }}"
    auth="Authorization: token ${CODEBERG_TOKEN}"
    commit=$(git rev-parse --verify "${tag}^{commit}")
    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' EXIT

    # Find an existing release for the tag; create one ONLY on a clean 404, and
    # fail loudly on any other status so a transient error is not read as "absent".
    code=$(curl -sS -o "$tmp/rel" -w '%{http_code}' -H "$auth" "${api}/releases/tags/${tag}")
    if [ "$code" = 200 ]; then
        id=$(jq -e -r '.id' <"$tmp/rel")
        echo "reusing release ${tag} (id ${id})"
    elif [ "$code" = 404 ]; then
        echo "creating release ${tag} at ${commit}"
        payload=$(jq -n --arg t "$tag" --arg c "$commit" \
            '{tag_name:$t, target_commitish:$c, name:$t, draft:false, prerelease:false}')
        code=$(curl -sS -o "$tmp/rel" -w '%{http_code}' -H "$auth" \
            -H 'Content-Type: application/json' -X POST "${api}/releases" -d "$payload")
        [ "$code" = 201 ] || { echo "create failed (HTTP ${code}):" >&2; cat "$tmp/rel" >&2; exit 1; }
        id=$(jq -e -r '.id' <"$tmp/rel")
    else
        echo "release lookup failed (HTTP ${code}):" >&2; cat "$tmp/rel" >&2; exit 1
    fi

    # Snapshot existing assets so re-runs converge: delete-and-replace by name,
    # leaving exactly one copy of each expected asset.
    code=$(curl -sS -o "$tmp/assets" -w '%{http_code}' -H "$auth" "${api}/releases/${id}/assets")
    [ "$code" = 200 ] || { echo "asset list failed (HTTP ${code}):" >&2; cat "$tmp/assets" >&2; exit 1; }

    for f in "{{ dist }}"/*; do
        name=$(basename "$f")
        old=$(jq -r --arg n "$name" 'map(select(.name == $n)) | .[0].id // empty' <"$tmp/assets")
        if [ -n "$old" ]; then
            echo "replacing ${name} (old id ${old})"
            code=$(curl -sS -o /dev/null -w '%{http_code}' -H "$auth" \
                -X DELETE "${api}/releases/${id}/assets/${old}")
            [ "$code" = 204 ] || { echo "delete of ${name} failed (HTTP ${code})" >&2; exit 1; }
        else
            echo "uploading ${name}"
        fi
        code=$(curl -sS -o "$tmp/up" -w '%{http_code}' -H "$auth" -X POST \
            "${api}/releases/${id}/assets?name=${name}" \
            -F "attachment=@${f};type=application/octet-stream")
        [ "$code" = 201 ] || { echo "upload of ${name} failed (HTTP ${code}):" >&2; cat "$tmp/up" >&2; exit 1; }
    done
    echo "released ${tag} -> https://codeberg.org/{{ owner }}/{{ repo }}/releases/tag/${tag}"

# Remove build artifacts.
clean:
    rm -rf {{ dist }} {{ bin }}
