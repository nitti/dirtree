package ui

import (
	"testing"

	"github.com/nitti/dirtree/internal/ui/canvas"
)

// TestLegendTier1FitsMinTerminalWidth guards SPEC.md §6.4's minimum
// terminal size against §5.2's legend tiering: previewLegend's
// priority-1 (never-dropped) text must fit within canvas.MinTerminalWidth
// on its own, with no left-hand label — otherwise a terminal at exactly
// the enforced minimum would still see a clipped legend, the same class
// of bug a too-long jumpLegend/searchLegend priority-1 entry caused
// before those entries were demoted to priority 2. Every view extracted
// into internal/ui/views has an equivalent test alongside its own
// package (views.TestQuickOpenLegendTier1FitsMinTerminalWidth,
// views.TestSearchLegendTier1FitsMinTerminalWidth,
// views.TestBrowserLegendsTier1FitMinTerminalWidth,
// views.TestPreviewLegendsTier1FitMinTerminalWidth,
// views.TestOpenFilesLegendTier1FitsMinTerminalWidth); previewLegend is
// the one legend still owned by this package, since it belongs to the
// coordinator's own top-of-screen header rather than to any one view.
func TestLegendTier1FitsMinTerminalWidth(t *testing.T) {
	tier1 := canvas.LegendString(canvas.KeepUpToPriority(previewLegend, 1))
	if n := len([]rune(tier1)); n > canvas.MinTerminalWidth {
		t.Errorf("previewLegend's priority-1 text is %d runes, exceeding MinTerminalWidth (%d): %q", n, canvas.MinTerminalWidth, tier1)
	}
}
