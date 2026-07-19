package views

import (
	"testing"

	"github.com/nitti/dirtree/internal/ui/canvas"
)

// TestBrowserLegendsTier1FitMinTerminalWidth guards SPEC.md §6.4's
// minimum terminal size against §5.2's legend tiering: the browser's
// own legend and jump-to-file mode's legend must each have priority-1
// (never-dropped) text that fits within canvas.MinTerminalWidth on its
// own, with no left-hand label — otherwise a terminal at exactly the
// enforced minimum would still see a clipped legend.
func TestBrowserLegendsTier1FitMinTerminalWidth(t *testing.T) {
	named := map[string][]canvas.LegendEntry{
		"browserLegend": browserLegend,
		"jumpLegend":    jumpLegend,
	}
	for name, entries := range named {
		tier1 := canvas.LegendString(canvas.KeepUpToPriority(entries, 1))
		if n := len([]rune(tier1)); n > canvas.MinTerminalWidth {
			t.Errorf("%s's priority-1 text is %d runes, exceeding MinTerminalWidth (%d): %q", name, n, canvas.MinTerminalWidth, tier1)
		}
	}
}
