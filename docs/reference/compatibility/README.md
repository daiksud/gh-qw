---
type: reference
title: "Compatibility reference"
description: "Current ghq compatibility and gh/Git interoperability contracts with source and test evidence."
resource: gh-qw
tags: [gh-qw, reference, compatibility, ghq, github-cli, git]
timestamp: 2026-08-06
---

# Compatibility reference

This page records the current compatibility and interoperability contracts for `gh-qw`. The
[CLI reference](../cli/), [configuration reference](../configuration/), and
[versioning reference](../versioning/) are normative; the tables below connect those contracts to
implementation and regression evidence.

## ghq compatibility

The ghq comparison uses `x-motemen/ghq` `v1.10.1` as a fixed upstream reference. `gh-qw` adopts
the repository layout and selected command conventions, but remains Git-only and uses `gh` for
GitHub network operations.

| Surface | Current contract | Relationship to ghq | Source evidence | Test evidence |
| --- | --- | --- | --- | --- |
| Repository layout and identity | Main repositories use `<root>/<host>/<owner>/<repo>`; paths are the discovery source of truth. | Compatible | `internal/local/path.go`; ADR-0004 | `internal/local/path_selector_test.go` |
| Command names | `get` (`clone` alias), `list`, `root`, and `rm` have the same core purpose. `migrate` and `worktree` provide gh-qw-specific lifecycle operations. | Compatible core with extensions | `internal/cmd/app.go`; ADR-0006 | `internal/cmd/get_test.go`; command-specific tests |
| `get` flags and batching | `-u`, `-p`, `--shallow`, `--branch`, `--no-recursive`, `--silent`, `--parallel`, and `--partial` follow ghq's flag vocabulary. Positional input ignores stdin; stdin batches are line-based; parallelism is capped at six. | Compatible surface | `internal/cmd/get.go` | `internal/cmd/get_test.go` |
| Branch suffix | A repository `@<branch>` suffix overrides `--branch`. Parsing excludes an SSH authority's `@` and permits later `@` characters inside the branch. | Compatible precedence, stricter delimiter | `internal/repospec/parser.go` | `internal/repospec/parser_test.go`; `internal/cmd/get_test.go` |
| Repository specifications | Bare names, owner/repository, explicit hosts, HTTP(S), SSH, SCP-like, and configured-root-relative forms are supported. Canonical identities require exactly host/owner/repository and reject traversal or extra path components. | Compatible forms, stricter validation | `internal/repospec/parser.go` | `internal/repospec/parser_test.go` |
| Bare-name completion | Bare `<repo>` obtains host and owner from authenticated `gh` state. It never reads `ghq.user`, `ghq.completeUser`, or an ambient OS username. | gh-qw authentication model | `internal/ghapi/client.go` | `internal/ghapi/client_test.go` |
| `file://` input | Local and non-local file URLs can be canonicalized for local discovery and migration. `get` rejects them because `gh repo clone` and `gh repo sync` require a GitHub repository. | Command-specific adaptation | `internal/repospec/parser.go`; `internal/cmd/get.go` | `internal/repospec/parser_test.go`; `internal/cmd/get_test.go` |
| Multiple roots | Existing repositories are searched in configured order; the first root is primary for new clones and migration destinations. Read-only discovery deduplicates identities with the earliest root winning. Mutating ambiguity is rejected. | Compatible intent with explicit ordering and safety | `internal/root/resolver.go`; `internal/local/selector.go` | `internal/cmd/root_test.go`; `internal/local/path_selector_test.go` |
| `list` | Substring matching, smartcase, host-prefix matching, exact matching, shortest unique suffixes, and ascending output are supported. Linked worktrees add `@<slot>`. `--fzf` selects one entry through the external `fzf` executable and prints only its absolute path; `gh-qw` itself never launches a shell. | Compatible matching with worktree extension | `internal/cmd/list.go`; `internal/fzf/runner.go`; ADR-0009 | `internal/cmd/list_test.go`; `internal/fzf/runner_test.go`; `tests/cli_test.go` |
| `root` | `root` prints the primary root; `root --all` prints every root in order. Paths use `/` separators on every platform. | Compatible core with normalized output | `internal/cmd/root.go` | `internal/cmd/root_test.go` |
| Removal confirmation | Destructive confirmation comes from the controlling terminal, never piped repository input. Repository ambiguity and Git safety refusals fail without broadening the target. | Safety adaptation | `internal/cmd/rm.go` | `internal/cmd/rm_test.go` |
| Gone-upstream worktree removal | `worktree remove --gone` uses fully qualified structured upstream refs and the local ref database, emits one revalidated bulk plan, and preserves all local branches and commits. It never performs an implicit fetch. | gh-qw extension | `internal/gitcmd/repository.go`; `internal/cmd/worktree_remove_gone.go`; ADR-0019 | `internal/gitcmd/repository_test.go`; `internal/cmd/worktree_remove_test.go`; `tests/cli_test.go` |
| Herdr workspace integration | `worktree add`, single and `--gone` bulk `worktree remove`, and `rm` (for an `@<branch>` target only) accept `--herdr`/`--no-herdr`, integrating with the external `herdr` executable the same way `list --fzf` integrates with `fzf`: `gh-qw` never launches a shell and only reports the outcome. | Extension modeled on the fzf integration | `internal/herdr/runner.go`; `internal/cmd/herdr.go`; ADR-0009; ADR-0018; ADR-0019 | `internal/herdr/runner_test.go`; `internal/cmd/herdr_test.go`; `internal/cmd/worktree_add_test.go`; `internal/cmd/worktree_remove_test.go`; `internal/cmd/rm_test.go`; `tests/cli_test.go` |
| Exit and output | Statuses are `0` success, `1` runtime/safety failure, and `2` usage/configuration failure. Successful result data alone is written to stdout; diagnostics use stderr. | Stronger gh-qw contract | `internal/cmd/app.go`; command implementations | `internal/cmd/*_test.go`; `tests/cli_test.go` |
| ghq configuration | Normal commands ignore `GHQ_ROOT` and every `ghq.*` Git setting. | Deliberate isolation | `internal/root`; `internal/repospec` | package tests and repository-wide command tests |
| `migrate` source discovery | With no directory, `GHQ_ROOT` wins; otherwise effective `ghq.root` values, the `~/ghq` default, and URL-specific roots locate source repositories. Values are source input only and are not persisted. | Explicit ghq migration support | `internal/migrate/legacy.go`; `internal/cmd/migrate.go` | `internal/migrate/legacy_test.go`; `internal/cmd/migrate_test.go` |
| Scope | `create`, shell-launch behavior, bare repositories, and non-Git VCS backends are not part of the command surface. | Deliberate product boundary | ADR-0006; `internal/cmd/app.go` | command help and flag tests |

## GitHub CLI interoperability

| Surface | Current contract | Source evidence | Test evidence |
| --- | --- | --- | --- |
| Network delegation | New clones use `gh repo clone --no-upstream`; updates and default-branch synchronization use `gh repo sync`. Network-capable operations require `github.com` or a `gh`-authenticated GitHub Enterprise host. | `internal/ghcmd/repository.go`; ADR-0002 | `internal/ghcmd/repository_test.go`; `internal/cmd/get_test.go`; `internal/cmd/worktree_add_test.go` |
| Executable and process model | `gh` is found through `GH_PATH` or `PATH`, runs directly without a shell, and receives no caller stdin. | `internal/ghcmd/executable.go`; `internal/ghcmd/runner.go`; ADR-0003 | `internal/ghcmd/executable_test.go`; `internal/ghcmd/runner_test.go`; `tests/cli_test.go` |
| Account resolution | Resolution order is explicit `GH_TOKEN`/`GITHUB_TOKEN`, valid cache, exactly one owner-matching login, sole authenticated account, then interactive selection. A stale cached login is removed before fresh selection. Ambiguity, cache I/O/schema/update failure, account-listing failure, selected-token failure, and prompt failure stop before network access. No unresolved choice is delegated to an ambient active account. | `internal/ghauth/resolver.go`; `internal/ghauth/cache.go`; ADR-0002 | `internal/ghauth/resolver_test.go`; `internal/ghauth/cache_test.go`; command account-resolution tests |
| Lazy resolution | `get` resolves an account only for a clone or requested update. `worktree add` resolves one only when its API/default-branch path needs network access. | `internal/cmd/get.go`; `internal/cmd/worktree_add.go` | `internal/cmd/get_test.go`; `internal/cmd/worktree_add_test.go` |
| Token handling | A selected token is injected as subprocess `GH_TOKEN` after inherited token variables are removed. Tokens are never placed in argv, URLs, logs, or the account cache. | `internal/ghcmd/runner.go`; `internal/ghauth/cache.go` | `internal/ghcmd/runner_test.go`; `internal/ghauth/cache_test.go` |
| API lookup | Default-branch lookup uses the repository host and the resolved credential; wrapped errors redact secrets. | `internal/ghapi/client.go` | `internal/ghapi/client_test.go` |
| Output descriptors | When safe, `gh` inherits the diagnostic file descriptor so terminal progress and color work. Other writers use relay logic with bounded error capture; silent and parallel `get` disable direct progress output. | `internal/procio/procio.go`; `internal/ghcmd/runner.go`; ADR-0003 | `internal/procio/procio_test.go`; `internal/ghcmd/runner_test.go`; `internal/cmd/get_test.go` |
| Stdout purity | `gh` stdout is routed to gh-qw's diagnostic destination, never gh-qw's result stdout. | `internal/cmd/get.go` | `internal/cmd/get_test.go` |

## Git interoperability

| Surface | Current contract | Source evidence | Test evidence |
| --- | --- | --- | --- |
| Local Git boundary | Branch/upstream inspection, exact ref and revision checks, worktree creation, enumeration, removal, pruning, and repair use Git directly; network cloning and synchronization use `gh`. Bulk gone-upstream removal does not fetch. | `internal/gitcmd`; ADR-0002; ADR-0019 | `internal/gitcmd/*_test.go`; command worktree tests |
| Process I/O | Git runs directly without a shell, receives no caller stdin, and uses the same descriptor passthrough or relay strategy as `gh`. | `internal/gitcmd/runner.go`; `internal/procio/procio.go`; ADR-0003 | `internal/gitcmd/runner_test.go`; `tests/cli_test.go` |
| Worktree add | gh-qw validates mode combinations, derives the deterministic destination, and maps the request to `git worktree add`. Automatic branch resolution is local branch, preferred or sole remote-tracking branch, then the network-backed default branch. | `internal/gitcmd/worktree.go`; `internal/cmd/worktree_add.go` | `internal/gitcmd/worktree_test.go`; `internal/cmd/worktree_add_test.go` |
| Worktree listing | Git's NUL-delimited porcelain is parsed internally. `gh qw worktree list --porcelain` emits gh-qw's stable identity/path/head/kind format rather than passing Git's format through. | `internal/gitcmd/worktree.go`; `internal/cmd/worktree_list.go` | `internal/gitcmd/worktree_test.go`; `internal/cmd/worktree_list_test.go` |
| Object format | Worktree HEAD object IDs are relayed at Git's actual width. SHA-1 and SHA-256 repositories are supported without gh-qw computing or constraining the width. | `internal/gitcmd/worktree.go` | `internal/cmd/worktree_list_test.go`; real SHA-256 coverage in `internal/gitcmd/worktree_test.go` |
| Environment | Git and `gh` inherit the process environment except for deliberate credential overrides, so standard Git transport, credential-helper, proxy, and configuration variables continue to apply. | `internal/gitcmd/runner.go`; `internal/ghcmd/runner.go` | runner tests and command integration tests |
