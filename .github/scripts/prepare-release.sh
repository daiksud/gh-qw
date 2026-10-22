#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${GITHUB_SHA:?GITHUB_SHA is required}"

# Published stable releases are the baseline; failed or stray tags are not.
if ! PREVIOUS_TAG="$(gh api "repos/${GITHUB_REPOSITORY}/releases/latest" --jq '.tag_name')"; then
  echo "Could not read the latest published release." >&2
  exit 1
fi
if [[ ! "$PREVIOUS_TAG" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "The latest published release is not a plain v<major>.<minor>.<patch> tag." >&2
  exit 1
fi

PREVIOUS_SHA="$(git rev-parse --verify "refs/tags/${PREVIOUS_TAG}^{commit}")"
TARGET_SHA="$(git rev-parse --verify "${GITHUB_SHA}^{commit}")"
if ! git merge-base --is-ancestor "$PREVIOUS_SHA" "$TARGET_SHA"; then
  echo "The selected commit does not include the latest published release." >&2
  exit 1
fi

HAS_BREAKING=false
HAS_MINOR=false
HAS_PATCH=false

# Only PRs merged at commits in this release range affect the version.
declare -A RANGE_COMMITS=()
commits="$(git rev-list "${PREVIOUS_SHA}..${TARGET_SHA}")"
while IFS= read -r sha; do
  [ -z "$sha" ] && continue
  RANGE_COMMITS["$sha"]=1
done <<< "$commits"

# Read labels with the merged PR list instead of one API request per commit
# plus another API request per PR. Squash merge uses merge_commit_sha on main.
if ! labeled_merges="$(gh api --paginate \
  "repos/${GITHUB_REPOSITORY}/pulls?state=closed&per_page=100" \
  --jq '.[] | select(.merged_at != null and .base.ref == "main" and .merge_commit_sha != null) | .merge_commit_sha as $sha | .labels[].name | [$sha, .] | @tsv')"; then
  echo "Could not list merged pull request labels." >&2
  exit 1
fi

while IFS=$'\t' read -r sha label; do
  [ -z "$sha" ] && continue
  [ -n "${RANGE_COMMITS[$sha]:-}" ] || continue
  case "$label" in
    "BREAKING CHANGE") HAS_BREAKING=true ;;
    "DEPRECATED" | feat | perf) HAS_MINOR=true ;;
    fix | revert) HAS_PATCH=true ;;
  esac
done <<< "$labeled_merges"
if ! $HAS_BREAKING && ! $HAS_MINOR && ! $HAS_PATCH; then
  echo "No merged pull request since ${PREVIOUS_TAG} has a version-affecting label." >&2
  exit 1
fi

IFS='.' read -r major minor patch <<< "${PREVIOUS_TAG#v}"
if [ "$major" = "0" ]; then
  # Graduation to 1.0.0 requires an explicit policy change.
  if $HAS_BREAKING || $HAS_MINOR; then
    TAG="v0.$((minor + 1)).0"
  else
    TAG="v0.${minor}.$((patch + 1))"
  fi
elif $HAS_BREAKING; then
  TAG="v$((major + 1)).0.0"
elif $HAS_MINOR; then
  TAG="v${major}.$((minor + 1)).0"
else
  TAG="v${major}.${minor}.$((patch + 1))"
fi

printf 'previous_tag=%s\ntag=%s\nsha=%s\n' "$PREVIOUS_TAG" "$TAG" "$TARGET_SHA"
