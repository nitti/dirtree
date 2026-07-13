// Command dirtree is a terminal directory-tree browser. See
// specs/SPEC.md for the full behavioral specification.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nitti/dirtree/internal/ui"
)

// version, commit, and date are set via -ldflags at release build time
// (see .goreleaser.yaml); they stay "dev" for a plain `go build`.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("dirtree %s (commit %s, built %s)\n", version, commit, date)
		return
	}

	path := "."
	if len(os.Args) > 1 {
		path = os.Args[1]
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dirtree: %v\n", err)
		os.Exit(1)
	}

	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "dirtree: not a directory: %s\n", abs)
		os.Exit(1)
	}

	app := ui.New(abs)
	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "dirtree: %v\n", err)
		os.Exit(1)
	}
}
