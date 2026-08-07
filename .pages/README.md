# Documentation site

The Astro/Starlight source lives in this hidden directory while the canonical Markdown remains in
[`../docs/`](../docs/). Vite+ provides the Node-based task runner, Vitest, Oxlint, and Oxfmt; it
does not replace Astro or Starlight.

```console
$ cd .pages
$ vp install --frozen-lockfile
$ vpr dev
```

Run the complete local validation before pushing:

```console
$ vpr validate
```

The project uses Node.js with pnpm `11.20.0` as Vite+'s package-manager backend. Vite+ `0.2.8`
provides the task runner, Vitest, Oxlint, and Oxfmt, while Astro continues to build the Starlight
site. The task names live in `vite.config.ts`; `vpr` is Vite+'s shorthand for `vp run`.

Use `vpr dev`, `vpr build`, `vpr preview`, `vpr check`, `vpr check:links`, `vpr test`,
`vpr validate`, and `vpr format` for this site. Do not use `vp dev` or `vp build`: those are
Vite+'s built-in Vite commands, not the Astro tasks. `vp dev` starts a bare Vite server, and
`vp build` expects an `index.html` entry and does not build this Astro site.

`vpr format` is opt-in Oxfmt formatting; the validation check does not rewrite the existing source
tree.

The published site is [https://daiksud.github.io/gh-qw/](https://daiksud.github.io/gh-qw/).
`.github/workflows/pages.yml` deploys it after changes to `docs/` or `.pages/` reach `main`; a
maintainer can also start that workflow manually.
