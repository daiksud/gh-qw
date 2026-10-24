#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${SOURCE_SHA:?SOURCE_SHA is required}"

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
TARGET_SHA="$(git rev-parse --verify "${SOURCE_SHA}^{commit}")"
if [ "$TARGET_SHA" != "$SOURCE_SHA" ]; then
  echo "The selected source is not an exact commit SHA." >&2
  exit 1
fi
if ! git merge-base --is-ancestor "$PREVIOUS_SHA" "$TARGET_SHA"; then
  echo "The selected commit does not include the latest published release." >&2
  exit 1
fi

HAS_SUBSTANTIVE=false
HAS_BREAKING=false
HAS_MINOR=false
HEADER_RE='^([[:alpha:]][[:alnum:]_-]*)(\([^)]*\))?(!)?:'
BREAKING_FOOTER_RE='^(BREAKING CHANGE|BREAKING-CHANGE)(:([[:space:]].*)?$|[[:space:]]#.*$)'
DEPRECATED_FOOTER_RE='^DEPRECATED(:([[:space:]].*)?$|[[:space:]]#.*$)'

# Classify complete Conventional Commit messages in the selected source range.
# Empty commits do not change the selected source tree and are the only release
# preparation commits gh-qw can safely ignore. A preparation-named commit with
# any tree change remains substantive and therefore receives at least a patch.
while IFS= read -r -d '' sha && IFS= read -r -d '' message; do
  if git diff-tree --quiet "${sha}^" "$sha"; then
    continue
  fi

  HAS_SUBSTANTIVE=true
  header="${message%%$'\n'*}"
  if [[ "$header" =~ $HEADER_RE ]]; then
    type="${BASH_REMATCH[1]}"
    if [ -n "${BASH_REMATCH[3]}" ]; then
      HAS_BREAKING=true
    fi
    case "$type" in
      feat | perf) HAS_MINOR=true ;;
    esac
  fi

  while IFS= read -r line; do
    if [[ "$line" =~ $BREAKING_FOOTER_RE ]]; then
      HAS_BREAKING=true
    elif [[ "$line" =~ $DEPRECATED_FOOTER_RE ]]; then
      HAS_MINOR=true
    fi
  done <<< "$message"
done < <(git log -z --format='%H%x00%B' "${PREVIOUS_SHA}..${TARGET_SHA}")

if ! $HAS_SUBSTANTIVE; then
  echo "No substantive commits exist since ${PREVIOUS_TAG}." >&2
  exit 1
fi

IFS='.' read -r major minor patch <<< "${PREVIOUS_TAG#v}"
if [ "$major" = "0" ]; then
  # Graduation to 1.0.0 requires an explicit policy change.
  if $HAS_BREAKING || $HAS_MINOR; then
    VERSION="v0.$((minor + 1)).0"
  else
    VERSION="v0.${minor}.$((patch + 1))"
  fi
elif $HAS_BREAKING; then
  VERSION="v$((major + 1)).0.0"
elif $HAS_MINOR; then
  VERSION="v${major}.$((minor + 1)).0"
else
  VERSION="v${major}.${minor}.$((patch + 1))"
fi

printf 'version=%s\nprevious-tag=%s\nprevious-sha=%s\n' \
  "$VERSION" "$PREVIOUS_TAG" "$PREVIOUS_SHA"
