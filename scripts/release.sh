#!/bin/sh
# Publish a Codeberg release for a single pushed v* tag.
#
# Usage: scripts/release.sh <tag>
# Requires: git, go, curl, jq, tar, and $CODEBERG_TOKEN (a token with
# repository write scope).
#
# The release boundary is deliberately strict: it accepts ONE validated, pushed
# v* tag, resolves it once to an immutable commit, builds that exact commit, and
# reconciles the release's assets deterministically. The tag arrives as a single
# quoted positional argument ("$1") and is validated against a strict pattern
# BEFORE it is used in any shell, linker, or URL context, so a hostile value such
# as 'v1$(...)' or a branch name cannot be executed or published.
set -eu

tag=${1:?usage: release.sh <tag>}

owner=botfile
repo=botfile
api="https://codeberg.org/api/v1/repos/$owner/$repo"
dist=dist
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

fail() {
	echo "release: $1" >&2
	exit 1
}

# --- 1. validate the tag name ------------------------------------------------
# valid-tag.sh is the single source of truth for the naming contract: a strict
# v-prefixed semver that admits no shell metacharacters, whitespace, or '/', so
# later use in shell, the Go linker flag, and the API URL path is safe.
"$here/valid-tag.sh" "$tag" ||
	fail "tag '$tag' is not a valid vMAJOR.MINOR.PATCH[-prerelease] version"

# --- 2. the tag must exist LOCALLY as a real tag (not a branch or raw SHA) ----
# Resolving refs/tags/<tag> specifically (not <tag>^{commit}) rejects branch
# names and commit-ish values that happen to resolve to a commit.
local_commit=$(git rev-parse --verify --quiet "refs/tags/$tag^{commit}") ||
	fail "no local tag refs/tags/$tag (create it with: git tag $tag)"

# --- 3. the tag must be PUSHED and agree with origin --------------------------
# Forgejo's workflow pushes the git tag first, then creates the release for it.
# We require the remote tag to exist and resolve to the same commit, so a release
# can never be cut from a tag that was never published or has drifted.
remote_refs=$(git ls-remote --tags origin "refs/tags/$tag" "refs/tags/$tag^{}") ||
	fail "cannot read tags from origin"
# Prefer the peeled (^{}) line, which dereferences annotated tags to the commit.
remote_commit=$(printf '%s\n' "$remote_refs" |
	awk -v d="refs/tags/$tag" -v p="refs/tags/$tag^{}" \
		'$2==p{peeled=$1} $2==d{direct=$1} END{print (peeled!=""?peeled:direct)}')
[ -n "$remote_commit" ] ||
	fail "tag $tag is not pushed to origin (push it first: git push origin $tag)"
[ "$remote_commit" = "$local_commit" ] ||
	fail "local tag $tag ($local_commit) does not match origin ($remote_commit)"
commit=$local_commit

# --- 4. credentials (after validation so the checks above can run token-free) -
: "${CODEBERG_TOKEN:?set CODEBERG_TOKEN to a Codeberg API token with repository write scope}"
auth="Authorization: token $CODEBERG_TOKEN"

# --- 5. build the tagged source in isolation ---------------------------------
work=$(mktemp -d)
tmp=$(mktemp -d)
trap 'rm -rf "$work" "$tmp"' EXIT
git archive --format=tar "$tag" | tar -x -C "$work"
rm -rf "$dist"
mkdir -p "$dist"
dist_abs=$(cd "$dist" && pwd)
(cd "$work" && "$here/build-matrix.sh" "$tag" "$dist_abs")

# --- 6. create or reuse the release ------------------------------------------
code=$(curl -sS -o "$tmp/rel" -w '%{http_code}' -H "$auth" "$api/releases/tags/$tag")
if [ "$code" = 200 ]; then
	id=$(jq -e -r '.id' <"$tmp/rel")
	echo "reusing release $tag (id $id)"
elif [ "$code" = 404 ]; then
	echo "creating release $tag at $commit"
	payload=$(jq -n --arg t "$tag" --arg c "$commit" \
		'{tag_name:$t, target_commitish:$c, name:$t, draft:false, prerelease:false}')
	code=$(curl -sS -o "$tmp/rel" -w '%{http_code}' -H "$auth" \
		-H 'Content-Type: application/json' -X POST "$api/releases" -d "$payload")
	[ "$code" = 201 ] || {
		echo "create failed (HTTP $code):" >&2
		cat "$tmp/rel" >&2
		exit 1
	}
	id=$(jq -e -r '.id' <"$tmp/rel")
else
	echo "release lookup failed (HTTP $code):" >&2
	cat "$tmp/rel" >&2
	exit 1
fi

# --- 7. collect ALL existing assets across ALL pages -------------------------
# The asset list is paginated; gather every page so the reconciliation below
# sees every pre-existing copy, not just the first page.
per_page=50
page=1
echo '[]' >"$tmp/assets"
while :; do
	code=$(curl -sS -o "$tmp/page" -w '%{http_code}' -H "$auth" \
		"$api/releases/$id/assets?limit=$per_page&page=$page")
	[ "$code" = 200 ] || {
		echo "asset list failed (HTTP $code):" >&2
		cat "$tmp/page" >&2
		exit 1
	}
	count=$(jq 'length' <"$tmp/page")
	[ "$count" -eq 0 ] && break
	jq -s '.[0] + .[1]' "$tmp/assets" "$tmp/page" >"$tmp/merged"
	mv "$tmp/merged" "$tmp/assets"
	[ "$count" -lt "$per_page" ] && break
	page=$((page + 1))
done

# --- 8. reconcile each expected asset: delete EVERY duplicate, then upload ----
# Deleting all matching ids (not just the first) guarantees convergence to
# exactly one copy of each asset even if a prior run left duplicates.
for f in "$dist"/*; do
	name=$(basename "$f")
	# Asset ids are integers, so word-splitting this list is safe; a plain for
	# loop (not a `jq | while` pipe) keeps the body in this shell so a failed
	# delete aborts via the exit below rather than dying in a subshell.
	ids=$(jq -r --arg n "$name" '.[] | select(.name == $n) | .id' <"$tmp/assets")
	for old in $ids; do
		echo "deleting existing $name (id $old)"
		code=$(curl -sS -o /dev/null -w '%{http_code}' -H "$auth" \
			-X DELETE "$api/releases/$id/assets/$old")
		[ "$code" = 204 ] || {
			echo "delete of $name (id $old) failed (HTTP $code)" >&2
			exit 1
		}
	done
	echo "uploading $name"
	code=$(curl -sS -o "$tmp/up" -w '%{http_code}' -H "$auth" -X POST \
		"$api/releases/$id/assets?name=$name" \
		-F "attachment=@$f;type=application/octet-stream")
	[ "$code" = 201 ] || {
		echo "upload of $name failed (HTTP $code):" >&2
		cat "$tmp/up" >&2
		exit 1
	}
done

echo "released $tag -> https://codeberg.org/$owner/$repo/releases/tag/$tag"
