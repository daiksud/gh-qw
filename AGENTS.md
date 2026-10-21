# Agent instructions

## Repository language

Use English for all content written for this repository, regardless of the
language used in the user's request. This applies to:

- GitHub issue and pull request titles, descriptions, comments, and reviews.
- Documentation, including READMEs, guides, references, and architecture decision
  records.
- Commit messages, release notes, code comments, and user-facing messages.

Translate non-English requirements into English before publishing them to the
repository or GitHub. Preserve their meaning, acceptance criteria, issue
references, command names, identifiers, and paths. Do not add parallel Japanese
versions of repository content.

## Change classification and releases

- Treat branch names only as identifiers. Do not infer a pull request's
  Conventional Commits type, labels, or release version from its branch name,
  or rename a branch just to correct classification.
- When creating or reviewing a pull request, check that its title's type and
  labels reflect the actual changes and their impact on gh-qw users. Correct
  the title and labels before merging. Version-affecting labels require
  judgment; the labeler automates only unambiguous path-based labels.
- Treat merging and releasing as separate actions. Unless the user explicitly
  requests a release, do not create or push release tags, dispatch release
  workflows, or publish GitHub Releases. When requested, use the documented
  manual release workflow with its required validation and build steps.

## Code review

When reviewing changes:

- Prioritize actionable security, correctness, backward-compatibility, and
  data-loss risks. Distinguish blocking defects from optional improvements.
- Check relevant tests and realistic failure modes, as well as performance,
  concurrency, maintainability, and architecture when the change warrants it.
- Make review comments specific and constructive: identify the affected code,
  explain the impact, and propose a practical correction. Avoid boilerplate and
  speculative edge cases that would not justify their maintenance cost.
- Apply the pull request type, label, and compatibility guidance above.

## GitHub Actions

When editing workflows in `.github/workflows/`:

- Pin **every external action** to its full 40-character commit SHA, regardless
  of publisher. Keep the version as a trailing comment:
  `owner/repo@<40-character-sha> # vX.Y.Z`.
- Never reference external actions by branch or version tag. The repository's
  `.github/settings.yml` requires SHA-pinned actions. Resolve a version tag with
  `gh api repos/<owner>/<repo>/commits/<tag> --jq .sha`.
- For actions without version tags (such as `dtolnay/rust-toolchain`), pin the
  selected ref's SHA and name that ref in the trailing comment.
- Dependabot updates pinned SHAs and their version comments; preserve its
  consistent updates rather than re-pinning them unnecessarily.

## Documentation

When editing documentation, including READMEs:

- Describe behavior and design rather than copying most of an implementation
  or workflow file into the documentation.
- Keep examples small, and prefer relative links to source files when readers
  need the complete implementation.
- If a short excerpt intentionally mirrors a source file, update it whenever
  the source changes so the two cannot drift.

## Contribution guidance

Follow the [contribution guidelines](docs/development/contributing/README.md)
for detailed procedures, and the relevant documentation in `docs/reference/`.
