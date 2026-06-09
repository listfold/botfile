#!/bin/sh
# Cross-compile the botfile release binaries for every supported platform.
#
# Usage: scripts/build-matrix.sh <version> <output-dir>
#
# Builds from the CURRENT WORKING DIRECTORY, so the caller controls which source
# is compiled: run it at the repo root for a working-tree snapshot, or `cd` into
# a clean checkout of a tag first to pin the build to that tag's source. The
# <version> string is stamped into the binary via -ldflags and is independent of
# the source location.
set -eu

version=$1
outdir=$2

bin=botfile
# GOOS/GOARCH targets. Keep in sync with the install scripts' arch detection.
platforms="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"

mkdir -p "$outdir"
for p in $platforms; do
	os=${p%/*}
	arch=${p#*/}
	out="$outdir/$bin-$os-$arch"
	[ "$os" = windows ] && out="$out.exe"
	echo "building $out"
	GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 \
		go build -ldflags "-s -w -X main.version=$version" -o "$out" ./cmd/"$bin"
done

# Checksums over just the binaries (sha256sum on Linux, shasum on macOS/BSD).
(
	cd "$outdir"
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$bin"-* >checksums.txt
	else
		shasum -a 256 "$bin"-* >checksums.txt
	fi
)
echo "wrote $outdir/checksums.txt"
