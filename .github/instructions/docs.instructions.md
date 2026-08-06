---
applyTo: "docs/**/*.md,README.md,CHANGELOG.md"
---

# Documentation: don't duplicate source code

- Don't paste most of a real source file's contents into documentation verbatim — docs should
  describe behavior and design, not duplicate the implementation.
- Small snippets are fine: short examples, sample CLI usage/output, a single function signature, or
  a minimal illustrative config block.
- If a doc intentionally mirrors part of a real file (for example, an embedded copy of a workflow
  YAML), keep it short and update it whenever the real file changes so the two don't drift.
- Prefer a relative link to the real file over inlining it when the full contents matter.
