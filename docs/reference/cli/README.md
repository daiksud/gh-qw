---
type: reference
title: "CLI reference"
description: "Normative v1 command, argument, output, and exit-status contract for gh-qw."
resource: gh-qw
tags: [gh-qw, reference, cli]
timestamp: 2026-08-05
---

# CLI reference

This page is the normative command-line contract for `gh-qw`. For the design boundaries behind
this surface, see [The gh-qw concept](../../concept/) and
[ADR-0006](../../development/adr/0006-command-set-v1/). The
[compatibility reference](../compatibility/) maps these commands to current ghq, GitHub CLI, and
Git contracts.

## Invocation and common behavior

`gh-qw` is a GitHub CLI extension and is invoked through `gh`:

```text
gh qw <command> [options]
```

`get` also has the exact alias `clone`. Command and flag names are case-sensitive. Every command
level supports `-h`/`--help`; help exits successfully without performing an operation.

### Exit status

| Status | Meaning |
| --- | --- |
| `0` | The requested operation completed successfully. A documented skip, such as a bulk-migration collision, is non-fatal. |
| `1` | A runtime operation failed or a safety check refused the operation. Examples include Git or API failure, an unreadable file, a destination collision, or a path-containment failure. |
| `2` | Command syntax, a repository specification, configuration, or usage is invalid. Examples include an unknown flag, a missing argument, an invalid `--partial` value, an ambiguous selector, incompatible flags, or an explicit `--herdr` outside a Herdr-managed pane. |
| `130` | Canceling the external `fzf` picker (Esc or Ctrl-C) during `list --fzf`. This reuses `fzf`'s own documented cancellation status; see [`list`](#list). |

For a batch `get`, all started items may finish before the process exits. Status `2` takes
precedence if any item has an invalid specification; otherwise any runtime failure produces status
`1`.

### Standard output and standard error

- `stdout` contains only successful result data. A command that returns a path writes one absolute
  path per line and no explanatory text.
- Identity output uses `/` separators. Absolute path output is also normalized to `/`, including
  `C:/...` form on Windows.
- Progress, warnings, skipped-item notices, dry-run plans, prompts, and errors go to `stderr`.
- `get`'s and `worktree add`'s ordinary progress is `gh`'s and Git's own `gh repo clone`/
  `gh repo sync`/`git worktree add` output. When `gh-qw`'s own `stderr` is a file (including a
  terminal) and progress is not suppressed (see `--silent`/`--parallel` below for `get`), that
  output is passed through to the same file directly instead of being relayed line by line, so
  `gh` and Git can still detect a terminal there and render interactive progress and color exactly
  as they would running standalone. `gh`'s own `stdout` is passed through to that same `stderr`
  file rather than to `gh-qw`'s `stdout`, so the result-path-only contract above still holds even
  for `gh repo sync`'s own completion message. Piping or redirecting `gh-qw`'s `stderr` relays the
  output when direct descriptor use is unavailable.
- A confirmation response is read from the controlling terminal. Declining a destructive prompt
  changes nothing and exits with status `1`. The default answer is no; if no controlling terminal
  is available, the command fails safely instead of reading piped repository input as confirmation.
- `--silent` suppresses ordinary progress only. It never suppresses result paths or errors.
- If a batch partly succeeds, paths for completed items can already be present on `stdout` when the
  final status is nonzero.

### gh account selection

`get` and `worktree add` are the only commands that perform network-capable Git or GitHub API
operations, and both delegate them to `gh` (`gh repo clone`, `gh repo sync`, and the REST API used
to look up a repository's default branch). In an environment authenticated as multiple gh
accounts, whichever account `gh` considers active is not necessarily the one with access to a
given repository. Before each such operation, `gh-qw` resolves which account to use for the
repository's `<owner>`:

1. If the process environment sets `GH_TOKEN` or `GITHUB_TOKEN`, that explicit credential is used
   and automatic selection is skipped.
2. Otherwise, a cached choice for `<host>/<owner>` is used only when its login and token are
   valid.
3. Otherwise, every gh-authenticated account for the host is listed. Exactly one login matching
   `<owner>` case-insensitively is selected.
4. Otherwise, exactly one authenticated account is selected.
5. Otherwise, a controlling terminal is required to choose from the accounts interactively.

The selected login is cached before the network operation. If a cached login no longer has a
usable token, its mapping is removed and resolution continues from account listing. An unreadable
or malformed cache, failed stale-entry removal, account-listing or selected-token lookup failure,
cache-write failure, no available account, non-interactive ambiguity, or cancelled prompt is an
error. Resolution finishes before any clone, synchronization, or API request starts; `gh-qw`
never defers an unresolved choice to whichever account `gh` currently considers active.

A failure from `gh` after this resolution includes a hint naming the account gh-qw used, except
when step 1 applied — an explicit token's account is never inspected or reported.

### Herdr workspace integration

`worktree add`, `worktree remove`, and `rm` (for an `@<branch>` linked-worktree target only)
accept `--herdr` and `--no-herdr`, mutually exclusive flags that integrate with Herdr, a terminal-
workspace manager for coding agents, through its own `herdr` executable resolved from `PATH`.
`gh-qw` never launches a shell itself; it only runs `herdr` and reports the outcome, the same shape
[`list --fzf`](#list) already uses for `fzf` (see
[ADR-0009](../../development/adr/0009-interactive-selection-via-fzf/) and
[ADR-0018](../../development/adr/0018-herdr-workspace-integration/)).

Enablement resolves once per invocation, in this order: an explicit `--herdr`/`--no-herdr` flag;
otherwise a set `GHQW_HERDR` environment variable; otherwise the configuration file's `herdr` key
(see the [configuration reference](../configuration/#schema)); otherwise disabled. `GHQW_HERDR`
accepts `1`/`true`/`yes`/`on` and `0`/`false`/`no`/`off`, case-insensitively; any other non-empty
value is a configuration error (status `2`).

An explicit `--herdr` outside of a Herdr-managed pane (`HERDR_ENV` unset or not `1`) is a usage
error (status `2`) before anything else runs. Enablement through `GHQW_HERDR` or the configuration
file outside Herdr instead only writes one warning line to `stderr` and skips the integration,
leaving the command's own result and exit status unaffected — a shared configuration file does not
break ordinary use outside Herdr.

When enabled, `worktree add` opens and focuses a Herdr workspace at the new worktree's own
directory after creating it, labeled `<repo>@<branch>`. `worktree remove` and `rm` resolve the
workspace already open for that worktree, remove the worktree exactly as they otherwise would, and
then close the resolved workspace; a worktree with no open workspace is left alone. A Herdr
failure (herdr missing from `PATH`, or any operation it reports failing) is a status `1` error that
never changes the underlying worktree add or removal, which has already completed; `worktree add`'s
`stdout` still holds only the new absolute path.

## Repository specifications and canonical identities

Main repositories use a canonical slash-separated identity:

```text
<host>/<owner>/<repo>
```

The identity always has exactly these three components. Any third or later remote-path component
after owner and repository is invalid. A linked worktree adds `@<slot>` to the main identity.

### Forms accepted by `get`

| Input | Interpretation |
| --- | --- |
| `<repo>` | Complete with the authenticated user and host selected through `gh`. |
| `<owner>/<repo>` | `https://github.com/<owner>/<repo>`. |
| `<host>[:<port>]/<owner>/<repo>` | HTTPS on the explicit host and optional port. |
| `http://<host>/<owner>/<repo>[.git]` | Explicit HTTP URL. |
| `https://<host>/<owner>/<repo>[.git]` | Explicit HTTPS URL. |
| `ssh://[<user>@]<host>[:<port>]/<owner>/<repo>[.git]` | Explicit SSH URL. |
| `[<user>@]<host>:<owner>/<repo>[.git]` | SCP-like SSH URL. A slash immediately after `:` is optional. |
| `.`, `..`, `./...`, or `../...` | A relative path that maps to exactly one configured-root identity. |

`get` clones and synchronizes through `gh repo clone` and `gh repo sync`, so its host must be
`github.com` or a `gh`-authenticated GitHub Enterprise host; `gh` reports an unresolvable host as a
runtime failure. A `file://` input is rejected as a usage error because it can never name a `gh`
repository, even though other commands still resolve it (see Canonicalization below).

An optional trailing `@<branch>` may follow any repository path. It selects the initial clone
branch; it does not create a linked worktree. The delimiter is the first `@` in the repository
component, so an SSH authority such as `git@host` is not mistaken for a branch suffix and later
`@` characters remain part of the branch. A slash after the suffix belongs to the branch, not to
the repository identity. An empty or invalid Git branch suffix is a usage error. If both the
suffix and `--branch` are present, the suffix wins for ghq compatibility.

For a relative path, `@` may also occur in a parent directory name. `gh-qw` tests possible suffix
boundaries against the configured roots and accepts the input only when one unambiguous
`<host>/<owner>/<repo>` mapping remains.

Relative `./` and `../` forms are identity shorthand, not arbitrary clone destinations. After
physical path resolution, the result must remain inside a configured repository root and its path
relative to that root must contain exactly `<host>/<owner>/<repo>[.git]`. For example, from
`<root>/github.com/acme`, `./widget` resolves to `github.com/acme/widget`; a relative path outside
every root or with a deeper relative path is invalid.

### Canonicalization

Before deriving a destination or comparing identities, `gh-qw`:

1. requires `/` path separators, rejects `\`, and parses SCP-like syntax as SSH;
2. validates the host as DNS or IPv4, lowercases it, and rejects a trailing DNS dot or IPv6;
3. excludes URL user information and port from the identity while retaining them for transport;
4. requires exactly one owner and one repository component and rejects empty, escaped, or
   traversal components;
5. strips one terminal `.git` suffix from the repository component and rejects a trailing slash;
   and
6. preserves the case of owner and repository components.

URL queries and fragments are not identity components and are rejected in repository
specifications. Two URLs that differ only by scheme, SSH user, port, or terminal `.git` therefore
have the same canonical identity. A collision between such URLs is handled as one repository, not
as two destinations. URL passwords are rejected. Owner and repository components accept only ASCII
letters, digits, `.`, `_`, and `-`, may not be `.` or `..`, and may not end in a dot. Escaped
remote URL paths are not decoded into identity components; they are rejected. A terminal `.git`
suffix is retained in the clone transport but omitted from identity and canonical URL.

A `file://` URL with an empty authority or the authority `localhost` is local-path input. Its
physical path must lie under a configured repository root, and the relative path must be exactly
`<host>/<owner>/<repo>[.git]`; those three relative components supply the canonical identity.
The local machine name and filesystem volume never become identity components. A non-local
authority instead uses exactly `file://<host>/<owner>/<repo>[.git]`; a deeper path, user
information, or port is invalid. `list` and `migrate` resolve this form as described here; `get`
rejects it (see above).

For bare `<repo>` input, `GH_HOST`, when set, selects the authentication host. Otherwise
an authenticated `github.com` account is preferred; if none exists, exactly one authenticated
other host must be available. The login for the selected account supplies `<owner>`. Missing
authentication, a missing login, or multiple remaining hosts is a specification error; `gh-qw`
asks for an explicit `<owner>/<repo>` or `<host>/<owner>/<repo>` instead. No
`ghq.user`, `ghq.completeUser`, or `ghq.defaultHost` setting is read.

`-p` changes an HTTPS or shorthand clone transport to
`ssh://git@<host>/<owner>/<repo>` unless an HTTP(S) URL already provides a user. It leaves
an explicit SSH transport unchanged and does not change canonical identity. SCP-like
input is normalized to its equivalent `ssh://` URL regardless of `-p`.

### Local repository selectors

Commands that select an existing repository accept the forms shown in their usage:

- `<repo>` is an exact final-component match;
- `<owner>/<repo>` is an exact identity suffix; and
- `<host>/<owner>/<repo>` is a full canonical identity.

Selection is not fuzzy. A selector matching more than one repository, including duplicate
canonical identities in different roots for a destructive operation, is ambiguous and exits with
status `2`. `-R/--repo` does not accept a filesystem path or URL.

## `get` / `clone`

```text
gh qw get [-u|--update] [-p] [--shallow] [-b|--branch <branch>]
          [--no-recursive] [-s|--silent] [-P|--parallel]
          [--partial blobless|treeless] [<repo>...]

gh qw clone <same options and arguments>
```

`get` clones missing repositories into the primary repository root using `gh repo clone`. If the
canonical identity already exists in any configured root, that existing checkout is selected;
without `--update` it is left unchanged. With `--update`, `gh repo sync` synchronizes the
existing checkout's default branch, as resolved by `gh`, from its own repository — not necessarily
the currently checked-out branch, and never a fork's parent. A result path is emitted even when
the repository already existed.

`get` and `--update` require `gh` authentication and a `github.com` or `gh`-authenticated GitHub
Enterprise host; an unauthenticated or unresolvable host is a runtime failure reported by `gh`.

| Option | Contract |
| --- | --- |
| `-u`, `--update` | Synchronize an existing checkout's default branch. It has no extra effect on a new clone. |
| `-p` | Use SSH instead of HTTPS for shorthand or HTTPS input. |
| `--shallow` | Pass depth `1` for a new clone. It does not rewrite an existing checkout. |
| `-b`, `--branch <branch>` | Clone the named branch with single-branch history. It applies only to initial clone selection. |
| `--no-recursive` | Do not recursively initialize submodules for a new clone. Recursive behavior is the default. It has no effect on `--update`, which has no submodule behavior. |
| `-s`, `--silent` | Suppress ordinary clone/update progress on `stderr`; keep paths and errors. |
| `-P`, `--parallel` | Process at most six repositories concurrently and imply `--silent`. Passing through `gh`'s and Git's own output directly is disabled, since several repositories' subprocesses could otherwise write to the same descriptor at once; this has no visible effect beyond `--silent` since progress is already suppressed. |
| `--partial blobless` | Clone with Git filter `blob:none`. |
| `--partial treeless` | Clone with Git filter `tree:0`. |

Any other `--partial` value is a usage error. `--shallow`, `--branch`, `--no-recursive`, and
`--partial` affect only a new clone. `--branch` and an `@branch` suffix select a main checkout and
never create a worktree.

If positional arguments are present, each is one repository specification and `stdin` is ignored.
With no positional arguments, `get` reads one specification per line from non-terminal `stdin`.
Leading and trailing whitespace is removed, empty lines are skipped, and there is no comment
syntax. No arguments with terminal `stdin` is a usage error.

Without `--parallel`, items run in input order and stop at the first failure. With `--parallel`,
the concurrency cap is six, all valid items are attempted, and successful paths are written in
completion order. Each success writes exactly one absolute main-worktree path to `stdout`.

The v1 command has no `--look`, `--vcs`, or `--bare` option and never starts a shell.

```text
gh qw get cli/cli
printf '%s\n' acme/api acme/web | gh qw get -P
gh qw get --partial blobless https://github.example.com/acme/widget.git
```

## `list`

```text
gh qw list [-e|--exact] [-p|--full-path] [--unique] [--worktree] [--fzf] [<query>]
```

By default, `list` discovers ordinary Git main worktrees beneath every repository root and emits
their canonical identities. `--worktree` also emits linked worktrees registered with those main
repositories. Main records use `<canonical>`; linked records use `<canonical>@<slot>`.

For a linked worktree, the slot is its path relative to
`<worktree-root>/<canonical>/`. For an externally located attached worktree, the short branch name
is the fallback slot. For an externally located detached worktree, the final path component is the
fallback. An unsafe or duplicate fallback slot is reported as a runtime error instead of emitting
an ambiguous identity. For every normally attached `gh-qw` worktree, slot and short branch are the
same, so its output is exactly `<host>/<owner>/<repo>@<branch>`.

### Query matching

- With no query, every discovered main repository matches.
- A lowercase query uses case-insensitive substring matching. A query containing any uppercase
  letter uses case-sensitive substring matching (smartcase).
- Normal substring matching is against the non-host identity. If the first query component looks
  like a host and is followed by `/`, that component instead restricts the canonical host.
  Consequently `github.com` alone is not a host-only query, while `github.com/` is.
- A URL query is canonicalized first.
- `-e`/`--exact` disables substring matching and requires a case-sensitive exact identity suffix:
  repository, owner/repository, or the full canonical identity. With `--worktree`, an optional
  `@slot` suffix narrows the match; without it, the selected repository's main and linked entries
  match.

### Output, sorting, and deduplication

| Option | Output |
| --- | --- |
| none | Canonical identities, one per line. |
| `-p`, `--full-path` | Absolute physical paths instead of identities. |
| `--unique` | The shortest unique identity suffix for every selected entry. |
| `--worktree` | Include linked worktree entries in addition to main worktrees. |

`--full-path` and `--unique` are mutually exclusive. Entries are deduplicated by canonical
identity and slot before output; when the same main identity exists in several roots, the earliest
configured root wins for this read-only command. Main and linked worktrees are distinct entries.
The final emitted strings are sorted in ascending bytewise order, so output is deterministic.

For `--unique`, candidates are considered shortest first (`repository`,
`owner/repository`, then full identity), retaining `@slot` for a linked entry. A candidate is used
only if it uniquely identifies that selected entry; otherwise more leading components are kept.
No matches is a successful command with empty `stdout`.

### Interactive selection (`--fzf`)

`--fzf` feeds the same filtered, sorted canonical identities to the external `fzf` executable
(resolved from `PATH`) for a person to pick exactly one, then writes only that entry's absolute
path to `stdout`. `--full-path` and `--unique` are accepted alongside `--fzf` without error but
have no effect, since `--fzf`'s output is always a path. `list` itself still never launches a
shell or changes directory — see [ADR-0006](../../development/adr/0006-command-set-v1/) and
[ADR-0009](../../development/adr/0009-interactive-selection-via-fzf/) — so a caller wires a small
shell function to `cd` into the result:

```sh
qwcd() { local dir; dir=$(gh qw list --fzf) || return; cd "$dir"; }
```

No candidates exits successfully (status `0`) without starting `fzf`. Canceling `fzf` with Esc or
Ctrl-C, or `fzf` finding no match for the typed query, exit with `fzf`'s own documented status (130
or 1) and empty `stdout` and `stderr`. Any other selection failure, including `fzf` missing from
`PATH`, is an ordinary error on `stderr` with status `1`.

The v1 command has no `--vcs` or `--bare` option.

## `root`

```text
gh qw root [--all]
```

Without `--all`, `root` writes the absolute physical primary repository root. `--all` writes every
configured repository root in precedence order, one per line. It does not create missing roots.

## `rm`

```text
gh qw rm [--dry-run] [--herdr|--no-herdr]
         <repo>|<owner>/<repo>|<host>/<owner>/<repo>[@<branch>]
```

Without `@<branch>`, `rm` targets the main worktree. It first enumerates every linked worktree
registered with that repository, displays the complete removal set, and asks once for
confirmation. On confirmation, linked worktrees are unregistered through Git before the main
directory is removed. A refusal, ambiguous repository, containment failure, or Git safety refusal
removes nothing further and exits nonzero.

With `@<branch>`, only the linked worktree for that branch/slot is targeted. The first `@` after
the repository selector is the delimiter; later `@` characters remain part of the branch. The
main worktree is never selected by a suffix. `rm` verifies that the path is registered to the
selected main repository and remains within its expected physical location before invoking `git
worktree remove`.

`--dry-run` performs all discovery, ambiguity, registration, and containment checks, then writes
the exact planned paths to `stderr`. It neither prompts nor removes anything. Removal progress is
also diagnostic output on `stderr`; successful `rm` leaves `stdout` empty.

There is no `--bare` or implicit force flag. If Git refuses a dirty or locked linked worktree,
`rm` exits with status `1`; use the dedicated worktree command or Git directly when an explicit
force operation is intended.

`--herdr`/`--no-herdr` (see [Herdr workspace integration](#herdr-workspace-integration)) apply only
to an `@<branch>` target; both are accepted without error but have no effect when removing a whole
repository.

## `migrate`

```text
gh qw migrate [-y] [--dry-run]
gh qw migrate [-y] [--dry-run] <directory>
```

With no directory, `migrate` discovers legacy ghq source roots and processes ordinary Git
repositories beneath them in source-root order and canonical-path order. With one directory, it
migrates only that ordinary Git repository; the directory may be absolute or relative to the
current directory. Single-directory mode derives its destination identity from `origin`, or from
the first configured remote when `origin` is absent. A non-Git directory, bare repository, linked
worktree, or submodule is rejected and left unchanged.

All destinations are beneath the primary `gh-qw` repository root. A successful migration writes
the absolute destination path to `stdout`. Bulk mode writes one line per moved repository.
Warnings and skips go to `stderr`.

### Bulk source discovery

Legacy roots are input data only:

1. a non-empty `GHQ_ROOT` platform path-list replaces all Git-config sources;
2. otherwise all `ghq.root` values are used;
3. if there are no `ghq.root` values, `~/ghq` is used; and
4. when `GHQ_ROOT` is absent, URL-specific `ghq.<url>.root` values are appended and deduplicated.

See [Configuration reference](../configuration/#legacy-ghq-settings-during-migration) for the
normalization and non-persistence rules.

Bulk migration leaves non-Git repositories and bare repositories in their source roots. A linked
worktree or submodule represented by a `.git` pointer is not migrated independently. Only
directories at exactly `<source-root>/<host>/<owner>/<repo>` are repository candidates;
deeper paths are not alternate identities. If a destination already exists, that item is left
untouched, a warning is emitted, and bulk processing continues. Collision-only skips do not make
an otherwise successful bulk run fail.

In single-directory mode, an existing distinct destination is a safety error with status `1`.
Its selected remote must also canonicalize to exactly `<host>/<owner>/<repo>`; a deeper
remote path is a usage error.

### Movement and worktree repair

Migration never overwrites a destination. It first attempts a filesystem rename. On a
cross-device error, it copies the tree while preserving regular files, directories, modes, and
symbolic links, removes any incomplete destination after a copy failure, and removes the source
only after the copy completes successfully.

For a main repository with linked worktrees, migration:

1. rewrites `.git/worktrees/*/gitdir` back-pointers that referred to a path moved with the main
   repository; and
2. runs `git worktree repair` from the new main location to repair linked-worktree `.git`
   pointers.

A repair failure is reported as status `1` with the repository left at its new destination for
manual recovery; it is never silently treated as success.

Unless `-y` is present, `migrate` prints the complete plan and prompts before changing files.
`--dry-run` prints the same plan, performs validation, does not prompt, and changes nothing.
`-y` has no additional effect with `--dry-run`.

## `worktree`

`worktree` manages the linked worktrees of one ordinary main repository:

```text
gh qw worktree <subcommand> ...
```

Each subcommand resolves the repository from `-R/--repo` when supplied. Otherwise, it resolves the
main repository from the current directory, whether the current directory is inside the main
worktree or one of its linked worktrees. Outside a discovered repository, `-R` is required. An
explicit `-R` wins over current-directory context.

```text
-R, --repo <owner>/<repo>|<host>/<owner>/<repo>
```

The selector must resolve uniquely. Worktree commands accept no arbitrary worktree path argument.
The destination for slot `<branch>` is always:

```text
<worktree-root>/<host>/<owner>/<repo>/<branch>
```

Slashes in a branch form nested directories without escaping or replacement. Absolute names,
empty components, `.`/`..`, or a result outside the per-repository worktree directory are invalid.
Before adding, `gh-qw` rejects both an existing destination and prefix collisions such as `feat`
versus `feat/x`. `-f` never bypasses path, containment, or prefix-collision checks.

### `worktree add`

```text
gh qw worktree add [-R|--repo <repo>] [-b|-B] [--detach] [--orphan]
                    [-f] [--herdr|--no-herdr] <branch> [<commit-ish>]
```

With no explicit creation mode, `<branch>` is both the desired branch and the deterministic path
slot. Resolution is:

1. if a local branch named `<branch>` exists, check it out;
2. otherwise, if `origin/<branch>` exists, create a local tracking branch; if there is no
   `origin` match, use the sole matching `<remote>/<branch>` and reject multiple matches;
3. otherwise, create `<branch>` from `<commit-ish>`; when it is omitted, query the repository
   host through the `gh` API for the default branch and use that ref.

The optional `<commit-ish>` is consulted only by step 3. If API host resolution, authentication,
or default-branch lookup is unavailable, step 3 fails and asks for an explicit `<commit-ish>`.
When the API-selected default branch is not present locally, `gh-qw` runs `gh repo sync` to bring
it from the repository's GitHub host before creating the branch, using `gh`'s own authentication.

`-b` forces creation of the positional `<branch>` and fails if it already exists. `-B` creates or
resets the positional `<branch>`. These adapt Git's `worktree add -b/-B` semantics to a command
that has no separate path operand. They bypass automatic steps 1 and 2, but not layout checks.

`--detach` creates a detached worktree. `<branch>` remains its path slot; `<commit-ish>` is the
checkout target, or `<branch>` is resolved as the target when `<commit-ish>` is omitted.
`--orphan` creates an unborn orphan branch named `<branch>` and does not accept `<commit-ish>`.
`-b`, `-B`, `--detach`, and `--orphan` are mutually exclusive except that `-f` may accompany any
mode. `-f` has the same checkout-safety meaning as one Git `--force`.

`--herdr` opens and focuses a Herdr workspace at the new worktree after it is created; `--no-herdr`
disables that even when `GHQW_HERDR` or configuration would otherwise enable it. See
[Herdr workspace integration](#herdr-workspace-integration) for enablement precedence and failure
handling.

On success, `add` writes exactly the new absolute worktree path to `stdout`.

```text
gh qw worktree add feature/login
gh qw worktree add -b experiment origin/main
gh qw worktree add --detach review/123 refs/pull/123/head
```

### `worktree list`

```text
gh qw worktree list [-R|--repo <repo>] [-v] [--porcelain] [--full-path]
```

Human output contains one line per registered worktree, with the main worktree first and linked
worktrees sorted by identity. Its columns are location, full HEAD object ID, and
`[<short-branch>]` or `[detached]`. The default location is canonical identity;
`--full-path` replaces it with the absolute path. `-v` may append human diagnostics such as lock
and prunable reasons. Human spacing and decoration are not a machine interface.

#### Stable porcelain format

`--porcelain` is a `gh-qw` format, never passthrough from
`git worktree list --porcelain`. `-v` and `--full-path` are each incompatible with
`--porcelain` and are rejected with status `2`.

Output is UTF-8 with LF line endings. Records are separated by one empty line, the final record is
also followed by an empty line, and keys use this fixed order:

```text
identity <host>/<owner>/<repo>[@<slot>]
path <absolute-path>
head <full-object-id>
kind main|linked
branch <short-branch>
locked <reason>
prunable <reason>

```

Exactly one of `branch <short-branch>` or the value-less line `detached` appears after `kind`.
`locked` and `prunable` are optional and, when both exist, appear in that order. `identity`,
`path`, `head`, and `kind` are mandatory. Main identity has no slot; every linked identity does.
An unborn worktree uses the all-zero object ID at the repository's object-format width.

Every value is either an unquoted printable UTF-8 token or a double-quoted C-style string.
Quoting is mandatory for an empty value or a value containing whitespace, `\`, `"`, a control
character, or a non-UTF-8 byte. Escapes are `\\`, `\"`, `\n`, `\r`, `\t`, and `\xHH` for other
bytes. Literal newlines never occur inside a record. Consumers must unescape quoted values once.

Example:

```text
identity github.com/acme/widget
path "/home/alice/ghqw/github.com/acme/widget"
head 0123456789abcdef0123456789abcdef01234567
kind main
branch main

identity github.com/acme/widget@feature/login
path "/home/alice/.local/share/ghqw/worktrees/github.com/acme/widget/feature/login"
head 89abcdef0123456789abcdef0123456789abcdef
kind linked
branch feature/login
locked "deployment check"

```

Porcelain output is buffered: if any record cannot be inspected or represented, the command exits
with status `1` without emitting a partial record set.

### `worktree remove`

```text
gh qw worktree remove [-R|--repo <repo>] [-f] [--herdr|--no-herdr] <branch>
```

`remove` resolves `<branch>` to the deterministic linked-worktree slot, verifies that Git
registers that path with the selected main repository, and invokes `git worktree remove`. It never
removes the main worktree. `-f` supplies one Git force level for a dirty worktree; a lock that Git
requires stronger intervention to remove remains an error.

After successful removal, empty slot-parent directories are removed upward to, but never
including, `<worktree-root>/<canonical>`. Result and progress text go to `stderr`; `stdout` is
empty.

`--herdr` closes the Herdr workspace open for the removed worktree, if any; `--no-herdr` disables
that even when `GHQW_HERDR` or configuration would otherwise enable it. See
[Herdr workspace integration](#herdr-workspace-integration) for enablement precedence and failure
handling.

### `worktree prune`

```text
gh qw worktree prune [-R|--repo <repo>] [-n|--dry-run] [-v]
                     [--expire <expire>]
```

`prune` runs Git's worktree-pruning logic for the selected repository and then examines its
deterministic worktree directory for stale slots. `--expire` is passed to Git using Git's accepted
expiry syntax. `-n`/`--dry-run` reports eligible metadata and directories without deleting them;
`-v` reports each decision.

Additional directory cleanup is conservative. A non-empty slot is eligible only when it is absent
from the current Git worktree list, its `.git` pointer identifies the selected repository's common
Git directory, and either Git considers the corresponding metadata prunable or that metadata is
already absent and the slot is older than the effective expiry. Without `--expire`, the additional
scan uses Git's effective `gc.worktreePruneExpire` value and default. Empty directories left below
the per-repository directory may also be removed. Unknown files, unrelated `.git` pointers, and
paths outside the physical worktree root are warned about and left untouched.

Prune reports are diagnostics on `stderr`; successful `stdout` is empty.

## Deliberately absent commands and options

The v1 surface has no `create`, `look`, worktree `move`, `lock`, `unlock`, or `repair` command. It
has no bare-repository mode, VCS selector, arbitrary worktree path, or shell-launch behavior. See
[ADR-0005](../../development/adr/0005-dedicated-worktree-root/) and
[ADR-0006](../../development/adr/0006-command-set-v1/) for the rationale.
