package search

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing fixture %s: %v", name, err)
	}
	return path
}

func TestRunEmptyQueryMatchesNothing(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", "hello world\n")
	candidates := []Candidate{{AbsPath: path, RelPath: "a.txt"}}

	matches, err := Run(context.Background(), "", ModeSubstring, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if matches == nil {
		t.Fatal("expected a non-nil (possibly empty) slice, got nil")
	}
	if len(matches) != 0 {
		t.Fatalf("expected no matches for empty query, got %v", matches)
	}
}

func TestRunPlainSubstringMatchIsCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", "line one\nline TWO has Needle\nline three\n")
	candidates := []Candidate{{AbsPath: path, RelPath: "a.txt"}}

	matches, err := Run(context.Background(), "needle", ModeSubstring, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d: %v", len(matches), matches)
	}
	m := matches[0]
	if m.AbsPath != path || m.RelPath != "a.txt" {
		t.Fatalf("unexpected match identity: %+v", m)
	}
	if m.Issue != "" {
		t.Fatalf("expected no issue for a clean scan, got %q", m.Issue)
	}
	if len(m.Hits) != 1 || m.Hits[0].LineNum != 2 {
		t.Fatalf("expected match on line 2, got hits %+v", m.Hits)
	}
	if m.Hits[0].LineText != "line TWO has Needle" {
		t.Fatalf("unexpected line text: %q", m.Hits[0].LineText)
	}
}

func TestRunReportsEveryMatchingLineInFile(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", "no match here\nfirst needle\nsecond needle\n")
	candidates := []Candidate{{AbsPath: path, RelPath: "a.txt"}}

	matches, err := Run(context.Background(), "needle", ModeSubstring, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match (one file), got %d", len(matches))
	}
	hits := matches[0].Hits
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits within the file, got %d: %+v", len(hits), hits)
	}
	if hits[0].LineNum != 2 || hits[1].LineNum != 3 {
		t.Fatalf("expected hits on lines 2 and 3 in source order, got %+v", hits)
	}
}

func TestRunSkipsBinaryFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bin.dat")
	if err := os.WriteFile(path, []byte("needle\x00binary"), 0o644); err != nil {
		t.Fatalf("writing binary fixture: %v", err)
	}
	candidates := []Candidate{{AbsPath: path, RelPath: "bin.dat"}}

	matches, err := Run(context.Background(), "needle", ModeSubstring, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected binary file to never match, got %v", matches)
	}
}

func TestRunReportsUnreadablePathsAsAnIssueInstead(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.txt")
	present := writeFile(t, dir, "present.txt", "needle here\n")
	candidates := []Candidate{
		{AbsPath: missing, RelPath: "does-not-exist.txt"},
		{AbsPath: present, RelPath: "present.txt"},
	}

	matches, err := Run(context.Background(), "needle", ModeSubstring, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("expected the readable file to match and the unreadable one to be reported as an issue, got %v", matches)
	}
	byPath := map[string]FileResult{}
	for _, m := range matches {
		byPath[m.RelPath] = m
	}
	present1, ok := byPath["present.txt"]
	if !ok || len(present1.Hits) != 1 || present1.Issue != "" {
		t.Fatalf("expected present.txt to match cleanly, got %+v", present1)
	}
	missing1, ok := byPath["does-not-exist.txt"]
	if !ok || len(missing1.Hits) != 0 || missing1.Issue == "" {
		t.Fatalf("expected does-not-exist.txt to be reported with a non-empty Issue and no hits, got %+v", missing1)
	}
}

func TestRunScansPastTheOldByteCap(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("x", 2_000_000) + "\nneedle past the old cap\n"
	path := writeFile(t, dir, "a.txt", content)
	candidates := []Candidate{{AbsPath: path, RelPath: "a.txt"}}

	matches, err := Run(context.Background(), "needle", ModeSubstring, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected the match past the old 1MB cap to now be found, got %v", matches)
	}
	if matches[0].Issue != "" {
		t.Fatalf("expected a clean scan with no issue, got %q", matches[0].Issue)
	}
}

func TestRunPerFileTimeoutReportsIssueWithPartialHits(t *testing.T) {
	orig := perFileTimeout
	perFileTimeout = time.Nanosecond
	t.Cleanup(func() { perFileTimeout = orig })

	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", "needle\nmore needle text\n")
	candidates := []Candidate{{AbsPath: path, RelPath: "a.txt"}}

	matches, err := Run(context.Background(), "needle", ModeSubstring, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected the file to still produce a result despite the timeout, got %v", matches)
	}
	if matches[0].Issue == "" {
		t.Fatalf("expected an near-instant timeout to be reported as an issue, got %+v", matches[0])
	}
}

// TestRunManyPerFileTimeoutsDoNotHangTheScan is a regression test for a
// deadlock found while developing this: dispatching more candidates than
// maxConcurrentScans while nothing yet drains resultCh wedges the
// semaphore forever (Run.<dispatch> blocks trying to acquire a slot,
// Run's own collector loop — the only reader of resultCh — never starts
// because it's sequenced after the full dispatch loop). Many candidates
// all hitting a tiny per-file timeout, well over maxConcurrentScans in
// count, exercises exactly that path; the test's own -timeout catches a
// regression.
func TestRunManyPerFileTimeoutsDoNotHangTheScan(t *testing.T) {
	origTimeout := perFileTimeout
	perFileTimeout = time.Nanosecond
	t.Cleanup(func() { perFileTimeout = origTimeout })

	dir := t.TempDir()
	candidates := make([]Candidate, 0, maxConcurrentScans*3)
	for i := range cap(candidates) {
		name := fmt.Sprintf("f%03d.txt", i)
		path := writeFile(t, dir, name, "needle\n")
		candidates = append(candidates, Candidate{AbsPath: path, RelPath: name})
	}

	matches, err := Run(context.Background(), "needle", ModeSubstring, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != len(candidates) {
		t.Fatalf("expected every candidate to produce a result, got %d of %d", len(matches), len(candidates))
	}
	for _, m := range matches {
		if m.Issue == "" {
			t.Fatalf("expected every candidate to report the near-instant timeout, got %+v", m)
		}
	}
}

func TestRunBoundedConcurrencyProducesSameResultsAsUnbounded(t *testing.T) {
	dir := t.TempDir()
	candidates := make([]Candidate, 0, 20)
	for i := range 20 {
		name := fmt.Sprintf("f%02d.txt", i)
		content := "no match\n"
		if i%3 == 0 {
			content = "has needle\n"
		}
		path := writeFile(t, dir, name, content)
		candidates = append(candidates, Candidate{AbsPath: path, RelPath: name})
	}

	origLimit := maxConcurrentScans
	t.Cleanup(func() { maxConcurrentScans = origLimit })

	maxConcurrentScans = 1
	serial, err := Run(context.Background(), "needle", ModeSubstring, candidates)
	if err != nil {
		t.Fatalf("unexpected error (serial): %v", err)
	}

	maxConcurrentScans = origLimit
	concurrent, err := Run(context.Background(), "needle", ModeSubstring, candidates)
	if err != nil {
		t.Fatalf("unexpected error (concurrent): %v", err)
	}

	if len(serial) != len(concurrent) {
		t.Fatalf("expected the same result count regardless of concurrency, got serial=%d concurrent=%d", len(serial), len(concurrent))
	}
	for i := range serial {
		if serial[i].RelPath != concurrent[i].RelPath {
			t.Fatalf("expected identical ordering, got serial[%d]=%s concurrent[%d]=%s", i, serial[i].RelPath, i, concurrent[i].RelPath)
		}
	}
}

func TestRunSortsResultsByRelPathCaseInsensitively(t *testing.T) {
	dir := t.TempDir()
	pathB := writeFile(t, dir, "b.txt", "needle\n")
	pathA := writeFile(t, dir, "A.txt", "needle\n")
	candidates := []Candidate{
		{AbsPath: pathB, RelPath: "b.txt"},
		{AbsPath: pathA, RelPath: "A.txt"},
	}

	matches, err := Run(context.Background(), "needle", ModeSubstring, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	if matches[0].RelPath != "A.txt" || matches[1].RelPath != "b.txt" {
		t.Fatalf("expected case-insensitive sort order [A.txt, b.txt], got [%s, %s]", matches[0].RelPath, matches[1].RelPath)
	}
}

func TestRunStopsEarlyWhenContextCanceled(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", "needle\n")
	candidates := []Candidate{{AbsPath: path, RelPath: "a.txt"}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	matches, err := Run(ctx, "needle", ModeSubstring, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected an already-canceled context to short-circuit before scanning, got %v", matches)
	}
}

func TestRunRegexModeMatchesPattern(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", "foo1\nbar\nfoo22\nbaz\n")
	candidates := []Candidate{{AbsPath: path, RelPath: "a.txt"}}

	matches, err := Run(context.Background(), `foo\d+`, ModeRegex, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 file match, got %d", len(matches))
	}
	hits := matches[0].Hits
	if len(hits) != 2 || hits[0].LineNum != 1 || hits[1].LineNum != 3 {
		t.Fatalf("expected regex hits on lines 1 and 3, got %+v", hits)
	}
}

func TestRunRegexModeIsCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", "Hello World\n")
	candidates := []Candidate{{AbsPath: path, RelPath: "a.txt"}}

	matches, err := Run(context.Background(), `hello`, ModeRegex, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
}

func TestRunRegexModeInvalidPatternReturnsErrorWithoutScanning(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", "needle\n")
	candidates := []Candidate{{AbsPath: path, RelPath: "a.txt"}}

	matches, err := Run(context.Background(), `foo(`, ModeRegex, candidates)
	if err == nil {
		t.Fatal("expected an error for an invalid regex pattern")
	}
	if matches != nil {
		t.Fatalf("expected no results returned alongside a compile error, got %v", matches)
	}
}

func TestRunSubstringModeTreatsRegexMetacharactersLiterally(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", "foo(bar)\nfoo1bar\n")
	candidates := []Candidate{{AbsPath: path, RelPath: "a.txt"}}

	matches, err := Run(context.Background(), "foo(bar)", ModeSubstring, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 file match, got %d", len(matches))
	}
	hits := matches[0].Hits
	if len(hits) != 1 || hits[0].LineNum != 1 {
		t.Fatalf("expected substring mode to match only the literal parenthesized text, got %+v", hits)
	}
}
