package garden

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Obsidian tags: #tag in the text, or `tags:` in frontmatter. A tag is
// letters, digits, _, - and /, with at least one character that isn't a
// digit (so #1984 and #123 stay text), and it must follow a space or start
// a line (so URL fragments and C#-style words stay text). A / makes nested
// tags: #by/claude sits under #by.

// KindTag is the node kind of [Tag].
var KindTag = ast.NewNodeKind("Tag")

// Tag is an inline #tag.
type Tag struct {
	ast.BaseInline
	Name string // without the #
}

func (n *Tag) Kind() ast.NodeKind { return KindTag }

func (n *Tag) Dump(src []byte, level int) {
	ast.DumpHelper(n, src, level, map[string]string{"Name": n.Name}, nil)
}

type tagParser struct{}

func (tagParser) Trigger() []byte { return []byte{'#'} }

func (tagParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	if p := block.PrecendingCharacter(); !unicode.IsSpace(p) {
		return nil
	}
	line, _ := block.PeekLine()
	n := tagLen(line[1:])
	if n == 0 {
		return nil
	}
	block.Advance(1 + n)
	return &Tag{Name: string(line[1 : 1+n])}
}

// tagLen is the length of the tag name at the start of b, or 0 if there is
// none.
func tagLen(b []byte) int {
	n, letter := 0, false
	for n < len(b) {
		r, size := utf8.DecodeRune(b[n:])
		switch {
		case unicode.IsLetter(r) || unicode.Is(unicode.Mn, r) || r == '_' || r == '-':
			letter = true
		case unicode.IsDigit(r):
		case r == '/' && n > 0:
		default:
			goto done
		}
		n += size
	}
done:
	for n > 0 && b[n-1] == '/' { // "#a/" is the tag "a"
		n--
	}
	if !letter || n == 0 {
		return 0
	}
	return n
}

type tagRenderer struct{}

func (tagRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(KindTag, func(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			name := node.(*Tag).Name
			fmt.Fprintf(w, `<a class="tag" href="%s" data-tag="%s">#%s</a>`, tagHref(name), util.EscapeHTML([]byte(name)), util.EscapeHTML([]byte(name)))
		}
		return ast.WalkSkipChildren, nil
	})
}

// tagHref is the stacked view with the search box open on the tag. It is
// relative to the site root, like note links.
func tagHref(name string) string {
	return "index.html?q=" + url.QueryEscape("tag:#"+name)
}

// frontmatterTags reads `tags:` (or `tag:`) as a YAML list or a string of
// names separated by commas or spaces. A leading # is optional.
func frontmatterTags(fm map[string]any) []string {
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "#"))
		if n := tagLen([]byte(s)); n > 0 && n == len(s) {
			out = append(out, s)
		}
	}
	for _, key := range []string{"tags", "tag"} {
		switch v := fm[key].(type) {
		case string:
			for _, s := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
				add(s)
			}
		case []any:
			for _, x := range v {
				if s, ok := x.(string); ok {
					add(s)
				}
			}
		}
	}
	return out
}

// mergeTags dedupes tags case-insensitively, keeping the first spelling,
// and sorts them.
func mergeTags(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range lists {
		for _, t := range l {
			if k := strings.ToLower(t); !seen[k] {
				seen[k] = true
				out = append(out, t)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// tagLine reports whether a paragraph starts with tags and, if so, whether
// it is nothing but tags. A line of tags followed by text on the next line
// gets a hard break, so the text starts on a line of its own.
func tagLine(p *ast.Paragraph, src []byte) (leading, only bool) {
	for c := p.FirstChild(); c != nil; c = c.NextSibling() {
		switch t := c.(type) {
		case *Tag:
			leading = true
			continue
		case *ast.Text:
			if len(strings.TrimSpace(string(t.Segment.Value(src)))) == 0 {
				if leading && t.SoftLineBreak() && c.NextSibling() != nil {
					t.SetHardLineBreak(true)
				}
				continue
			}
		}
		return leading, false
	}
	return leading, leading
}

func insideLink(n ast.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if _, ok := p.(*ast.Link); ok {
			return true
		}
	}
	return false
}

func isTagLine(n ast.Node, src []byte) bool {
	p, ok := n.(*ast.Paragraph)
	if !ok {
		return false
	}
	_, only := tagLine(p, src)
	return only
}
