#!/usr/bin/env bash
set -euo pipefail
script=$(cd "$(dirname "$0")" && pwd)/validate-settings.sh
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
mkdir "$fixture/bin" "$fixture/tmp"
cat > "$fixture/bin/uname" <<'MOCK'
#!/usr/bin/env bash
if [[ $1 == -s ]]; then echo "${MOCK_OS:-Linux}"; else echo x86_64; fi
MOCK
cat > "$fixture/bin/curl" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
[[ -z ${GH_TOKEN:-} && -z ${GITHUB_TOKEN:-} ]]
[[ $* == *'https://github.com/daiksud/gh-settings/releases/download/v0.23.0/gh-settings-linux-amd64'* ]]
[[ $* == *"--proto =https --tlsv1.2"* ]]
[[ ${SCENARIO:-} != download ]] || exit 22
while [[ $1 != --output ]]; do shift; done
cp "$MOCK_BINARY" "$2"
printf 'download\n' >> "$CALLS"
MOCK
cat > "$fixture/bin/sha256sum" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
[[ $* == '--check --status' ]]
IFS= read -r line
[[ $line == '93d742d6634801a18e2adaf85656abdad4eed8a6ecb887f2b10778f236ac2dd9  '* ]]
printf 'digest\n' >> "$CALLS"
[[ ${SCENARIO:-} != digest ]]
MOCK
cat > "$fixture/product" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
[[ -z ${GH_TOKEN:-} && -z ${GITHUB_TOKEN:-} ]]
printf '%s\n' "$1" >> "$CALLS"
case $1 in
  --version)
    [[ ${SCENARIO:-} != version-exit ]] || exit 18
    if [[ ${SCENARIO:-} == version ]]; then echo 'gh-settings v0.20.0'; else echo 'gh-settings v0.23.0'; fi ;;
  validate) [[ ${SCENARIO:-} != validate ]] || exit 37 ;;
  *) exit 99 ;;
esac
MOCK
chmod +x "$fixture/bin/"* "$fixture/product"
for scenario in success download digest version version-exit validate platform; do
  calls=$fixture/$scenario.calls
  : > "$calls"
  status=0
  os=Linux
  [[ $scenario != platform ]] || os=Darwin
  PATH="$fixture/bin:$PATH" TMPDIR="$fixture/tmp" MOCK_BINARY="$fixture/product" \
    CALLS="$calls" SCENARIO="$scenario" MOCK_OS="$os" GH_TOKEN=unused-fixture \
    GITHUB_TOKEN=unused-fixture bash "$script" > "$fixture/$scenario.log" 2>&1 || status=$?
  case $scenario in
    success) [[ $status == 0 && $(cat "$calls") == $'download\ndigest\n--version\nvalidate' ]] ;;
    validate) [[ $status == 37 && $(tail -1 "$calls") == validate ]] ;;
    download) [[ $status == 22 && ! -s $calls ]] ;;
    platform) [[ $status != 0 && ! -s $calls ]] ;;
    digest) [[ $status != 0 && $(cat "$calls") == $'download\ndigest' ]] ;;
    *) [[ $status != 0 && $(cat "$calls") == $'download\ndigest\n--version' ]] ;;
  esac
  [[ -z $(find "$fixture/tmp" -mindepth 1 -print -quit) ]]
done
echo 'settings validator: 7 scenarios passed'
