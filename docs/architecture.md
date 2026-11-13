# Architecture

This describes the current implementation's responsibilities and boundaries.
The [purpose](purpose.md) owns product direction; the [CLI](cli.md) and
[configuration](configuration.md) references own user-facing contracts.

## Responsibilities

| Component | Owns |
| --- | --- |
| `cmd` | CLI arguments, narrow dependency composition, orchestration, diagnostics, and exit classification. |
| `repospec` | Input syntax, canonical repository identity, transport separation, and native literal-ref validation. |
| `config`, `root`, `xdg` | TOML loading, environment precedence, root normalization, and XDG defaults. |
| `registry` | Explicit identity-to-location mappings under XDG data; no Git metadata. |
| `local` | Registered-repository selectors, current checkout resolution, worktree inspection, slots, and Git associations. |
| `gitcmd`, `ghcmd`, `ghapi` | Native command arguments and structured output interpretation. |
| `fzfcmd` | External selector invocation and validation of one returned candidate. |
| `procio` | Context-bound processes, descriptors, captured output, bounded diagnostic tails, and typed failures. |
| `physicalpath`, `fsidentity` | Physical paths, established boundaries, and filesystem object identity. |
| `removal` | Shared observations, revalidation, native removal, verification, and bounded parent cleanup. |
| `movement` | Safe directory movement and native worktree-registration repair for `add --move`. |

## External commands

Use argument vectors with `exec.CommandContext`, without a shell. Git/gh stdin
reads from the null device. The optional fzf selector receives generated
candidates on stdin and returns its selection through captured stdout.
Progress uses stderr; successful result data alone uses public stdout.
File-backed streams pass their descriptors through directly;
pipe-based diagnostics retain at most 64 KiB. Error messages exclude arguments,
environment values, and captured stderr while preserving process status and
cancellation through the error chain.

Git owns ref validation, branch creation/reset, tracking and remote choice,
worktree registration, pruning, and repair. Worktree creation uses the selected
main checkout as its Git working directory and needs no GitHub API access.
gh owns cloning, authentication, and API transport. Unsupported external
commands fail through the native tool rather than invoking a second
implementation.

fzf owns the interactive interface. `cmd` maps each display row to its original
repository identity, path, and stable worktree slot as needed by the command.
`fzfcmd` exchanges NUL-delimited records and accepts only one complete, unchanged candidate.
Selection output never becomes public result output or an arbitrary path to
delete. Clone discovery passes the full candidate set and an editable initial
query independently; it prepares selections before starting parallel clones.
List selection maps rows to precomputed result values; its optional shell
wrapper consumes the resulting absolute path to navigate in the invoking shell.
The [CLI reference](cli.md#fzf-selection) owns selector
requirements, environment handling, cancellation, and error behavior.

Help, version, syntax validation, and local-only operations do not perform
unnecessary configuration reads, discovery, or authentication. Pure syntax is
separate from host and filesystem-based input resolution.

## Filesystem and failure boundaries

Repository identity is `<host>/<owner>/<repo>` and excludes transport details.
Linked worktrees live at `<root>/worktrees/<unique-slot>/<repo>`,
independent of their current branch. Build slot names from the canonical
repository identity and branch, normalize punctuation to hyphens, and select
the first unoccupied numeric suffix. Atomically reserve the directory; Git
remains authoritative for the registration and current branch.
The explicit XDG location registry owns gh-qw's managed main-repository set.
Git remains the source of truth for actual worktree registrations, and the
filesystem for existing files. The configured root determines default placement;
its contents are never scanned or imported into the managed set. Commands read
the registry directly; each picker, confirmation, and mutation revalidation
phase reads a fresh inventory.

Configured-path normalization and runtime validation against an established
physical root are distinct. Capture filesystem identity before mutation,
including on Windows. Reject unrelated paths, symlink escapes, nested
registrations, and unknown registration state.

Worktree enumeration validates each slot and captures filesystem identities once
per observation. Display rendering uses those canonical fields without Git or
filesystem queries. Git top-level and common-directory paths come from one
native query; failed commands never yield usable partial observations.

Share observations only within one read-only phase. Rebuild bulk removal plans
after confirmation and revalidate each target immediately before deletion.
Interactive removal also preserves target observations across the picker and
rejects changed repository mappings, filesystem identities, or worktree state
before entering the existing removal flow.
Whole-repository, single-worktree, and gone-upstream policies retain separate
selection and continuation decisions.

Git removes linked worktrees; gh-qw verifies path and registration absence
before empty-parent cleanup. gh-qw deletes the main directory only after its
linked worktrees and boundary checks. An explicitly registered repository
outside the standard configured layout uses its immediate parent only as a
non-removable boundary, including an arbitrary path inside the configured root.
After confirmed main-directory deletion, unregister it even when a later cleanup
or output step fails, provided the mapping still names the removed path.
Whole-repository removal and `add --move` refuse a source that physically contains
another registered repository, including registrations observed during removal
revalidation after confirmation. Removal applies the same inventory protection
to every linked worktree scheduled for deletion. Containment follows filesystem
identity rather than path spelling, including case-insensitive aliases.
Single and gone-upstream worktree removal apply the same check to explicit
repository registrations and refresh that inventory immediately before Git
removes each linked directory.

Report operations already completed when a later step fails. Do not undo a
created branch, completed repository move, or successful worktree creation merely
because a subsequent checkout, repair, cleanup, or output write fails. A failed
plan or diagnostic write prevents mutation; an output failure after mutation
reports the actual partial result. See the [CLI reference](cli.md) for command-specific rules.

## Tests

Shared process behavior belongs in `procio`; adapters test invocation and
interpretation; command tests exercise arguments, output, statuses, and effects.
Use real Git/filesystems for lifecycle, identity, symlink, nested-registration,
confirmation-race, lock/dirty-state, and partial-failure cases.

The CLI suite shares immutable executable builds only. Each fixture owns HOME,
XDG paths, configuration, repositories, working directories, and response values.
Test child environments remove ambient Git/gh credentials and settings, then
install explicit synthetic values. Production environment delegation is unchanged.
