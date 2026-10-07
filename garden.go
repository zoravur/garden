package garden

import (
	"context"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Note is one rendered note and the links found in it.
type Note struct {
	ID          string         // vault path without ".md", e.g. "ideas/backlinks"
	Path        string         // vault path, e.g. "ideas/backlinks.md"
	Title       string         // frontmatter title, else the leading # heading, else the file name
	Date        string         // frontmatter date, as written
	Frontmatter map[string]any // parsed YAML frontmatter; nil if there is none
	HTML        string         // rendered body; links are relative to the site root
	Excerpt     string         // plain text of the first paragraph, trimmed
	Links       []Link         // outgoing links to other published notes, in document order
	Assets      []string       // vault paths of images and files the note uses
	Broken      []string       // link targets that match no note or file
	HasCode     bool           // contains a code block
}

// Link is an edge from one note to another, with the text around it.
type Link struct {
	Target  string // ID of the linked note
	Anchor  string // heading ID within the target, if any
	Label   string // the link's text
	Section string // heading in the source note the link sits under
	Context string // the sentence or list item around the link, trimmed
}

// Backlink is a note that links to another, with each place it does so.
type Backlink struct {
	Source string // ID of the linking note
	Refs   []Link // the links, with Target set to the linked note
}

// NavNode is an entry in the contents tree. A section can also be a note
// (its index page), so ID and Children can both be set.
type NavNode struct {
	Title    string     `json:"t"`
	ID       string     `json:"id,omitempty"`
	Children []*NavNode `json:"c,omitempty"`
}

// Garden is a vault loaded into memory: every published note rendered, plus
// the link graph. It is safe for concurrent reads.
type Garden struct {
	Config *Config    // normalized copy of the config it was loaded with
	Notes  []*Note    // published notes, sorted by ID
	Nav    []*NavNode // contents tree

	// Broken maps link targets that match nothing to the IDs of notes that
	// link to them.
	Broken map[string][]string

	byID   map[string]*Note
	back   map[string][]Backlink
	vault  *vault
	phases []Phase
}

// Phase is how long one step of a load or write took.
type Phase struct {
	Name string
	Took time.Duration
}

// Load scans the vault, renders every published note in parallel and
// computes backlinks. Nothing is written to disk.
func Load(ctx context.Context, config *Config) (*Garden, error) {
	cfg, err := config.normalized()
	if err != nil {
		return nil, err
	}
	g := &Garden{Config: cfg, byID: map[string]*Note{}, back: map[string][]Backlink{}, Broken: map[string][]string{}}
	timer := time.Now()
	phase := func(name string) {
		g.phases = append(g.phases, Phase{name, time.Since(timer)})
		timer = time.Now()
	}

	v, err := scanVault(cfg)
	if err != nil {
		return nil, fmt.Errorf("garden: scanning vault: %w", err)
	}
	g.vault = v
	sort.Strings(v.notes)

	// Read every note once; frontmatter decides publication before any
	// note is rendered, so links to private notes never resolve.
	src := make([][]byte, len(v.notes))
	if err := parallel(ctx, cfg.Workers, len(v.notes), func(i int) error {
		b, err := fs.ReadFile(cfg.FS, v.notes[i]+".md")
		src[i] = b
		return err
	}); err != nil {
		return nil, fmt.Errorf("garden: reading notes: %w", err)
	}
	if cfg.PublicOnly {
		public := map[string]bool{}
		for i, id := range v.notes {
			fm, _ := splitFrontmatter(src[i])
			public[id], _ = fm["public"].(bool)
		}
		kept := src[:0]
		for i, id := range v.notes {
			if public[id] {
				kept = append(kept, src[i])
			}
		}
		src = kept
		v.unpublish(func(id string) bool { return public[id] })
	}
	phase("scan")

	g.Notes = make([]*Note, len(v.notes))
	if err := parallel(ctx, cfg.Workers, len(v.notes), func(i int) error {
		n, err := renderNote(v, v.notes[i], src[i])
		if err != nil {
			return fmt.Errorf("garden: %s: %w", v.notes[i]+".md", err)
		}
		// Raw HTML (<img src="...">) can reference assets too.
		for _, m := range reRawSrc.FindAllStringSubmatch(n.HTML, -1) {
			if p := strings.TrimPrefix(m[1], "./"); v.assets[p] && !contains(n.Assets, p) {
				n.Assets = append(n.Assets, p)
			}
		}
		g.Notes[i] = n
		return nil
	}); err != nil {
		return nil, err
	}
	phase("render")

	sources := map[string]map[string]*Backlink{}
	for _, n := range g.Notes {
		g.byID[n.ID] = n
		for _, b := range n.Broken {
			g.Broken[b] = appendUniq(g.Broken[b], n.ID)
		}
		for _, l := range n.Links {
			bySrc := sources[l.Target]
			if bySrc == nil {
				bySrc = map[string]*Backlink{}
				sources[l.Target] = bySrc
			}
			b := bySrc[n.ID]
			if b == nil {
				b = &Backlink{Source: n.ID}
				bySrc[n.ID] = b
			}
			b.Refs = append(b.Refs, l)
		}
	}
	for target, bySrc := range sources {
		list := make([]Backlink, 0, len(bySrc))
		for _, b := range bySrc {
			list = append(list, *b)
		}
		sort.Slice(list, func(i, j int) bool {
			a, b := strings.ToLower(g.byID[list[i].Source].Title), strings.ToLower(g.byID[list[j].Source].Title)
			if a != b {
				return a < b
			}
			return list[i].Source < list[j].Source
		})
		g.back[target] = list
	}
	phase("backlinks")

	titles := make(map[string]string, len(g.Notes))
	for _, n := range g.Notes {
		titles[n.ID] = n.Title
	}
	if g.Nav, err = buildNav(v, titles); err != nil {
		return nil, err
	}
	return g, nil
}

// Note returns the published note with the given ID.
func (g *Garden) Note(id string) (*Note, bool) {
	n, ok := g.byID[id]
	return n, ok
}

// Backlinks returns the notes that link to id, sorted by title.
func (g *Garden) Backlinks(id string) []Backlink { return g.back[id] }

// Assets returns the vault paths of every file a published note uses, sorted.
func (g *Garden) Assets() []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range g.Notes {
		for _, a := range n.Assets {
			if !seen[a] {
				seen[a] = true
				out = append(out, a)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Resolve reports which note a link destination written in note `from`
// points at, using the same rules as rendering. ok is false if it doesn't
// point at a published note.
func (g *Garden) Resolve(from, dest string) (id, anchor string, ok bool) {
	kind, target, anchor := g.vault.resolve(from, dest)
	if kind != targetNote {
		return "", "", false
	}
	return target, anchor, true
}

// Report lists broken links in a plain-text form suitable for a log or file.
func (g *Garden) Report() string {
	var b strings.Builder
	keys := make([]string, 0, len(g.Broken))
	for k := range g.Broken {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintf(&b, "# Links to notes that don't exist (%d targets)\n\n", len(keys))
	for _, k := range keys {
		fmt.Fprintf(&b, "%s  <-  %s\n", k, strings.Join(g.Broken[k], ", "))
	}
	return b.String()
}

var reRawSrc = regexp.MustCompile(`(?:src|href)="([^"#?:]+)"`)

func contains(s []string, x string) bool {
	for _, y := range s {
		if y == x {
			return true
		}
	}
	return false
}

func appendUniq(s []string, x string) []string {
	if contains(s, x) {
		return s
	}
	return append(s, x)
}
