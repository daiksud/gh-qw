#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${GITHUB_SHA:?GITHUB_SHA is required}"

# Published stable releases are the baseline; failed or stray tags are not.
PREVIOUS_TAG="$(gh api "repos/${GITHUB_REPOSITORY}/releases/latest" --jq '.tag_name')"
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
declare -A SEEN_PRS=()

# Fail closed on API errors. Do not silently omit a change.
while IFS= read -r sha; do
  [ -z "$sha" ] && continue
  pr_numbers="$(gh api --paginate "repos/${GITHUB_REPOSITORY}/commits/${sha}/pulls" \
    --jq '.[] | select(.merged_at != null and .base.ref == "main") | .number')"
  for pr in $pr_numbers; do
    [ -n "${SEEN_PRS[$pr]:-}" ] && continue
    SEEN_PRS[$pr]=1
    labels="$(gh api "repos/${GITHUB_REPOSITORY}/pulls/${pr}" --jq '.labels[].name')"
    while IFS= read -r label; do
      case "$label" in
        "BREAKING CHANGE") HAS_BREAKING=true ;;
        "DEPRECATED" | feat | perf) HAS_MINOR=true ;;
        fix | revert) HAS_PATCH=true ;;
      esac
    done <<< "$labels"
  done
done < <(git rev-list "${PREVIOUS_SHA}..${TARGET_SHA}")

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
