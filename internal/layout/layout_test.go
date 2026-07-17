package layout

import "testing"

func TestComputeBrowserPaneWidthMinimumForShortList(t *testing.T) {
	if got := ComputeBrowserPaneWidth([]int{1, 2}, 20, 60); got != 20 {
		t.Fatalf("got %d, want 20", got)
	}
	if got := ComputeBrowserPaneWidth(nil, 20, 60); got != 20 {
		t.Fatalf("got %d, want 20", got)
	}
}

func TestComputeBrowserPaneWidthGrowsAndClamps(t *testing.T) {
	if got := ComputeBrowserPaneWidth([]int{5, 30, 10}, 20, 60); got != 30 {
		t.Fatalf("got %d, want 30", got)
	}
	if got := ComputeBrowserPaneWidth([]int{5, 999}, 20, 60); got != 60 {
		t.Fatalf("got %d, want 60 (clamped)", got)
	}
}

func TestShouldSplitViewBoundary(t *testing.T) {
	browserWidth, minPreview := 20, 40
	threshold := browserWidth + minPreview + 1
	if ShouldSplitView(threshold-1, browserWidth, minPreview) {
		t.Fatal("expected false just under threshold")
	}
	if !ShouldSplitView(threshold, browserWidth, minPreview) {
		t.Fatal("expected true at threshold")
	}
}
