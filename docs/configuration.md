# Configuration

## File and settings

Read `$XDG_CONFIG_HOME/ghqw/config.toml` when `XDG_CONFIG_HOME` is absolute;
otherwise read `~/.config/ghqw/config.toml`. This rule applies on every platform.
The file and both keys are optional.

```toml
root = ["~/ghqw", "~/work/repos"]
worktree_root = "~/.local/share/ghqw/worktrees"
```

| Key | Type | Meaning |
| --- | --- | --- |
| `root` | String or nonempty string array | Ordered repository roots; the first is primary. |
| `worktree_root` | String | Root for linked worktree slots. |

Unknown keys or tables, duplicates, empty values or arrays, mixed array types,
and invalid TOML are configuration errors (status `2`). File I/O failures are
runtime errors (status `1`). An absent file uses defaults.

## Location registry

The managed repository set is defined solely by explicit registrations in
`$XDG_DATA_HOME/ghqw/repositories.toml` when `XDG_DATA_HOME` is absolute,
or `~/.local/share/ghqw/repositories.toml` otherwise. This XDG data file
stores only canonical repository identity-to-absolute-path mappings and is
separate from the user-owned `config.toml`.

`add <path>` registers an existing local repository; `add --move` moves
it to the configured layout first. `forget <repo>` only unregisters. A
missing file represents an empty managed set. No first-run import, root scan,
or v0.x backward compatibility migration runs automatically. A structurally
valid mapping remains forgettable even when its checkout path is missing or no
longer resolvable; live path checks occur in commands that need the checkout.

## Precedence

Each setting is resolved independently, highest priority first:

| Setting | Environment | File | Default |
| --- | --- | --- | --- |
| Repository roots | Nonempty `GHQW_ROOT` | `root` | `~/ghqw` |
| Worktree root | Nonempty `GHQW_WORKTREE_ROOT` | `worktree_root` | `$XDG_DATA_HOME/ghqw/worktrees` when XDG is absolute; otherwise `~/.local/share/ghqw/worktrees` |

`GHQW_ROOT` replaces the entire list. Its separator is `:` on POSIX and `;` on
Windows; empty entries are invalid. The worktree root is one path, not a list.

## Layout and physical boundaries

```text
main:   <repository-root>/<host>/<owner>/<repo>
linked: <worktree-root>/<host>/<owner>/<repo>/<slot>
```

Roots expand a leading `~` or `~/`, require an absolute path, normalize path
components and volumes, resolve existing symlink prefixes, and append missing
suffixes. `~other-user` is invalid. Equivalent roots are deduplicated in order.
The worktree root must neither equal, contain, nor lie within any repository
root after physical resolution.

The XDG registry, not the directory layout, determines which repositories are
managed. Configured roots determine default destinations for `clone` and
`add --move`; a checkout under a configured root is not registered implicitly.
Reading configuration or listing roots does not create missing directories.

Slots may contain `/`, but must remain beneath their repository's worktree
directory. Absolute slots, empty or dot components, dot-ended components, and
prefix collisions such as `feat` and `feat/x` are rejected. A slot is a storage
path, not a promise about the currently checked-out branch.

Targets are checked again before mutation against the established boundary.
A replaced root or symlink cannot authorize a new destination. Cleanup never
removes the managed root or follows an unrelated path.

## Git and GitHub CLI environment

Git and gh inherit the caller's environment. gh-qw does not inject tokens,
switch accounts, read authentication files, or maintain an account cache.

`GH_HOST` selects the host for bare names and hostless `owner/repo` inputs;
explicit hosts take precedence and the default is `github.com`. API requests
pass `--hostname`. Local `gh repo sync` sets its child's `GH_HOST` to the
selected repository host; all token variables remain unchanged.

`GH_TOKEN`, `GITHUB_TOKEN`, `GH_ENTERPRISE_TOKEN`, and
`GITHUB_ENTERPRISE_TOKEN` are interpreted by gh. **`GH_HOST` is not a token
allowlist.** An explicitly supplied Enterprise host can receive an Enterprise
environment token. Use trusted repository input, including stdin batches.

`GH_PATH` selects the gh executable; an unset or empty value searches `PATH`.
`GH_CONFIG_DIR` affects gh, not this configuration file. Git configuration,
credential helpers, SSH settings, and proxies retain their native effects.
`GH_REPO` does not replace gh-qw's `-R` selector or current-directory discovery.

## Sources for `migrate`

Only no-argument `migrate` reads ghq source settings:

1. Nonempty `GHQ_ROOT` supplies a platform path-list and excludes Git-config sources.
2. Otherwise, effective `ghq.root` values supply roots; an empty result uses `~/ghq`.
3. Without `GHQ_ROOT`, append URL-specific `ghq.<url>.root` values.

Source roots are physicalized and deduplicated in order. Git's `--path` rules
apply to values read through Git. Invalid/unreadable roots are warned about and
skipped when usable roots remain. None of these values is persisted or changes
normal gh-qw root selection. See [migrate](cli.md#migrate) for movement rules.
