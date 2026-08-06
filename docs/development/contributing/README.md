---
type: index
title: "Contributing"
description: "Focused contribution and validation guidance for gh-qw."
resource: gh-qw
tags: [gh-qw, development, contributing]
timestamp: 2026-08-05
---

# Contributing

Keep changes focused and preserve the contracts in the [concept](../../concept/),
[CLI reference](../../reference/cli/), [configuration reference](../../reference/configuration/),
[compatibility reference](../../reference/compatibility/), and
[Architecture Decision Records](../adr/). Record a future durable choice by copying the
[ADR template](../adr/template/) and follow ADR-0001's immutability policy after acceptance.

## Validate changes

Run the relevant Go checks from the repository root:

```console
$ go test ./...
```

For documentation-site changes, use Bun:

```console
$ cd .pages
$ bun ci
$ bun run validate
```

Do not commit generated `.pages/node_modules/`, `.pages/.astro/`, or `.pages/dist/` content.
Use relative documentation links and follow the
[Conventional Commits decision](../adr/0008-conventional-commits-and-release-notes/).
