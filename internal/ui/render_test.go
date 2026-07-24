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

// TestPreviewLegendOrder pins previewLegend's left-to-right order: open
// files, quick open, browse, search, quit — survivors of narrow-terminal
// priority dropping keep their original order (§5.2), so this order is
// itself part of the legend's observable behavior, not just a cosmetic
// detail of how the var literal happens to be written.
func TestPreviewLegendOrder(t *testing.T) {
	want := []string{
		"[tab] switch files",
		"[o] quick open",
		"[b] browse",
		"[s] search",
		"[q] quit",
	}
	if len(previewLegend) != len(want) {
		t.Fatalf("previewLegend has %d entries, want %d", len(previewLegend), len(want))
	}
	for i, w := range want {
		if got := previewLegend[i].Text; got != w {
			t.Errorf("previewLegend[%d] = %q, want %q", i, got, w)
		}
	}
}
