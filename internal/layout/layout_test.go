package layout

import "testing"

func TestComputeTreePaneWidthMinimumForShortList(t *testing.T) {
	if got := ComputeTreePaneWidth([]int{1, 2}, 20, 60); got != 20 {
		t.Fatalf("got %d, want 20", got)
	}
	if got := ComputeTreePaneWidth(nil, 20, 60); got != 20 {
		t.Fatalf("got %d, want 20", got)
	}
}

func TestComputeTreePaneWidthGrowsAndClamps(t *testing.T) {
	if got := ComputeTreePaneWidth([]int{5, 30, 10}, 20, 60); got != 30 {
		t.Fatalf("got %d, want 30", got)
	}
	if got := ComputeTreePaneWidth([]int{5, 999}, 20, 60); got != 60 {
		t.Fatalf("got %d, want 60 (clamped)", got)
	}
}

func TestShouldSplitViewBoundary(t *testing.T) {
	treeWidth, minPreview := 20, 40
	threshold := treeWidth + minPreview + 1
	if ShouldSplitView(threshold-1, treeWidth, minPreview) {
		t.Fatal("expected false just under threshold")
	}
	if !ShouldSplitView(threshold, treeWidth, minPreview) {
		t.Fatal("expected true at threshold")
	}
}
