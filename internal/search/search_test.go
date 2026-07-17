package search

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	matches, err := Run(context.Background(), "", ModeSubstring, candidates, 1_000_000)
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

	matches, err := Run(context.Background(), "needle", ModeSubstring, candidates, 1_000_000)
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

	matches, err := Run(context.Background(), "needle", ModeSubstring, candidates, 1_000_000)
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

	matches, err := Run(context.Background(), "needle", ModeSubstring, candidates, 1_000_000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected binary file to never match, got %v", matches)
	}
}

func TestRunSkipsUnreadablePathsWithoutFailing(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.txt")
	present := writeFile(t, dir, "present.txt", "needle here\n")
	candidates := []Candidate{
		{AbsPath: missing, RelPath: "does-not-exist.txt"},
		{AbsPath: present, RelPath: "present.txt"},
	}

	matches, err := Run(context.Background(), "needle", ModeSubstring, candidates, 1_000_000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].RelPath != "present.txt" {
		t.Fatalf("expected only the readable file to match, got %v", matches)
	}
}

func TestRunDoesNotMatchBeyondByteCap(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("x", 100) + "\nneedle after the cap\n"
	path := writeFile(t, dir, "a.txt", content)
	candidates := []Candidate{{AbsPath: path, RelPath: "a.txt"}}

	matches, err := Run(context.Background(), "needle", ModeSubstring, candidates, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no match when the match falls beyond the byte cap, got %v", matches)
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

	matches, err := Run(context.Background(), "needle", ModeSubstring, candidates, 1_000_000)
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

	matches, err := Run(ctx, "needle", ModeSubstring, candidates, 1_000_000)
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

	matches, err := Run(context.Background(), `foo\d+`, ModeRegex, candidates, 1_000_000)
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

	matches, err := Run(context.Background(), `hello`, ModeRegex, candidates, 1_000_000)
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

	matches, err := Run(context.Background(), `foo(`, ModeRegex, candidates, 1_000_000)
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

	matches, err := Run(context.Background(), "foo(bar)", ModeSubstring, candidates, 1_000_000)
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
