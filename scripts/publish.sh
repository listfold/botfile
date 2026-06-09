#!/bin/sh
# Push a single v* tag to origin, then publish its Codeberg release.
#
# Usage: scripts/publish.sh <tag>
#
# This is the post-push release path. release.sh requires the tag to already be
# on origin, so publish.sh validates the name, pushes ONLY that tag ref (never a
# branch), and hands off to release.sh, which re-validates, confirms the remote
# tag resolves to the same commit, builds the tagged source, and reconciles the
# release assets.
set -eu

tag=${1:?usage: publish.sh <tag>}
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

# Validate the name BEFORE touching the remote, so a bad value never reaches a
# push. Pushing refs/tags/<tag> explicitly (not <tag>) also means a branch name
# could never push a branch by accident.
"$here/valid-tag.sh" "$tag" ||
	{ echo "publish: tag '$tag' is not a valid vMAJOR.MINOR.PATCH[-prerelease] version" >&2; exit 1; }

git rev-parse --verify --quiet "refs/tags/$tag^{commit}" >/dev/null ||
	{ echo "publish: no local tag refs/tags/$tag (create it with: git tag $tag)" >&2; exit 1; }

echo "pushing refs/tags/$tag to origin"
git push origin "refs/tags/$tag"

exec "$here/release.sh" "$tag"
