package garden

import (
	"bytes"
	"fmt"
	"html"
	"path"
	"regexp"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	ghtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"golang.org/x/text/unicode/norm"
)

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM, extension.Footnote, extension.DefinitionList),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	goldmark.WithRendererOptions(ghtml.WithUnsafe()),
)

// renderNote parses and renders one note. src is the file's content.
func renderNote(v *vault, id string, src []byte) (*Note, error) {
	cfg := v.cfg
	fm, body := splitFrontmatter(src)
	n := &Note{ID: id, Path: id + ".md", Frontmatter: fm}
	if t, ok := fm["title"].(string); ok {
		n.Title = strings.TrimSpace(t)
	}
	if d, ok := fm["date"]; ok && d != nil {
		n.Date = fmt.Sprint(d)
	}

	body = preprocessWikilinks(body)
	body = preprocessAdmonitions(body)

	ctx := parser.NewContext(parser.WithIDs(newMkdocsIDs()))
	doc := md.Parser().Parse(text.NewReader(body), parser.WithContext(ctx))

	// Title: frontmatter, else a leading top-level heading, else the file name.
	// A leading heading that repeats the title is dropped; the pane shows it.
	if first := doc.FirstChild(); first != nil {
		if h, ok := first.(*ast.Heading); ok && h.Level == 1 {
			ht := strings.TrimSpace(plainText(h, body))
			if n.Title == "" {
				n.Title = ht
			}
			if strings.EqualFold(ht, n.Title) {
				doc.RemoveChild(doc, h)
			}
		}
	}
	if n.Title == "" {
		n.Title = humanize(path.Base(id))
	}

	section := ""
	seenAsset := map[string]bool{}
	var unlink []*ast.Link
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch x := node.(type) {
		case *ast.Heading:
			section = strings.TrimSpace(plainText(x, body))
			if cfg.HeadingShift != 0 {
				x.Level = min(6, max(1, x.Level+cfg.HeadingShift))
			}
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			n.HasCode = true
		case *ast.Link:
			kind, target, anchor := v.resolve(id, string(x.Destination))
			switch kind {
			case targetNote:
				x.Destination = []byte(noteHref(target, anchor))
				x.SetAttributeString("data-id", []byte(target))
				if anchor != "" {
					x.SetAttributeString("data-anchor", []byte(anchor))
				}
				x.SetAttributeString("class", []byte("internal"))
				if target != id {
					n.Links = append(n.Links, Link{Target: target, Anchor: anchor, Section: section,
						Label: strings.TrimSpace(plainText(x, body)), Context: contextOf(x, body)})
				}
			case targetAsset:
				x.Destination = []byte(assetHref(target))
				if !seenAsset[target] {
					seenAsset[target] = true
					n.Assets = append(n.Assets, target)
				}
			case targetMissing:
				x.Destination = []byte("")
				x.SetAttributeString("class", []byte("missing"))
				x.SetAttributeString("title", []byte("Not in this garden: "+target))
				n.Broken = append(n.Broken, target)
			case targetPrivate:
				unlink = append(unlink, x) // keep the text, drop the link
			default:
				x.SetAttributeString("target", []byte("_blank"))
				x.SetAttributeString("rel", []byte("noopener"))
				x.SetAttributeString("class", []byte("external"))
			}
		case *ast.Image:
			if kind, target, _ := v.resolve(id, string(x.Destination)); kind == targetAsset {
				x.Destination = []byte(assetHref(target))
				if !seenAsset[target] {
					seenAsset[target] = true
					n.Assets = append(n.Assets, target)
				}
			}
			x.SetAttributeString("loading", []byte("lazy"))
		}
		return ast.WalkContinue, nil
	})

	// Links to unpublished notes keep their text, wrapped so themes can
	// style them, but lose the target: nothing names the private note.
	raw := func(h string) *ast.String {
		str := ast.NewString([]byte(h))
		str.SetCode(true) // written verbatim, unescaped
		return str
	}
	for _, l := range unlink {
		parent := l.Parent()
		parent.InsertBefore(parent, l, raw(`<span class="private" title="Not published">`))
		for c := l.FirstChild(); c != nil; {
			next := c.NextSibling()
			parent.InsertBefore(parent, l, c)
			c = next
		}
		parent.InsertBefore(parent, l, raw(`</span>`))
		parent.RemoveChild(parent, l)
	}

	if p := firstParagraph(doc, body); p != nil {
		n.Excerpt = truncate(plainText(p, body), 320)
	}

	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, body, doc); err != nil {
		return nil, err
	}
	out := buf.String()
	// Raw HTML in notes sometimes hard-codes the published site prefix.
	if p := cfg.SitePrefix; p != "" {
		out = strings.ReplaceAll(out, `src="`+p, `src="`)
		out = strings.ReplaceAll(out, `href="`+p, `href="`)
	}
	n.HTML = out
	return n, nil
}

// Every link in rendered HTML is relative to the site root; static pages
// set <base> so the same HTML works there and in the single-page view.
func noteHref(id, anchor string) string {
	h := "notes/" + escapePath(id) + ".html"
	if anchor != "" {
		h += "#" + anchor
	}
	return h
}

func assetHref(p string) string { return escapePath(p) }

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = strings.ReplaceAll(urlPathEscape(s), "%2F", "/")
	}
	return strings.Join(parts, "/")
}

func urlPathEscape(s string) string {
	var b strings.Builder
	for _, r := range []byte(s) {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.IndexByte("-_.~", r) >= 0 {
			b.WriteByte(r)
		} else {
			fmt.Fprintf(&b, "%%%02X", r)
		}
	}
	return b.String()
}

// firstParagraph skips admonition titles, which are paragraphs right after
// an HTML block opening the title wrapper.
func firstParagraph(doc ast.Node, src []byte) ast.Node {
	var prev ast.Node
	for c := doc.FirstChild(); c != nil; prev, c = c, c.NextSibling() {
		p, ok := c.(*ast.Paragraph)
		if !ok {
			continue
		}
		if hb, ok := prev.(*ast.HTMLBlock); ok {
			l := hb.Lines()
			if l.Len() > 0 {
				seg := l.At(l.Len() - 1)
				if bytes.Contains(seg.Value(src), []byte("admonition-title")) {
					continue
				}
			}
		}
		return p
	}
	return nil
}

// plainText concatenates the text under a node.
func plainText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(src))
			if t.SoftLineBreak() || t.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(t.Value)
		case *ast.AutoLink:
			b.Write(t.URL(src))
		case *ast.RawHTML:
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return strings.Join(strings.Fields(b.String()), " ")
}

// contextOf returns the text of the nearest enclosing block, trimmed to a
// window around the link label.
func contextOf(link *ast.Link, src []byte) string {
	var block ast.Node = link
	for p := link.Parent(); p != nil; p = p.Parent() {
		block = p
		if p.Type() == ast.TypeBlock {
			break
		}
	}
	t := plainText(block, src)
	label := plainText(link, src)
	const win = 280
	if len(t) <= win {
		return t
	}
	i := strings.Index(t, label)
	if i < 0 {
		return truncate(t, win)
	}
	start := max(0, i-win/2)
	end := min(len(t), start+win)
	start = max(0, end-win)
	for start > 0 && start < len(t) && !isSpace(t[start-1]) { // snap to word edges
		start++
	}
	for end < len(t) && !isSpace(t[end]) {
		end--
	}
	s := strings.TrimSpace(t[start:end])
	if start > 0 {
		s = "… " + s
	}
	if end < len(t) {
		s += " …"
	}
	return s
}

func isSpace(b byte) bool { return b == ' ' }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := strings.LastIndexByte(s[:n], ' ')
	if cut < n/2 {
		cut = n
	}
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return strings.TrimRight(s[:cut], " ,;:.") + "…"
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

func humanize(s string) string {
	s = strings.NewReplacer("_", " ", "-", " ").Replace(s)
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// --- heading ids that match Python-Markdown's toc slugify, so existing
// "note.md#some-heading" links from an mkdocs site keep working.

type mkdocsIDs struct{ used map[string]bool }

func newMkdocsIDs() *mkdocsIDs { return &mkdocsIDs{used: map[string]bool{}} }

var (
	reNonWord   = regexp.MustCompile(`[^\w\s-]`)
	reDashSpace = regexp.MustCompile(`[-\s]+`)
)

func slugify(s string) string {
	s = norm.NFKD.String(s)
	var b strings.Builder
	for _, r := range s {
		if r < 128 {
			b.WriteRune(r)
		}
	}
	s = reNonWord.ReplaceAllString(b.String(), "")
	s = strings.ToLower(strings.TrimSpace(s))
	return reDashSpace.ReplaceAllString(s, "-")
}

func (m *mkdocsIDs) Generate(value []byte, kind ast.NodeKind) []byte {
	base := slugify(stripInlineMarkup(string(value)))
	if base == "" {
		base = "_"
	}
	id := base
	for i := 1; m.used[id]; i++ {
		id = fmt.Sprintf("%s_%d", base, i)
	}
	m.used[id] = true
	return []byte(id)
}

func (m *mkdocsIDs) Put(value []byte) { m.used[string(value)] = true }

var reMdLink = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)

func stripInlineMarkup(s string) string { return reMdLink.ReplaceAllString(s, "$1") }

// --- admonitions: Python-Markdown's "!!! type "Title"" and pymdownx.details'
// "??? type" (collapsed) / "???+ type" (open), converted to HTML wrappers
// before parsing so their bodies are still parsed as markdown.

var reAdmon = regexp.MustCompile(`^([ \t]*)(!!!|\?\?\?\+?)[ \t]+([\w-]+)(?:[ \t]+"(.*)")?[ \t]*$`)
var reFence = regexp.MustCompile("^[ \t]*(```+|~~~+)")

func preprocessAdmonitions(src []byte) []byte {
	if !bytes.Contains(src, []byte("!!!")) && !bytes.Contains(src, []byte("???")) {
		return src
	}
	lines := strings.Split(string(src), "\n")
	return []byte(strings.Join(admonLines(lines), "\n"))
}

func indentWidth(s string) int {
	w := 0
	for _, r := range s {
		switch r {
		case ' ':
			w++
		case '\t':
			w += 4 - w%4
		default:
			return w
		}
	}
	return -1 // blank line
}

func dedent(s string, n int) string {
	w := 0
	for i, r := range s {
		if w >= n {
			return s[i:]
		}
		switch r {
		case ' ':
			w++
		case '\t':
			w += 4 - w%4
		default:
			return s[i:]
		}
	}
	return ""
}

func admonLines(lines []string) []string {
	var out []string
	fence := ""
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if m := reFence.FindStringSubmatch(l); m != nil {
			if fence == "" {
				fence = m[1][:3]
			} else if strings.HasPrefix(m[1], fence) {
				fence = ""
			}
			out = append(out, l)
			continue
		}
		m := reAdmon.FindStringSubmatch(l)
		if fence != "" || m == nil {
			out = append(out, l)
			continue
		}
		ind, marker, kind := m[1], m[2], strings.ToLower(m[3])
		base := indentWidth(l)
		// Body: following lines indented past the marker; blank lines only
		// count if more body follows them.
		j := i + 1
		var body []string
		for k := j; k < len(lines); k++ {
			w := indentWidth(lines[k])
			if w == -1 {
				continue
			}
			if w < base+4 {
				break
			}
			for ; j <= k; j++ {
				body = append(body, dedent(lines[j], base+4))
			}
		}
		i = j - 1
		title := humanize(kind)
		hasTitle := len(m) > 4 && strings.Contains(l, `"`)
		if hasTitle {
			title = m[4]
		}
		inner := admonLines(body)
		cls := html.EscapeString(kind)
		if marker == "!!!" {
			out = append(out, ind+`<div class="admonition `+cls+`">`)
			if title != "" {
				out = append(out, ind+`<div class="admonition-title">`, "", ind+title, "", ind+`</div>`)
			}
		} else {
			open := ""
			if marker == "???+" {
				open = " open"
			}
			out = append(out, ind+`<details class="admonition `+cls+`"`+open+`>`,
				ind+`<summary class="admonition-title">`, "", ind+title, "", ind+`</summary>`)
		}
		out = append(out, "")
		for _, b := range inner {
			if b == "" {
				out = append(out, "")
			} else {
				out = append(out, ind+b)
			}
		}
		out = append(out, "")
		if marker == "!!!" {
			out = append(out, ind+`</div>`)
		} else {
			out = append(out, ind+`</details>`)
		}
		out = append(out, "")
	}
	return out
}

// --- Obsidian-style [[wikilinks]]: [[Note]], [[Note|label]], [[Note#Heading]],
// ![[image.png]]. Rewritten to ordinary markdown links before parsing, so
// they go through the same resolution as [label](note.md). Code fences and
// inline code are left alone.

var reImageExt = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|webp|svg|avif)$`)

var reWiki = regexp.MustCompile(`(!?)\[\[([^\[\]|#]*)(?:#([^\[\]|]*))?(?:\|([^\[\]]*))?\]\]`)

func preprocessWikilinks(src []byte) []byte {
	if !bytes.Contains(src, []byte("[[")) {
		return src
	}
	lines := strings.Split(string(src), "\n")
	fence := ""
	for i, l := range lines {
		if m := reFence.FindStringSubmatch(l); m != nil {
			if fence == "" {
				fence = m[1][:3]
			} else if strings.HasPrefix(m[1], fence) {
				fence = ""
			}
			continue
		}
		if fence != "" || strings.HasPrefix(l, "    ") || !strings.Contains(l, "[[") {
			continue
		}
		// Only touch text outside `inline code`.
		parts := strings.Split(l, "`")
		for j := 0; j < len(parts); j += 2 {
			parts[j] = reWiki.ReplaceAllStringFunc(parts[j], wikiToMarkdown)
		}
		lines[i] = strings.Join(parts, "`")
	}
	return []byte(strings.Join(lines, "\n"))
}

func wikiToMarkdown(m string) string {
	g := reWiki.FindStringSubmatch(m)
	embed, target, heading, label := g[1] == "!", strings.TrimSpace(g[2]), strings.TrimSpace(g[3]), strings.TrimSpace(g[4])
	dest := target
	if dest != "" && path.Ext(dest) == "" {
		dest += ".md"
	}
	if heading != "" {
		dest += "#" + slugify(heading)
	}
	if label == "" {
		label = target
		if heading != "" {
			label = strings.TrimSpace(target + " › " + heading)
		}
	}
	if embed && reImageExt.MatchString(target) {
		return "![" + label + "](<" + dest + ">)"
	}
	return "[" + label + "](<" + dest + ">)"
}
