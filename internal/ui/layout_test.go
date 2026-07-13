package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nitti/dirtree/internal/tree"
)

func testApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := tree.NewRoot(dir, nil)
	return &App{rootPath: dir, root: root, selected: root}
}

func TestComputeSplitLayoutNarrowFallsBackToPopup(t *testing.T) {
	a := testApp(t)
	_, _, split := a.computeSplitLayout(30)
	if split {
		t.Fatal("expected popup fallback for a narrow terminal")
	}
}

func TestComputeSplitLayoutCapsPreviewPaneAndGrowsTree(t *testing.T) {
	a := testApp(t)
	treeWidth, previewPaneWidth, split := a.computeSplitLayout(300)
	if !split {
		t.Fatal("expected split view for a wide terminal")
	}
	if previewPaneWidth != previewMaxWidth {
		t.Fatalf("expected preview pane capped at %d, got %d", previewMaxWidth, previewPaneWidth)
	}
	if treeWidth != maxTreePaneWidth {
		t.Fatalf("expected leftover width absorbed by tree pane up to its max %d, got %d", maxTreePaneWidth, treeWidth)
	}
	if treeWidth+1+previewPaneWidth > 300 {
		t.Fatalf("panes overflow terminal width: tree=%d preview=%d total-avail=300", treeWidth, previewPaneWidth)
	}
}

func TestComputeSplitLayoutModeratelyWideDoesNotCapPreview(t *testing.T) {
	a := testApp(t)
	treeWidth, previewPaneWidth, split := a.computeSplitLayout(100)
	if !split {
		t.Fatal("expected split view")
	}
	if treeWidth+1+previewPaneWidth != 100 {
		t.Fatalf("expected panes to exactly fill terminal width when under the preview cap, got tree=%d preview=%d", treeWidth, previewPaneWidth)
	}
	if previewPaneWidth >= previewMaxWidth {
		t.Fatalf("expected preview pane below cap in a moderately wide terminal, got %d", previewPaneWidth)
	}
}
