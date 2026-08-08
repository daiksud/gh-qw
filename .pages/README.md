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

The homepage is authored in `../docs/README.mdx` with the Astro components `Hero`, `Install`,
`CardGrid`, `Card`, `CardTitle`, and `CardIcon`. Presentation-only props such as `CardGrid`'s
boolean `stagger` are allowed, but reader-facing values such as titles, descriptions, icons, and
links must remain Markdown children. Do not add import statements to the MDX file. GitHub renders
the source directly, strips component tags and their attributes, and displays import statements as
ordinary text. The build injects the component imports through
`.pages/src/plugins/mdx-auto-import.ts`, so the source remains readable on GitHub.

When a component contains block Markdown, leave a blank line after its opening tag. This is
required for GitHub's Markdown parser to treat the following content as Markdown instead of literal
text inside an HTML block.

The published site is [https://daiksud.github.io/gh-qw/](https://daiksud.github.io/gh-qw/).
`.github/workflows/pages.yml` deploys it after changes to `docs/` or `.pages/` reach `main`; a
maintainer can also start that workflow manually.
