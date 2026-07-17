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

	matches := Run(context.Background(), "", candidates, 1_000_000)
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

	matches := Run(context.Background(), "needle", candidates, 1_000_000)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d: %v", len(matches), matches)
	}
	m := matches[0]
	if m.AbsPath != path || m.RelPath != "a.txt" {
		t.Fatalf("unexpected match identity: %+v", m)
	}
	if m.LineNum != 2 {
		t.Fatalf("expected match on line 2, got line %d", m.LineNum)
	}
	if m.LineText != "line TWO has Needle" {
		t.Fatalf("unexpected line text: %q", m.LineText)
	}
}

func TestRunReportsFirstMatchingLineOnly(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", "no match here\nfirst needle\nsecond needle\n")
	candidates := []Candidate{{AbsPath: path, RelPath: "a.txt"}}

	matches := Run(context.Background(), "needle", candidates, 1_000_000)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match (one file), got %d", len(matches))
	}
	if matches[0].LineNum != 2 {
		t.Fatalf("expected first match to be reported (line 2), got line %d", matches[0].LineNum)
	}
}

func TestRunSkipsBinaryFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bin.dat")
	if err := os.WriteFile(path, []byte("needle\x00binary"), 0o644); err != nil {
		t.Fatalf("writing binary fixture: %v", err)
	}
	candidates := []Candidate{{AbsPath: path, RelPath: "bin.dat"}}

	matches := Run(context.Background(), "needle", candidates, 1_000_000)
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

	matches := Run(context.Background(), "needle", candidates, 1_000_000)
	if len(matches) != 1 || matches[0].RelPath != "present.txt" {
		t.Fatalf("expected only the readable file to match, got %v", matches)
	}
}

func TestRunDoesNotMatchBeyondByteCap(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("x", 100) + "\nneedle after the cap\n"
	path := writeFile(t, dir, "a.txt", content)
	candidates := []Candidate{{AbsPath: path, RelPath: "a.txt"}}

	matches := Run(context.Background(), "needle", candidates, 50)
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

	matches := Run(context.Background(), "needle", candidates, 1_000_000)
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

	matches := Run(ctx, "needle", candidates, 1_000_000)
	if len(matches) != 0 {
		t.Fatalf("expected an already-canceled context to short-circuit before scanning, got %v", matches)
	}
}
