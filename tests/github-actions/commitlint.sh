#!/usr/bin/env bash
set -euo pipefail

# Exercise the selected style rules and prove that unselected limits stay off.
commitlint="$(mise which commitlint)"
lint() {
  printf '%s\n' "$1" | mise exec -- bun run --bun --no-install "$commitlint" --strict >/dev/null 2>&1
}
accept() {
  if ! lint "$1"; then
    printf 'Expected commitlint to accept: %s\n' "$1" >&2
    exit 1
  fi
}
reject() {
  if lint "$1"; then
    printf 'Expected commitlint to reject: %s\n' "$1" >&2
    exit 1
  fi
}

accept 'feat: add repository cloning'
accept 'feat(cli): add repository cloning'
reject 'feat(CLI): add repository cloning'
accept 'feat!: change the registry format'
accept $'feat: change the registry format\n\nBREAKING CHANGE: previous registry files are no longer accepted'
accept $'feat: change the registry format\n\nBREAKING-CHANGE: previous registry files are no longer accepted'
accept $'feat: add repository cloning\n\nDEPRECATED: use clone instead of get'
accept 'perf: reduce registry reads'
accept 'fix: preserve the registered location'
accept 'revert: restore repository selection'
accept 'docs: explain repository selection'
accept 'refactor: simplify registry loading'
accept 'style: format command source'
accept 'test: cover repository selection'
accept 'build: update module dependencies'
accept 'ci: validate pull request messages'
accept 'chore: maintain repository metadata'
accept "feat: $(printf 'x%.0s' {1..120})"
accept "fix: correct a bug"$'\n\n'"$(printf 'x%.0s' {1..120})"
accept 'feat: some Message'
reject 'other: perform maintenance'
reject 'FEAT: add a feature'
reject 'feat: Add a feature'
reject 'feat: ADD A FEATURE'
reject 'feat: CloneRepository'
reject 'feat: add a feature.'
reject ' feat: add a feature'
reject 'feat: add a feature '
reject 'feat:'
reject 'fixup! feat: add a feature'
reject $'feat: add a feature\nbody without a blank line'
