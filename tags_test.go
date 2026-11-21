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

func TestTags(t *testing.T) {
	g := load(t, Config{FS: fstest.MapFS{
		"note.md": file("---\ntags: [Fm, \"#by/claude\"]\n---\n#stub #By/claude\n\n" +
			"Text with #inline, C#sharp, #123, `#code`, [link #x](other.md), http://x.com/#frag and #trail/.\n\n" +
			"```\n#not-a-tag\n```\n"),
		"titled.md": file("#stub\n\n# Real title\n\nBody.\n"),
		"lead.md":   file("#a #b\nFirst words.\n"),
		"other.md":  file("Other.\n"),
	}})

	n, _ := g.Note("note")
	if got, want := strings.Join(n.Tags, " "), "by/claude Fm inline stub trail"; got != want {
		t.Errorf("tags = %q, want %q", got, want)
	}
	if got := strings.Join(n.metaTags, " "); got != "Fm" {
		t.Errorf("frontmatter tags not in the body = %q, want Fm", got)
	}
	for _, want := range []string{
		`<p class="tag-line"><a class="tag" href="index.html?q=tag%3A%23stub" data-tag="stub">#stub</a>`,
		`data-tag="By/claude">#By/claude</a>`,
		`C#sharp, #123, <code>#code</code>`,
		`class="internal">link #x</a>`,
		`http://x.com/#frag</a>`,
		`data-tag="trail">#trail</a>/.`,
		"#not-a-tag\n</code>",
	} {
		if !strings.Contains(n.HTML, want) {
			t.Errorf("html lacks %s:\n%s", want, n.HTML)
		}
	}
	if n.Excerpt != "Text with #inline, C#sharp, #123, #code, link #x, http://x.com/#frag and #trail/." {
		t.Errorf("a line of tags isn't the excerpt: %q", n.Excerpt)
	}
	if !strings.HasPrefix(n.Text, "#stub #By/claude\n") {
		t.Errorf("text keeps tags for search: %q", n.Text)
	}

	if ti, _ := g.Note("titled"); ti.Title != "Real title" || strings.Contains(ti.HTML, "<h1") {
		t.Errorf("a heading under a line of tags is the title: %q %s", ti.Title, ti.HTML)
	}
	lead, _ := g.Note("lead")
	if lead.Excerpt != "First words." || !strings.Contains(lead.HTML, "#b</a><br>") {
		t.Errorf("tags on the first line of a paragraph: excerpt %q, html %s", lead.Excerpt, lead.HTML)
	}

	out := t.TempDir()
	if _, err := g.Write(context.Background(), out); err != nil {
		t.Fatal(err)
	}
	var idx struct {
		Notes map[string][]any `json:"notes"`
	}
	b, _ := os.ReadFile(filepath.Join(out, "data/index.json"))
	if err := json.Unmarshal(b, &idx); err != nil {
		t.Fatal(err)
	}
	if e := idx.Notes["note"]; len(e) != 5 {
		t.Errorf("index entry should end with the tags: %v", e)
	}
	if e := idx.Notes["other"]; len(e) != 4 {
		t.Errorf("untagged index entry: %v", e)
	}
	page, _ := os.ReadFile(filepath.Join(out, "notes/note.html"))
	if !strings.Contains(string(page), `<div class="pane-meta"><a class="tag" href="index.html?q=tag%3A%23Fm">#Fm</a></div>`) {
		t.Errorf("static page shows frontmatter tags:\n%s", page)
	}
}

func TestFrontmatterTagForms(t *testing.T) {
	for _, c := range []struct {
		fm   map[string]any
		want string
	}{
		{map[string]any{"tags": "a, b c"}, "a b c"},
		{map[string]any{"tags": []any{"#a", "b/c", 3, "bad tag"}}, "a b/c"},
		{map[string]any{"tag": "x"}, "x"},
		{map[string]any{"tags": nil}, ""},
	} {
		if got := strings.Join(frontmatterTags(c.fm), " "); got != c.want {
			t.Errorf("%v: got %q, want %q", c.fm, got, c.want)
		}
	}
}
