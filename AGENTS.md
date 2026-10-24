# Agent instructions

## Product direction

Read [Purpose and scope](docs/purpose.md) before proposing features or
refactoring. gh-qw exists to reduce the effort of thinking about working
directories: preparing them, finding them again, and cleaning them up.
An explicit XDG identity-to-path registry is the only managed-repository set;
configured roots only provide placement defaults. Git owns worktree metadata.
Do not build a second registry of Git branches or worktrees.

For a change, identify the concrete user decision or manual step it removes,
or the current safety or failure-handling contract it protects. For internal
refactoring, explain how it makes that behavior simpler to maintain. Apply the
scope and decision criteria in the purpose document; more features or fewer
lines alone are not success. Do not add new configuration, bookkeeping, or
workflow ownership without a concrete benefit to that purpose.
During v0.x, do not add automatic imports or compatibility-only behavior.
Keep filesystem safety, failure reporting, and user data protections.

Keep the purpose document as the owner of product direction, README as its
user-facing summary, and these instructions as guidance for applying it.

## Current contract

Maintain only the current implementation and specification in
[docs/](docs/README.md). Remove obsolete APIs, aliases, tests, historical
comparisons, transition guides, and compatibility-only branches rather than
keeping parallel paths. Update the owning document directly when behavior changes.

Preserve current filesystem safety, identity checks, confirmation revalidation,
stdout/stderr contracts, and partial-failure behavior. Use external Git and gh
for their capabilities; do not duplicate authentication, HTTP transport, ref
validation, remote selection, or worktree metadata repair.

## Language and documentation

Write repository files, code comments, commits, issues, PRs, and reviews in
English. Reports to the user follow the conversation language. Use plain
Markdown, one owner per rule, short examples, and relative source links.
Do not create an ADR archive or repeat workflow implementations in prose.

## Changes and releases

Branch names are identifiers only. Choose PR types and reviewed labels from
actual impact; `feat` and `fix` concern user-visible gh-qw behavior, not tooling.
Keep the resulting merge commit consistent with the reviewed change. Follow
[contribution guidance](docs/contributing.md) for validation and release labels.

Merging and releasing are separate actions. Without an explicit release
request, do not push release tags, dispatch release workflows, or publish
GitHub Releases. Do not alter protections, permissions, or security settings
as a shortcut to merge.

## Review and validation

Prioritize actionable security, correctness, data-loss, and current-contract
failures. Check realistic failure modes, concurrency, maintainability, and
observable tests. Separate blocking defects from optional improvements.

Inspect existing branches and changes before editing; preserve unfinished work.
Run relevant normal/race/vet tests and inspect the final diff. Resolve review
findings, pass required CI on the final HEAD, merge normally, and verify CI on
the exact merged commit. Report validation or review limitations accurately.

## GitHub Actions

Pin every external action to its full 40-character commit SHA with its version
in a trailing comment. Do not reference external actions by tag or branch.
Preserve appropriate existing pins and Dependabot updates. Resolve changed
versions against the action's repository, not from memory.
