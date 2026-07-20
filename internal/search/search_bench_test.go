package search

import (
	"context"
	"fmt"
	"os"
	"testing"
)

// benchDir returns the directory to scan for benchmarking, from the
// DIRTREE_BENCH_DIR env var. Skips immediately if unset, so a plain `go
// test ./...` run never touches the filesystem beyond the module — these
// benchmarks only run when explicitly pointed at real data (see
// examples/README.md for how to fetch some).
func benchDir(b *testing.B) string {
	b.Helper()
	dir := os.Getenv("DIRTREE_BENCH_DIR")
	if dir == "" {
		b.Skip("set DIRTREE_BENCH_DIR to a directory to scan, e.g. DIRTREE_BENCH_DIR=examples/data/linux — see examples/README.md")
	}
	return dir
}

// benchQuery returns the query to scan for, from the DIRTREE_BENCH_QUERY
// env var, defaulting to a fixed term if unset. Match count doesn't affect
// scan cost (every line is read regardless of whether it matches), so the
// default only needs to be non-empty.
func benchQuery() string {
	if q := os.Getenv("DIRTREE_BENCH_QUERY"); q != "" {
		return q
	}
	return "TODO"
}

// BenchmarkRunConcurrency measures Run's throughput across a range of
// maxConcurrentScans values, to pick that knob's default from real
// numbers instead of judgment. Run with -benchtime=1x (each subtest scans
// the whole tree once per b.N iteration, so the default auto-scaling of
// b.N would otherwise repeat a multi-second scan many times over trying
// to stabilize timing): e.g.
//
//	DIRTREE_BENCH_DIR=examples/data/linux go test ./internal/search/... -run=^$ -bench=. -benchtime=1x
func BenchmarkRunConcurrency(b *testing.B) {
	dir := benchDir(b)
	query := benchQuery()
	candidates, err := WalkCandidates(dir)
	if err != nil {
		b.Fatalf("walking %s: %v", dir, err)
	}

	for _, n := range []int{1, 2, 4, 8, 16, 32, 64, 128} {
		b.Run(fmt.Sprintf("concurrency=%d", n), func(b *testing.B) {
			prev := maxConcurrentScans
			maxConcurrentScans = n
			defer func() { maxConcurrentScans = prev }()

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := Run(context.Background(), query, ModeSubstring, candidates); err != nil {
					b.Fatalf("Run: %v", err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(len(candidates)*b.N)/b.Elapsed().Seconds(), "files/sec")
		})
	}
}
