package garden

import (
	"html"
	"regexp"
	"strconv"
	"strings"
)

// Math is lifted out of a note before markdown parsing and put back
// afterwards, so the parser never sees it: `_` and `*` inside a formula
// stay as written, and `|` can't split a table cell. Each formula is
// replaced by a placeholder made of private-use characters, which no
// markdown rule touches.
//
// Recognised delimiters, covering Obsidian, Pandoc and MkDocs (arithmatex):
//
//	$x$          inline: no space just inside either dollar, and the closing
//	             dollar not followed by a digit, so "$5 and $10" stays text
//	$$x$$        display, inline or on its own lines
//	\(x\)        inline
//	\[x\]        display, when \[ starts a line and \] ends one (elsewhere
//	             \[ is far more often an escaped bracket)
//	\begin{env}  display, from a line starting \begin{env} to \end{env}
//	...\end{env}
//
// Inline $...$ is also rejected when it reads like prose (three or more
// ordinary words in a row, as in "1$/hour of an instance type and 2$") or
// when, in a table row, it would span a cell border. A backslash before a
// dollar (\$) keeps it literal. Code fences and inline
// code are never scanned, and a placeholder that still lands in code (an
// indented code block) is put back as the original text.

const (
	mathOpen  = ''
	mathClose = ''
)

type mathItem struct {
	tex     string // the formula, without delimiters
	source  string // as written, delimiters included
	display bool
}

type mathSpans struct{ items []mathItem }

var reMathToken = regexp.MustCompile("([0-9]+)")

func (m *mathSpans) token(it mathItem) string {
	m.items = append(m.items, it)
	return string(mathOpen) + strconv.Itoa(len(m.items)-1) + string(mathClose)
}

func (m *mathSpans) empty() bool { return m == nil || len(m.items) == 0 }

// restoreText puts the original source back, for plain-text uses such as
// excerpts, backlink snippets and heading slugs.
func (m *mathSpans) restoreText(s string) string {
	if m.empty() || !strings.ContainsRune(s, mathOpen) {
		return s
	}
	return reMathToken.ReplaceAllStringFunc(s, func(t string) string {
		if it, ok := m.lookup(t); ok {
			return it.source
		}
		return t
	})
}

// restoreHTML turns placeholders into elements garden.js renders with
// KaTeX. Inside <code> they go back to plain text.
func (m *mathSpans) restoreHTML(h string) string {
	if m.empty() {
		return h
	}
	codeRanges := htmlCodeRanges(h)
	inCode := func(i int) bool {
		for _, r := range codeRanges {
			if i >= r[0] && i < r[1] {
				return true
			}
		}
		return false
	}
	var b strings.Builder
	last := 0
	for _, loc := range reMathToken.FindAllStringIndex(h, -1) {
		it, ok := m.lookup(h[loc[0]:loc[1]])
		if !ok {
			continue
		}
		start, end := loc[0], loc[1]
		var repl string
		switch {
		case inCode(start):
			repl = html.EscapeString(it.source)
		case it.display:
			// A formula alone in a paragraph becomes a block of its own.
			if strings.HasSuffix(h[:start], "<p>") && strings.HasPrefix(h[end:], "</p>") {
				start -= len("<p>")
				end += len("</p>")
				repl = `<div class="math math-display">` + html.EscapeString(it.tex) + `</div>`
			} else {
				repl = `<span class="math math-display">` + html.EscapeString(it.tex) + `</span>`
			}
		default:
			repl = `<span class="math math-inline">` + html.EscapeString(it.tex) + `</span>`
		}
		b.WriteString(h[last:start])
		b.WriteString(repl)
		last = end
	}
	b.WriteString(h[last:])
	return b.String()
}

func (m *mathSpans) lookup(tok string) (mathItem, bool) {
	i, err := strconv.Atoi(tok[len(string(mathOpen)) : len(tok)-len(string(mathClose))])
	if err != nil || i < 0 || i >= len(m.items) {
		return mathItem{}, false
	}
	return m.items[i], true
}

// htmlCodeRanges returns the byte ranges between <code ...> and </code>.
func htmlCodeRanges(h string) [][2]int {
	var out [][2]int
	for i := 0; ; {
		o := strings.Index(h[i:], "<code")
		if o < 0 {
			return out
		}
		o += i
		c := strings.Index(h[o:], "</code>")
		if c < 0 {
			return append(out, [2]int{o, len(h)})
		}
		out = append(out, [2]int{o, o + c})
		i = o + c
	}
}

var (
	reMathFence = regexp.MustCompile("^[ \t]*(```+|~~~+)")
	reBeginEnv  = regexp.MustCompile(`^[ \t]*\\begin\{([A-Za-z*]+)\}`)
)

// protectMath replaces formulas in src with placeholders.
func protectMath(src []byte) ([]byte, *mathSpans) {
	s := string(src)
	if !strings.ContainsAny(s, `$\`) {
		return src, nil
	}
	m := &mathSpans{}
	var out strings.Builder
	out.Grow(len(s))

	fence := ""
	lineStart := true
	i := 0
	for i < len(s) {
		// Fenced code: copy whole lines untouched.
		if lineStart {
			end := strings.IndexByte(s[i:], '\n')
			if end < 0 {
				end = len(s)
			} else {
				end += i + 1
			}
			line := s[i:end]
			if f := reMathFence.FindStringSubmatch(line); f != nil {
				switch {
				case fence == "":
					fence = f[1][:3]
				case strings.HasPrefix(f[1], fence):
					fence = ""
				}
				out.WriteString(line)
				i = end
				continue
			}
			if fence != "" {
				out.WriteString(line)
				i = end
				continue
			}
			// \begin{env} ... \end{env} as a display block.
			if e := reBeginEnv.FindStringSubmatchIndex(line); e != nil {
				name := line[e[2]:e[3]]
				closeTag := `\end{` + name + `}`
				if j := strings.Index(s[i:], closeTag); j >= 0 && !hasBlankLine(s[i:i+j]) {
					stop := i + j + len(closeTag)
					indent := line[:strings.IndexByte(line, '\\')]
					body := strings.TrimSpace(s[i:stop])
					out.WriteString(indent + m.token(mathItem{tex: body, source: body, display: true}))
					i = stop
					lineStart = false
					continue
				}
			}
		}
		lineStart = false
		c := s[i]
		switch {
		case c == '\n':
			out.WriteByte(c)
			i++
			lineStart = true
			continue

		case c == '`': // inline code: copy through the matching backtick run
			n := runLen(s, i, '`')
			if j := matchingBackticks(s, i+n, n); j >= 0 {
				out.WriteString(s[i : j+n])
				i = j + n
			} else {
				out.WriteString(s[i : i+n])
				i += n
			}
			continue

		case c == '\\' && i+1 < len(s):
			switch s[i+1] {
			case '$':
				out.WriteString(`\$`)
				i += 2
				continue
			case '(', '[':
				closer := `\)`
				if s[i+1] == '[' {
					closer = `\]`
				}
				j := strings.Index(s[i+2:], closer)
				ok := j >= 0 && !hasBlankLine(s[i+2:i+2+j])
				if ok && s[i+1] == '[' {
					ok = atLineStart(s, i) && atLineEnd(s, i+2+j+2)
				}
				if ok {
					tex := s[i+2 : i+2+j]
					if strings.TrimSpace(tex) != "" {
						src := s[i : i+2+j+2]
						out.WriteString(m.token(mathItem{tex: strings.TrimSpace(tex), source: src, display: s[i+1] == '['}))
						i += 2 + j + 2
						continue
					}
				}
			}
			out.WriteString(s[i : i+2])
			i += 2
			continue

		case c == '$':
			if strings.HasPrefix(s[i:], "$$") {
				if j := strings.Index(s[i+2:], "$$"); j >= 0 && !hasBlankLine(s[i+2:i+2+j]) && strings.TrimSpace(s[i+2:i+2+j]) != "" {
					src := s[i : i+2+j+2]
					out.WriteString(m.token(mathItem{tex: strings.TrimSpace(s[i+2 : i+2+j]), source: src, display: true}))
					i += 2 + j + 2
					continue
				}
				out.WriteString("$$")
				i += 2
				continue
			}
			if j := closingDollar(s, i); j > 0 && plausibleInline(s, i, j) {
				out.WriteString(m.token(mathItem{tex: s[i+1 : j], source: s[i : j+1]}))
				i = j + 1
				continue
			}
		}
		out.WriteByte(c)
		i++
	}
	if m.empty() {
		return src, nil
	}
	return []byte(out.String()), m
}

// closingDollar finds the dollar that closes an inline formula opened at
// s[open], or -1. Pandoc's rules: the opening dollar has a non-space
// character right after it, the closing one a non-space character right
// before it and no digit right after it. Formulas don't cross blank lines.
func closingDollar(s string, open int) int {
	if open+1 >= len(s) || isMathSpace(s[open+1]) || s[open+1] == '$' {
		return -1
	}
	for j := open + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++ // skip the escaped character
		case '\n':
			if k := j + 1; k < len(s) && strings.TrimLeft(s[k:k+lineLen(s, k)], " \t") == "" {
				return -1
			}
		case '$':
			if isMathSpace(s[j-1]) {
				continue
			}
			if j+1 < len(s) && s[j+1] >= '0' && s[j+1] <= '9' {
				continue
			}
			return j
		}
	}
	return -1
}

// plausibleInline rejects $...$ candidates that are really prose with two
// dollar signs in it.
func plausibleInline(s string, open, close int) bool {
	tex := s[open+1 : close]
	if reProse.MatchString(reTexCommand.ReplaceAllString(tex, " ")) {
		return false
	}
	lineStart := strings.LastIndexByte(s[:open], '\n') + 1
	if strings.HasPrefix(strings.TrimLeft(s[lineStart:open], " \t"), "|") && reCellBorder.MatchString(tex) {
		return false
	}
	return true
}

var (
	// \text{...}, \mathrm{...} etc. and bare commands like \sin are TeX,
	// not prose, so they're removed before looking for words.
	reTexCommand = regexp.MustCompile(`\\[A-Za-z]+(\{[^{}]*\})?`)
	reProse      = regexp.MustCompile(`[A-Za-z]{3,}\s+[A-Za-z]{3,}\s+[A-Za-z]{3,}`)
	reCellBorder = regexp.MustCompile(`\s\|\s`)
)

func atLineStart(s string, i int) bool {
	return strings.TrimLeft(s[strings.LastIndexByte(s[:i], '\n')+1:i], " \t") == ""
}

func atLineEnd(s string, i int) bool {
	return strings.TrimRight(s[i:i+lineLen(s, i)], " \t\r") == ""
}

func lineLen(s string, i int) int {
	if n := strings.IndexByte(s[i:], '\n'); n >= 0 {
		return n
	}
	return len(s) - i
}

func isMathSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// hasBlankLine reports whether s contains a whitespace-only line between
// two line breaks, which ends a paragraph and so any formula in it.
func hasBlankLine(s string) bool { return reBlankLine.MatchString(s) }

var reBlankLine = regexp.MustCompile(`\n[ \t]*\r?\n`)

func runLen(s string, i int, c byte) int {
	n := 0
	for i+n < len(s) && s[i+n] == c {
		n++
	}
	return n
}

// matchingBackticks finds the next run of exactly n backticks at or after
// i, within the same block.
func matchingBackticks(s string, i, n int) int {
	for j := i; j < len(s); {
		if s[j] == '\n' && j+1 < len(s) && strings.TrimSpace(s[j+1:j+1+lineLen(s, j+1)]) == "" {
			return -1
		}
		if s[j] == '`' {
			k := runLen(s, j, '`')
			if k == n {
				return j
			}
			j += k
			continue
		}
		j++
	}
	return -1
}

// snippetHTML renders a plain-text snippet (an excerpt, a backlink's
// context or section) as HTML: text escaped, the first occurrence of mark
// wrapped in <mark>, and any formulas turned into inline math elements.
func snippetHTML(text, mark string) string {
	protected, m := protectMath([]byte(text))
	h := html.EscapeString(string(protected))
	if mark != "" {
		if em := html.EscapeString(mark); strings.Contains(h, em) {
			h = strings.Replace(h, em, "<mark>"+em+"</mark>", 1)
		}
	}
	if m.empty() {
		return h
	}
	for i := range m.items {
		m.items[i].display = false // snippets stay on one line
	}
	return m.restoreHTML(h)
}
