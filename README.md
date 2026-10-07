# garden

[![Go Reference](https://pkg.go.dev/badge/github.com/zoravur/garden.svg)](https://pkg.go.dev/github.com/zoravur/garden)
[![CI](https://github.com/zoravur/garden/actions/workflows/ci.yml/badge.svg)](https://github.com/zoravur/garden/actions/workflows/ci.yml)

Turn a folder of markdown notes into a knowledge garden: a static site where links open notes in **stacked panes**, side by side, in the style of [Andy Matuschak's working notes](https://notes.andymatuschak.org/). Every note lists its **backlinks**, and hovering a link shows a **preview** of the linked note, scrolled to the linked section.

- A single Go binary, also usable as a library.
- Reads Obsidian vaults (`[[wikilinks]]`) and MkDocs sites (`[text](note.md)`, admonitions, `mkdocs.yml` nav).
- Parses and renders notes in parallel: 636 notes in about 0.3 s.
- Outputs plain static files that you can host on GitHub Pages or anywhere else.
- Only rewrites output files that changed.

## Install

```bash
go install github.com/zoravur/garden/cmd/garden@latest
```

Requires Go 1.22 or newer.

## Use

```bash
garden init my-garden      # garden.yaml and a starter note
cd my-garden
garden serve               # http://localhost:8080, rebuilds when a note changes
garden build               # writes the site to dist/
```

To publish on GitHub Pages:

1. Copy [`examples/github-pages/publish.yml`](examples/github-pages/publish.yml) into your notes repo as `.github/workflows/publish.yml`.
2. Turn on Pages under **Settings → Pages → Source: GitHub Actions**.

Every push to `main` then rebuilds and publishes the site.

## Configuration

`garden.yaml` sits next to your notes. Relative paths resolve from the file's folder.

```yaml
title: My Garden
description: Working notes, linked together.
vault: vault                  # folder of .md files
out: dist
home: index                   # note opened first (its path without .md)
nav: folders                  # contents tree from folders, or mkdocs:path/to/mkdocs.yml
exclude: [templates/**, "*.excalidraw.md"]
public_only: false            # true: publish only notes with `public: true`
heading_shift: 0              # 1 turns "# X" into <h2> (MkDocs toc baselevel: 2)
site_prefix: /my-site/        # strip this absolute prefix from links in raw HTML
source_url: https://github.com/me/notes/blob/main/   # adds a "Source" link to each note
static_pages: true            # also write notes/<id>.html for search engines and no-JS readers
max_asset_mb: 0               # skip larger files (0 = no limit)...
asset_fallback_url: ""        # ...and link them from here instead
theme: theme                  # folder of overrides (garden.css, garden.js, index.html, note.html)
```

With `public_only: true`, private notes never reach the output. A link to a private note becomes plain text, so neither the note's name nor its path appears anywhere on the site.

## What it understands

| Markdown | Notes |
|---|---|
| `[label](other-note.md#heading)` | Resolved relative to the note, then from the vault root, then **by file name anywhere in the vault**. Notes can move between folders without breaking links. |
| `[[Note]]`, `[[Note\|label]]`, `[[Note#Heading]]`, `![[image.png]]` | Obsidian wikilinks. Links inside code are left alone. |
| `!!! note "Title"`, `??? warning`, `???+ tip` | MkDocs Material admonitions. The `???` forms are collapsible. |
| Frontmatter `title`, `date`, `public` | Without a `title`, the leading `#` heading is used, then the file name. |
| GFM tables, task lists, footnotes, definition lists | Rendered by [goldmark](https://github.com/yuin/goldmark). |
| Fenced code with a language | Highlighted in the browser. |

Heading IDs follow MkDocs' slug rules, so existing `#section` links keep working. `garden build -report broken.txt` lists links that point nowhere.

## As a library

The `garden` package does everything the command does, and exposes the link graph for use in larger systems: search indexes, crawlers, a graph database, or a server that renders notes on demand.

```go
import "github.com/zoravur/garden"

// One call, like `garden build`:
cfg, _ := garden.LoadConfig("garden.yaml")
g, stats, err := garden.Build(ctx, cfg)

// Or load into memory and work with the graph:
g, err := garden.Load(ctx, &garden.Config{FS: os.DirFS("notes")})
for _, n := range g.Notes {
	fmt.Println(n.ID, n.Title, len(n.Links), len(g.Backlinks(n.ID)))
}
id, anchor, ok := g.Resolve("index", "backlinks.md#why")  // same rules as rendering
stats, err := g.Write(ctx, "dist")                          // write the site later, or never
```

- **Notes come from any `fs.FS`:** a directory, an `embed.FS`, a zip archive, or an in-memory `fstest.MapFS`.
- **`Load` and `Write` are separate.** You can index notes or validate links without producing a site, and loading honours context cancellation.
- **What each note carries:** a `Note` has its rendered HTML, frontmatter, excerpt and outgoing `Link`s. Each link records the heading it sits under and the sentence around it.
- **Backlinks:** `Garden.Backlinks` returns the inverted edges, sorted.

Full API docs are at [pkg.go.dev](https://pkg.go.dev/github.com/zoravur/garden).

## Theming

All colours, fonts and pane sizes are CSS variables at the top of [`web/garden.css`](web/garden.css). To change them without forking, put any of `garden.css`, `garden.js`, `index.html` or `note.html` in a `theme/` folder next to `garden.yaml`. `garden.DefaultTheme()` returns the built-in files as a starting point.

## Output

```
dist/
  index.html, garden.css, garden.js   the app shell
  data/index.json                     titles, excerpts, contents tree
  data/shard-NNN.json                 note bodies and backlinks, fetched on demand
  notes/<id>.html                     one plain page per note
  <assets>                            images and files the notes use
```

The URL fragment stores the open panes, separated by `~`: `index.html#ideas.2Fbacklinks~index`.

## Examples

- [`examples/starter`](examples/starter): a four-note Obsidian-style vault.
- [`examples/blue-book`](examples/blue-book): config for [Lyz's Blue Book](https://github.com/lyz-code/blue-book), a 636-note MkDocs garden. It reads the MkDocs navigation and leaves out the auto-generated newsletter, which links to nearly every note and would bury the real backlinks.

## License

MIT
