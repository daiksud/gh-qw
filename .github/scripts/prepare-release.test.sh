#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
TEMP="$(mktemp -d)"
trap 'rm -rf "$TEMP"' EXIT
mkdir -p "$TEMP/bin" "$TEMP/repo"
git init --quiet "$TEMP/repo"
cd "$TEMP/repo"
git config user.name "Release test"
git config user.email "release@example.invalid"
git config commit.gpgsign false
git commit --quiet --allow-empty -m baseline
BASE="$(git rev-parse HEAD)"
git tag v0.3.0
git tag v1.2.3
git tag -a v0.2.9 -m "Annotated baseline"
git commit --quiet --allow-empty -m change
TARGET="$(git rev-parse HEAD)"
git tag v0.99.0
git commit --quiet --allow-empty -m later
LATER="$(git rev-parse HEAD)"
git tag v0.4.0

cat > "$TEMP/bin/gh" <<'GH'
#!/usr/bin/env bash
set -euo pipefail
endpoint=""
query=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    repos/*) endpoint="$1" ;;
    --jq) shift; query="$1" ;;
  esac
  shift
done
echo "$endpoint" >> "$FAKE_LOG"
case "$endpoint" in
  */releases/latest)
    [ "${FAIL_API:-}" != latest ] || exit 42
    echo "$PREV_TAG"
    ;;
  */commits/*/pulls)
    [ "${FAIL_API:-}" != association ] || exit 42
    printf '%s\n' "$ASSOCIATIONS" | jq -r "$query"
    ;;
  */pulls/*)
    [ "${FAIL_API:-}" != labels ] || exit 42
    printf '%s\n' "$LABELS"
    ;;
  *)
    echo "Unexpected gh API call: $endpoint" >&2
    exit 42
    ;;
esac
GH
chmod +x "$TEMP/bin/gh"

export PATH="$TEMP/bin:$PATH"
export GITHUB_REPOSITORY="example/gh-qw"
export GITHUB_SHA="$TARGET"
export PREV_TAG="v0.3.0"
export FAKE_LOG="$TEMP/api.log"
export ASSOCIATIONS='[{"number":1,"merged_at":"2026-01-01","base":{"ref":"main"}}]'
export LABELS=""
export FAIL_API=""

expect_version() {
  local name="$1" version="$2" actual expected
  : > "$FAKE_LOG"
  actual="$(bash "$SCRIPT_DIR/prepare-release.sh")"
  expected="$(printf 'previous_tag=%s\ntag=%s\nsha=%s' "$PREV_TAG" "$version" "$GITHUB_SHA")"
  if [ "$actual" != "$expected" ]; then
    printf 'FAIL %s\nexpected: %s\nactual: %s\n' "$name" "$expected" "$actual" >&2
    exit 1
  fi
  echo "PASS $name"
}
expect_failure() {
  local name="$1" expected="$2"
  : > "$FAKE_LOG"
  if bash "$SCRIPT_DIR/prepare-release.sh" > "$TEMP/out" 2> "$TEMP/err"; then
    echo "FAIL $name (unexpected success)" >&2
    exit 1
  fi
  if [ -s "$TEMP/out" ] || [[ "$(cat "$TEMP/err")" != *"$expected"* ]]; then
    echo "FAIL $name (wrong failure)" >&2
    cat "$TEMP/out" "$TEMP/err" >&2
    exit 1
  fi
  echo "PASS $name"
}

LABELS=fix expect_version "fix" v0.3.1
LABELS=revert expect_version "revert" v0.3.1
LABELS=feat expect_version "feature" v0.4.0
LABELS=perf expect_version "performance" v0.4.0
LABELS=DEPRECATED expect_version "deprecation" v0.4.0
LABELS="BREAKING CHANGE" expect_version "v0 breaking" v0.4.0
LABELS=$'fix\nfeat\nBREAKING CHANGE' expect_version "highest tier" v0.4.0
PREV_TAG=v1.2.3 LABELS=fix expect_version "stable patch" v1.2.4
PREV_TAG=v1.2.3 LABELS=feat expect_version "stable minor" v1.3.0
PREV_TAG=v1.2.3 LABELS="BREAKING CHANGE" expect_version "stable major" v2.0.0
LABELS=$'docs\nci\nbuild' expect_failure "maintenance only" "No merged pull request"
ASSOCIATIONS="[]" LABELS=feat expect_failure "no associated PR" "No merged pull request"
ASSOCIATIONS='[{"number":2,"merged_at":null,"base":{"ref":"main"}}]' LABELS=feat expect_failure "open PR not counted" "No merged pull request"
GITHUB_SHA="$BASE" LABELS=fix expect_failure "empty range" "No merged pull request"
PREV_TAG=v0.2.9 LABELS=fix expect_version "annotated tag" v0.2.10
PREV_TAG=v0.3.0-rc.1 LABELS=fix expect_failure "bad baseline" "not a plain"
PREV_TAG=v0.4.0 LABELS=fix expect_failure "old selected SHA" "does not include"
FAIL_API=latest LABELS=fix expect_failure "latest API failure" ""
FAIL_API=association LABELS=fix expect_failure "associated PR API failure" ""
FAIL_API=labels LABELS=fix expect_failure "labels API failure" ""

GITHUB_SHA="$LATER" LABELS=fix expect_version "ignore stray tags; deduplicate PRs" v0.3.1
if [ "$(grep -c '^repos/example/gh-qw/pulls/1$' "$FAKE_LOG")" -ne 1 ]; then
  echo "FAIL associated PR labels requested multiple times" >&2
  exit 1
fi
if git show-ref --verify --quiet refs/tags/v0.3.1; then
  echo "FAIL script created a tag" >&2
  exit 1
fi
echo "All release preparation tests passed."
