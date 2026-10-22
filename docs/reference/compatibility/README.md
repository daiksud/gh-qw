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

## Required external tool capabilities

For the complete supported command set, the minimum upstream capability baselines are
**Git 2.42.0** and **GitHub CLI 2.88.0**. Git's
[2.42.0 worktree manual](https://git-scm.com/docs/git-worktree/2.42.0) includes
`worktree add --orphan`, NUL-delimited worktree listing, and registration repair.
GitHub CLI [2.88.0](https://github.com/cli/cli/releases/tag/v2.88.0) introduced the
`repo clone --no-upstream` flag used by gh-qw. Use maintained releases at or above these
baselines with the capabilities below. These baselines come from upstream command availability;
they are not a claim that CI runs every historical patch release.

`gh-qw` does not add a version preflight or substitute an internal implementation when a
capability is unavailable. Unsupported commands fail through the native tool at the requested
operation. This documents existing delegated commands, not new authentication or host restrictions.

| Tool | Required capability | Used by |
| --- | --- | --- |
| GitHub CLI | [`gh api`](https://cli.github.com/manual/gh_api) with `--hostname` and its built-in `--jq` response selection | Authenticated-login lookup and the REST repository `default_branch` field, including empty repositories |
| GitHub CLI | [`gh repo clone`](https://cli.github.com/manual/gh_repo_clone) with `--no-upstream` and Git argument forwarding; [`gh repo sync`](https://cli.github.com/manual/gh_repo_sync) with `--source` and `--branch` | Cloning and synchronization |
| Git | [`git worktree`](https://git-scm.com/docs/git-worktree) add, remove, prune, and NUL-delimited `list --porcelain -z`; `add --orphan` for orphan creation; native remote guessing and `checkout.defaultRemote` | Local worktree lifecycle |
| Git | `git branch [--force] -- <branch> [<start-point>]` with standard ref resolution and tracking | Creating/resetting a requested branch before adding its worktree |
| Git | `git worktree repair <paths...>` that repairs a moved main worktree and moved linked worktrees together | Migration with registered worktrees |

`gh` is resolved lazily through [`GH_PATH`](https://cli.github.com/manual/gh_help_environment)
or `PATH`, and Git through `PATH`. Help, local commands, and existing-branch worktree creation do
not require an API lookup. Missing executables or unsupported options fail at the requested
operation; there is no HTTP, authentication-file, or worktree-metadata fallback. The jq queries
run inside `gh`; no separate `jq` executable is required.

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
| Bare-name completion | Bare `<repo>` uses `GH_HOST` or `github.com` and obtains the authenticated login on that host. It does not infer an Enterprise host from stored accounts. It never reads `ghq.user`, `ghq.completeUser`, or an ambient OS username. | gh-qw authentication model | `internal/ghapi/client.go` | `internal/ghapi/client_test.go` |
| `file://` input | Local and non-local file URLs can be canonicalized for local discovery and migration. `get` rejects them because `gh repo clone` and `gh repo sync` require a GitHub repository. | Command-specific adaptation | `internal/repospec/parser.go`; `internal/cmd/get.go` | `internal/repospec/parser_test.go`; `internal/cmd/get_test.go` |
| Multiple roots | Existing repositories are searched in configured order; the first root is primary for new clones and migration destinations. Read-only discovery deduplicates identities with the earliest root winning. Mutating ambiguity is rejected. | Compatible intent with explicit ordering and safety | `internal/root/resolver.go`; `internal/local/selector.go` | `internal/cmd/root_test.go`; `internal/local/path_selector_test.go` |
| `list` | Substring matching, smartcase, host-prefix matching, exact matching, shortest unique suffixes, and ascending output are supported. Linked worktrees add `@<slot>`. `gh-qw` prints identities or paths and leaves selection to callers. | Compatible matching with worktree extension | `internal/cmd/list.go`; ADR-0009 | `internal/cmd/list_test.go`; `tests/cli_test.go` |
| `root` | `root` prints the primary root; `root --all` prints every root in order. Paths use `/` separators on every platform. | Compatible core with normalized output | `internal/cmd/root.go` | `internal/cmd/root_test.go` |
| Removal confirmation | Destructive confirmation comes from the controlling terminal, never piped repository input. Repository ambiguity and Git safety refusals fail without broadening the target. | Safety adaptation | `internal/cmd/rm.go` | `internal/cmd/rm_test.go` |
| Gone-upstream worktree removal | `worktree remove --gone` uses fully qualified structured upstream refs and the local ref database, emits one revalidated bulk plan, and preserves all local branches and commits. It never performs an implicit fetch. | gh-qw extension | `internal/gitcmd/repository.go`; `internal/cmd/worktree_remove_gone.go`; ADR-0019 | `internal/gitcmd/repository_test.go`; `internal/cmd/worktree_remove_test.go`; `tests/cli_test.go` |
| Exit and output | Statuses are `0` success, `1` runtime/safety failure, and `2` usage/configuration failure. Successful result data alone is written to stdout; diagnostics use stderr. | Stronger gh-qw contract | `internal/cmd/app.go`; command implementations | `internal/cmd/*_test.go`; `tests/cli_test.go` |
| ghq configuration | Normal commands ignore `GHQ_ROOT` and every `ghq.*` Git setting. | Deliberate isolation | `internal/root`; `internal/repospec` | package tests and repository-wide command tests |
| `migrate` source discovery | With no directory, `GHQ_ROOT` wins; otherwise effective `ghq.root` values, the `~/ghq` default, and URL-specific roots locate source repositories. Values are source input only and are not persisted. | Explicit ghq migration support | `internal/migrate/legacy.go`; `internal/cmd/migrate.go` | `internal/migrate/legacy_test.go`; `internal/cmd/migrate_test.go` |
| Scope | `create`, shell-launch behavior, bare repositories, and non-Git VCS backends are not part of the command surface. | Deliberate product boundary | ADR-0006; `internal/cmd/app.go` | command help and flag tests |

## GitHub CLI interoperability

| Surface | Current contract | Source evidence | Test evidence |
| --- | --- | --- | --- |
| Network delegation | New clones use `gh repo clone --no-upstream`; updates and default-branch synchronization use `gh repo sync`. Network-capable operations require `github.com` or a `gh`-authenticated GitHub Enterprise host. | `internal/ghcmd/repository.go`; ADR-0002 | `internal/ghcmd/repository_test.go`; `internal/cmd/get_test.go`; `internal/cmd/worktree_add_test.go` |
| Executable and process model | `gh` is found through `GH_PATH` or `PATH`, runs directly without a shell, and receives no caller stdin. | `internal/ghcmd/executable.go`; `internal/ghcmd/runner.go`; ADR-0003 | `internal/ghcmd/executable_test.go`; `internal/ghcmd/runner_test.go`; `tests/cli_test.go` |
| Authentication | Use gh active accounts and standard token environment variables. No owner matching, selection prompts, cache reads/writes, token overrides, or per-repository account switching. | `internal/cmd/get.go`; `internal/cmd/worktree_add.go`; ADR-0002 | `tests/cli_test.go`; command tests |
| Local operations | Help/version, local commands, and existing-branch worktree creation do not query authentication. Clone/sync have no unconditional authentication preflight. | `internal/cmd/get.go`; `internal/cmd/worktree_add.go` | `tests/cli_test.go`; `internal/cmd/worktree_add_test.go` |
| Execution environment | Clone, sync, and API queries inherit all standard GitHub.com/GHES token variables unchanged, leaving precedence to gh. Local sync scopes `GH_HOST` to the selected repository host; API queries pass the selected host with `--hostname`. | `internal/ghcmd/runner.go`; `internal/ghapi/client.go` | `internal/ghcmd/repository_test.go`; `internal/ghapi/process_test.go` |
| API lookup | External `gh api` retrieves `user.login` and the REST repository `default_branch`, preserving empty-repository default branches. Missing, null, or empty fields fail. Errors retain operation and cancellation context without copying gh output, arguments, or token values into the error string. No Go HTTP transport or authentication-setting reader is used. | `internal/ghapi/client.go` | `internal/ghapi/client_test.go`; `internal/ghapi/process_test.go` |
| Output descriptors | When safe, `gh` inherits the diagnostic file descriptor so terminal progress and color work. Other writers use relay logic with bounded error capture; silent and parallel `get` disable direct progress output. | `internal/procio/procio.go`; `internal/ghcmd/runner.go`; ADR-0003 | `internal/procio/procio_test.go`; `internal/ghcmd/runner_test.go`; `internal/cmd/get_test.go` |
| Stdout purity | Clone/sync stdout is routed to gh-qw's diagnostic destination; API stdout is captured for field interpretation. Neither reaches gh-qw's result stdout. | `internal/cmd/get.go`; `internal/ghapi/client.go` | `internal/cmd/get_test.go`; `tests/cli_test.go` |

### Migrating API host aliases

Use the repository host when setting `GH_HOST` or supplying an explicit host: `github.com` or
`<tenant>.ghe.com`. Earlier API integration implicitly converted `api.github.com` and
`api.<tenant>.ghe.com` to those repository hosts. That conversion has been removed: the selected
host is now passed directly to `gh api --hostname`, and gh determines its API endpoint. Update
API-host values to their repository hosts before running bare-name completion or default-branch
lookup; otherwise native gh may construct an unintended endpoint such as `api.api.github.com`.

## Git interoperability

| Surface | Current contract | Source evidence | Test evidence |
| --- | --- | --- | --- |
| Local Git boundary | Branch/upstream inspection, exact ref and revision checks, worktree creation, enumeration, removal, pruning, and repair use Git directly; network cloning and synchronization use `gh`. Bulk gone-upstream removal does not fetch. | `internal/gitcmd`; ADR-0002; ADR-0019 | `internal/gitcmd/*_test.go`; command worktree tests |
| Process I/O | Git runs directly without a shell, receives no caller stdin, and uses the same descriptor passthrough or relay strategy as `gh`. | `internal/gitcmd/runner.go`; `internal/procio/procio.go`; ADR-0003 | `internal/gitcmd/runner_test.go`; `tests/cli_test.go` |
| Worktree add | gh-qw validates deterministic slots and delegates branch/remote selection and ambiguity to Git. Git respects `checkout.defaultRemote`. Explicit creation/reset uses native `git branch` followed by `git worktree add`. Only `-b`/`-B` without a start point queries the GitHub default and synchronizes it when missing locally. An unknown branch without an explicit start point is no longer created implicitly. | `internal/gitcmd/worktree.go`; `internal/cmd/worktree_add.go` | Native multi-remote tests; `tests/fixture_isolation_test.go` custom fetch-mapping regression; command integration tests |
| Worktree listing | Git's NUL-delimited porcelain is parsed internally. `gh qw worktree list --porcelain` emits gh-qw's stable identity/path/head/kind format rather than passing Git's format through. | `internal/gitcmd/worktree.go`; `internal/cmd/worktree_list.go` | `internal/gitcmd/worktree_test.go`; `internal/cmd/worktree_list_test.go` |
| Filesystem boundaries | Existing-prefix path resolution and identity capture are shared. Configured-path normalization and runtime validation against an established root remain distinct; replacing that root with a symlink cannot move the trusted boundary. Windows identity is captured before mutation. | `internal/physicalpath`; `internal/fsidentity`; `internal/root/path.go` | shared-package tests; root, migration, removal, prune, and gone-worktree safety tests |
| Removal observations | Repository boundaries, object identity, Git association, and nested registrations share read-only observations. Initial planning may share one Git inventory; confirmation and late discovery start fresh observations. Command-specific selection and force policies remain separate. | `internal/removal`; `internal/cmd/rm.go`; `internal/cmd/worktree_remove_gone.go` | shared observation tests; real-Git phase counts and late nested-worktree protection in command tests |
| Removal execution | Linked removal shares native Git invocation, exact-path and registration verification, and identity-aware empty-parent cleanup. Commands retain revalidation, force, continuation, diagnostics, and partial-failure policy. Main-directory deletion and Git prune metadata/expiry decisions remain separate. | `internal/removal`; `internal/cmd/rm.go`; `internal/cmd/worktree_remove.go`; `internal/cmd/worktree_remove_gone.go` | shared execution tests; `internal/cmd/removal_execution_test.go`; existing removal and prune lifecycle tests |
| Migration repair | Requires `git worktree list --porcelain -z` and `git worktree repair <paths...>` capable of repairing both a moved main and moved linked worktrees. gh-qw maps paths and validates boundaries; Git repairs administrative metadata. No manual-file fallback is provided. | `internal/migrate/worktree.go`; `internal/cmd/migrate.go` | `internal/migrate/worktree_test.go`; `TestMigrateDelegatesWorktreeRepairToGit` in `internal/cmd/migrate_test.go` |
| Object format | Worktree HEAD object IDs are relayed at Git's actual width. SHA-1 and SHA-256 repositories are supported without gh-qw computing or constraining the width. | `internal/gitcmd/worktree.go` | `internal/cmd/worktree_list_test.go`; real SHA-256 coverage in `internal/gitcmd/worktree_test.go` |
| Environment | Git and `gh` inherit the process environment without credential overrides, so standard Git transport, credential-helper, proxy, and configuration variables continue to apply. | `internal/gitcmd/runner.go`; `internal/ghcmd/runner.go` | runner tests and command integration tests |

### Migrating native worktree branch selection

An unknown branch with no explicit start point now follows native `git worktree add` and fails rather than querying the GitHub default branch. Use `gh qw worktree add -b <branch>` to create from the GitHub default, or provide `<start-point>` explicitly. Git selects among remotes; configure `checkout.defaultRemote` when multiple remotes have the same branch. Existing local/remote branches do not trigger unnecessary API authentication or synchronization.

## Verification and fixture isolation

The CLI suite builds the immutable gh-qw executable and its single fake gh executable once per
test-package process. It does not share HOME, XDG paths, Git configuration, working directories,
repository roots, remote data, or response environment values between fixtures. `TestMain`
removes the shared executable directory only after every test has finished.

Test child environments discard inherited Git/GitHub configuration and token variables,
including `GH_ENTERPRISE_TOKEN`, `GITHUB_ENTERPRISE_TOKEN`, `GH_HOST`, `GH_REPO`, and `GH_PATH`,
then install fixture-local paths and explicitly requested synthetic values. This is test
isolation only: production Git/gh environment delegation is unchanged. Clone/API scenarios
use fake responses and local Git repositories, never real credentials or unknown-host requests.
`tests/fixture_isolation_test.go` verifies this through the public CLI, including distinct
repository results across two fixtures and a native custom fetch mapping with gh unavailable.

Shared OS/process behavior is tested in `internal/procio`; Git/gh adapters test their invocation
and result contracts. Command tests cover arguments, diagnostics, exit codes and orchestration.
Existing real-Git lifecycle, boundary, identity, confirmation-race, nested registration,
locked/dirty-state and partial-failure scenarios remain in their owning packages. Removed-feature
flag tests are not duplicated alongside generic unknown-option and configuration-key tests.
Performance improvements are not quantified without comparable before-and-after measurements.
