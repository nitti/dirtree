package tree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nitti/dirtree/internal/ignore"
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

// --- Jump to file matching (§4.3) ---

func TestJumpMatchesScopedToVisibleRows(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)

	if len(JumpMatches(root, "leaf")) != 0 {
		t.Fatal("expected leaf.txt, inside a currently-collapsed directory, to not be a candidate")
	}

	aDir := findChild(root, "a_dir")
	aDir.Expand(rootPath, nil)
	deep := findChild(aDir, "deep")
	deep.Expand(rootPath, nil)

	matches := JumpMatches(root, "leaf")
	if len(matches) != 1 || matches[0].Name != "leaf.txt" {
		t.Fatalf("expected leaf.txt to become a candidate once its ancestors are expanded, got %v", matches)
	}
}

func TestJumpMatchesPrefixOnLeafNameOnly(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)

	matches := JumpMatches(root, "a")
	if len(matches) != 2 {
		t.Fatalf("expected a_dir and a.txt to match prefix \"a\", got %v", matches)
	}
	for _, m := range matches {
		if m.Name != "a_dir" && m.Name != "a.txt" {
			t.Fatalf("unexpected match %q", m.Name)
		}
	}
}

func TestJumpMatchesCaseInsensitive(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)

	matches := JumpMatches(root, "B")
	if len(matches) != 1 || matches[0].Name != "B_dir" {
		t.Fatalf("expected case-insensitive prefix match on B_dir, got %v", matches)
	}
}

func TestJumpMatchesIncludesDirsAndFiles(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)

	if len(JumpMatches(root, "a_dir")) != 1 {
		t.Fatal("expected a directory row to be a valid match target")
	}
	if len(JumpMatches(root, "a.txt")) != 1 {
		t.Fatal("expected a file row to be a valid match target")
	}
}

func TestJumpMatchesEmptyQueryReturnsNone(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)

	if matches := JumpMatches(root, ""); len(matches) != 0 {
		t.Fatalf("expected empty query to match nothing, got %v", matches)
	}
}

func TestJumpMatchesDisplayOrder(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)

	matches := JumpMatches(root, "a")
	flat := root.Flatten()
	var lastIdx = -1
	for _, m := range matches {
		idx := indexOfNode(flat, m)
		if idx <= lastIdx {
			t.Fatalf("expected matches in display order, got %v", matches)
		}
		lastIdx = idx
	}
}

func indexOfNode(list []*Node, n *Node) int {
	for i, c := range list {
		if c == n {
			return i
		}
	}
	return -1
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
	defer func() { _ = os.Chmod(noperm, 0o755) }()

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
	if strings.Contains(n.Err, noperm) {
		t.Fatalf("expected error message not to repeat the already-visible path, got %q", n.Err)
	}
}

func TestLoadChildrenRetriesAfterPreviousFailure(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root, permission denial won't apply")
	}
	rootPath := t.TempDir()
	dir := filepath.Join(rootPath, "fixable")
	must(t, os.Mkdir(dir, 0o000))
	must(t, os.WriteFile(filepath.Join(rootPath, "keep.txt"), []byte("z"), 0o644))

	root := NewRoot(rootPath, nil)
	n := findChild(root, "fixable")
	n.LoadChildren(rootPath, nil)
	if n.Err == "" {
		t.Fatal("expected an error from the first, still-unreadable load attempt")
	}

	must(t, os.Chmod(dir, 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "inner.txt"), []byte("z"), 0o644))

	// A second LoadChildren call (mirroring collapse/re-expand in the
	// UI, SPEC.md §5) must actually retry rather than silently no-op
	// just because the node was already marked loaded by the failed
	// attempt.
	n.LoadChildren(rootPath, nil)
	if n.Err != "" {
		t.Fatalf("expected error cleared once the directory became readable, got %q", n.Err)
	}
	if findChild(n, "inner.txt") == nil {
		t.Fatal("expected the retry to actually list the now-readable directory's contents")
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

func TestMoveSelectionClampedClampsAtEnd(t *testing.T) {
	if got := MoveSelectionClamped(4, 1, 5); got != 4 {
		t.Fatalf("got %d, want 4", got)
	}
}

func TestMoveSelectionClampedClampsAtStart(t *testing.T) {
	if got := MoveSelectionClamped(0, -1, 5); got != 0 {
		t.Fatalf("got %d, want 0", got)
	}
}

func TestMoveSelectionClampedJumpsWithinBounds(t *testing.T) {
	if got := MoveSelectionClamped(2, 3, 10); got != 5 {
		t.Fatalf("got %d, want 5", got)
	}
	if got := MoveSelectionClamped(8, -3, 10); got != 5 {
		t.Fatalf("got %d, want 5", got)
	}
}

func TestMoveSelectionClampedEmptyList(t *testing.T) {
	if got := MoveSelectionClamped(0, 1, 0); got != 0 {
		t.Fatalf("got %d, want 0", got)
	}
	if got := MoveSelectionClamped(0, -1, 0); got != 0 {
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

func TestDirtreeIgnoreExcludesAcrossWholeApp(t *testing.T) {
	rootPath := t.TempDir()
	must(t, os.WriteFile(filepath.Join(rootPath, "secret.env"), []byte("x"), 0o644))
	must(t, os.WriteFile(filepath.Join(rootPath, "normal.txt"), []byte("x"), 0o644))
	must(t, os.WriteFile(filepath.Join(rootPath, ".dirtreeignore"), []byte("*.env\n"), 0o644))

	matcher := ignore.LoadAll(rootPath)
	root := NewRoot(rootPath, matcher)
	if findChild(root, "secret.env") != nil {
		t.Fatal("expected .dirtreeignore pattern to exclude secret.env from the tree listing")
	}
	if findChild(root, "normal.txt") == nil {
		t.Fatal("expected non-matching file to still be listed")
	}
}

// --- Path display and reveal ---

func TestRelativeDisplayPathIsPosix(t *testing.T) {
	rootPath := t.TempDir()
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

// --- Live refresh (§6a) ---

func TestRefreshTreeAddsNewEntry(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	if findChild(root, "new.txt") != nil {
		t.Fatal("fixture should not yet contain new.txt")
	}
	must(t, os.WriteFile(filepath.Join(rootPath, "new.txt"), []byte("z"), 0o644))

	RefreshTree(root, rootPath, nil)

	if findChild(root, "new.txt") == nil {
		t.Fatal("expected refresh to pick up newly-created new.txt")
	}
}

func TestRefreshTreeRemovesDeletedEntry(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	must(t, os.Remove(filepath.Join(rootPath, "a.txt")))

	RefreshTree(root, rootPath, nil)

	if findChild(root, "a.txt") != nil {
		t.Fatal("expected refresh to drop deleted a.txt")
	}
}

func TestRefreshTreePreservesIdentityAndExpandState(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	bDir := findChild(root, "B_dir")
	bDir.Expand(rootPath, nil)
	nested := findChild(bDir, "nested.txt")

	// unrelated change elsewhere in the tree
	must(t, os.WriteFile(filepath.Join(rootPath, "new.txt"), []byte("z"), 0o644))
	RefreshTree(root, rootPath, nil)

	refreshedB := findChild(root, "B_dir")
	if refreshedB != bDir {
		t.Fatal("expected B_dir node identity to be preserved across refresh")
	}
	if !refreshedB.Expanded {
		t.Fatal("expected B_dir to remain expanded across refresh")
	}
	if findChild(refreshedB, "nested.txt") != nested {
		t.Fatal("expected nested.txt node identity to be preserved across refresh")
	}
}

func TestRefreshTreeOnlyTouchesLoadedDirectories(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	aDir := findChild(root, "a_dir") // never expanded/loaded

	must(t, os.RemoveAll(filepath.Join(rootPath, "a_dir", "deep")))
	RefreshTree(root, rootPath, nil)

	if aDir.Loaded() {
		t.Fatal("expected never-expanded a_dir to remain unloaded after refresh")
	}
}

func TestNearestSurvivingReturnsSelfWhenPresent(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	target := findChild(root, "a.txt")
	if got := NearestSurviving(target); got != target {
		t.Fatal("expected NearestSurviving to return the node itself when still present")
	}
}

func TestNearestSurvivingFallsBackToParentWhenDeleted(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	bDir := findChild(root, "B_dir")
	bDir.Expand(rootPath, nil)
	nested := findChild(bDir, "nested.txt")

	must(t, os.Remove(filepath.Join(rootPath, "B_dir", "nested.txt")))
	RefreshTree(root, rootPath, nil)

	got := NearestSurviving(nested)
	if got != bDir {
		t.Fatalf("expected fallback to surviving parent B_dir, got %v", got)
	}
}

func TestNearestSurvivingFallsBackToRootWhenWholeSubtreeDeleted(t *testing.T) {
	rootPath := buildFixture(t)
	root := NewRoot(rootPath, nil)
	aDir := findChild(root, "a_dir")
	aDir.Expand(rootPath, nil)
	deep := findChild(aDir, "deep")
	deep.Expand(rootPath, nil)
	leaf := findChild(deep, "leaf.txt")

	must(t, os.RemoveAll(filepath.Join(rootPath, "a_dir")))
	RefreshTree(root, rootPath, nil)

	got := NearestSurviving(leaf)
	if got != root {
		t.Fatalf("expected fallback all the way to root, got %v", got)
	}
}

func TestRefreshTreeReportsNewlyErroringDirectory(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root, permission denial won't apply")
	}
	rootPath := t.TempDir()
	dir := filepath.Join(rootPath, "goesbad")
	must(t, os.Mkdir(dir, 0o755))
	root := NewRoot(rootPath, nil)
	n := findChild(root, "goesbad")
	n.LoadChildren(rootPath, nil)
	if n.Err != "" {
		t.Fatalf("expected no error while dir is still readable, got %q", n.Err)
	}

	must(t, os.Chmod(dir, 0o000))
	defer func() { _ = os.Chmod(dir, 0o755) }()

	newlyErrored := RefreshTree(root, rootPath, nil)

	if n.Err == "" {
		t.Fatal("expected refresh to record the new listing error")
	}
	if len(newlyErrored) != 1 || newlyErrored[0] != dir {
		t.Fatalf("expected newlyErrored=[%q], got %v", dir, newlyErrored)
	}
}

func TestRefreshTreeDoesNotReReportAlreadyErroringDirectory(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root, permission denial won't apply")
	}
	rootPath := t.TempDir()
	dir := filepath.Join(rootPath, "noperm")
	must(t, os.Mkdir(dir, 0o000))
	defer func() { _ = os.Chmod(dir, 0o755) }()
	root := NewRoot(rootPath, nil)
	n := findChild(root, "noperm")
	n.LoadChildren(rootPath, nil)
	if n.Err == "" {
		t.Fatal("expected initial load to record an error")
	}

	newlyErrored := RefreshTree(root, rootPath, nil)

	if len(newlyErrored) != 0 {
		t.Fatalf("expected an already-erroring directory not to be reported again, got %v", newlyErrored)
	}
}

func TestRefreshTreeIgnoresIgnoredEntries(t *testing.T) {
	rootPath := buildFixture(t)
	must(t, os.WriteFile(filepath.Join(rootPath, ".gitignore"), []byte("*.log\n"), 0o644))
	ig := ignore.LoadAll(rootPath)
	root := NewRoot(rootPath, ig)

	must(t, os.WriteFile(filepath.Join(rootPath, "new.log"), []byte("z"), 0o644))
	RefreshTree(root, rootPath, ig)

	if findChild(root, "new.log") != nil {
		t.Fatal("expected refresh to respect .gitignore for newly-created entries")
	}
}
