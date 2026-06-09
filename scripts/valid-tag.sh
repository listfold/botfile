#!/bin/sh
# Exit 0 iff $1 is a valid botfile release tag: a strict v-prefixed semver,
# vMAJOR.MINOR.PATCH with an optional -prerelease. Single source of truth for the
# tag naming contract, used by release.sh and publish.sh so the rule cannot drift.
# The pattern admits no shell metacharacters, whitespace, or '/', which is what
# makes later use in shell, the Go linker flag, and API URL paths safe.
tag=${1:?usage: valid-tag.sh <tag>}
printf '%s' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$'
