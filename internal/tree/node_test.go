package tree

import (
	"os"
	"path/filepath"
	"testing"
)

// buildFixture creates a small directory tree under a temp dir:
//
//	root/
//	  a.txt
//	  B_dir/
//	    nested.txt
//	  a_dir/
//	    deep/
//	      leaf.txt
//	  noperm/        (permission-denied on non-root platforms)
func buildFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	must(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("hi"), 0o644))
	must(t, os.Mkdir(filepath.Join(root, "B_dir"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "B_dir", "nested.txt"), []byte("x"), 0o644))
	must(t, os.MkdirAll(filepath.Join(root, "a_dir", "deep"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "a_dir", "deep", "leaf.txt"), []byte("y"), 0o644))
	return root
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// --- Node / tree construction and flattening ---

func TestNewRootIsExpandedAndLoaded(t *testing.T) {
	root := NewRoot(buildFixture(t), nil)
	if !root.Expanded {
		t.Fatal("expected root to be expanded")
	}
	if root.Children == nil {
		t.Fatal("expected root children to be loaded")
	}
}

func TestFlattenRootOnly(t *testing.T) {
	rootPath := t.TempDir()
	root := NewRoot(rootPath, nil)
	root.Collapse() // no children anyway; ensure a bare root flattens to itself
	root.Expanded = true
	flat := root.Flatten()
	if len(flat) != 1 || flat[0] != root {
		t.Fatalf("expected flatten of empty root to be [root], got %v", flat)
	}
}

func TestFlattenRespectsAncestorExpansion(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)

	var aDir *Node
	for _, c := range root.Children {
		if c.Name == "a_dir" {
			aDir = c
		}
	}
	if aDir == nil {
		t.Fatal("a_dir not found")
	}
	aDir.Expand(rootPath, nil)
	deep := aDir.Children[0]
	deep.Expand(rootPath, nil)

	flat := root.Flatten()
	if !containsNode(flat, deep.Children[0]) {
		t.Fatal("expected leaf.txt visible when all ancestors expanded")
	}

	aDir.Collapse()
	flat = root.Flatten()
	if containsNode(flat, deep) || containsNode(flat, deep.Children[0]) {
		t.Fatal("collapsing an intermediate ancestor should hide its entire subtree")
	}
}

func containsNode(list []*Node, n *Node) bool {
	for _, c := range list {
		if c == n {
			return true
		}
	}
	return false
}

func TestLoadChildrenTwiceIsNoop(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	first := root.Children
	root.LoadChildren(rootPath, nil)
	if &root.Children[0] != &first[0] {
		t.Fatal("expected identical children slice after redundant load")
	}
}

func TestPermissionErrorYieldsZeroChildrenAndErrString(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root, permission denial won't apply")
	}
	rootPath := t.TempDir()
	noperm := filepath.Join(rootPath, "noperm")
	must(t, os.Mkdir(noperm, 0o000))
	defer os.Chmod(noperm, 0o755)

	root := NewRoot(rootPath, nil)
	var n *Node
	for _, c := range root.Children {
		if c.Name == "noperm" {
			n = c
		}
	}
	if n == nil {
		t.Fatal("noperm dir not found")
	}
	n.LoadChildren(rootPath, nil)
	if n.Err == "" {
		t.Fatal("expected non-empty error string")
	}
	if len(n.Children) != 0 {
		t.Fatal("expected zero children on listing error")
	}
}

func TestDirectoryListingOrder(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	var names []string
	for _, c := range root.Children {
		names = append(names, c.Name)
	}
	want := []string{"a_dir", "B_dir", "a.txt"}
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("got %v, want %v", names, want)
		}
	}
}

// --- Expand / collapse / toggle ---

func TestExpandFileIsNoop(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	var f *Node
	for _, c := range root.Children {
		if c.Name == "a.txt" {
			f = c
		}
	}
	f.Expand(rootPath, nil)
	if f.Expanded {
		t.Fatal("expanding a file should be a no-op")
	}
}

func TestExpandCollapsedDirLoadsAndMarksExpanded(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	var d *Node
	for _, c := range root.Children {
		if c.Name == "a_dir" {
			d = c
		}
	}
	if d.Expanded {
		t.Fatal("expected fresh non-root dir to start collapsed")
	}
	d.Expand(rootPath, nil)
	if !d.Expanded || d.Children == nil {
		t.Fatal("expected expand to load children and mark expanded")
	}
}

func TestCollapseIsIdempotent(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	root.Collapse()
	root.Collapse()
	if root.Expanded {
		t.Fatal("expected collapse to leave node collapsed")
	}
}

func TestToggleExpand(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	var d *Node
	for _, c := range root.Children {
		if c.Name == "a_dir" {
			d = c
		}
	}
	d.ToggleExpand(rootPath, nil)
	if !d.Expanded {
		t.Fatal("expected toggle to expand a collapsed dir")
	}
	d.ToggleExpand(rootPath, nil)
	if d.Expanded {
		t.Fatal("expected toggle to collapse an expanded dir")
	}
}

// --- Right-arrow / left-arrow semantics ---

func findChild(n *Node, name string) *Node {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestMoveRightOnCollapsedDirExpandsWithoutMovingSelection(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	d := findChild(root, "a_dir")
	result := d.MoveRight(rootPath, nil)
	if result != d || !d.Expanded {
		t.Fatal("expected right on collapsed dir to expand it and keep selection")
	}
}

func TestMoveRightOnExpandedDirWithChildrenDescends(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	d := findChild(root, "a_dir")
	d.Expand(rootPath, nil)
	result := d.MoveRight(rootPath, nil)
	if result != d.Children[0] {
		t.Fatal("expected right on expanded dir to select first child")
	}
}

func TestMoveRightOnExpandedDirWithZeroChildrenNoop(t *testing.T) {
	rootPath := t.TempDir()
	empty := filepath.Join(rootPath, "empty")
	must(t, os.Mkdir(empty, 0o755))
	root := NewRoot(rootPath, nil)
	d := findChild(root, "empty")
	d.Expand(rootPath, nil)
	result := d.MoveRight(rootPath, nil)
	if result != d {
		t.Fatal("expected right on expanded empty dir to no-op")
	}
}

func TestMoveRightOnFileNoop(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	f := findChild(root, "a.txt")
	result := f.MoveRight(rootPath, nil)
	if result != f {
		t.Fatal("expected right on file to no-op")
	}
}

func TestMoveLeftOnExpandedDirCollapses(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	d := findChild(root, "a_dir")
	d.Expand(rootPath, nil)
	result := d.MoveLeft()
	if result != d || d.Expanded {
		t.Fatal("expected left on expanded dir to collapse it and keep selection")
	}
}

func TestMoveLeftOnCollapsedDirWithParentGoesToParent(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	d := findChild(root, "a_dir")
	result := d.MoveLeft()
	if result != root {
		t.Fatal("expected left on collapsed dir to select parent")
	}
}

func TestMoveLeftOnRootNoop(t *testing.T) {
	rootPath := t.TempDir()
	root := NewRoot(rootPath, nil)
	root.Collapse()
	result := root.MoveLeft()
	if result != root {
		t.Fatal("expected left on root to no-op")
	}
}

func TestMoveLeftOnFileGoesToParentAndCollapsesIt(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	d := findChild(root, "B_dir")
	d.Expand(rootPath, nil)
	f := findChild(d, "nested.txt")
	result := f.MoveLeft()
	if result != d {
		t.Fatal("expected left on file to select its parent")
	}
	if d.Expanded {
		t.Fatal("expected parent to end up collapsed")
	}
}

func TestMoveLeftOnFileWhoseParentIsRoot(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	f := findChild(root, "a.txt")
	result := f.MoveLeft()
	if result != root {
		t.Fatal("expected left on root-level file to select root")
	}
	if root.Expanded {
		t.Fatal("expected root to end up collapsed")
	}
}

// --- Selection movement ---

func TestMoveSelectionWrapsForward(t *testing.T) {
	if got := MoveSelection(4, 1, 5); got != 0 {
		t.Fatalf("got %d, want 0", got)
	}
}

func TestMoveSelectionWrapsBackward(t *testing.T) {
	if got := MoveSelection(0, -1, 5); got != 4 {
		t.Fatalf("got %d, want 4", got)
	}
}

func TestMoveSelectionSingleItem(t *testing.T) {
	if got := MoveSelection(0, 1, 1); got != 0 {
		t.Fatalf("got %d, want 0", got)
	}
	if got := MoveSelection(0, -1, 1); got != 0 {
		t.Fatalf("got %d, want 0", got)
	}
}

func TestMoveSelectionEmptyList(t *testing.T) {
	if got := MoveSelection(0, 1, 0); got != 0 {
		t.Fatalf("got %d, want 0", got)
	}
	if got := MoveSelection(0, -1, 0); got != 0 {
		t.Fatalf("got %d, want 0", got)
	}
}

// --- .git exclusion (gitignore-specific cases live in internal/ignore) ---

func TestGitDirNeverListed(t *testing.T) {
	rootPath := t.TempDir()
	must(t, os.Mkdir(filepath.Join(rootPath, ".git"), 0o755))
	must(t, os.WriteFile(filepath.Join(rootPath, ".git", "config"), []byte(""), 0o644))
	root := NewRoot(rootPath, nil)
	if findChild(root, ".git") != nil {
		t.Fatal("expected .git to never be listed")
	}
}

// --- Path display and reveal ---

func TestRelativeDisplayPathIsPosix(t *testing.T) {
	rootPath := filepath.Join(t.TempDir())
	target := filepath.Join(rootPath, "a", "b", "c.txt")
	rel := RelativeDisplayPath(rootPath, target)
	if rel != "a/b/c.txt" {
		t.Fatalf("got %q, want %q", rel, "a/b/c.txt")
	}
}

func TestRevealPathDeeplyNested(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	target := filepath.Join(rootPath, "a_dir", "deep", "leaf.txt")
	n := RevealPath(root, rootPath, target, nil)
	if n == nil || n.Name != "leaf.txt" {
		t.Fatal("expected reveal to find leaf.txt")
	}
	d := findChild(root, "a_dir")
	if !d.Expanded {
		t.Fatal("expected intermediate ancestor a_dir to be expanded")
	}
	if !findChild(d, "deep").Expanded {
		t.Fatal("expected intermediate ancestor deep to be expanded")
	}
}

func TestRevealPathRootLevel(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	target := filepath.Join(rootPath, "a.txt")
	n := RevealPath(root, rootPath, target, nil)
	if n == nil || n.Name != "a.txt" {
		t.Fatal("expected reveal to find root-level a.txt")
	}
}

func TestRevealPathNotFound(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	target := filepath.Join(rootPath, "does", "not", "exist")
	n := RevealPath(root, rootPath, target, nil)
	if n != nil {
		t.Fatal("expected reveal of missing path to return nil")
	}
}

func TestRevealPathOutsideRoot(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	outside := t.TempDir()
	n := RevealPath(root, rootPath, filepath.Join(outside, "x.txt"), nil)
	if n != nil {
		t.Fatal("expected reveal of path outside root to return nil")
	}
}
