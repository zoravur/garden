package garden

import (
	"fmt"
	"html"
	"sort"
	"strings"
)

// generateIndex builds a landing page for vaults without an index note:
// the most-linked notes, which are usually the hubs, followed by the whole
// contents tree. Its links are not recorded as Links, so it never shows up
// in other notes' backlinks.
func generateIndex(g *Garden, id string) *Note {
	cfg := g.Config
	var b strings.Builder
	link := func(id string) string {
		n := g.byID[id]
		return fmt.Sprintf(`<a href="%s" data-id="%s" class="internal">%s</a>`,
			noteHref(id, ""), html.EscapeString(id), html.EscapeString(n.Title))
	}

	if cfg.Description != "" {
		fmt.Fprintf(&b, "<p>%s</p>\n", html.EscapeString(cfg.Description))
	}
	switch len(g.Notes) {
	case 0:
		b.WriteString("<p>No notes yet. Add a markdown file to the vault and rebuild.</p>\n")
	case 1:
		b.WriteString("<p>1 note.</p>\n")
	default:
		fmt.Fprintf(&b, "<p>%d notes. Every link opens a note beside the one you're reading.</p>\n", len(g.Notes))
	}

	// Hubs: notes with the most other notes pointing at them.
	type hub struct {
		id    string
		count int
	}
	var hubs []hub
	for _, n := range g.Notes {
		if c := len(g.back[n.ID]); c > 0 {
			hubs = append(hubs, hub{n.ID, c})
		}
	}
	sort.Slice(hubs, func(i, j int) bool {
		if hubs[i].count != hubs[j].count {
			return hubs[i].count > hubs[j].count
		}
		return strings.ToLower(g.byID[hubs[i].id].Title) < strings.ToLower(g.byID[hubs[j].id].Title)
	})
	if len(hubs) > 10 {
		hubs = hubs[:10]
	}
	if len(hubs) >= 3 { // with fewer, the contents list below says it all
		b.WriteString("<h2 id=\"most-linked\">Most linked</h2>\n<ul class=\"index-hubs\">\n")
		for _, h := range hubs {
			fmt.Fprintf(&b, "<li>%s <span class=\"index-count\">%d link%s</span>", link(h.id), h.count, plural(h.count))
			if ex := g.byID[h.id].Excerpt; ex != "" {
				fmt.Fprintf(&b, "<br><span class=\"index-excerpt\">%s</span>", html.EscapeString(ex))
			}
			b.WriteString("</li>\n")
		}
		b.WriteString("</ul>\n")
	}

	if len(g.Nav) > 0 {
		b.WriteString("<h2 id=\"all-notes\">All notes</h2>\n")
		openFolders := len(g.Notes) <= 60 // small gardens show everything at once
		var tree func(nodes []*NavNode)
		tree = func(nodes []*NavNode) {
			b.WriteString("<ul class=\"index-tree\">\n")
			for _, n := range nodes {
				label := html.EscapeString(n.Title)
				if n.ID != "" && g.byID[n.ID] != nil {
					label = link(n.ID)
				}
				if len(n.Children) == 0 {
					fmt.Fprintf(&b, "<li>%s</li>\n", label)
					continue
				}
				open := ""
				if openFolders {
					open = " open"
				}
				fmt.Fprintf(&b, "<li><details%s><summary>%s <span class=\"index-count\">%d</span></summary>\n",
					open, label, countNotes(n.Children))
				tree(n.Children)
				b.WriteString("</details></li>\n")
			}
			b.WriteString("</ul>\n")
		}
		tree(g.Nav)
	}

	excerpt := cfg.Description
	if excerpt == "" {
		excerpt = fmt.Sprintf("Index of %d notes.", len(g.Notes))
	}
	return &Note{
		ID: id, Path: "", Title: cfg.Title, HTML: b.String(),
		Excerpt: excerpt, Generated: true,
	}
}

func countNotes(nodes []*NavNode) int {
	c := 0
	for _, n := range nodes {
		if n.ID != "" {
			c++
		}
		c += countNotes(n.Children)
	}
	return c
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
