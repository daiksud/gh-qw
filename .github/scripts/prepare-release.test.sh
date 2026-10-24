#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
TEMP="$(mktemp -d)"
trap 'rm -rf "$TEMP"' EXIT
mkdir -p "$TEMP/bin"

cat > "$TEMP/bin/gh" <<'GH'
#!/usr/bin/env bash
set -euo pipefail
endpoint=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    repos/*) endpoint="$1" ;;
    --jq) shift ;;
  esac
  shift
done
echo "$endpoint" >> "$FAKE_LOG"
case "$endpoint" in
  */releases/latest)
    [ "${FAIL_API:-}" != latest ] || exit 42
    echo "$PREV_TAG"
    ;;
  */pulls*)
    echo "Version preparation must not query pull request labels." >&2
    exit 42
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
export FAKE_LOG="$TEMP/api.log"
export FAIL_API=""

case_index=0
change_index=0
start_case() {
  local baseline_tag="$1"
  case_index=$((case_index + 1))
  CASE_DIR="$TEMP/case-$case_index"
  mkdir -p "$CASE_DIR"
  git init --quiet "$CASE_DIR"
  cd "$CASE_DIR"
  git config user.name "Release test"
  git config user.email "release@example.invalid"
  git config commit.gpgsign false
  printf 'baseline\n' > baseline.txt
  git add baseline.txt
  git commit --quiet -m "chore: establish baseline"
  git tag "$baseline_tag"
  PREV_TAG="$baseline_tag"
  export PREV_TAG
  change_index=0
}

commit_change() {
  local message="$1"
  change_index=$((change_index + 1))
  printf 'change %s\n' "$change_index" > "change-$change_index.txt"
  git add "change-$change_index.txt"
  git commit --quiet -m "$message"
}

commit_empty() {
  git commit --quiet --allow-empty -m "$1"
}

expect_version() {
  local name="$1" version="$2" actual expected
  GITHUB_SHA="$(git rev-parse HEAD)"
  export GITHUB_SHA
  : > "$FAKE_LOG"
  actual="$(bash "$SCRIPT_DIR/prepare-release.sh")"
  expected="$(printf 'previous_tag=%s\ntag=%s\nsha=%s' "$PREV_TAG" "$version" "$GITHUB_SHA")"
  if [ "$actual" != "$expected" ]; then
    printf 'FAIL %s\nexpected: %s\nactual: %s\n' "$name" "$expected" "$actual" >&2
    exit 1
  fi
  if grep -Fq '/pulls' "$FAKE_LOG"; then
    echo "FAIL $name (queried pull request labels)" >&2
    exit 1
  fi
  echo "PASS $name"
}

expect_failure() {
  local name="$1" expected="$2"
  GITHUB_SHA="$(git rev-parse HEAD)"
  export GITHUB_SHA
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

start_case v0.3.0
commit_change "feat!: replace the public contract"
expect_version "v0 breaking header" v0.4.0

start_case v1.2.3
commit_change $'refactor(core): reorganize internals\n\nBREAKING CHANGE: remove the old API'
expect_version "stable breaking footer" v2.0.0

start_case v1.2.3
commit_change $'refactor(core): reorganize internals\n\nBREAKING-CHANGE: remove the old API'
expect_version "hyphenated breaking footer" v2.0.0

start_case v0.3.0
commit_change "feat(cli): add a command"
expect_version "feature" v0.4.0

start_case v0.3.0
commit_change "perf(scan): reduce filesystem work"
expect_version "performance" v0.4.0

start_case v0.3.0
commit_change $'docs: update current usage\n\nDEPRECATED: remove the old form next time'
expect_version "deprecation footer" v0.4.0

start_case v0.3.0
commit_change "fix(paths): preserve the selected target"
expect_version "fix" v0.3.1

start_case v0.3.0
commit_change "revert: restore the previous behavior"
expect_version "revert" v0.3.1

for internal_type in docs ci build chore refactor; do
  start_case v0.3.0
  commit_change "$internal_type: maintain internals"
  expect_version "$internal_type-only patch" v0.3.1
done

start_case v1.2.3
commit_change "fix: correct an error"
commit_change "feat: add behavior"
commit_change $'docs: explain the replacement\n\nBREAKING CHANGE: remove the old behavior'
expect_version "mixed changes use highest bump once" v2.0.0

start_case v0.3.0
expect_failure "no changes" "No substantive commits"

start_case v0.3.0
commit_empty "chore(release): prepare v0.3.1"
expect_failure "preparation-only empty commit" "No substantive commits"

start_case v0.3.0
commit_change "chore(release): prepare v0.3.1"
expect_version "substantive preparation-named commit" v0.3.1

start_case v0.3.0
commit_change "docs: update usage"
LABELS="BREAKING CHANGE" expect_version "pull request labels do not affect version" v0.3.1

start_case v0.3.0
commit_change "fix: correct an error"
git tag v0.99.0
expect_version "ignore unpublished tags" v0.3.1

start_case v0.2.9
git tag -f -a v0.2.9 -m "Annotated baseline"
commit_change "fix: correct an error"
expect_version "annotated baseline" v0.2.10

start_case v0.3.0-rc.1
commit_change "fix: correct an error"
expect_failure "bad baseline" "not a plain"

start_case v0.3.0
OLD_SHA="$(git rev-parse HEAD)"
commit_change "fix: correct an error"
git tag v0.4.0
PREV_TAG=v0.4.0
export PREV_TAG
GITHUB_SHA="$OLD_SHA"
export GITHUB_SHA
: > "$FAKE_LOG"
if bash "$SCRIPT_DIR/prepare-release.sh" > "$TEMP/out" 2> "$TEMP/err"; then
  echo "FAIL old selected SHA (unexpected success)" >&2
  exit 1
fi
if [[ "$(cat "$TEMP/err")" != *"does not include"* ]]; then
  echo "FAIL old selected SHA (wrong failure)" >&2
  cat "$TEMP/err" >&2
  exit 1
fi
echo "PASS old selected SHA"

start_case v0.3.0
commit_change "fix: correct an error"
FAIL_API=latest expect_failure "latest API failure" "Could not read"

echo "All release preparation tests passed."
