package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nitti/dirtree/internal/match"
)

// TestFilterMatchesMiddleSegmentAndDoesNotDuplicate builds a fixture
// where the same basename ("leaf.txt") recurs at multiple nesting
// depths, and a middle directory name ("mid") also recurs, then
// verifies that filtering the flat index by a query matching "mid"
// returns exactly the expected set with no accidental duplication or
// under-counting (TESTING.md, "Filtering / matching").
func TestFilterMatchesMiddleSegmentAndDoesNotDuplicate(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, "mid", "a"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, "other", "mid", "b"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "mid", "a", "leaf.txt"), []byte("x"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "other", "mid", "b", "leaf.txt"), []byte("x"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "unrelated.txt"), []byte("x"), 0o644))

	idx := Start(root, nil)
	entries := waitDone(t, idx)

	var got []string
	for _, e := range entries {
		if match.Matches("mid", e.RelPath) {
			got = append(got, e.RelPath)
		}
	}

	want := map[string]bool{
		"mid":                  true,
		"mid/a":                true,
		"mid/a/leaf.txt":       true,
		"other/mid":            true,
		"other/mid/b":          true,
		"other/mid/b/leaf.txt": true,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d matches %v, want %d matches %v", len(got), got, len(want), want)
	}
	seen := map[string]bool{}
	for _, g := range got {
		if seen[g] {
			t.Fatalf("duplicate match for %q", g)
		}
		seen[g] = true
		if !want[g] {
			t.Fatalf("unexpected match %q", g)
		}
	}
}
