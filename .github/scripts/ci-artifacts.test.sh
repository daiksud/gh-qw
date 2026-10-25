#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
TOOL="$SCRIPT_DIR/ci-artifacts.sh"
TEMP="$(mktemp -d)"
trap 'rm -rf "$TEMP"' EXIT
SOURCE_SHA=0123456789abcdef0123456789abcdef01234567
OTHER_SHA=1123456789abcdef0123456789abcdef01234567

build_matrix="$(bash "$TOOL" build-matrix)"
acceptance_matrix="$(bash "$TOOL" acceptance-matrix)"
jq -e '.include == [{"target":"linux-amd64","runner":"ubuntu-slim"},{"target":"linux-arm64","runner":"ubuntu-24.04-arm"},{"target":"darwin-arm64","runner":"macos-14"},{"target":"windows-amd64","runner":"windows-latest"}]' <<< "$build_matrix" >/dev/null
jq -e '.include == [{"target":"linux-amd64","kind":"process","runner":"ubuntu-slim"},{"target":"linux-arm64","kind":"process","runner":"ubuntu-24.04-arm"},{"target":"darwin-arm64","kind":"process","runner":"macos-14"},{"target":"windows-amd64","kind":"process","runner":"windows-latest"}]' <<< "$acceptance_matrix" >/dev/null

for target in linux-amd64 linux-arm64 darwin-arm64 windows-amd64; do
  directory="$TEMP/$target"
  mkdir -p "$directory"
  description="$(bash "$TOOL" target "$target")"
  asset="$(sed -n 's/^asset=//p' <<< "$description")"
  printf 'binary for %s\n' "$target" > "$directory/$asset"
  bash "$TOOL" manifest "$target" "$SOURCE_SHA" "$directory"
  bash "$TOOL" verify "$target" "$SOURCE_SHA" "$directory"
done

# A Windows-style path must produce a portable digest after relocation.
escaped_directory="$TEMP/with\backslashes"
mkdir -p "$escaped_directory"
cp "$TEMP/linux-amd64/gh-qw-linux-amd64" "$escaped_directory/"
bash "$TOOL" manifest linux-amd64 "$SOURCE_SHA" "$escaped_directory"
digest="$(sed -n 's/^sha256=//p' "$escaped_directory/gh-qw-linux-amd64.manifest")"
[[ "$digest" =~ ^[0-9a-f]{64}$ ]] || {
  echo "FAIL path-dependent CI artifact digest" >&2
  exit 1
}
cp -R "$escaped_directory" "$TEMP/relocated"
bash "$TOOL" verify linux-amd64 "$SOURCE_SHA" "$TEMP/relocated"

cp -R "$TEMP/linux-amd64" "$TEMP/corrupt"
printf 'corrupt\n' >> "$TEMP/corrupt/gh-qw-linux-amd64"
if bash "$TOOL" verify linux-amd64 "$SOURCE_SHA" "$TEMP/corrupt" >/dev/null 2>&1; then
  echo "FAIL corrupt CI artifact was accepted" >&2
  exit 1
fi

cp -R "$TEMP/linux-amd64" "$TEMP/extra"
touch "$TEMP/extra/unexpected"
if bash "$TOOL" verify linux-amd64 "$SOURCE_SHA" "$TEMP/extra" >/dev/null 2>&1; then
  echo "FAIL unexpected CI artifact inventory was accepted" >&2
  exit 1
fi

if bash "$TOOL" verify linux-amd64 "$OTHER_SHA" "$TEMP/linux-amd64" >/dev/null 2>&1; then
  echo "FAIL artifact with another source identity was accepted" >&2
  exit 1
fi

for invalid_sha in short 0123456789abcdef0123456789abcdef0123456g; do
  if bash "$TOOL" verify linux-amd64 "$invalid_sha" "$TEMP/linux-amd64" >/dev/null 2>&1; then
    echo "FAIL invalid source SHA was accepted: $invalid_sha" >&2
    exit 1
  fi
done

if bash "$TOOL" target darwin-amd64 >/dev/null 2>&1; then
  echo "FAIL unsupported CI target was accepted" >&2
  exit 1
fi

echo "All CI artifact contract tests passed."
