#!/usr/bin/env bash
set -euo pipefail
# Immutable gh-settings v0.23.0, source cbc112ae1330b4ad4debe013857bd67d2e1a1e1e.
# A nested composite can have an empty github.action_ref. Select and verify
# the published CLI directly instead of falling back to a candidate version.
unset GH_TOKEN GITHUB_TOKEN
version=v0.23.0
digest=93d742d6634801a18e2adaf85656abdad4eed8a6ecb887f2b10778f236ac2dd9
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 ]] || {
  echo 'settings validation requires Linux x86_64' >&2
  exit 1
}
directory=$(mktemp -d)
trap 'rm -rf "$directory"' EXIT
binary=$directory/gh-settings
curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
  "https://github.com/daiksud/gh-settings/releases/download/$version/gh-settings-linux-amd64" --output "$binary"
printf '%s  %s\n' "$digest" "$binary" | sha256sum --check --status
chmod 700 "$binary"
[[ "$("$binary" --version)" == "gh-settings $version" ]] || {
  echo 'settings CLI version mismatch' >&2
  exit 1
}
"$binary" validate
