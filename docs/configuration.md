# Configuration

## File and settings

Read `$XDG_CONFIG_HOME/ghqw/config.toml` when `XDG_CONFIG_HOME` is absolute;
otherwise read `~/.config/ghqw/config.toml`. This rule applies on every platform.
The file and `root` setting are optional.

```toml
root = ["~/ghqw", "~/work/repos"]
```

| Key | Type | Meaning |
| --- | --- | --- |
| `root` | String or nonempty string array | Ordered repository roots; the first is primary. |

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

The repository roots use the first nonempty value, highest priority first:

| Environment | File | Default |
| --- | --- | --- |
| `GHQW_ROOT` | `root` | `~/ghqw` |

`GHQW_ROOT` replaces the entire ordered list; `:` separates entries on POSIX
and `;` on Windows. Empty entries are invalid. The first root is primary.
The linked-worktree root is always `<primary-root>/worktrees`; it has no
independent configuration or environment override.

## Layout and physical boundaries

```text
main:   <repository-root>/<host>/<owner>/<repo>
linked: <primary-root>/worktrees/<unique-slot>/<repo>
```

Roots expand a leading `~` or `~/`, require an absolute path, normalize path
components and volumes, resolve existing symlink prefixes, and append missing
suffixes. `~other-user` is invalid. Equivalent roots are deduplicated in order.
The worktree root is reserved directly below the primary repository root.
Symlinks cannot redirect that directory. Other configured roots must not
overlap it, and the host name `worktrees` is reserved in the main layout.

The XDG registry, not the directory layout, determines which repositories are
managed. Configured roots determine default destinations for `clone` and
`add --move`; a checkout under a configured root is not registered implicitly.
Reading configuration or listing roots does not create missing directories.

Worktree names are derived from `<host>-<owner>-<repo>-<branch>` and all
punctuation and separators are replaced with `-` (for example,
`github.com/acme/widget` on `feature/login` becomes
`github-com-acme-widget-feature-login`). Git branches remain unchanged.
A name already taken by any file, directory, or worktree is skipped: the first
available `-1`, `-2`, and subsequent suffix is reserved atomically. The
actual directory name, including any numeric suffix, identifies the worktree
when listing or removing it, independently of later branch switches. Names
are resolved from Git's registered worktree paths; no additional registry
is maintained.

The unique name is a single directory component, even if the Git branch
contains slashes. Existing paths are not adopted, overwritten, or renamed
automatically. An existing symlink is also considered occupied.

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
