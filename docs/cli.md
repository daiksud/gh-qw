# CLI reference

```text
gh qw <command> [options]
```

Names are case-sensitive. Every command supports `-h`/`--help`; the root supports
`--version`. Help/version do not perform an operation.

## Results and errors

| Status | Meaning |
| --- | --- |
| `0` | Success, including an empty query result or a documented nonfatal skip. |
| `1` | Runtime failure, safety refusal, declined confirmation, or partial failure. |
| `2` | Invalid arguments, repository specification, selector, or configuration. |

Successful data alone goes to stdout. Paths are absolute and use `/` separators
on every platform; a path result is one line per completed item. Progress,
warnings, plans, prompts, and errors go to stderr. Completed batch results may
already have been printed when a later item fails. For batch `clone`, invalid
specifications take status `2` precedence over runtime status `1`.

Destructive confirmation reads the controlling terminal, defaults to no, and
fails safely when no terminal is available. Piped repository input is never a
confirmation response. A failed warning write prevents mutation; an output
failure after mutation reports the completed operation rather than undoing it.

Git/gh diagnostics use the diagnostic file descriptor directly when available.
Captured API output never becomes public result output. `--silent` suppresses
ordinary progress, not errors or result paths.

## Repository input

A canonical identity is `<host>/<owner>/<repo>`. A linked worktree adds `@<slot>`.
Transport details are separate from identity.

| Input to `clone` | Interpretation |
| --- | --- |
| `<repo>` | Complete owner using the authenticated login on `GH_HOST` or `github.com`. |
| `<owner>/<repo>` | HTTPS on `GH_HOST` or `github.com`. |
| `<host>[:<port>]/<owner>/<repo>` | HTTPS on the explicit host. |
| `http[s]://<host>/<owner>/<repo>[.git]` | Explicit HTTP(S) transport. |
| `ssh://[<user>@]<host>[:<port>]/<owner>/<repo>[.git]` | Explicit SSH transport. |
| `[<user>@]<host>:<owner>/<repo>[.git]` | SCP-like SSH; a slash after `:` is optional. |
| `.`, `..`, `./...`, `../...` | Resolve a unique identity beneath a configured root. |

An optional `@<branch>` after the repository selects the clone branch, not a
linked worktree. It takes precedence over `--branch`. The delimiter is the
first `@` in the repository component, not an SSH authority; subsequent `@`
characters and slashes belong to the branch. Relative paths may contain `@` in
parent names and must still resolve to exactly one identity.

Git validates literal refs with `check-ref-format --allow-onelevel`; checkout
history expressions are not expanded. Reject `@`, a leading `-`, and an invalid
ref. `HEAD` is accepted as a clone suffix but not as `--branch` or a worktree
branch/slot. Git-start failures and cancellation are runtime errors; invalid
refs are usage errors. Storage-slot restrictions are independent of ref syntax.

Identity parsing requires `/` separators and exactly one owner and repository.
Hosts are lowercase DNS names or IPv4; IPv6 and trailing DNS dots are rejected.
URL users and ports affect transport, not identity; passwords, queries,
fragments, escaped remote paths, trailing slashes, and extra path components are
rejected. Owner/repository components preserve case and allow ASCII letters,
digits, `.`, `_`, and `-`, excluding empty, dot/dot-dot, and dot-ended components.
One terminal `.git` is removed from identity, not from clone transport.

Relative identity input must physicalize inside a configured repository root
at exactly `<host>/<owner>/<repo>[.git]` depth. It is not a destination override.
For local `file://` URLs (empty authority or `localhost`), those relative path
components supply identity. A nonlocal file URL requires exactly
`file://<host>/<owner>/<repo>[.git]`, with no user or port. Local repository
inspection can resolve file URLs; `clone` rejects them as usage errors.

Network operations use native gh authentication for the selected host. See
[environment and authentication](configuration.md#git-and-github-cli-environment),
including the Enterprise-token trust boundary.

### Local selectors

Existing repositories are selected by exact `repo`, `owner/repo`, or full
`host/owner/repo` suffixes. `-R/--repo` accepts selectors, not paths or URLs.
Multiple matches are usage errors for mutating selection. `add --worktree` and
`remove --worktree` use an explicit `-R` first; otherwise they resolve the
discovered main repository from the current main or linked worktree. Outside
one, supply `-R`.

## clone

```text
gh qw clone [-p] [--shallow] [-b|--branch <branch>]
            [--no-recursive] [-s|--silent] [-P|--parallel]
            [--partial blobless|treeless] [<repo>...]
```

Acquire new repositories in the primary root using `gh repo clone`. Register
each successful clone before printing its absolute path. If the identity is
already registered or the destination already exists, fail without reusing,
updating, adopting, or overwriting it. Use `add` for an existing checkout.
A failed clone is never registered.

| Option | Effect |
| --- | --- |
| `-p` | Use SSH for shorthand/HTTPS input unless an HTTP(S) URL supplies a user; explicit SSH stays unchanged. |
| `--shallow` | New clone depth `1`. |
| `-b`, `--branch` | New clone's named branch with single-branch history. |
| `--no-recursive` | Do not initialize submodules recursively for a new clone. |
| `-s`, `--silent` | Suppress ordinary progress. |
| `-P`, `--parallel` | Up to six concurrent items; implies silent progress. |
| `--partial blobless` | New clone filter `blob:none`. |
| `--partial treeless` | New clone filter `tree:0`. |

An unsupported partial-clone mode is a usage error.

Positional inputs ignore stdin. Otherwise read one trimmed specification per
line from nonterminal stdin, skipping empty lines with no comment syntax.
Terminal stdin with no arguments is a usage error. Ordered mode stops at the
first failure; parallel mode attempts valid items and emits completion-order
paths. Syntax is checked before root resolution, discovery, or authentication.
Earlier completed items remain complete if a later item is invalid.

## add

```text
gh qw add [--move] <local-repository-path>
gh qw add --worktree [-R|--repo <repo>] [-b|-B] [--detach] [--orphan] [-f] <slot> [<commit-ish>]
```

By default, validate an existing nonbare main Git repository, derive its
canonical identity from its configured remote, and register its current
absolute path in the XDG location registry. The directory is not moved.
`--move` instead moves it into the configured primary root, delegates linked
worktree repair to Git, and then registers the destination. An existing
registration at the source moves to the destination with it. Never overwrite
another destination or a different registered location. Refuse movement when
another registered repository is physically nested inside the source. The
`--move` flag explicitly authorizes that movement; a registry or repair failure
after a completed move reports the resulting path and error.

Movement requires an ordinary nonbare main repository and derives its identity
from `origin` or the first configured remote. Revalidate the repository,
destination, registry inventory, and linked-worktree metadata before mutation.
Try an atomic rename first; across filesystems, copy files, directories, modes,
and symlinks, remove an incomplete copy on failure, and delete the source only
after a successful copy. Run `git worktree repair` with mapped internal and
unchanged external worktree paths from the new main repository. A repair failure
leaves the completed move in place and reports the paths needed for recovery.

`--worktree` selects a registered repository via `-R` or the current
directory, determines the deterministic slot path, and delegates creation
to Git. Git owns branch and worktree behavior; see the worktree-add details
below for the remaining flags. Both successful modes print the resulting
absolute path.

## forget

```text
gh qw forget <repository-selector>
```

Remove exactly one repository mapping from the XDG registry. Leave the actual
checkout, its Git metadata, branches, and linked worktrees untouched. A
forgotten directory is not automatically rediscovered as managed. Successful
stdout is empty. Forgetting remains available when the registered checkout is
missing, a symlink is dangling, or the location cannot otherwise be resolved.

## list

```text
gh qw list [-e|--exact] [-p|--full-path] [--unique] [--worktree [--gone]|--all] [<query>]
```

List explicitly registered main repositories by default. `--worktree`
lists only their linked worktrees; `--all` includes both main repositories
and linked worktrees. These mode flags are mutually exclusive. Worktree
registration comes from Git, not the location registry.
With `--worktree --gone`, include only attached linked worktrees whose local
branch has a configured, fully qualified upstream ref that is absent from the
local ref database. Do not fetch or infer remote state. `--gone` requires
`--worktree`; it cannot be combined with `--all`.
Match substrings against the non-host identity using smartcase:
lowercase queries ignore case; uppercase queries match case. A host-looking
first component followed by `/` restricts the host (`github.com/`, not bare
`github.com`). URL queries are canonicalized first.

`--exact` matches a case-sensitive identity suffix. With `--worktree` or
`--all`, `@slot` narrows the match; otherwise entries of the selected types
can match.

Default output is canonical identities. `--full-path` prints physical paths;
`--unique` prints the shortest unambiguous identity suffix among selected
entries, retaining linked slots. Those two flags are mutually exclusive.
The XDG registry is the sole source of managed-repository membership.
Missing registrations are never imported from configured roots. Sort emitted
strings bytewise. No matches succeeds with empty stdout.

Managed linked slots are decoded from the single directory name between
`worktrees` and `<repo>` (e.g. `feature%2Flogin` maps to `feature/login`).
External attached worktrees use their short branch; external
detached worktrees use their final path component. Unsafe or ambiguous slots
are rejected rather than treated as a managed target.

## root

```text
gh qw root [--all]
```

Print the physical primary root, or every root in precedence order with `--all`.
Do not create missing roots.

## remove

```text
gh qw remove [--dry-run] <repository-selector>
gh qw remove --worktree [-R|--repo <repo>] [-f|--force] <slot>
gh qw remove --worktree [-R|--repo <repo>] --gone
                    [-n|--dry-run] [-y|--yes] [-f|--force]
```

Repository mode shows the main repository and all registered linked worktrees
and asks once for confirmation. Remove linked worktrees through Git first,
then revalidate and remove the main directory. No implicit force is applied.

`--dry-run` performs discovery and safety checks and prints the plan without
prompting or changing anything. Successful stdout is empty. Refusals stop further
removal; errors identify already-removed and remaining paths. Verify both path
and registration absence before cleaning empty parents. A registered repository
outside configured roots may be removed, but its parent is an established
boundary and is never cleaned. Once the main directory is confirmed absent,
remove its registry entry even if later cleanup or progress output fails. The
removal is conditional on that same registered path, so a concurrent replacement
registration is preserved and reported as a partial failure. Refuse
whole-repository removal when another registered repository is physically nested
inside the main or any linked worktree scheduled for deletion, and recheck that
condition after confirmation. The same protection applies to single-worktree
removal.

Single-worktree mode verifies the linked slot's association and invokes Git.
It never removes the main worktree. Force supplies one level only; Git locks
that require stronger intervention remain errors. Verify path and registration
absence, then clean the empty slot directory, never the shared `worktrees`
root. Preserve nonempty directories and symlinks; reject replaced
identities or boundaries. Refuse deletion when another explicitly registered
repository is physically nested in the linked target, and re-read that managed
inventory immediately before mutation. Cleanup failures still report a
completed removal. Stdout is empty.

### Gone-upstream selection

`--gone` excludes a slot argument. Select registered linked worktrees attached
to local branches whose configured, fully qualified upstream ref is absent
from the local ref database. Use the attached branch, not the storage slot.
Exclude main/detached worktrees, branches without upstreams, and existing refs.
Never fetch; branches and commits remain after removal.

Validate every candidate and emit one stable slot-sorted plan. Keep current,
locked, prunable, unmanaged, misassociated, nested-registration, and dirty
worktrees; force permits only the dirty case and keeps it annotated in the plan.
Missing registered paths and symlink-resolved prefixes still protect nested
registrations. `--yes` skips confirmation only; dry-run stops after validation
and the plan, including when combined with yes.

Rebuild the full plan and repository inventory after confirmation (or with yes)
before deletion. Revalidate each target's slot/path, branch/HEAD/upstream,
lock/prune/dirty state, association, filesystem identity, and nested registrations
immediately before removal. Candidate-specific refusals/Git failures allow
independent candidates to continue. Changed shared boundaries/inventory, unknown
registration state, cancellation, and output failures stop the remainder.

No candidates succeeds. Declined confirmation, any kept candidate, or partial
failure returns `1`, including a dry-run with kept candidates. Invalid flag or
argument combinations return `2`.

### Linked-worktree creation

The path is always `<primary-root>/worktrees/<slot>/<repo>`, without host or
owner components. Git-valid slash-separated slot names are URL path-segment
escaped into a single directory component. Encoded slot directories are unique
across repositories; a pre-existing slot directory is a collision. Validate the
physical boundary and reserve the slot before mutation. `-f` never bypasses
these conditions.

Automatic mode delegates the literal branch to `git worktree add`: Git selects
existing local/remote candidates, honors `checkout.defaultRemote`, and rejects
unknown or ambiguous branches. With a start point and no local requested
branch, create it through `git branch` first. An existing local branch wins
regardless of a supplied start point.

`-b` creates; `-B` creates or resets. An explicit start point is resolved by Git.
Without one, query the GitHub default via `gh api`; when its local ref is absent,
run `gh repo sync` after destination validation and re-observe the local ref.
Existing-branch and explicit-start operations do not need an API lookup.

Named creation/reset uses `git branch` before `git worktree add`. If the branch
step succeeds but adding the worktree fails, leave the branch intact and report
the partial result. Never perform automatic forced retries.

`--detach` uses the branch as a slot and the start point as its checkout target,
or the branch itself when no start point is given. `--orphan` creates an unborn
branch and accepts no start point. `-b`, `-B`, detach, and orphan are mutually
exclusive; `-f` may accompany a mode and delegates one force flag to Git.
On success, print the new absolute worktree path.
