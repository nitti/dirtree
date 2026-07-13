package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nitti/dirtree/internal/ignore"
	"github.com/nitti/dirtree/internal/tree"
)

func waitDone(t *testing.T, idx *Index) []Entry {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if entries, done := idx.Snapshot(); done {
			return entries
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("index did not finish building in time")
	return nil
}

func TestIndexReachesIntoCollapsedDirs(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "sub", "f.txt"), []byte("x"), 0o644))

	idx := Start(root, nil)
	entries := waitDone(t, idx)

	found := false
	for _, e := range entries {
		if e.RelPath == "sub/f.txt" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected index to include a path nested in a collapsed directory")
	}
}

func TestIndexDoesNotMutateInteractiveTree(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "sub", "f.txt"), []byte("x"), 0o644))

	interactive := tree.NewRoot(root, nil)
	before := len(interactive.Children)
	beforeExpanded := interactive.Children[0].Expanded

	idx := Start(root, nil)
	waitDone(t, idx)

	if len(interactive.Children) != before {
		t.Fatal("expected interactive tree child count unchanged")
	}
	if interactive.Children[0].Expanded != beforeExpanded {
		t.Fatal("expected interactive tree expand state unchanged")
	}
}

func TestIndexSortedCaseInsensitive(t *testing.T) {
	root := t.TempDir()
	must(t, os.WriteFile(filepath.Join(root, "Banana.txt"), []byte("x"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "apple.txt"), []byte("x"), 0o644))

	idx := Start(root, nil)
	entries := waitDone(t, idx)
	if len(entries) != 2 || entries[0].RelPath != "apple.txt" || entries[1].RelPath != "Banana.txt" {
		t.Fatalf("expected case-insensitive sort, got %+v", entries)
	}
}

func TestIndexRespectsGitignore(t *testing.T) {
	root := t.TempDir()
	must(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.log\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "a.log"), []byte("x"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644))

	m := ignore.Load(root)
	idx := Start(root, m)
	entries := waitDone(t, idx)
	for _, e := range entries {
		if e.RelPath == "a.log" {
			t.Fatal("expected .gitignore'd file excluded from index")
		}
	}
}

func TestIndexSymlinkCycleTerminates(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	// sub/loop -> root (a cycle back to an ancestor)
	if err := os.Symlink(root, filepath.Join(root, "sub", "loop")); err != nil {
		t.Skip("symlinks not supported in this environment")
	}

	idx := Start(root, nil)
	entries := waitDone(t, idx) // must terminate; test itself times out otherwise
	if len(entries) == 0 {
		t.Fatal("expected at least the 'sub' and 'sub/loop' entries")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
