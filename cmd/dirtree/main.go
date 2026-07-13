// Command dirtree is a terminal directory-tree browser. See
// specs/SPEC.md for the full behavioral specification.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nitti/dirtree/internal/ui"
)

func main() {
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
