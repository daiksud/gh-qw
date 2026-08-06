---
type: adr
title: "ADR-0003: Use Go, Cobra, prebuilt binaries, and controlled subprocess I/O"
description: "Implement gh-qw in Go with Cobra, publish prebuilt binaries, and run gh/Git without a shell or inherited stdin."
resource: gh-qw
tags: [gh-qw, adr, adr-0003, go, cobra, release, subprocess]
timestamp: 2026-08-05
---

# ADR-0003: Use Go, Cobra, prebuilt binaries, and controlled subprocess I/O

- **Status:** Accepted
- **Date:** 2026-08-05
- **Deciders:** gh-qw maintainers
- **Related:** [ADR-0002](../0002-distribute-as-gh-cli-extension/), [ADR-0008](../0008-conventional-commits-and-release-notes/)

## Context

`gh-qw` is a cross-platform command-line tool that coordinates filesystem operations, `gh` and
Git subprocesses, configuration, and GitHub APIs. It needs fast startup, predictable deployment,
clear command organization, non-blocking batch behavior, and strict separation between result
output and subprocess diagnostics.

Go supports straightforward cross-compilation and self-contained executables. Cobra provides a
mature command hierarchy, flag handling, validation hooks, and generated help suitable for the
planned subcommands.

## Decision

We will implement `gh-qw` in Go and define its CLI with Cobra. Releases will publish prebuilt
binaries for the supported operating-system and architecture combinations using filenames
compatible with GitHub CLI extension installation.

The implementation will favor portable Go dependencies so that cross-platform release builds
remain reproducible.

`gh` and Git subprocesses run directly with `exec.CommandContext`, never through a shell. Their
stdin is the null device, so they cannot consume repository specifications or block on an open
caller stream. Commands that require confirmation open the controlling terminal themselves.

When a diagnostic writer exposes a real file descriptor and only one subprocess can write to it,
the child inherits that descriptor so terminal progress and color work normally. Other writers
use a pipe relay with bounded diagnostic capture. Silent and parallel `get` runs do not pass
progress descriptors through. Subprocess stdout that contains human output is routed to gh-qw's
diagnostic destination; gh-qw's stdout remains reserved for documented result data.

## Consequences

### Positive

- Users install a single executable without a separate runtime.
- Go's tooling and cross-compilation support simplify testing and release packaging.
- Cobra gives all commands consistent parsing, errors, and help output.
- Subprocesses cannot interpret shell syntax or consume gh-qw's input stream.
- Interactive progress is preserved without contaminating machine-readable stdout.

### Negative

- The release process must build and validate a platform matrix.
- Cobra's command model and Go's compatibility constraints shape the implementation.
- Direct descriptor use cannot retain a stderr tail, so callers that need captured diagnostics
  must use the relay path.

### Neutral

- Prebuilt assets are the installation artifact even though contributors can build from source.
- Confirmation remains a gh-qw responsibility rather than subprocess stdin behavior.
