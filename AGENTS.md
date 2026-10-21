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

## Contribution guidance

Follow the [contribution guidelines](docs/development/contributing/README.md)
and the applicable instructions in `.github/instructions/`.
