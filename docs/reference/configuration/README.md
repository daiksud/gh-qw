---
type: reference
title: "Configuration reference"
description: "Normative configuration, root-resolution, and directory-layout contract for gh-qw."
resource: gh-qw
tags: [gh-qw, reference, configuration, filesystem]
timestamp: 2026-08-05
---

# Configuration reference

This page is the normative contract for `gh-qw` configuration and path resolution. See
[The gh-qw concept](../../concept/), [CLI reference](../cli/), and
[ADR-0007](../../development/adr/0007-configuration-sources/) for related context. The
[compatibility reference](../compatibility/) maps current behavior to ghq, `gh`, and Git.

## Default layout

With no configuration, main repositories and linked worktrees use separate roots:

```text
~/ghqw/
└── github.com/acme/widget/                    # ordinary main worktree
    ├── .git/
    └── ...

~/.config/ghqw/
└── config.toml

~/.local/share/ghqw/
└── worktrees/
    └── github.com/acme/widget/
        └── feature/login/                     # linked worktree
            ├── .git                           # Git pointer file
            └── ...
```

Every canonical identity has exactly one host, one owner, and one repository component. For
identity `<host>/<owner>/<repo>` and worktree slot `<branch>`:

```text
main path     = <repository-root>/<host>/<owner>/<repo>
worktree path = <worktree-root>/<host>/<owner>/<repo>/<branch>
```

The remote path must be exactly `/<owner>/<repo>`. Any additional component is invalid rather than
creating extra directory levels.

Slashes in a branch are path separators, so `feature/login` creates nested directories. Identity
and branch components are validated before joining, and the physical result must remain inside its
selected root. URL canonicalization, including local `file://` path resolution, is defined in
[Repository specifications and canonical identities](../cli/#repository-specifications-and-canonical-identities).

For a local `file://` URL with an empty or `localhost` authority, the physical file path must be
under a configured repository root and its relative path must be exactly
`<host>/<owner>/<repo>[.git]`. Those relative components—not `localhost`, a drive name, or
the rest of an absolute path—supply the identity. A non-local file authority accepts only
`file://<host>/<owner>/<repo>[.git]`. `list` and `migrate` resolve `file://` input this way; `get`
rejects it because it clones and synchronizes through `gh repo clone` and `gh repo sync`, which
never accept a `file://` URL (see the [CLI reference](../cli/#forms-accepted-by-get)).

Main repositories are ordinary non-bare Git clones. Linked worktrees remain ordinary Git
worktrees. `gh-qw` does not create a repository registry or write custom managed, identity,
adoption, or health metadata; see [ADR-0004](../../development/adr/0004-ghq-directory-convention/)
and [ADR-0005](../../development/adr/0005-dedicated-worktree-root/).

## Configuration file

The configuration file location is:

```text
$XDG_CONFIG_HOME/ghqw/config.toml
```

`gh-qw` uses `$XDG_CONFIG_HOME/ghqw/config.toml` only when `XDG_CONFIG_HOME` is set to an absolute
path. An unset, empty, or relative `XDG_CONFIG_HOME` is treated as if it were not set at all, and
`gh-qw` falls back to `~/.config/ghqw/config.toml`, where `~` is the current user's home directory
as resolved by the operating system. This rule is uniform across platforms, including Windows.

The file and all three keys are optional:

```toml
# One string:
root = "~/ghqw"

# Or an ordered array of strings:
# root = ["~/ghqw", "~/work/repos"]

worktree_root = "~/.local/share/ghqw/worktrees"

# Enable Herdr workspace integration by default for worktree add/remove and
# rm, without passing --herdr on every invocation (see the CLI reference's
# "Herdr workspace integration" section):
# herdr = true
```

### Schema

| Key | Type | Meaning |
| --- | --- | --- |
| `root` | string or non-empty array of strings | Ordered repository roots. The first normalized root is primary. |
| `worktree_root` | string | Root for linked worktrees created and managed by `gh-qw`. |
| `herdr` | boolean | Default for `--herdr`/`--no-herdr` on `worktree add`, `worktree remove`, and `rm` when neither flag is given. |

Only these three top-level keys are valid. Tables, unknown keys, duplicate keys, an empty root
array, empty path strings, mixed-type arrays, TOML parse errors, and type mismatches (including a
non-boolean `herdr`) are configuration errors and produce CLI status `2`. An unreadable file or
other filesystem I/O failure produces status `1`.

If the file is absent, all values default. If the file exists but a key is absent, only that key
defaults. The file is read as UTF-8 TOML; comments and ordinary TOML string escaping are allowed.

## Precedence

Each setting is resolved independently.

### Repository roots

From highest to lowest precedence:

1. non-empty `GHQW_ROOT`, split as the platform path-list;
2. `root` in the [configuration file](#configuration-file); and
3. `~/ghqw`.

`GHQW_ROOT` replaces the entire configured root list; it does not prepend or append to it.
Path-list separators are `:` on POSIX systems and `;` on Windows, using the operating system's
standard path-list parser. Empty path-list entries are invalid.

### Worktree root

From highest to lowest precedence:

1. non-empty `GHQW_WORKTREE_ROOT`;
2. `worktree_root` in the [configuration file](#configuration-file); and
3. `$XDG_DATA_HOME/ghqw/worktrees`, following the same absolute-only XDG rule as the configuration
   file above, otherwise `~/.local/share/ghqw/worktrees`.

The worktree setting is one path, not a path-list. `GHQW_ROOT` does not affect it.

### Herdr integration default

From highest to lowest precedence, before any command-line `--herdr`/`--no-herdr` flag, which
always wins over all three:

1. `GHQW_HERDR`, when set to a recognized boolean token (`1`/`true`/`yes`/`on` or
   `0`/`false`/`no`/`off`, case-insensitively; any other non-empty value is a configuration error,
   CLI status `2`);
2. `herdr` in the [configuration file](#configuration-file); and
3. disabled.

See the CLI reference's
[Herdr workspace integration](../cli/#herdr-workspace-integration) for the commands this affects,
the mutually exclusive `--herdr`/`--no-herdr` flags, and how enablement outside of a Herdr-managed
pane is handled.

## Multiple repository roots

Repository-root order is significant:

- the first normalized root is the **primary root**;
- discovery scans every root in order;
- new clones and migration destinations are created only in the primary root;
- `gh qw root` prints the primary root, while `gh qw root --all` prints every root in order; and
- an existing canonical identity in a secondary root is reused and updated there rather than
  cloned again into the primary root.

For read-only discovery, duplicate canonical identities are deduplicated and the earliest root
wins. Acquisition also selects the earliest existing copy. A destructive command selected by
identity refuses a duplicate identity as ambiguous instead of silently choosing one. A
`worktree` command without `-R` may operate on the exact physical repository identified from its
current main or linked worktree.

A configured root need not exist for read-only discovery; it contributes no repositories until it
exists. When an operation must create a destination, `gh-qw` creates the required primary-root
parents. It does not create secondary roots merely by reading configuration or running `list` or
`root`.

## Path normalization and validation

Every repository root and the worktree root passes through the same normalization pipeline before
it is used:

1. expand a leading `~` or `~/` to the current user's home directory; `~other-user` is rejected;
2. require an absolute path after expansion—relative paths are never resolved against the current
   directory;
3. clean redundant separators and lexical `.` components;
4. resolve symbolic links in the longest existing ancestor and append any not-yet-existing suffix;
5. normalize the filesystem volume representation; and
6. deduplicate physically equivalent paths while preserving the first occurrence.

Physical equivalence follows the target platform's path rules, including case-insensitive
comparison where the filesystem reports it. Resolving only the longest existing ancestor makes a
future root deterministic without requiring it to exist at startup.

The final worktree root must be disjoint from every repository root: it may not equal, contain, or
be physically contained by one. Containment is checked after symbolic-link resolution with
path-component boundaries, never by a raw string prefix. This prevents repository discovery from
walking linked worktrees and prevents worktree cleanup from reaching main repositories.

Every command repeats containment checks on its derived target before creating, copying, moving,
or removing files. A symlink introduced after startup cannot authorize escape from a configured
root. Invalid roots are configuration errors; a changed or unsafe runtime path is a safety error.

## Normal operation does not read ghq Git configuration

Outside legacy source discovery for `migrate`, `gh-qw` never reads:

- `ghq.root`;
- URL-specific `ghq.<url>.root`;
- `ghq.user`;
- `ghq.completeUser`;
- `ghq.defaultHost`;
- `ghq.vcs`; or
- any other `ghq.*` Git configuration.

Those settings do not alter normal roots, identity completion, clone transport, or VCS behavior,
and they are never imported into `config.toml`. Git's own repository, credential, transport, and
worktree configuration continues to apply when Git itself is invoked.

## Legacy ghq settings during migration

`gh qw migrate` with no directory reads legacy settings only to locate source repositories. Its
source-root algorithm is:

1. If non-empty `GHQ_ROOT` exists, split it as a platform path-list and use only those roots.
   Neither generic nor URL-specific Git configuration is consulted.
2. Otherwise, read every effective `ghq.root` value. Values are considered in Git's effective
   precedence order and deduplicated after physicalization.
3. If no generic value exists, use `~/ghq`.
4. When `GHQ_ROOT` is absent, append every URL-specific root matched by
   `ghq.<url>.root`, then physicalize and deduplicate while preserving the first occurrence.

Relative legacy values are resolved only as Git's `--path` handling defines them for source
discovery; they do not become valid relative `gh-qw` roots. Invalid or unreadable legacy entries
are warned about and skipped when other source roots remain usable. No discovered source is
persisted, and later commands return to the normal `GHQW_ROOT`/file/default precedence.

Bulk discovery treats only `<source-root>/<host>/<owner>/<repo>` as a repository identity.
Directories with additional path components do not define repository identities and are not
migrated as such.

The legacy variable is deliberately named `GHQ_ROOT`, not `GHQW_ROOT`. `GHQ_ROOT` has no effect on
normal `gh-qw` operation.

## Account cache

`get` and `worktree add` cache the gh account selected for each `<host>/<owner>` (see
[gh account selection](../cli/#gh-account-selection)). A valid cache entry avoids listing all
accounts again, while the cached account's token is still validated before use.

The cache file location is:

- `$XDG_CACHE_HOME/ghqw/accounts.json`, when `XDG_CACHE_HOME` is set to an absolute path; otherwise
- `~/.cache/ghqw/accounts.json`.

The account cache, the [configuration file](#configuration-file), and the
[worktree root](#worktree-root) all follow the same absolute-only XDG rule, but under different
variables: the cache is machine-written operational state produced by resolution, so it uses
`XDG_CACHE_HOME`, distinct from the user-authored configuration file's `XDG_CONFIG_HOME` and the
worktree data root's `XDG_DATA_HOME`.

The file is JSON, holds only `<host>/<owner>` to gh login mappings — never a token or other
secret — and is written with restrictive permissions (`0700` directory, `0600` file) on POSIX
platforms. A missing file means no choices have been cached. An unreadable or malformed file, an
invalid schema, or a failed cache update is an account-resolution error and prevents the network
operation. A cached login whose token is no longer available is removed before account listing
continues; failure to persist that removal is also an error. Delete the file to forget every
cached choice; delete or edit an entry to change one repository owner's cached account, keeping
the JSON schema valid.

## Relevant environment inherited from `gh` and Git

`GHQW_ROOT`, `GHQW_WORKTREE_ROOT`, and `GHQW_HERDR` are the only `gh-qw` configuration variables
(see [Herdr integration default](#herdr-integration-default) for the last one). There is no
`GHQW_CONFIG`, `GHQW_HOST`, `GHQW_LOOK`, or VCS-selection variable.

Every `gh` or Git subprocess `gh-qw` runs inherits the calling process's complete environment
unless a specific variable is deliberately overridden (currently only `GH_TOKEN`, when automatic
account selection resolves one — see [gh account selection](../cli/#gh-account-selection)).
`gh-qw` never clears or restricts the rest of the environment before spawning `gh` or Git, so any
variable either of them already documents — including the families below — reaches them exactly as
it would running standalone.

Standard tool variables can still affect the external services used by a command:

| Variable or family | Effect |
| --- | --- |
| `HOME` and the platform's user-profile settings | Affect operating-system home resolution and therefore `~`, and therefore the fallback location of every XDG-based path below when its respective XDG variable is unset, empty, or relative. |
| `XDG_CONFIG_HOME` | Relocates the [configuration file](#configuration-file) when set to an absolute path; it does not affect the worktree root or the account cache. |
| `XDG_DATA_HOME` | Relocates the default [worktree root](#worktree-root) when set to an absolute path; it does not affect the configuration file or the account cache, and it has no effect when `GHQW_WORKTREE_ROOT` or `worktree_root` is set. |
| `XDG_CACHE_HOME` | Relocates the [account cache](#account-cache) used by `get` and `worktree add`; it does not affect the configuration file or the repository/worktree roots. |
| `GH_HOST` | Selects the `gh` authentication host for bare `<repo>` completion and host API calls when no explicit host is present. |
| `GH_TOKEN`, `GITHUB_TOKEN` | When set, skip `get`'s and `worktree add`'s automatic gh [account selection](../cli/#gh-account-selection) entirely; that token authenticates every network operation, exactly as it would without `gh-qw`. |
| `GH_ENTERPRISE_TOKEN`, `GITHUB_ENTERPRISE_TOKEN` | May supply standard GitHub CLI/API authentication for the selected host. |
| `GH_CONFIG_DIR` | May relocate GitHub CLI's own authentication/configuration state; it does not relocate `gh-qw` configuration. |
| `GIT_SSH`, `GIT_SSH_COMMAND`, `GIT_ASKPASS`, `SSH_ASKPASS`, `GIT_TERMINAL_PROMPT` | Affect Git transport and credential prompting when inherited by Git subprocesses. |
| Git credential helpers and standard Git config-location variables | Continue to affect Git itself, but never define `gh-qw` roots or identity rules. |
| `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY` and lowercase equivalents | May affect HTTPS access by Git or GitHub API clients. |
| `GHQ_ROOT` | Used only for no-argument legacy migration source discovery as described above. |

`GH_REPO` does not replace `-R/--repo` and does not change current-directory repository
selection. Authentication variables cannot supply a missing canonical owner for non-bare
shorthand; only the authenticated login selected through `gh` is used.

## Examples

One primary root:

```toml
root = "~/ghqw"
worktree_root = "~/.local/share/ghqw/worktrees"
```

Ordered discovery roots:

```toml
root = [
  "~/ghqw",
  "/Volumes/code/repos",
]
worktree_root = "/Volumes/code/worktrees"
```

Temporary environment replacement on POSIX:

```text
GHQW_ROOT="$HOME/ghqw:$HOME/archive/repos" \
GHQW_WORKTREE_ROOT="$HOME/.local/share/ghqw/worktrees" \
gh qw root --all
```

The equivalent `GHQW_ROOT` list uses `;` on Windows. In every case, all paths must normalize to
absolute, physically disjoint roots.
