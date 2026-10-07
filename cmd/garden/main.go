// Command garden builds a stacked-pane knowledge garden from a folder of
// markdown notes.
//
// Usage:
//
//	garden build [-c garden.yaml] [-o dir] [-report file] [-q]
//	garden serve [-c garden.yaml] [-addr localhost:8080]
//	garden init  [dir]
//	garden version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/zoravur/garden"
)

const usage = `garden turns a folder of markdown notes into a stacked-pane website.

Usage:
  garden build [-c garden.yaml] [-o dir] [-report file] [-q]
  garden serve [-c garden.yaml] [-addr localhost:8080]
  garden init  [dir]      write a starter garden.yaml and vault
  garden version
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "build":
		err = runBuild(ctx, args)
	case "serve":
		err = runServe(ctx, args)
	case "init":
		err = runInit(args)
	case "version", "-v", "--version":
		fmt.Println(version())
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "garden: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "garden:", err)
		os.Exit(1)
	}
}

func runBuild(ctx context.Context, args []string) error {
	fl := flag.NewFlagSet("build", flag.ExitOnError)
	cfgPath := fl.String("c", "garden.yaml", "config file")
	out := fl.String("o", "", "output folder (overrides `out` in the config)")
	report := fl.String("report", "", "write broken links to this file")
	quiet := fl.Bool("q", false, "print nothing on success")
	_ = fl.Parse(args)

	cfg, err := garden.LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	if *out != "" {
		cfg.Out = *out
	}
	g, st, err := garden.Build(ctx, cfg)
	if err != nil {
		return err
	}
	if *report != "" {
		if err := os.WriteFile(*report, []byte(g.Report()), 0o644); err != nil {
			return err
		}
	}
	if *quiet {
		return nil
	}
	for _, p := range st.Phases {
		fmt.Printf("  %-10s %v\n", p.Name, p.Took.Round(time.Millisecond))
	}
	fmt.Printf("%d notes, %d links, %d broken; %d assets (%d too large, linked to fallback)\n",
		st.Notes, st.Links, st.Broken, st.AssetsCopied, st.AssetsSkipped)
	fmt.Printf("%d files written, %d unchanged -> %s in %v\n",
		st.Written, st.Unchanged, g.Config.Out, st.Took.Round(time.Millisecond))
	if home, ok := g.Note(g.Config.Home); ok && home.Generated {
		fmt.Println("no index.md in the vault, so the home page is a generated index (set `home` to choose a note)")
	}
	if st.Broken > 0 && *report == "" {
		fmt.Println("run with -report broken.txt to list broken links")
	}
	return nil
}

// runServe builds, serves the output, and rebuilds when anything in the
// vault changes (checked once a second).
func runServe(ctx context.Context, args []string) error {
	fl := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fl.String("c", "garden.yaml", "config file")
	addr := fl.String("addr", "localhost:8080", "listen address")
	_ = fl.Parse(args)

	cfg, err := garden.LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	g, _, err := garden.Build(ctx, cfg)
	if err != nil {
		return err
	}
	out, vaultFS := g.Config.Out, g.Config.FS

	go func() {
		last := latestMod(vaultFS)
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			m := latestMod(vaultFS)
			if !m.After(last) {
				continue
			}
			last = m
			_, st, err := garden.Build(ctx, cfg)
			if err != nil {
				fmt.Fprintln(os.Stderr, "build:", err)
				continue
			}
			fmt.Printf("rebuilt: %d files changed in %v\n", st.Written, st.Took.Round(time.Millisecond))
		}
	}()

	srv := &http.Server{Addr: *addr, Handler: http.FileServer(http.Dir(out))}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	fmt.Printf("serving %s at http://%s (Ctrl-C to stop)\n", out, *addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func latestMod(fsys fs.FS) time.Time {
	var t time.Time
	_ = fs.WalkDir(fsys, ".", func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info, err := d.Info(); err == nil && info.ModTime().After(t) {
			t = info.ModTime()
		}
		return nil
	})
	return t
}

const starterConfig = `title: My Garden
description: Working notes, linked together.
vault: vault
out: dist
nav: folders
# home: index          # note to open first; blank uses index.md, or generates an index page
# public_only: true   # publish only notes with "public: true" in their frontmatter
`

const starterNote = `---
title: Start here
---

Every link opens a note beside this one. Write a new note as a markdown
file in this folder and link to it with ` + "`[[Its Title]]`" + ` or ` + "`[a label](its-file.md)`" + `.

!!! tip "Building"
    Run ` + "`garden serve`" + ` to preview, or ` + "`garden build`" + ` to write the site to dist/.
`

func runInit(args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	cfg := filepath.Join(dir, "garden.yaml")
	if _, err := os.Stat(cfg); err == nil {
		return fmt.Errorf("%s already exists", cfg)
	}
	if err := os.MkdirAll(filepath.Join(dir, "vault"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(cfg, []byte(starterConfig), 0o644); err != nil {
		return err
	}
	note := filepath.Join(dir, "vault", "index.md")
	if _, err := os.Stat(note); os.IsNotExist(err) {
		if err := os.WriteFile(note, []byte(starterNote), 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("wrote %s and %s\nnext: cd %s && garden serve\n", cfg, note, dir)
	return nil
}

func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return "garden " + bi.Main.Version
	}
	return "garden (devel)"
}
