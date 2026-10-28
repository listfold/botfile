#!/usr/bin/env bash
# scripts/build.sh - Local build and test script for botfile
#
# Usage:
#   scripts/build.sh [test|release|homebrew]
#
# Commands:
#   test      - Run all tests (default)
#   release   - Build release binaries for all platforms
#   homebrew  - Test the Homebrew formula locally
#   tidy      - Clean up build artifacts

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

build_test() {
    echo "Running tests..."
    cd "${PROJECT_ROOT}"
    go test ./... -race -coverprofile=coverage.txt -covermode=atomic
    echo "Tests passed."
}

build_release() {
    local version
    if [[ -n "${VERSION:-}" ]]; then
        version="${VERSION}"
    else
        version="$(git describe --tags --always --dirty 2>/dev/null || echo 'dev')"
    fi
    echo "Building release for version: ${version}"

    cd "${PROJECT_ROOT}"

    # Clean previous builds
    rm -rf dist
    mkdir -p dist

    # Build for all supported platforms
    local platforms=(
        "linux/amd64"
        "linux/arm64"
        "darwin/amd64"
        "darwin/arm64"
    )

    for platform in "${platforms[@]}"; do
        local goos="${platform%/*}"
        local goarch="${platform#*/}"
        local output="dist/botfile-${goos}-${goarch}"

        echo "Building for ${goos}/${goarch}..."
        CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
            go build -trimpath -ldflags "-s -w -X main.version=${version}" -o "${output}" ./cmd/botfile

        # Generate checksum
        sha256sum "${output}" > "${output}.sha256"
    done

    # Create release tarballs
    for platform in "${platforms[@]}"; do
        local goos="${platform%/*}"
        local goarch="${platform#*/}"
        local binary="dist/botfile-${goos}-${goarch}"
        local tarball="dist/botfile-${goos}-${goarch}.tar.gz"

        tar -czf "${tarball}" -C dist "botfile-${goos}-${goarch}"
    done

    # Generate combined checksums file
    (cd dist && sha256sum *.tar.gz > checksums.txt 2>/dev/null || true)

    echo "Release artifacts in dist/:"
    ls -la dist/
}

build_homebrew() {
    echo "Testing Homebrew formula..."
    cd "${PROJECT_ROOT}"

    # Check if Homebrew is available
    if ! command -v brew &> /dev/null; then
        echo "Homebrew not found. Skipping formula test."
        return 0
    fi

    # Create a temporary formula with local path
    local formula="${PROJECT_ROOT}/Homebrew/botfile.rb"
    if [[ ! -f "${formula}" ]]; then
        echo "Formula not found at ${formula}"
        return 1
    fi

    # Test the formula
    echo "Running brew install with local formula..."
    brew install --formula "${formula}"

    # Verify installation
    botfile version
    botfile help
    botfile guide --format text

    echo "Homebrew formula test passed!"
}

build_tidy() {
    echo "Cleaning up build artifacts..."
    cd "${PROJECT_ROOT}"
    rm -rf dist
    rm -f botfile
    go clean -cache -modcache -testcache
    echo "Cleanup complete."
}

case "${1:-test}" in
    test)
        build_test
        ;;
    release)
        build_release
        ;;
    homebrew)
        build_homebrew
        ;;
    tidy)
        build_tidy
        ;;
    *)
        echo "Usage: $0 {test|release|homebrew|tidy}"
        echo ""
        echo "Commands:"
        echo "  test      - Run all tests (default)"
        echo "  release   - Build release binaries for all platforms"
        echo "  homebrew  - Test the Homebrew formula locally"
        echo "  tidy      - Clean up build artifacts"
        exit 1
        ;;
esac