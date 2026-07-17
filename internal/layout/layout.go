// Package layout implements the pure split-view/popup layout
// computations from SPEC.md §9, kept free of any terminal-rendering
// dependency so it's unit-testable.
package layout

// ComputeBrowserPaneWidth returns the browser pane width for split
// view: wide enough to fit the longest label, clamped to [min, max].
func ComputeBrowserPaneWidth(labelLengths []int, min, max int) int {
	longest := 0
	for _, l := range labelLengths {
		if l > longest {
			longest = l
		}
	}
	width := longest
	if width < min {
		width = min
	}
	if width > max {
		width = max
	}
	return width
}

// ShouldSplitView reports whether split view should be used: true only
// when totalWidth is at least browserPaneWidth + minPreviewWidth + the
// one-column separator.
func ShouldSplitView(totalWidth, browserPaneWidth, minPreviewWidth int) bool {
	return totalWidth >= browserPaneWidth+minPreviewWidth+1
}
