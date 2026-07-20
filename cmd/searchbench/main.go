// Command searchbench is a dev tool (not part of the dirtree release,
// see .goreleaser.yaml) that times content-search scans (internal/search)
// over a directory: either one aggregate scan, for comparison against
// external tools like ripgrep/grep/ag (see examples/compare_search.sh),
// or -per-file-stats mode, which times each candidate individually and
// reports the latency distribution — used to inform internal/search's
// perFileTimeout default (see examples/README.md).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/nitti/dirtree/internal/search"
)

func main() {
	dir := flag.String("dir", "", "directory to scan (required)")
	query := flag.String("query", "", "query to search for (required)")
	regex := flag.Bool("regex", false, "interpret query as a regular expression")
	perFileStats := flag.Bool("per-file-stats", false, "time each candidate individually and report the latency distribution, instead of one aggregate scan")
	flag.Parse()

	if *dir == "" || *query == "" {
		fmt.Fprintln(os.Stderr, "usage: searchbench -dir <path> -query <term> [-regex] [-per-file-stats]")
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

	if *perFileStats {
		runPerFileStats(candidates, *query, mode)
		return
	}
	runAggregate(candidates, *query, mode)
}

func runAggregate(candidates []search.Candidate, query string, mode search.Mode) {
	start := time.Now()
	results, err := search.Run(context.Background(), query, mode, candidates)
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

// runPerFileStats times query against each candidate individually (one
// search.Run call per file) and reports the resulting latency
// distribution, rather than search.Run's usual aggregate-over-all-files
// timing. Each call's goroutine/channel dispatch overhead (search.Run
// always fans out through a semaphore-gated goroutine even for a single
// candidate) is negligible next to real file I/O and regex cost for this
// purpose, so it's left as is rather than special-cased away. This is
// what perFileTimeout's default should be sized against — see
// examples/README.md.
func runPerFileStats(candidates []search.Candidate, query string, mode search.Mode) {
	type timing struct {
		relPath string
		elapsed time.Duration
	}
	timings := make([]timing, 0, len(candidates))

	ctx := context.Background()
	for _, c := range candidates {
		start := time.Now()
		if _, err := search.Run(ctx, query, mode, []search.Candidate{c}); err != nil {
			fmt.Fprintf(os.Stderr, "searchbench: %s: %v\n", c.RelPath, err)
			continue
		}
		timings = append(timings, timing{relPath: c.RelPath, elapsed: time.Since(start)})
	}

	if len(timings) == 0 {
		fmt.Println("no files scanned")
		return
	}

	sort.Slice(timings, func(i, j int) bool { return timings[i].elapsed < timings[j].elapsed })

	percentile := func(p float64) time.Duration {
		idx := int(p * float64(len(timings)-1))
		return timings[idx].elapsed
	}

	fmt.Printf("dirtree: %d files, per-file scan latency:\n", len(timings))
	fmt.Printf("  min:   %s\n", timings[0].elapsed)
	fmt.Printf("  p50:   %s\n", percentile(0.50))
	fmt.Printf("  p90:   %s\n", percentile(0.90))
	fmt.Printf("  p99:   %s\n", percentile(0.99))
	fmt.Printf("  p999:  %s\n", percentile(0.999))
	fmt.Printf("  max:   %s\n", timings[len(timings)-1].elapsed)

	fmt.Println("slowest files:")
	n := 5
	if len(timings) < n {
		n = len(timings)
	}
	for i := 0; i < n; i++ {
		t := timings[len(timings)-1-i]
		fmt.Printf("  %s\t%s\n", t.elapsed, t.relPath)
	}
}
