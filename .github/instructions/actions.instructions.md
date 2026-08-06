---
applyTo: ".github/workflows/*.yml,.github/workflows/*.yaml"
---

# GitHub Actions version pinning

- Action published by GitHub or a Marketplace **Verified creator** (e.g. anything under the
  [`actions`](https://github.com/actions) org) → pin to a **major version tag**: `owner/repo@vN`.
- Any other (untrusted/unverified) action → pin to a **full-length commit SHA**, with the version
  as a trailing comment: `owner/repo@<40-char-sha> # vX.Y.Z`. Resolve a tag to its SHA with
  `gh api repos/<owner>/<repo>/commits/<tag> --jq .sha`.
- Never use a branch name, `@main`/`@master`, or a minor/patch tag.
- `dtolnay/rust-toolchain` has no version tags — its ref selects the Rust toolchain itself
  (`@stable`, `@nightly`, `@1.89.0`, ...). Pin to the commit SHA at the tip of that ref, commented
  with the ref name instead of a version: `dtolnay/rust-toolchain@<sha> # stable`.
