package garden_test

import (
	"context"
	"fmt"
	"log"
	"testing/fstest"

	"github.com/zoravur/garden"
)

// Load a vault into memory and walk its link graph without writing a site.
func ExampleLoad() {
	vault := fstest.MapFS{
		"index.md":           {Data: []byte("Start with [[Evergreen notes]].")},
		"Evergreen notes.md": {Data: []byte("They grow through [[ideas/Backlinks|backlinks]].")},
		"ideas/Backlinks.md": {Data: []byte("A link seen from the other end. Back to [[index]].")},
	}
	g, err := garden.Load(context.Background(), &garden.Config{FS: vault})
	if err != nil {
		log.Fatal(err)
	}
	for _, n := range g.Notes {
		fmt.Printf("%-16s links out: %d, linked from: %d\n", n.Title, len(n.Links), len(g.Backlinks(n.ID)))
	}
	// Output:
	// Evergreen notes  links out: 1, linked from: 1
	// Backlinks        links out: 1, linked from: 1
	// Index            links out: 1, linked from: 1
}

// Build a site from a garden.yaml, as the garden command does.
func ExampleBuild() {
	cfg, err := garden.LoadConfig("garden.yaml")
	if err != nil {
		log.Fatal(err)
	}
	_, stats, err := garden.Build(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%d notes written to %s\n", stats.Notes, cfg.Out)
}
