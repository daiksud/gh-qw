#!/usr/bin/env bash
set -euo pipefail

fail() {
  printf '%s\n' "$*" >&2
  exit 1
}

require_source_sha() {
  [[ "$1" =~ ^[0-9a-f]{40}$ ]] || fail "invalid source SHA: $1"
}

require_version() {
  [[ "$1" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] ||
    fail "invalid release version: $1"
}

describe_target() {
  case "$1" in
    darwin-amd64)
      TARGET_GOOS=darwin
      TARGET_GOARCH=amd64
      TARGET_ASSET=gh-qw-darwin-amd64
      TARGET_RUNNER_OS=macOS
      TARGET_RUNNER_ARCH=X64
      ;;
    darwin-arm64)
      TARGET_GOOS=darwin
      TARGET_GOARCH=arm64
      TARGET_ASSET=gh-qw-darwin-arm64
      TARGET_RUNNER_OS=macOS
      TARGET_RUNNER_ARCH=ARM64
      ;;
    linux-amd64)
      TARGET_GOOS=linux
      TARGET_GOARCH=amd64
      TARGET_ASSET=gh-qw-linux-amd64
      TARGET_RUNNER_OS=Linux
      TARGET_RUNNER_ARCH=X64
      ;;
    linux-arm64)
      TARGET_GOOS=linux
      TARGET_GOARCH=arm64
      TARGET_ASSET=gh-qw-linux-arm64
      TARGET_RUNNER_OS=Linux
      TARGET_RUNNER_ARCH=ARM64
      ;;
    windows-amd64)
      TARGET_GOOS=windows
      TARGET_GOARCH=amd64
      TARGET_ASSET=gh-qw-windows-amd64.exe
      TARGET_RUNNER_OS=Windows
      TARGET_RUNNER_ARCH=X64
      ;;
    *) fail "unsupported release target: $1" ;;
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

verify_one() {
  local target="$1" version="$2" source_sha="$3" directory="$4"
  local asset manifest digest expected actual
  describe_target "$target"
  asset="$directory/$TARGET_ASSET"
  manifest="$directory/$TARGET_ASSET.manifest"
  [ -f "$asset" ] && [ ! -L "$asset" ] || fail "missing regular release asset: $TARGET_ASSET"
  [ -f "$manifest" ] && [ ! -L "$manifest" ] || fail "missing regular release manifest: $TARGET_ASSET.manifest"
  digest="$(sha256_file "$asset")"
  expected="$(printf 'source-sha=%s\nversion=%s\nasset=%s\nsha256=%s' \
    "$source_sha" "$version" "$TARGET_ASSET" "$digest")"
  actual="$(cat "$manifest")"
  [ "$actual" = "$expected" ] || fail "release manifest mismatch for $TARGET_ASSET"
}

verify_single_inventory() {
  local target="$1" directory="$2" expected actual
  describe_target "$target"
  expected="$(printf '%s\n%s' "$TARGET_ASSET" "$TARGET_ASSET.manifest" | LC_ALL=C sort)"
  actual="$(inventory "$directory")"
  [ "$actual" = "$expected" ] || fail "unexpected artifact inventory for $target"
}

verify_all_inventory() {
  local directory="$1" expected actual target
  expected="$(
    for target in darwin-amd64 darwin-arm64 linux-amd64 linux-arm64 windows-amd64; do
      describe_target "$target"
      printf '%s\n%s\n' "$TARGET_ASSET" "$TARGET_ASSET.manifest"
    done | LC_ALL=C sort
  )"
  actual="$(inventory "$directory")"
  [ "$actual" = "$expected" ] || fail "unexpected complete release artifact inventory"
}

verify_published_inventory() {
  local directory="$1" expected actual target
  expected="$(
    for target in darwin-amd64 darwin-arm64 linux-amd64 linux-arm64 windows-amd64; do
      describe_target "$target"
      printf '%s\n' "$TARGET_ASSET"
    done | LC_ALL=C sort
  )"
  actual="$(inventory "$directory")"
  [ "$actual" = "$expected" ] || fail "unexpected published release asset inventory"
}

command="${1:-}"
case "$command" in
  target)
    [ "$#" -eq 2 ] || fail "usage: release-artifacts.sh target TARGET"
    describe_target "$2"
    printf 'goos=%s\ngoarch=%s\nasset=%s\nrunner-os=%s\nrunner-arch=%s\n' \
      "$TARGET_GOOS" "$TARGET_GOARCH" "$TARGET_ASSET" \
      "$TARGET_RUNNER_OS" "$TARGET_RUNNER_ARCH"
    ;;
  build-matrix)
    [ "$#" -eq 1 ] || fail "usage: release-artifacts.sh build-matrix"
    printf '%s\n' '{"include":[{"target":"darwin-amd64","runner":"ubuntu-slim"},{"target":"darwin-arm64","runner":"ubuntu-slim"},{"target":"linux-amd64","runner":"ubuntu-slim"},{"target":"linux-arm64","runner":"ubuntu-slim"},{"target":"windows-amd64","runner":"ubuntu-slim"}]}'
    ;;
  acceptance-matrix)
    [ "$#" -eq 1 ] || fail "usage: release-artifacts.sh acceptance-matrix"
    printf '%s\n' '{"include":[{"target":"linux-amd64","runner":"ubuntu-slim"},{"target":"linux-arm64","runner":"ubuntu-24.04-arm"},{"target":"darwin-arm64","runner":"macos-14"},{"target":"windows-amd64","runner":"windows-latest"}]}'
    ;;
  manifest)
    [ "$#" -eq 5 ] || fail "usage: release-artifacts.sh manifest TARGET VERSION SOURCE_SHA DIRECTORY"
    require_version "$3"
    require_source_sha "$4"
    describe_target "$2"
    asset="$5/$TARGET_ASSET"
    [ -f "$asset" ] && [ ! -L "$asset" ] || fail "missing regular release asset: $TARGET_ASSET"
    digest="$(sha256_file "$asset")"
    printf 'source-sha=%s\nversion=%s\nasset=%s\nsha256=%s\n' \
      "$4" "$3" "$TARGET_ASSET" "$digest" > "$asset.manifest"
    ;;
  verify)
    [ "$#" -eq 5 ] || fail "usage: release-artifacts.sh verify TARGET VERSION SOURCE_SHA DIRECTORY"
    require_version "$3"
    require_source_sha "$4"
    verify_single_inventory "$2" "$5"
    verify_one "$2" "$3" "$4" "$5"
    ;;
  verify-all)
    [ "$#" -eq 4 ] || fail "usage: release-artifacts.sh verify-all VERSION SOURCE_SHA DIRECTORY"
    require_version "$2"
    require_source_sha "$3"
    verify_all_inventory "$4"
    for target in darwin-amd64 darwin-arm64 linux-amd64 linux-arm64 windows-amd64; do
      verify_one "$target" "$2" "$3" "$4"
    done
    ;;
  prepare-executable)
    [ "$#" -eq 6 ] || fail "usage: release-artifacts.sh prepare-executable TARGET VERSION SOURCE_SHA ARTIFACT_DIR EXECUTION_DIR"
    require_version "$3"
    require_source_sha "$4"
    verify_single_inventory "$2" "$5"
    verify_one "$2" "$3" "$4" "$5"
    execution_directory="$6"
    [ -d "$execution_directory" ] && [ ! -L "$execution_directory" ] || fail "missing execution directory"
    [ -z "$(inventory "$execution_directory")" ] || fail "execution directory must be empty"
    # The identity sidecar is not a Windows XML side-by-side EXE manifest.
    # Execute identical verified bytes without placing that sidecar beside them.
    cp -- "$5/$TARGET_ASSET" "$execution_directory/$TARGET_ASSET"
    cmp -- "$5/$TARGET_ASSET" "$execution_directory/$TARGET_ASSET"
    chmod +x "$execution_directory/$TARGET_ASSET"
    printf '%s\n' "$execution_directory/$TARGET_ASSET"
    ;;
  verify-published)
    [ "$#" -eq 5 ] || fail "usage: release-artifacts.sh verify-published VERSION SOURCE_SHA ACCEPTED_DIR PUBLISHED_DIR"
    require_version "$2"
    require_source_sha "$3"
    verify_all_inventory "$4"
    verify_published_inventory "$5"
    for target in darwin-amd64 darwin-arm64 linux-amd64 linux-arm64 windows-amd64; do
      verify_one "$target" "$2" "$3" "$4"
      describe_target "$target"
      [ "$(sha256_file "$4/$TARGET_ASSET")" = "$(sha256_file "$5/$TARGET_ASSET")" ] ||
        fail "published asset differs from accepted bytes: $TARGET_ASSET"
    done
    ;;
  *) fail "unknown release artifact command: $command" ;;
esac
