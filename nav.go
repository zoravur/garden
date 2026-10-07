package garden

import (
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func buildNav(v *vault, titles map[string]string) ([]*NavNode, error) {
	if p, ok := strings.CutPrefix(v.cfg.Nav, "mkdocs:"); ok {
		return mkdocsNav(v, v.cfg.abs(p), titles)
	}
	return folderNav(v, titles), nil
}

// mkdocsNav reads the `nav:` tree from mkdocs.yml. Entries are either
// "file.md", {Title: "file.md"} or {Title: [children...]}; a section whose
// first child is a bare "file.md" uses it as the section's own page
// (the mkdocs-section-index convention).
func mkdocsNav(v *vault, file string, titles map[string]string) ([]*NavNode, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Nav []any `yaml:"nav"`
	}
	// mkdocs.yml often uses !!python tags; strip them so plain YAML parses.
	clean := strings.NewReplacer("!!python/name:", "", "!!python/object/apply:", "", "!ENV", "").Replace(string(b))
	if err := yaml.Unmarshal([]byte(clean), &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	resolveID := func(p string) string {
		if k, id, _ := v.resolve("index", "/"+p); k == targetNote {
			return id
		}
		return ""
	}
	var walk func(items []any) []*NavNode
	walk = func(items []any) []*NavNode {
		var out []*NavNode
		for _, it := range items {
			switch x := it.(type) {
			case string:
				if id := resolveID(x); id != "" {
					out = append(out, &NavNode{Title: titles[id], ID: id})
				}
			case map[string]any:
				for title, val := range x {
					switch y := val.(type) {
					case string:
						if id := resolveID(y); id != "" {
							out = append(out, &NavNode{Title: title, ID: id})
						}
					case []any:
						n := &NavNode{Title: title}
						if len(y) > 0 {
							if s, ok := y[0].(string); ok {
								n.ID = resolveID(s)
								y = y[1:]
							}
						}
						n.Children = walk(y)
						if n.ID != "" || len(n.Children) > 0 {
							out = append(out, n)
						}
					}
				}
			}
		}
		return out
	}
	return walk(doc.Nav), nil
}

// folderNav mirrors the vault's folders; "index" or a note named like its
// folder becomes the folder's own page.
func folderNav(v *vault, titles map[string]string) []*NavNode {
	root := &NavNode{}
	dirs := map[string]*NavNode{"": root}
	var dirFor func(d string) *NavNode
	dirFor = func(d string) *NavNode {
		if n, ok := dirs[d]; ok {
			return n
		}
		parent := dirFor(parentDir(d))
		n := &NavNode{Title: humanize(path.Base(d))}
		parent.Children = append(parent.Children, n)
		dirs[d] = n
		return n
	}
	ids := append([]string(nil), v.notes...)
	sort.Strings(ids)
	for _, id := range ids {
		d := parentDir(id)
		base := path.Base(id)
		parent := dirFor(d)
		if d != "" && (base == "index" || base == path.Base(d)) && parent.ID == "" {
			parent.ID = id
			continue
		}
		parent.Children = append(parent.Children, &NavNode{Title: titles[id], ID: id})
	}
	var order func(n *NavNode)
	order = func(n *NavNode) {
		sort.SliceStable(n.Children, func(i, j int) bool {
			a, b := n.Children[i], n.Children[j]
			if (len(a.Children) > 0) != (len(b.Children) > 0) {
				return len(a.Children) > 0 // folders first
			}
			return strings.ToLower(a.Title) < strings.ToLower(b.Title)
		})
		for _, c := range n.Children {
			order(c)
		}
	}
	order(root)
	return root.Children
}

func parentDir(id string) string {
	d := path.Dir(id)
	if d == "." {
		return ""
	}
	return d
}
