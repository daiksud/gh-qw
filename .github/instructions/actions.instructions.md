---
applyTo: ".github/workflows/*.yml,.github/workflows/*.yaml"
---

# GitHub Actions version pinning

- Pin **every** action, regardless of publisher, to a **full-length commit SHA**, with the version
  as a trailing comment: `owner/repo@<40-char-sha> # vX.Y.Z`. Resolve a tag to its SHA with
  `gh api repos/<owner>/<repo>/commits/<tag> --jq .sha`.
- There is no exemption for actions published by GitHub or a Marketplace Verified creator: this
  repository's `spec.actions.sha_pinning_required: true` (`.github/settings.yml`) is enforced by
  GitHub itself and rejects a major-tag reference outright — `actions/checkout@v7` and similar
  fail with "not allowed ... all actions must be pinned to a full-length commit SHA" regardless of
  publisher trust.
- Never use a branch name, `@main`/`@master`, or a minor/patch tag.
- `dtolnay/rust-toolchain` has no version tags — its ref selects the Rust toolchain itself
  (`@stable`, `@nightly`, `@1.89.0`, ...). Pin to the commit SHA at the tip of that ref, commented
  with the ref name instead of a version: `dtolnay/rust-toolchain@<sha> # stable`.
- `.github/dependabot.yml`'s `github-actions` ecosystem raises pull requests to update SHA-pinned
  actions automatically. Dependabot updates both the commit SHA and the trailing version comment
  together, so the `owner/repo@<40-char-sha> # vX.Y.Z` format above is preserved without manual
  re-resolution — accept its pull requests as-is rather than re-pinning by hand.
