---
type: adr
title: "ADR-0016: Use ubuntu-slim for lightweight GitHub Actions jobs"
description: "Use GitHub's single-CPU ubuntu-slim runner for gh-qw's lightweight x64 Linux workflow jobs while preserving dedicated architecture and operating-system coverage."
resource: gh-qw
tags: [gh-qw, adr, adr-0016, github-actions, ci, runners]
timestamp: 2026-08-08
---

# ADR-0016: Use ubuntu-slim for lightweight GitHub Actions jobs

- **Status:** Accepted
- **Date:** 2026-08-08
- **Deciders:** gh-qw maintainers
- **Related:** [ADR-0012](../0012-github-settings-as-code/) and [ADR-0013](../0013-node-pnpm-viteplus-toolchain/)

## Context

The Linux jobs in `gh-qw` use `ubuntu-latest`, a four-CPU runner with substantially more
resources than the workflows need. Recent successful runs completed each Linux job in less than
one minute, including Go builds, the race detector, documentation validation, release builds, and
Pages artifact creation. The repository is public, so changing runner size is not a cost-saving
requirement; it is a better fit between the workload and the runner.

GitHub's `ubuntu-slim` is a single-CPU, 5 GB RAM, 14 GB SSD, x64 Linux container with a 15-minute
job timeout. It runs without privileges, so operations such as Docker-in-Docker, filesystem
mounts, and low-level kernel access are unavailable. The image includes the command-line tools
used by these workflows, including Git, GitHub CLI, Node.js, GCC, `jq`, `tar`, and `zstd`.
`actions/setup-go` installs the Go version declared by `go.mod`, so the workflows do not depend on
Go being preinstalled in the image.

The test matrix also verifies arm64, macOS, and Windows. `ubuntu-slim` has no arm64 variant, and
the cross-platform jobs require their respective operating-system runners. Those jobs cannot be
consolidated onto the slim image without losing coverage.

## Decision

All lightweight x64 Linux jobs use `ubuntu-slim`: CI formatting and vetting, the amd64 build and
test matrix entry, race detection, documentation validation, settings validation, pull-request
labeling, Pages build and deployment, and release version validation, cross-compilation, and
release creation. The arm64 test entry remains on `ubuntu-24.04-arm`; macOS and Windows entries
remain on their existing runners.

Release artifacts are cross-compiled with `CGO_ENABLED=0`, so an x64 slim host can produce the
darwin, linux, and windows targets, including the linux arm64 artifact. No privileged operation or
host-native build is required.

Each slim job declares a timeout below the platform's 15-minute limit. API- and action-oriented
jobs use five minutes, except Pages deployment, which uses ten minutes so the job does not
terminate before the deploy-pages action's own deployment timeout. Jobs that install toolchains
or build and test also use ten minutes. The `test` matrix declares its timeout at job scope, so
the same ten-minute limit applies to its Linux, arm64, macOS, and Windows entries.

The workflow files retain SHA-pinned actions and document the slim-image rationale inline. If a
job's resource needs grow beyond these limits, it must be moved to a suitable full runner rather
than weakening the timeout without a new decision.

## Consequences

### Positive

- Runner resources better match the short, non-privileged workloads in the repository.
- Explicit five- and ten-minute timeouts fail runaway jobs before the platform's hard limit.
- Existing arm64, macOS, and Windows coverage remains intact, while the release workflow still
  produces every target artifact.
- The decision and the constraints of `ubuntu-slim` are reviewable alongside the workflow changes.

### Negative

- A single CPU can increase wall-clock time for Go tests, race detection, and documentation
  builds; the current measurements provide margin but must be revisited as the repository grows.
- The race detector is the tightest resource fit because instrumentation increases memory use
  within the slim container's 5 GB limit.
- Release workflows cannot be exercised end to end before a tag push, so the first release after
  this change must confirm that the ten-minute budget remains sufficient.
- `ubuntu-slim` remains unsuitable for arm64-native jobs and privileged operations.

### Neutral

- The public repository receives no change in runner billing from this decision.
- The Go toolchain remains managed by `actions/setup-go`, and the documentation toolchain remains
  managed by the existing Vite+ setup.
- This decision changes runner selection and timeouts only; action versions, permissions, and
  workflow behavior remain otherwise unchanged.
