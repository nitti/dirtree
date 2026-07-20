package views

import (
	"testing"

	"github.com/nitti/dirtree/internal/ui/canvas"
)

// TestOpenFilesLegendTier1FitsMinTerminalWidth guards SPEC.md §6.4's
// minimum terminal size against §5.2's legend tiering: the open-files
// dropdown's legend, in both its single-page and multi-page forms,
// must have priority-1 (never-dropped) text that fits within
// canvas.MinTerminalWidth on its own, with no left-hand label —
// otherwise a terminal at exactly the enforced minimum would still see
// a clipped legend.
func TestOpenFilesLegendTier1FitsMinTerminalWidth(t *testing.T) {
	named := map[string][]canvas.LegendEntry{
		"openFilesLegend(false)": openFilesLegend(false),
		"openFilesLegend(true)":  openFilesLegend(true),
	}
	for name, entries := range named {
		tier1 := canvas.LegendString(canvas.KeepUpToPriority(entries, 1))
		if n := len([]rune(tier1)); n > canvas.MinTerminalWidth {
			t.Errorf("%s's priority-1 text is %d runes, exceeding MinTerminalWidth (%d): %q", name, n, canvas.MinTerminalWidth, tier1)
		}
	}
}
