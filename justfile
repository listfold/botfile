# justfile — build, test, and release recipes for botfile.
#
# Requires: just (https://just.systems) and go 1.23+. The release recipes also
# need curl, jq, and a Codeberg API token in $CODEBERG_TOKEN (a token with
# repository write scope).
#
# Releases are normally cut by pushing a v* tag, which the .githooks/pre-push
# hook turns into `just release <tag>`. Every recipe can also be run by hand.

owner := "botfile"
repo  := "botfile"
bin   := "botfile"
dist  := "dist"
api   := "https://codeberg.org/api/v1/repos/" + owner + "/" + repo

# GOOS/GOARCH targets cross-compiled for each release.
platforms := "linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"

# List available recipes.
default:
    @just --list

# Run the same checks as the pre-commit hook.
check:
    gofmt -l .
    go vet ./...
    go build ./...
    go test ./...
    GOOS=windows GOARCH=amd64 go vet ./...

# Build a local binary (./{{bin}}); pass a version string to stamp it.
build version="dev":
    go build -ldflags "-s -w -X main.version={{version}}" -o {{bin}} ./cmd/{{bin}}

# Install into the Go bin directory via `go install`.
install:
    go install ./cmd/{{bin}}

# Cross-compile every release asset for <tag> into ./{{dist}} (+ checksums.txt).
build-all tag:
    #!/usr/bin/env sh
    set -eu
    rm -rf "{{dist}}"
    mkdir -p "{{dist}}"
    for p in {{platforms}}; do
        os=${p%/*}
        arch=${p#*/}
        out="{{dist}}/{{bin}}-${os}-${arch}"
        [ "$os" = windows ] && out="${out}.exe"
        echo "building ${out}"
        GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 \
            go build -ldflags "-s -w -X main.version={{tag}}" -o "$out" ./cmd/{{bin}}
    done
    cd "{{dist}}"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum {{bin}}-* > checksums.txt
    else
        shasum -a 256 {{bin}}-* > checksums.txt
    fi
    echo "wrote {{dist}}/checksums.txt"

# Build assets for <tag> and publish them to the Codeberg release.
release tag: (build-all tag)
    #!/usr/bin/env sh
    set -eu
    : "${CODEBERG_TOKEN:?set CODEBERG_TOKEN to a Codeberg API token with repository write scope}"
    auth="Authorization: token ${CODEBERG_TOKEN}"
    sha=$(git rev-list -n1 "{{tag}}")
    # Reuse an existing release for this tag if present, otherwise create one.
    id=$(curl -fsS -H "$auth" "{{api}}/releases/tags/{{tag}}" 2>/dev/null | jq -r '.id // empty')
    if [ -z "$id" ]; then
        echo "creating release {{tag}} at ${sha}"
        id=$(curl -fsS -H "$auth" -H "Content-Type: application/json" -X POST "{{api}}/releases" \
            -d "$(printf '{"tag_name":"%s","target_commitish":"%s","name":"%s","draft":false,"prerelease":false}' "{{tag}}" "$sha" "{{tag}}")" \
            | jq -r '.id')
    else
        echo "reusing existing release {{tag}} (id ${id})"
    fi
    for f in {{dist}}/*; do
        name=$(basename "$f")
        echo "uploading ${name}"
        curl -fsS -H "$auth" -X POST "{{api}}/releases/${id}/assets?name=${name}" \
            -F "attachment=@${f};type=application/octet-stream" >/dev/null
    done
    echo "released {{tag}} -> https://codeberg.org/{{owner}}/{{repo}}/releases/tag/{{tag}}"

# Remove build artifacts.
clean:
    rm -rf {{dist}} {{bin}}
