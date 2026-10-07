package garden

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"
)

// Config describes a garden: where its notes live and how to publish them.
// It is usually read from a garden.yaml file with [LoadConfig], but can be
// built in code. Relative paths in a config file resolve against the file's
// directory; relative paths set in code resolve against the working
// directory.
type Config struct {
	// Title and Description are used for the site's <title> and metadata.
	Title       string `yaml:"title"`
	Description string `yaml:"description"`

	// Vault is the folder of markdown notes. Ignored when FS is set.
	Vault string `yaml:"vault"`

	// FS, when set, is read instead of the Vault directory. Paths in it are
	// vault-relative, e.g. "ideas/backlinks.md".
	FS fs.FS `yaml:"-"`

	// Out is the folder Write and Build write the site to. Default "dist".
	Out string `yaml:"out"`

	// Home is the ID of the note opened first (its path without ".md").
	// When blank, a note at the vault root named index.md is used if there
	// is one; otherwise an index page is generated: the most-linked notes
	// and a contents tree. When set, the note must exist.
	Home string `yaml:"home"`

	// Exclude lists vault-relative patterns to skip. "dir/**" skips a whole
	// folder; other patterns use path.Match against the full path and the
	// file name.
	Exclude []string `yaml:"exclude"`

	// Nav selects the contents tree: "folders" (default) mirrors the vault's
	// folders; "mkdocs:path/to/mkdocs.yml" reads an MkDocs nav.
	Nav string `yaml:"nav"`

	// PublicOnly publishes only notes whose frontmatter has `public: true`.
	// Links to other notes render as plain text, without their names.
	PublicOnly bool `yaml:"public_only"`

	// SitePrefix is an absolute URL prefix (e.g. "/blue-book/") to strip
	// from links and raw HTML, for vaults written for a site in a subpath.
	SitePrefix string `yaml:"site_prefix"`

	// HeadingShift is added to every heading level. 1 turns "# X" into <h2>,
	// matching MkDocs' toc baselevel: 2.
	HeadingShift int `yaml:"heading_shift"`

	// SourceURL, if set, adds a "Source" link to each note: SourceURL + the
	// note's vault path.
	SourceURL string `yaml:"source_url"`

	// StaticPages also writes notes/<id>.html, a plain page per note for
	// search engines and readers without JavaScript. Default true.
	StaticPages *bool `yaml:"static_pages"`

	// ShardKB is the target size of each note data file. Default 512.
	ShardKB int `yaml:"shard_kb"`

	// MaxAssetMB skips copying assets larger than this (0 means no limit).
	// References to skipped assets point at AssetFallbackURL instead.
	MaxAssetMB       float64 `yaml:"max_asset_mb"`
	AssetFallbackURL string  `yaml:"asset_fallback_url"`

	// Theme is a folder whose garden.css, garden.js, index.html or note.html
	// replace the built-in ones. Default "theme" next to the config file,
	// used only if it exists.
	Theme string `yaml:"theme"`

	// Workers is how many notes are processed at once. Default: CPU count.
	Workers int `yaml:"workers"`

	dir string // directory relative paths resolve against
}

// LoadConfig reads a garden.yaml file.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Config{}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c.dir, err = filepath.Abs(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if c.Theme == "" {
		if info, err := os.Stat(filepath.Join(c.dir, "theme")); err == nil && info.IsDir() {
			c.Theme = "theme"
		}
	}
	return c, nil
}

// normalized returns a copy with defaults filled in and paths made absolute.
func (c Config) normalized() (*Config, error) {
	if c.FS == nil {
		if c.Vault == "" {
			return nil, errors.New("garden: config needs a vault folder (or FS)")
		}
		c.Vault = c.abs(c.Vault)
		c.FS = os.DirFS(c.Vault)
	}
	if c.Out == "" {
		c.Out = "dist"
	}
	c.Out = c.abs(c.Out)
	if c.Theme != "" {
		c.Theme = c.abs(c.Theme)
	}
	if c.Title == "" {
		c.Title = "Notes"
	}
	if c.Nav == "" {
		c.Nav = "folders"
	}
	if c.ShardKB <= 0 {
		c.ShardKB = 512
	}
	if c.Workers <= 0 {
		c.Workers = runtime.NumCPU()
	}
	return &c, nil
}

func (c *Config) abs(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	if c.dir == "" {
		a, _ := filepath.Abs(p)
		return a
	}
	return filepath.Join(c.dir, p)
}

func (c *Config) staticPages() bool { return c.StaticPages == nil || *c.StaticPages }
