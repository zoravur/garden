package garden

import (
	"strings"
	"testing"
	"testing/fstest"
)

func renderOne(t *testing.T, md string) *Note {
	t.Helper()
	g := load(t, Config{FS: fstest.MapFS{"index.md": file(md)}})
	n, _ := g.Note("index")
	return n
}

func TestMathIsRecognised(t *testing.T) {
	for _, c := range []struct{ name, md, want string }{
		{"inline dollars", `Euler: $e^{i\pi} + 1 = 0$.`, `<span class="math math-inline">e^{i\pi} + 1 = 0</span>`},
		{"underscores and stars survive", `$a_1 * b_2 * c_3$`, `<span class="math math-inline">a_1 * b_2 * c_3</span>`},
		{"display block", "Before.\n\n$$\n\\sum_{k=1}^n k = \\frac{n(n+1)}{2}\n$$\n\nAfter.", `<div class="math math-display">\sum_{k=1}^n k = \frac{n(n+1)}{2}</div>`},
		{"display inline", `so $$x^2$$ here`, `<span class="math math-display">x^2</span>`},
		{"paren delimiters", `\(a < b\) and $$c > d$$`, `<span class="math math-inline">a &lt; b</span> and <span class="math math-display">c &gt; d</span>`},
		{"bracket block", "Text.\n\n\\[\nc > d\n\\]\n", `<div class="math math-display">c &gt; d</div>`},
		{"text command is not prose", `$\text{for all three cases} x > 0$`, `<span class="math math-inline">\text{for all three cases} x &gt; 0</span>`},
		{"abs value in a table", "| f | v |\n|---|---|\n| a | $|x| + |y|$ |\n", `<span class="math math-inline">|x| + |y|</span>`},
		{"begin env", "\\begin{align}\na &= b \\\\\nc &= d\n\\end{align}\n", `<div class="math math-display">\begin{align}`},
		{"pipe inside a table", "| f | value |\n|---|---|\n| abs | $|x|$ |\n", `<td><span class="math math-inline">|x|</span></td>`},
		{"spans lines", "a $x +\ny$ b", "<span class=\"math math-inline\">x +\ny</span>"},
		{"inside admonition", "!!! note\n    Area is $\\pi r^2$.\n", `<span class="math math-inline">\pi r^2</span>`},
	} {
		t.Run(c.name, func(t *testing.T) {
			n := renderOne(t, c.md)
			if !n.HasMath || !strings.Contains(n.HTML, c.want) {
				t.Errorf("want %s\ngot  %s", c.want, n.HTML)
			}
		})
	}
}

func TestDollarsThatAreNotMath(t *testing.T) {
	for _, c := range []struct{ name, md, want string }{
		{"prices", "It costs $5 and $10 today.", "It costs $5 and $10 today."},
		{"space inside", "Between $ 5 and 6 $ units.", "Between $ 5 and 6 $ units."},
		{"escaped", `Pay \$x\$ please.`, "Pay $x$ please."},
		{"inline code", "Run `echo $HOME/$USER` now.", "<code>echo $HOME/$USER</code>"},
		{"fenced code", "```sh\nexport A=$B$C\n```\n", "export A=$B$C"},
		{"indented code", "Text.\n\n    total=$a$b\n", "total=$a$b"},
		{"code spans with dollars", "`$` and `$`", "<code>$</code> and <code>$</code>"},
		{"blank line ends it", "a $b\n\nc$ d", "a $b</p>\n<p>c$ d"},
		{"escaped brackets", `just “\[sha-hash\] \[description\]”`, "[sha-hash] [description]"},
		{"prices in prose", "if you reserve 1$/hour of an instance type and you use 2$/hour", "1$/hour of an instance"},
		{"shell in prose", "for FILE in $(git diff | grep <your vars dir> | grep x); do echo $FILE", "$(git diff"},
		{"prices across table cells", "| a | b | c |\n|---|---|---|\n| From $60 | From $83 (2022) | 249$ |\n", "From $83 (2022)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			n := renderOne(t, c.md)
			if n.HasMath && strings.Contains(n.HTML, `class="math`) {
				t.Errorf("unexpected math in %s", n.HTML)
			}
			if !strings.Contains(n.HTML, c.want) {
				t.Errorf("want %q in\n%s", c.want, n.HTML)
			}
		})
	}
}

func TestMathInTextFields(t *testing.T) {
	g := load(t, Config{FS: fstest.MapFS{
		"index.md": file("# Euler $e^x$\n\nThe identity $e^{i\\pi}=-1$ links to [[other]].\n\n## Proof of $f_1$\n\nSee [[other]] for $a_b$."),
		"other.md": file("Other."),
	}})
	n, _ := g.Note("index")
	if n.Title != "Euler $e^x$" {
		t.Errorf("title = %q", n.Title)
	}
	if !strings.Contains(n.Excerpt, `$e^{i\pi}=-1$`) {
		t.Errorf("excerpt = %q", n.Excerpt)
	}
	if strings.ContainsRune(n.Excerpt, mathOpen) {
		t.Error("placeholder leaked into the excerpt")
	}
	refs := g.Backlinks("other")[0].Refs
	if refs[1].Section != "Proof of $f_1$" || !strings.Contains(refs[1].Context, "$a_b$") {
		t.Errorf("backlink text = %+v", refs[1])
	}
	if !strings.Contains(n.HTML, `id="proof-of-f_1"`) {
		t.Errorf("heading slug should come from the TeX source: %s", n.HTML)
	}
}

func TestSnippetHTML(t *testing.T) {
	got := snippetHTML(`For continuous $f$ see <Chebyshev points> & more`, "Chebyshev points")
	want := `For continuous <span class="math math-inline">f</span> see &lt;<mark>Chebyshev points</mark>&gt; &amp; more`
	if got != want {
		t.Errorf("snippetHTML:\n got %s\nwant %s", got, want)
	}
	if got := snippetHTML("costs $5 and $10", ""); got != "costs $5 and $10" {
		t.Errorf("prices: %s", got)
	}
}
