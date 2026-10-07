package garden

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

func testVault() fstest.MapFS {
	return fstest.MapFS{
		"index.md": file("---\ntitle: Home\n---\n\nStart with [[Evergreen notes]] or [backlinks](backlinks.md#why-they-matter).\n"),
		"Evergreen notes.md": file("# Evergreen notes\n\nThey compound through [[ideas/backlinks|backlinks]].\n\n" +
			"```go\n// [[not a link]]\n```\n\nAnd `[[not a link either]]`.\n"),
		"ideas/backlinks.md": file("---\ntitle: Backlinks\ndate: 2026-10-07\n---\n\nA link seen from the other end.\n\n" +
			"## Why they matter\n\nSee [[Evergreen notes]] and [a missing note](nowhere.md).\n\n![diagram](diagram.png)\n"),
		"ideas/diagram.png":     file("png"),
		"drafts/secret plan.md": file("---\npublic: false\n---\nHidden.\n"),
		".obsidian/app.json":    file("{}"),
	}
}

func load(t *testing.T, cfg Config) *Garden {
	t.Helper()
	g, err := Load(context.Background(), &cfg)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestLoadResolvesLinksAndBacklinks(t *testing.T) {
	g := load(t, Config{FS: testVault()})

	if got, want := len(g.Notes), 4; got != want {
		t.Fatalf("published %d notes, want %d (dot-folders skipped)", got, want)
	}
	home, ok := g.Note("index")
	if !ok || home.Title != "Home" {
		t.Fatalf("index note: %+v", home)
	}
	var targets []string
	for _, l := range home.Links {
		targets = append(targets, l.Target+"#"+l.Anchor)
	}
	// "backlinks.md" lives in ideas/ and is found by file name.
	if got, want := strings.Join(targets, " "), "Evergreen notes# ideas/backlinks#why-they-matter"; got != want {
		t.Errorf("index links = %q, want %q", got, want)
	}

	ev, _ := g.Note("Evergreen notes")
	if ev.Title != "Evergreen notes" || strings.Contains(ev.HTML, "<h1") {
		t.Errorf("title should come from the leading heading, which is then dropped: %q / %s", ev.Title, ev.HTML)
	}
	if len(ev.Links) != 1 || strings.Count(ev.HTML, "[[not a link") != 2 {
		t.Errorf("wikilinks in code must be left alone: links=%v html=%s", ev.Links, ev.HTML)
	}

	back := g.Backlinks("Evergreen notes")
	if len(back) != 2 || back[0].Source != "ideas/backlinks" || back[1].Source != "index" {
		t.Fatalf("backlinks sorted by title: %+v", back)
	}
	if r := back[0].Refs[0]; r.Section != "Why they matter" || !strings.Contains(r.Context, "See Evergreen notes") {
		t.Errorf("backlink context: %+v", r)
	}

	bl, _ := g.Note("ideas/backlinks")
	if !strings.Contains(bl.HTML, `id="why-they-matter"`) {
		t.Error("heading ids should use mkdocs slugs")
	}
	if len(bl.Assets) != 1 || bl.Assets[0] != "ideas/diagram.png" {
		t.Errorf("assets = %v", bl.Assets)
	}
	if got := g.Broken["nowhere.md"]; len(got) != 1 || got[0] != "ideas/backlinks" {
		t.Errorf("broken = %v", g.Broken)
	}
	if id, anchor, ok := g.Resolve("index", "backlinks.md#why-they-matter"); !ok || id != "ideas/backlinks" || anchor != "why-they-matter" {
		t.Errorf("Resolve = %q %q %v", id, anchor, ok)
	}
}

func TestPublicOnlyHidesPrivateNotes(t *testing.T) {
	fsys := fstest.MapFS{
		"index.md":       file("---\npublic: true\n---\nSee [[Secret plan]] and [[Open note]].\n"),
		"Open note.md":   file("---\npublic: true\n---\nOpen.\n"),
		"Secret plan.md": file("Not public.\n"),
	}
	g := load(t, Config{FS: fsys, PublicOnly: true})
	if len(g.Notes) != 2 {
		t.Fatalf("published %d notes, want 2", len(g.Notes))
	}
	home, _ := g.Note("index")
	if strings.Contains(home.HTML, "Secret plan.html") || strings.Contains(home.HTML, `title="`) {
		t.Errorf("a link to a private note must not reveal it: %s", home.HTML)
	}
	if !strings.Contains(home.HTML, "Secret plan") {
		t.Error("the link text should remain as plain text")
	}
	if len(g.Broken) != 0 {
		t.Errorf("private links are not broken links: %v", g.Broken)
	}
}

func TestAdmonitions(t *testing.T) {
	g := load(t, Config{FS: fstest.MapFS{
		"index.md": file("!!! warning \"Careful\"\n    Body with *emphasis*.\n\n??? note\n    Folded.\n\nAfter.\n"),
	}})
	h := g.Notes[0].HTML
	for _, want := range []string{
		`<div class="admonition warning">`, "Careful", "<em>emphasis</em>",
		`<details class="admonition note">`, "Folded.", "<p>After.</p>",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("missing %q in\n%s", want, h)
		}
	}
}

func TestSlugifyMatchesMkdocs(t *testing.T) {
	for in, want := range map[string]string{
		"Resource types and it's aliases": "resource-types-and-its-aliases",
		"Mejores películas de 2025":       "mejores-peliculas-de-2025",
		"  C++ / Go: notes  ":             "c-go-notes",
	} {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWriteIsIncremental(t *testing.T) {
	out := t.TempDir()
	g := load(t, Config{FS: testVault(), Title: "Test"})
	st, err := g.Write(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"index.html", "garden.js", "garden.css", "data/index.json", "notes/ideas/backlinks.html", "ideas/diagram.png"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("missing output %s", f)
		}
	}
	var idx struct {
		Home  string           `json:"home"`
		Notes map[string][]any `json:"notes"`
	}
	b, _ := os.ReadFile(filepath.Join(out, "data/index.json"))
	if err := json.Unmarshal(b, &idx); err != nil || idx.Home != "index" || len(idx.Notes) != 4 {
		t.Errorf("index.json: %v %+v", err, idx)
	}
	if st.Written == 0 {
		t.Fatal("first write wrote nothing")
	}
	st, err = g.Write(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	if st.Written != 0 {
		t.Errorf("second write rewrote %d unchanged files", st.Written)
	}
}

func TestLoadHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Load(ctx, &Config{FS: testVault()}); err == nil {
		t.Error("expected an error from a cancelled context")
	}
}

func TestHomeIsGeneratedWhenBlankAndNoIndex(t *testing.T) {
	fsys := fstest.MapFS{
		"Hub.md":          file("The centre."),
		"a.md":            file("See [[Hub]]."),
		"b.md":            file("See [[Hub]] and [[a]]."),
		"c.md":            file("See [[Hub]], [[a]] and [[b]]."),
		"projects/one.md": file("Part of [[Hub]]."),
	}
	g := load(t, Config{FS: fsys, Title: "My Garden"})
	home, ok := g.Note(g.Config.Home)
	if !ok || !home.Generated || home.ID != "index" || home.Title != "My Garden" {
		t.Fatalf("expected a generated index, got %+v", home)
	}
	for _, want := range []string{`id="most-linked"`, `data-id="Hub"`, `4 links`, `id="all-notes"`, `data-id="projects/one"`} {
		if !strings.Contains(home.HTML, want) {
			t.Errorf("index is missing %q:\n%s", want, home.HTML)
		}
	}
	if len(home.Links) != 0 {
		t.Error("the generated index must not add backlinks to every note")
	}
	for _, b := range g.Backlinks("Hub") {
		if b.Source == "index" {
			t.Error("Hub lists the generated index as a backlink")
		}
	}

	// A root index.md is used as-is when home is blank.
	fsys["index.md"] = file("# Welcome\n\nHand-written.")
	g = load(t, Config{FS: fsys})
	if n, _ := g.Note(g.Config.Home); n.Generated || n.Title != "Welcome" {
		t.Errorf("blank home should use index.md, got %+v", n)
	}

	// An explicit home that doesn't exist is an error, not a silent fallback.
	if _, err := Load(context.Background(), &Config{FS: fsys, Home: "nope"}); err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Errorf("expected a missing-home error, got %v", err)
	}
}

func TestEmptyVaultStillHasAHomePage(t *testing.T) {
	g := load(t, Config{FS: fstest.MapFS{}})
	if len(g.Notes) != 1 || !g.Notes[0].Generated {
		t.Fatalf("notes = %+v", g.Notes)
	}
	if _, err := g.Write(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
}
