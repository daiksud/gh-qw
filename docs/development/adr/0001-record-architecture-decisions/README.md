---
type: adr
title: "ADR-0001: Record architecture decisions"
description: "Record durable architecture choices as lightweight ADRs."
resource: gh-qw
tags: [gh-qw, adr, adr-0001, architecture]
timestamp: 2026-08-05
---

# ADR-0001: Record architecture decisions

- **Status:** Accepted
- **Date:** 2026-08-05
- **Deciders:** gh-qw maintainers
- **Related:** [ADR index](../), [ADR template](../template/)

## Context

`gh-qw` is a clean implementation with foundational choices about repository identity, filesystem
layout, command scope, configuration, and distribution. Source code and user reference
documentation can show current behavior, but they do not reliably preserve why one design was
chosen over another.

The project needs a decision history that is easy to review with the code without introducing a
heavyweight architecture process.

## Decision

We will record durable architecture choices as lightweight ADRs in
`docs/development/adr/NNNN-short-title/README.md`. Each ADR will focus on one decision and include
its context, decision, consequences, status, date, and links to related records.

An accepted ADR is immutable except for corrections that do not alter its meaning. A changed
decision requires a new ADR; the earlier record is marked as superseded and links to its
replacement.

## Consequences

### Positive

- Architectural rationale remains versioned, reviewable, and close to the implementation.
- Focused records can be linked and superseded independently.

### Negative

- Maintainers must keep the index and superseding links current.
- Writing an ADR adds a small cost when making a durable decision.

### Neutral

- ADRs explain architectural intent; they do not replace implementation or reference
  documentation.
