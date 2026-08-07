# Documentation site

The Astro/Starlight source lives in this hidden directory while the canonical Markdown remains in
[`../docs/`](../docs/). Vite+ provides the Node-based task runner, Vitest, Oxlint, and Oxfmt; it
does not replace Astro or Starlight.

```console
$ cd .pages
$ pnpm install --frozen-lockfile
$ pnpm run dev
```

Run the complete local validation before pushing:

```console
$ pnpm run validate
```

The project uses Node.js with pnpm `11.20.0`. Vite+ `0.2.8` runs the site commands and Vitest
tests, while Astro continues to build the Starlight site. `pnpm run format` is available for
opt-in Oxfmt formatting; the validation check does not rewrite the existing source tree.
