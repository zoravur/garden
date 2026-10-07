package garden

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"html/template"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

//go:embed web
var webFS embed.FS

// DefaultTheme returns the built-in theme files: index.html (the app
// shell), note.html (static note pages), garden.css and garden.js.
func DefaultTheme() fs.FS {
	sub, _ := fs.Sub(webFS, "web")
	return sub
}

// Stats summarizes a build.
type Stats struct {
	Notes         int     // published notes
	Links         int     // note-to-note links
	Broken        int     // links that match nothing
	Written       int     // output files created or changed
	Unchanged     int     // output files left as they were
	AssetsCopied  int     // assets in the output
	AssetsSkipped int     // assets over MaxAssetMB, served from AssetFallbackURL
	Phases        []Phase // time per step, load included
	Took          time.Duration
}

// Build loads the vault and writes the site to cfg.Out.
func Build(ctx context.Context, cfg *Config) (*Garden, *Stats, error) {
	start := time.Now()
	g, err := Load(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	st, err := g.Write(ctx, "")
	if err != nil {
		return g, nil, err
	}
	st.Took = time.Since(start)
	return g, st, nil
}

// Write writes the site to out (cfg.Out if out is empty). Files whose
// content is unchanged are not rewritten, so repeated writes are cheap.
func (g *Garden) Write(ctx context.Context, out string) (*Stats, error) {
	start := time.Now()
	cfg := g.Config
	if out == "" {
		out = cfg.Out
	}
	st := &Stats{Notes: len(g.Notes), Phases: append([]Phase(nil), g.phases...)}
	for _, n := range g.Notes {
		st.Links += len(n.Links)
		st.Broken += len(n.Broken)
	}
	var written, unchanged atomic.Int64
	count := func(changed bool) {
		if changed {
			written.Add(1)
		} else {
			unchanged.Add(1)
		}
	}
	timer := time.Now()
	phase := func(name string) {
		st.Phases = append(st.Phases, Phase{name, time.Since(timer)})
		timer = time.Now()
	}

	// Assets: copy what notes use; large ones can be served from a
	// fallback URL instead (for size-capped hosts).
	skipped := map[string]bool{}
	var assets []string
	for _, a := range g.Assets() {
		info, err := fs.Stat(cfg.FS, a)
		if err != nil {
			continue
		}
		if cfg.MaxAssetMB > 0 && float64(info.Size()) > cfg.MaxAssetMB*1e6 {
			skipped[a] = true
			continue
		}
		assets = append(assets, a)
	}
	if err := parallel(ctx, cfg.Workers, len(assets), func(i int) error {
		changed, err := copyIfChanged(cfg.FS, assets[i], filepath.Join(out, filepath.FromSlash(assets[i])))
		count(changed)
		return err
	}); err != nil {
		return nil, err
	}
	st.AssetsCopied, st.AssetsSkipped = len(assets), len(skipped)
	html := func(n *Note) string {
		h := n.HTML
		for _, a := range n.Assets {
			if skipped[a] {
				repl := ""
				if cfg.AssetFallbackURL != "" {
					repl = strings.TrimSuffix(cfg.AssetFallbackURL, "/") + "/" + assetHref(a)
				}
				h = strings.ReplaceAll(h, `"`+assetHref(a)+`"`, `"`+repl+`"`)
			}
		}
		return h
	}
	phase("assets")

	// Note data, sharded so the browser fetches only what it opens.
	total := 0
	for _, n := range g.Notes {
		total += len(n.HTML)
	}
	nShards := max(1, (total*13/10)/(cfg.ShardKB*1024)+1) // ~30% headroom for backlink data
	shardOf := func(id string) int {
		h := fnv.New32a()
		h.Write([]byte(id))
		return int(h.Sum32() % uint32(nShards))
	}
	type jsonBacklink struct {
		Source string      `json:"s"`
		Refs   [][4]string `json:"r"` // section, context, label, anchor
	}
	type jsonEntry struct {
		HTML string         `json:"h"`
		Back []jsonBacklink `json:"b,omitempty"`
		Code bool           `json:"code,omitempty"`
		Date string         `json:"d,omitempty"`
		Gen  bool           `json:"g,omitempty"`
	}
	shards := make([]map[string]*jsonEntry, nShards)
	for i := range shards {
		shards[i] = map[string]*jsonEntry{}
	}
	index := map[string][4]any{} // id -> [title, shard, excerpt, backlink count]
	for _, n := range g.Notes {
		s := shardOf(n.ID)
		var back []jsonBacklink
		for _, b := range g.back[n.ID] {
			jb := jsonBacklink{Source: b.Source}
			for i, r := range b.Refs {
				if i == 3 {
					break
				}
				jb.Refs = append(jb.Refs, [4]string{r.Section, r.Context, r.Label, r.Anchor})
			}
			back = append(back, jb)
		}
		shards[s][n.ID] = &jsonEntry{HTML: html(n), Back: back, Code: n.HasCode, Date: n.Date, Gen: n.Generated}
		index[n.ID] = [4]any{n.Title, s, n.Excerpt, len(back)}
	}
	home := cfg.Home
	if err := parallel(ctx, cfg.Workers, nShards, func(i int) error {
		b, err := json.Marshal(shards[i])
		if err != nil {
			return err
		}
		changed, err := writeIfChanged(filepath.Join(out, "data", fmt.Sprintf("shard-%03d.json", i)), b)
		count(changed)
		return err
	}); err != nil {
		return nil, err
	}
	ib, err := json.Marshal(map[string]any{
		"title": cfg.Title, "description": cfg.Description, "home": home,
		"shards": nShards, "notes": index, "nav": g.Nav, "source": cfg.SourceURL,
		"generated_home": g.byID[home] != nil && g.byID[home].Generated,
	})
	if err != nil {
		return nil, err
	}
	changed, err := writeIfChanged(filepath.Join(out, "data", "index.json"), ib)
	if err != nil {
		return nil, err
	}
	count(changed)

	// App shell.
	for _, f := range []string{"garden.css", "garden.js"} {
		b, err := g.themeFile(f)
		if err != nil {
			return nil, err
		}
		changed, err := writeIfChanged(filepath.Join(out, f), b)
		if err != nil {
			return nil, err
		}
		count(changed)
	}
	shell, err := g.renderTemplate("index.html", map[string]any{"Title": cfg.Title, "Description": cfg.Description})
	if err != nil {
		return nil, err
	}
	if changed, err = writeIfChanged(filepath.Join(out, "index.html"), shell); err != nil {
		return nil, err
	}
	count(changed)

	// A plain page per note.
	if cfg.staticPages() {
		titles := make(map[string]string, len(g.Notes))
		for _, n := range g.Notes {
			titles[n.ID] = n.Title
		}
		if err := parallel(ctx, cfg.Workers, len(g.Notes), func(i int) error {
			n := g.Notes[i]
			page, err := g.renderTemplate("note.html", map[string]any{
				"Site": cfg.Title, "Title": n.Title, "Excerpt": n.Excerpt, "ID": n.ID,
				"Base": strings.Repeat("../", strings.Count(n.ID, "/")+1), "Body": template.HTML(html(n)),
				"Back": g.back[n.ID], "Titles": titles,
			})
			if err != nil {
				return err
			}
			changed, err := writeIfChanged(filepath.Join(out, "notes", filepath.FromSlash(n.ID)+".html"), page)
			count(changed)
			return err
		}); err != nil {
			return nil, err
		}
	}
	phase("write")

	st.Written, st.Unchanged = int(written.Load()), int(unchanged.Load())
	st.Took = time.Since(start)
	return st, nil
}

func (g *Garden) themeFile(name string) ([]byte, error) {
	if dir := g.Config.Theme; dir != "" {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			return b, nil
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return fs.ReadFile(DefaultTheme(), name)
}

func (g *Garden) renderTemplate(name string, data any) ([]byte, error) {
	src, err := g.themeFile(name)
	if err != nil {
		return nil, err
	}
	tpl, err := template.New(name).Funcs(template.FuncMap{
		"href":      func(id string) string { return noteHref(id, "") },
		"title":     func(titles map[string]string, id string) string { return titles[id] },
		"stackHash": stackHash,
	}).Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("garden: theme %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("garden: theme %s: %w", name, err)
	}
	return buf.Bytes(), nil
}

// stackHash encodes a note ID the way garden.js reads it from the URL
// fragment: bytes outside [A-Za-z0-9_-] become ".XX".
func stackHash(id string) string {
	var b strings.Builder
	for _, c := range []byte(id) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, ".%02X", c)
		}
	}
	return b.String()
}

func writeIfChanged(dst string, b []byte) (bool, error) {
	if old, err := os.ReadFile(dst); err == nil && bytes.Equal(old, b) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(dst, b, 0o644)
}

func copyIfChanged(fsys fs.FS, src, dst string) (bool, error) {
	si, err := fs.Stat(fsys, src)
	if err != nil {
		return false, err
	}
	if di, err := os.Stat(dst); err == nil && di.Size() == si.Size() && !di.ModTime().Before(si.ModTime()) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	in, err := fsys.Open(src)
	if err != nil {
		return false, err
	}
	defer in.Close()
	f, err := os.Create(dst)
	if err != nil {
		return false, err
	}
	if _, err := io.Copy(f, in); err != nil {
		f.Close()
		return false, err
	}
	return true, f.Close()
}
