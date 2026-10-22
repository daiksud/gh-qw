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
- **Related:** [ADR-0012](../0012-github-settings-as-code/)

## Context

The Linux jobs in `gh-qw` use `ubuntu-latest`, a four-CPU runner with substantially more
resources than the workflows need. Recent successful runs completed each Linux job in less than
one minute, including Go builds, the race detector, and release builds. The repository is public, so changing runner size is not a cost-saving
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
test matrix entry, race detection, settings validation, pull-request labeling, and release
version validation, cross-compilation, and release creation. The arm64 test entry remains on `ubuntu-24.04-arm`; macOS and Windows entries
remain on their existing runners.

Release artifacts are cross-compiled with `CGO_ENABLED=0`, so an x64 slim host can produce the
darwin, linux, and windows targets, including the linux arm64 artifact. No privileged operation or
host-native build is required.

Each slim job declares a timeout below the platform's 15-minute limit. API- and
action-oriented jobs use five minutes; jobs that install toolchains or build
and test use ten minutes. The `test` matrix applies that ten-minute limit on
all supported operating systems and architectures.

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

- A single CPU can increase wall-clock time for Go tests and race detection; the current measurements provide margin but must be revisited as the repository grows.
- The race detector is the tightest resource fit because instrumentation increases memory use
  within the slim container's 5 GB limit.
- A manually dispatched release now validates and builds before creating a tag, and the job
  budgets must remain sufficient as the project grows.
- `ubuntu-slim` remains unsuitable for arm64-native jobs and privileged operations.

### Neutral

- The public repository receives no change in runner billing from this decision.
- The Go toolchain remains managed by `actions/setup-go`. The retired documentation site has no CI toolchain.
- This decision changes runner selection and timeouts only; action versions, permissions, and
  workflow behavior remain otherwise unchanged.
