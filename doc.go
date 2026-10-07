// Package garden turns a folder of markdown notes into a static knowledge
// garden: a site where links open notes in stacked panes, side by side, and
// every note lists the notes that link to it.
//
// The package can be used in two ways. [Build] does everything in one call,
// the same as the garden command:
//
//	cfg, err := garden.LoadConfig("garden.yaml")
//	if err != nil { ... }
//	_, stats, err := garden.Build(ctx, cfg)
//
// Or the steps can be separated, so a larger system can work with the link
// graph before (or instead of) writing a site. [Load] reads and renders every
// note in parallel and computes backlinks, all in memory:
//
//	g, err := garden.Load(ctx, cfg)
//	for _, n := range g.Notes {
//		fmt.Println(n.ID, len(n.Links), len(g.Backlinks(n.ID)))
//	}
//	stats, err := g.Write(ctx, "dist")
//
// Notes are read from Config.FS when it is set (an embed.FS, an
// fstest.MapFS, a zip archive), otherwise from the Config.Vault directory.
//
// # Links
//
// Standard markdown links ([label](note.md#heading)) and Obsidian wikilinks
// ([[Note]], [[Note|label]], [[Note#Heading]], ![[image.png]]) are both
// supported. A link target is resolved relative to the linking note, then
// from the vault root, then by file name anywhere in the vault, so notes can
// move between folders without breaking links.
//
// Heading IDs follow Python-Markdown's slug rules, so #anchor links written
// for an MkDocs site keep working.
//
// # Output
//
// [Garden.Write] produces plain static files: an app shell (index.html,
// garden.css, garden.js), the note data split into shards that the browser
// loads on demand, optional per-note HTML pages, and the assets notes
// reference. Files whose content hasn't changed are not rewritten.
package garden
