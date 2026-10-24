# Architecture

## Responsibilities

| Component | Owns |
| --- | --- |
| `cmd` | CLI arguments, narrow dependency composition, orchestration, diagnostics, and exit classification. |
| `repospec` | Input syntax, canonical repository identity, transport separation, and native literal-ref validation. |
| `config`, `root`, `xdg` | TOML loading, environment precedence, root normalization, and XDG defaults. |
| `registry` | Explicit identity-to-location mappings under XDG data; no Git metadata. |
| `local` | Read-only repository/worktree discovery, selectors, slots, and Git associations. |
| `gitcmd`, `ghcmd`, `ghapi` | Native command arguments and structured output interpretation. |
| `procio` | Context-bound processes, descriptors, captured output, bounded diagnostic tails, and typed failures. |
| `physicalpath`, `fsidentity` | Physical paths, established boundaries, and filesystem object identity. |
| `removal` | Shared observations, revalidation, native removal, verification, and bounded parent cleanup. |
| `migrate` | Source discovery, destination planning, directory movement, and native registration repair. |

## External commands

Use argument vectors with `exec.CommandContext`, without a shell. Child stdin
reads from the null device. Progress uses stderr; successful result data alone
uses stdout. File-backed streams pass their descriptors through directly;
pipe-based diagnostics retain at most 64 KiB. Error messages exclude arguments,
environment values, and captured stderr while preserving process status and
cancellation through the error chain.

Git owns ref validation, branch creation/reset, tracking and remote choice,
worktree registration, pruning, and repair. gh owns clone/sync, authentication,
and API transport. API field selection uses gh's built-in `--jq`; no separate
jq program is required by the CLI. Unsupported external commands fail through
the native tool rather than invoking a second implementation.

Help, version, syntax validation, and local-only operations do not perform
unnecessary configuration reads, discovery, or authentication. Pure syntax is
separate from host/login and filesystem-based input resolution.

## Filesystem and failure boundaries

Repository identity is `<host>/<owner>/<repo>` and excludes transport details.
Linked worktrees have deterministic slots independent of their current branch.
The explicit XDG location registry owns gh-qw's managed main-repository set.
Git remains the source of truth for actual worktree registrations, and the
filesystem for existing files. Configured roots determine default placement;
their contents are never imported into the managed set automatically.

Configured-path normalization and runtime validation against an established
physical root are distinct. Capture filesystem identity before mutation,
including on Windows. Reject unrelated paths, symlink escapes, nested
registrations, and unknown registration state.

Share observations only within one read-only phase. Rebuild bulk removal plans
after confirmation and revalidate each target immediately before deletion.
Whole-repository, single-worktree, gone-upstream, and prune policies retain
separate selection and continuation decisions.

Git removes linked worktrees; gh-qw verifies path and registration absence
before empty-parent cleanup. gh-qw deletes the main directory only after its
linked worktrees and boundary checks. An explicitly registered repository
outside configured roots uses its immediate parent only as a non-removable
boundary. After confirmed main-directory deletion, unregister it even when a
later cleanup or output step fails, provided the mapping still names the removed
path. A checkout registered at an arbitrary path inside a configured root uses
the same exact-path boundary; configured layout is not inferred from containment.
Prune's metadata/expiry evidence is not interchangeable with ordinary removal
evidence.

Report operations already completed when a later step fails. Do not undo a
created branch, completed migration, or successful worktree creation merely
because a subsequent checkout, repair, cleanup, or output write fails. A failed
warning write prevents mutation; an output failure after mutation reports the
actual partial result. See the [CLI reference](cli.md) for command-specific rules.

## Tests

Shared process behavior belongs in `procio`; adapters test invocation and
interpretation; command tests exercise arguments, output, statuses, and effects.
Use real Git/filesystems for lifecycle, identity, symlink, nested-registration,
confirmation-race, lock/dirty-state, and partial-failure cases.

The CLI suite shares immutable executable builds only. Each fixture owns HOME,
XDG paths, configuration, repositories, working directories, and response values.
Test child environments remove ambient Git/gh credentials and settings, then
install explicit synthetic values. Production environment delegation is unchanged.
