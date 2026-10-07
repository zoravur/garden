package garden

import (
	"bytes"
	"io/fs"
	"net/url"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// vault is the scanned file tree: which notes and assets exist and how link
// targets map onto them. It is read-only after scanning, so render workers
// share it without locking.
type vault struct {
	cfg       *Config
	notes     []string          // published note IDs, sorted
	noteSet   map[string]bool   // published ID -> true
	byBase    map[string]string // lowercased "kubectl.md" -> ID
	private   map[string]bool   // IDs and lowercased base names of unpublished notes
	assets    map[string]bool   // "img/foo.png" -> true
	assetBase map[string]string // lowercased "foo.png" -> "img/foo.png"
}

func scanVault(cfg *Config) (*vault, error) {
	v := &vault{cfg: cfg, noteSet: map[string]bool{}, byBase: map[string]string{},
		private: map[string]bool{}, assets: map[string]bool{}, assetBase: map[string]string{}}
	err := fs.WalkDir(cfg.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if p != "." && (strings.HasPrefix(name, ".") || v.excluded(p+"/")) {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") || v.excluded(p) {
			return nil
		}
		if strings.EqualFold(path.Ext(name), ".md") {
			id := p[:len(p)-3]
			v.notes = append(v.notes, id)
			v.noteSet[id] = true
			// First one wins on a file name clash; a relative or rooted
			// path still reaches the others.
			if _, dup := v.byBase[strings.ToLower(name)]; !dup {
				v.byBase[strings.ToLower(name)] = id
			}
			return nil
		}
		v.assets[p] = true
		if _, dup := v.assetBase[strings.ToLower(name)]; !dup {
			v.assetBase[strings.ToLower(name)] = p
		}
		return nil
	})
	return v, err
}

// unpublish removes notes that aren't public; links to them then resolve to
// targetPrivate, which renders without revealing the note's name.
func (v *vault) unpublish(keep func(id string) bool) {
	var ids []string
	for _, id := range v.notes {
		if keep(id) {
			ids = append(ids, id)
			continue
		}
		delete(v.noteSet, id)
		v.private[id] = true
		v.private[strings.ToLower(path.Base(id))+".md"] = true
	}
	v.notes = ids
	for b, id := range v.byBase {
		if !v.noteSet[id] {
			delete(v.byBase, b)
		}
	}
	// A public note may share a file name with a private one.
	for _, id := range v.notes {
		b := strings.ToLower(path.Base(id)) + ".md"
		if _, ok := v.byBase[b]; !ok {
			v.byBase[b] = id
		}
	}
}

func (v *vault) excluded(rel string) bool {
	for _, pat := range v.cfg.Exclude {
		if strings.HasSuffix(pat, "/**") {
			if strings.HasPrefix(rel, strings.TrimSuffix(pat, "**")) {
				return true
			}
			continue
		}
		if ok, _ := path.Match(pat, strings.TrimSuffix(rel, "/")); ok {
			return true
		}
		if ok, _ := path.Match(pat, path.Base(rel)); ok {
			return true
		}
	}
	return false
}

type targetKind int

const (
	targetExternal targetKind = iota
	targetNote
	targetAsset
	targetMissing
	targetPrivate
)

// resolve maps a link destination written inside note `from` to what it
// points at. Order: relative to the note, then from the vault root, then by
// file name anywhere in the vault (how mkdocs-autolinks and Obsidian both
// behave).
func (v *vault) resolve(from, dest string) (kind targetKind, target, anchor string) {
	d := strings.TrimSpace(dest)
	if d == "" {
		return targetExternal, dest, ""
	}
	if i := strings.Index(d, ":"); i > 0 && !strings.ContainsAny(d[:i], "/#.") || strings.HasPrefix(d, "//") {
		return targetExternal, dest, "" // has a scheme: https:, mailto:, ...
	}
	if i := strings.IndexByte(d, '#'); i >= 0 {
		d, anchor = d[:i], d[i+1:]
	}
	if i := strings.IndexByte(d, '?'); i >= 0 {
		d = d[:i]
	}
	if d == "" {
		return targetNote, from, anchor // same-note anchor
	}
	if u, err := url.PathUnescape(d); err == nil {
		d = u
	}
	rooted := false
	if p := v.cfg.SitePrefix; p != "" && strings.HasPrefix(d, p) {
		d, rooted = strings.TrimPrefix(d, p), true
	} else if strings.HasPrefix(d, "/") {
		d, rooted = strings.TrimPrefix(d, "/"), true
	}

	var candidates []string
	if !rooted {
		candidates = append(candidates, path.Join(path.Dir(from), d))
	}
	candidates = append(candidates, path.Clean(d))

	ext := strings.ToLower(path.Ext(d))
	noteLike := ext == ".md" || ext == ""
	if noteLike {
		for _, c := range candidates {
			if strings.EqualFold(path.Ext(c), ".md") {
				c = c[:len(c)-3]
			}
			for _, id := range []string{c, c + "/index"} {
				if v.noteSet[id] {
					return targetNote, id, anchor
				}
				if v.private[id] {
					return targetPrivate, "", ""
				}
			}
		}
		base := strings.ToLower(path.Base(strings.TrimSuffix(d, "/")))
		if ext == "" {
			base += ".md"
		}
		if id, ok := v.byBase[base]; ok {
			return targetNote, id, anchor
		}
		if v.private[base] {
			return targetPrivate, "", ""
		}
		if ext == ".md" {
			return targetMissing, d, anchor
		}
	}
	for _, c := range candidates {
		if v.assets[c] {
			return targetAsset, c, anchor
		}
	}
	if a, ok := v.assetBase[strings.ToLower(path.Base(d))]; ok {
		return targetAsset, a, anchor
	}
	if noteLike {
		return targetMissing, d, anchor
	}
	return targetExternal, dest, anchor
}

// splitFrontmatter separates a leading YAML block from the body. Malformed
// frontmatter is treated as empty rather than failing the build.
func splitFrontmatter(src []byte) (map[string]any, []byte) {
	if !bytes.HasPrefix(src, []byte("---\n")) && !bytes.HasPrefix(src, []byte("---\r\n")) {
		return nil, src
	}
	rest := src[bytes.IndexByte(src, '\n')+1:]
	for i := 0; i < len(rest); {
		j := bytes.IndexByte(rest[i:], '\n')
		line := rest[i:]
		if j >= 0 {
			line = rest[i : i+j]
		}
		if t := bytes.TrimRight(line, "\r "); bytes.Equal(t, []byte("---")) || bytes.Equal(t, []byte("...")) {
			fm := map[string]any{}
			_ = yaml.Unmarshal(rest[:i], &fm)
			if j < 0 {
				return fm, nil
			}
			return fm, rest[i+j+1:]
		}
		if j < 0 {
			break
		}
		i += j + 1
	}
	return nil, src
}
