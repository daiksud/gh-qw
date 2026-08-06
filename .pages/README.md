# Documentation site

The Astro/Starlight source lives in this hidden directory while the canonical Markdown remains in
[`../docs/`](../docs/).

```console
$ cd .pages
$ bun ci
$ bun run dev
```

Run the complete local validation before pushing:

```console
$ bun run validate
```

The project is intentionally Bun-only. `bunfig.toml` aliases Node shebangs to Bun so Astro and its
tooling use the same runtime locally and in CI.
