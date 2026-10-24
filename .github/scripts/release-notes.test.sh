#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
CONFIG="$(cd -- "$SCRIPT_DIR/.." && pwd)/release.yml"

if grep -Eq '^  exclude:|authors:' "$CONFIG"; then
  echo "FAIL release notes must not globally exclude labels or authors" >&2
  exit 1
fi

actual_titles="$(awk '
  /^    - title: / {
    line = $0
    sub(/^    - title: /, "", line)
    gsub(/^"|"$/, "", line)
    print line
  }
' "$CONFIG")"
expected_titles="$(printf '%s\n' \
  '💥 BREAKING CHANGE' \
  '⚠️ DEPRECATED' \
  '✨ New Feature' \
  '🐛 Bug Fix' \
  '🛠️ Maintenance')"
if [ "$actual_titles" != "$expected_titles" ]; then
  echo "FAIL release-note category order or spelling" >&2
  printf 'expected:\n%s\nactual:\n%s\n' "$expected_titles" "$actual_titles" >&2
  exit 1
fi

matching_categories() {
  local label_set="|" label
  for label in "$@"; do
    label_set="${label_set}${label}|"
  done

  awk -v label_set="$label_set" '
    function clean(line) {
      sub(/^ *- /, "", line)
      gsub(/^"|"$/, "", line)
      return line
    }
    function has_label(label) {
      return index(label_set, "|" label "|") != 0
    }
    function finish_category() {
      if (title != "" && included && !excluded) {
        if (matches != "") {
          matches = matches "\n"
        }
        matches = matches title
      }
      included = 0
      excluded = 0
    }
    /^    - title: / {
      finish_category()
      title = $0
      sub(/^    - title: /, "", title)
      gsub(/^"|"$/, "", title)
      mode = ""
      next
    }
    /^      labels:$/ {
      mode = "include"
      next
    }
    /^      exclude:$/ {
      mode = ""
      next
    }
    /^        labels:$/ {
      mode = "exclude"
      next
    }
    /^        - / && mode == "include" {
      label = clean($0)
      if (label == "*" || has_label(label)) {
        included = 1
      }
      next
    }
    /^          - / && mode == "exclude" {
      label = clean($0)
      if (has_label(label)) {
        excluded = 1
      }
      next
    }
    END {
      finish_category()
      print matches
    }
  ' "$CONFIG"
}

expect_category() {
  local name="$1" expected="$2" actual
  shift 2
  actual="$(matching_categories "$@")"
  if [ "$actual" != "$expected" ]; then
    printf 'FAIL %s\nexpected: %s\nactual: %s\n' "$name" "$expected" "$actual" >&2
    exit 1
  fi
  echo "PASS $name"
}

expect_category "breaking priority" "💥 BREAKING CHANGE" \
  "BREAKING CHANGE" "DEPRECATED" feat perf fix docs
expect_category "deprecation priority" "⚠️ DEPRECATED" \
  "DEPRECATED" feat perf fix chore
expect_category "feature priority" "✨ New Feature" feat perf fix test
expect_category "bug-fix priority" "🐛 Bug Fix" fix revert

for label in docs refactor style test build ci chore revert unknown; do
  expect_category "maintenance fallback: $label" "🛠️ Maintenance" "$label"
done
expect_category "unlabeled fallback" "🛠️ Maintenance"

echo "All release-note category tests passed."
