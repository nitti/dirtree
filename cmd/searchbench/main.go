// Command searchbench is a dev tool (not part of the dirtree release,
// see .goreleaser.yaml) that times a single content-search scan
// (internal/search) over a directory, so it can be compared against
// external tools like ripgrep/grep/ag — see examples/compare_search.sh.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/nitti/dirtree/internal/search"
)

func main() {
	dir := flag.String("dir", "", "directory to scan (required)")
	query := flag.String("query", "", "query to search for (required)")
	regex := flag.Bool("regex", false, "interpret query as a regular expression")
	flag.Parse()

	if *dir == "" || *query == "" {
		fmt.Fprintln(os.Stderr, "usage: searchbench -dir <path> -query <term> [-regex]")
		os.Exit(2)
	}

	candidates, err := search.WalkCandidates(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "searchbench: %v\n", err)
		os.Exit(1)
	}

	mode := search.ModeSubstring
	if *regex {
		mode = search.ModeRegex
	}

	start := time.Now()
	results, err := search.Run(context.Background(), *query, mode, candidates)
	elapsed := time.Since(start)
	if err != nil {
		fmt.Fprintf(os.Stderr, "searchbench: %v\n", err)
		os.Exit(1)
	}

	matches := 0
	for _, r := range results {
		matches += len(r.Hits)
	}
	fmt.Printf("dirtree: %d files scanned, %d files matched, %d lines matched, %s elapsed\n",
		len(candidates), len(results), matches, elapsed)
}
