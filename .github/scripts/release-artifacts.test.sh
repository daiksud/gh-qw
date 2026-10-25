#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
TOOL="$SCRIPT_DIR/release-artifacts.sh"
TEMP="$(mktemp -d)"
trap 'rm -rf "$TEMP"' EXIT
SOURCE_SHA=0123456789abcdef0123456789abcdef01234567
VERSION=v0.6.0

build_matrix="$(bash "$TOOL" build-matrix)"
acceptance_matrix="$(bash "$TOOL" acceptance-matrix)"
jq -e '.include | length == 5 and map(.target) == ["darwin-amd64","darwin-arm64","linux-amd64","linux-arm64","windows-amd64"] and all(.runner == "ubuntu-slim")' <<< "$build_matrix" >/dev/null
jq -e '.include == [{"target":"linux-amd64","runner":"ubuntu-slim"},{"target":"linux-arm64","runner":"ubuntu-24.04-arm"},{"target":"darwin-arm64","runner":"macos-14"},{"target":"windows-amd64","runner":"windows-latest"}]' <<< "$acceptance_matrix" >/dev/null

mkdir -p "$TEMP/all" "$TEMP/published"
for target in darwin-amd64 darwin-arm64 linux-amd64 linux-arm64 windows-amd64; do
  description="$(bash "$TOOL" target "$target")"
  asset="$(sed -n 's/^asset=//p' <<< "$description")"
  printf 'binary for %s\n' "$target" > "$TEMP/all/$asset"
  cp "$TEMP/all/$asset" "$TEMP/published/$asset"
  bash "$TOOL" manifest "$target" "$VERSION" "$SOURCE_SHA" "$TEMP/all"
done

bash "$TOOL" verify-all "$VERSION" "$SOURCE_SHA" "$TEMP/all"
bash "$TOOL" verify-published "$VERSION" "$SOURCE_SHA" "$TEMP/all" "$TEMP/published"

# Relocating across Windows-style paths must not change digest text.
escaped_directory="$TEMP/with\\backslashes"
mkdir -p "$escaped_directory"
cp "$TEMP/all/gh-qw-windows-amd64.exe" "$TEMP/all/gh-qw-windows-amd64.exe.manifest" "$escaped_directory/"
bash "$TOOL" verify windows-amd64 "$VERSION" "$SOURCE_SHA" "$escaped_directory"
mkdir -p "$TEMP/execution"
binary="$(bash "$TOOL" prepare-executable windows-amd64 "$VERSION" "$SOURCE_SHA" "$escaped_directory" "$TEMP/execution")"
cmp "$TEMP/all/gh-qw-windows-amd64.exe" "$binary"
[[ ! -e "$binary.manifest" ]]

cp -R "$TEMP/all" "$TEMP/corrupt"
printf 'corrupt\n' >> "$TEMP/corrupt/gh-qw-linux-amd64"
if bash "$TOOL" verify-all "$VERSION" "$SOURCE_SHA" "$TEMP/corrupt" >/dev/null 2>&1; then
  echo "FAIL corrupt asset was accepted" >&2
  exit 1
fi

mkdir -p "$TEMP/corrupt-execution"
mkdir -p "$TEMP/corrupt-single"
cp "$TEMP/corrupt/gh-qw-linux-amd64" "$TEMP/corrupt/gh-qw-linux-amd64.manifest" "$TEMP/corrupt-single/"
if bash "$TOOL" prepare-executable linux-amd64 "$VERSION" "$SOURCE_SHA" "$TEMP/corrupt-single" "$TEMP/corrupt-execution" >/dev/null 2>&1; then
  echo "FAIL corrupt executable was prepared" >&2
  exit 1
fi

# Native CI supplies its built candidate so Windows exercises real PE execution.
if [[ "$#" -eq 2 ]]; then
  target="$1"
  candidate="$2"
  asset="$(bash "$TOOL" target "$target" | sed -n 's/^asset=//p')"
  mkdir -p "$TEMP/native" "$TEMP/native-run"
  cp "$candidate" "$TEMP/native/$asset"
  bash "$TOOL" manifest "$target" "$VERSION" "$SOURCE_SHA" "$TEMP/native"
  binary="$(bash "$TOOL" prepare-executable "$target" "$VERSION" "$SOURCE_SHA" "$TEMP/native" "$TEMP/native-run")"
  [[ "$("$binary" --version)" == 'gh qw version dev' ]]
fi

cp -R "$TEMP/all" "$TEMP/extra"
touch "$TEMP/extra/unexpected"
if bash "$TOOL" verify-all "$VERSION" "$SOURCE_SHA" "$TEMP/extra" >/dev/null 2>&1; then
  echo "FAIL unexpected inventory was accepted" >&2
  exit 1
fi

if bash "$TOOL" target unknown >/dev/null 2>&1; then
  echo "FAIL unknown target was accepted" >&2
  exit 1
fi

echo "All release artifact contract tests passed."
