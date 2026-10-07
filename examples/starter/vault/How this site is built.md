`garden build` reads every `.md` file in the vault in parallel, resolves links by file name, computes [[ideas/Backlinks|backlinks]], and writes a static site you can host anywhere.

```bash
garden build -c garden.yaml   # writes dist/
garden serve -c garden.yaml   # local preview, rebuilds on save
```

??? note "What gets published"
    Everything in the vault, unless `public_only: true` is set, in which case only notes with `public: true` in their frontmatter. Links to unpublished notes show as plain dimmed text, so nothing private leaks through a URL.
