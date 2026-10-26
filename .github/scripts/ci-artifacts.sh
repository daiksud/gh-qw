#!/usr/bin/env bash
set -euo pipefail

fail() {
  printf '%s\n' "$*" >&2
  exit 1
}

require_source_sha() {
  [[ "$1" =~ ^[0-9a-f]{40}$ ]] || fail "invalid source SHA: $1"
}

describe_target() {
  case "$1" in
    linux-amd64)
      TARGET_GOOS=linux
      TARGET_GOARCH=amd64
      TARGET_ASSET=gh-qw-linux-amd64
      TARGET_RUNNER=ubuntu-slim
      TARGET_RUNNER_OS=Linux
      TARGET_RUNNER_ARCH=X64
      ;;
    darwin-arm64)
      TARGET_GOOS=darwin
      TARGET_GOARCH=arm64
      TARGET_ASSET=gh-qw-darwin-arm64
      TARGET_RUNNER=macos-14
      TARGET_RUNNER_OS=macOS
      TARGET_RUNNER_ARCH=ARM64
      ;;
    windows-amd64)
      TARGET_GOOS=windows
      TARGET_GOARCH=amd64
      TARGET_ASSET=gh-qw-windows-amd64.exe
      TARGET_RUNNER=windows-latest
      TARGET_RUNNER_OS=Windows
      TARGET_RUNNER_ARCH=X64
      ;;
    *) fail "unsupported CI target: $1" ;;
  esac
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum < "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 < "$1" | awk '{print $1}'
  else
    fail "no SHA-256 tool is available"
  fi
}

inventory() {
  find "$1" -mindepth 1 -maxdepth 1 -exec basename {} \; | LC_ALL=C sort
}

command="${1:-}"
case "$command" in
  target)
    [ "$#" -eq 2 ] || fail "usage: ci-artifacts.sh target TARGET"
    describe_target "$2"
    printf 'goos=%s\ngoarch=%s\nasset=%s\nrunner=%s\nrunner-os=%s\nrunner-arch=%s\n' \
      "$TARGET_GOOS" "$TARGET_GOARCH" "$TARGET_ASSET" "$TARGET_RUNNER" \
      "$TARGET_RUNNER_OS" "$TARGET_RUNNER_ARCH"
    ;;
  build-matrix)
    [ "$#" -eq 1 ] || fail "usage: ci-artifacts.sh build-matrix"
    printf '%s\n' '{"include":[{"target":"linux-amd64","runner":"ubuntu-slim"},{"target":"darwin-arm64","runner":"macos-14"},{"target":"windows-amd64","runner":"windows-latest"}]}'
    ;;
  acceptance-matrix)
    [ "$#" -eq 1 ] || fail "usage: ci-artifacts.sh acceptance-matrix"
    printf '%s\n' '{"include":[{"target":"linux-amd64","kind":"process","runner":"ubuntu-slim"},{"target":"darwin-arm64","kind":"process","runner":"macos-14"},{"target":"windows-amd64","kind":"process","runner":"windows-latest"}]}'
    ;;
  manifest)
    [ "$#" -eq 4 ] || fail "usage: ci-artifacts.sh manifest TARGET SOURCE_SHA DIRECTORY"
    describe_target "$2"
    require_source_sha "$3"
    asset="$4/$TARGET_ASSET"
    [ -f "$asset" ] && [ ! -L "$asset" ] || fail "missing regular CI asset: $TARGET_ASSET"
    digest="$(sha256_file "$asset")"
    printf 'source-sha=%s\ntarget=%s\ngoos=%s\ngoarch=%s\nasset=%s\nversion=dev\nsha256=%s\n' \
      "$3" "$2" "$TARGET_GOOS" "$TARGET_GOARCH" "$TARGET_ASSET" "$digest" \
      > "$asset.manifest"
    ;;
  verify)
    [ "$#" -eq 4 ] || fail "usage: ci-artifacts.sh verify TARGET SOURCE_SHA DIRECTORY"
    describe_target "$2"
    require_source_sha "$3"
    asset="$4/$TARGET_ASSET"
    manifest="$asset.manifest"
    [ -f "$asset" ] && [ ! -L "$asset" ] || fail "missing regular CI asset: $TARGET_ASSET"
    [ -f "$manifest" ] && [ ! -L "$manifest" ] || fail "missing regular CI manifest: $TARGET_ASSET.manifest"
    expected_inventory="$(printf '%s\n%s' "$TARGET_ASSET" "$TARGET_ASSET.manifest" | LC_ALL=C sort)"
    [ "$(inventory "$4")" = "$expected_inventory" ] || fail "unexpected CI artifact inventory for $2"
    digest="$(sha256_file "$asset")"
    expected="$(printf 'source-sha=%s\ntarget=%s\ngoos=%s\ngoarch=%s\nasset=%s\nversion=dev\nsha256=%s' \
      "$3" "$2" "$TARGET_GOOS" "$TARGET_GOARCH" "$TARGET_ASSET" "$digest")"
    [ "$(cat "$manifest")" = "$expected" ] || fail "CI manifest mismatch for $TARGET_ASSET"
    ;;
  *) fail "unknown CI artifact command: $command" ;;
esac
