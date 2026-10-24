#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
WORKFLOW="$(cd -- "$SCRIPT_DIR/../workflows" && pwd)/release.yml"
TEMP="$(mktemp -d)"
trap 'rm -rf "$TEMP"' EXIT
mkdir -p "$TEMP/bin"

cat > "$TEMP/bin/gh" <<'GH'
#!/usr/bin/env bash
set -euo pipefail
[[ "$*" == *"git/ref/heads/main"* ]]
printf '%s\n' "$CURRENT_MAIN"
GH
chmod +x "$TEMP/bin/gh"

extract_run() {
  local name="$1" destination="$2"
  awk -v target="$name" '
    $0 == "      - name: " target { found = 1; next }
    found && $0 == "        run: |" { run = 1; next }
    run && /^      - name: / { exit }
    run { sub(/^          /, ""); print }
  ' "$WORKFLOW" > "$destination"
  [ -s "$destination" ]
}

extract_run \
  "Require the dispatched main commit to remain current" \
  "$TEMP/require-current.sh"
extract_run \
  "Validate the optional reviewed source pin" \
  "$TEMP/validate-pin.sh"

SOURCE_SHA=0123456789abcdef0123456789abcdef01234567
OTHER_SHA=abcdef0123456789abcdef0123456789abcdef01
export PATH="$TEMP/bin:$PATH"
export GITHUB_REPOSITORY=example/gh-qw
export GH_TOKEN=test-only
export SOURCE_SHA

CURRENT_MAIN="$SOURCE_SHA" bash -e -o pipefail "$TEMP/require-current.sh"
echo "PASS dispatched commit is current main"

if CURRENT_MAIN="$OTHER_SHA" bash -e -o pipefail "$TEMP/require-current.sh" >/dev/null 2>&1; then
  echo "FAIL moved main was accepted" >&2
  exit 1
fi
echo "PASS moved main fails closed"

if CURRENT_MAIN=invalid bash -e -o pipefail "$TEMP/require-current.sh" >/dev/null 2>&1; then
  echo "FAIL invalid main response was accepted" >&2
  exit 1
fi
echo "PASS invalid main response fails closed"

EXPECTED_SOURCE_SHA="" bash -e -o pipefail "$TEMP/validate-pin.sh"
EXPECTED_SOURCE_SHA="$SOURCE_SHA" bash -e -o pipefail "$TEMP/validate-pin.sh"
echo "PASS omitted or matching reviewed source pin"

for expected in invalid "$OTHER_SHA"; do
  if EXPECTED_SOURCE_SHA="$expected" bash -e -o pipefail "$TEMP/validate-pin.sh" >/dev/null 2>&1; then
    echo "FAIL invalid or mismatched reviewed source pin was accepted" >&2
    exit 1
  fi
done
echo "PASS invalid or mismatched reviewed source pin fails closed"

echo "All release source trust tests passed."
