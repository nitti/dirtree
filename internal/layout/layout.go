// Package layout implements the pure split-view/popup layout
// computations from SPEC.md §9, kept free of any terminal-rendering
// dependency so it's unit-testable.
package layout

// ComputeTreePaneWidth returns the tree pane width for split view: wide
// enough to fit the longest label, clamped to [min, max].
func ComputeTreePaneWidth(labelLengths []int, min, max int) int {
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
// when totalWidth is at least treePaneWidth + minPreviewWidth + the
// one-column separator.
func ShouldSplitView(totalWidth, treePaneWidth, minPreviewWidth int) bool {
	return totalWidth >= treePaneWidth+minPreviewWidth+1
}
