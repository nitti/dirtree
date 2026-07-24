package canvas

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// TestLegendFit covers LegendFit's narrow-terminal drop order (SPEC.md
// §5.2): left and the legend's entries share one drop order, weakest
// first — priority-3 entries, then left itself (priority "2.5": dropped
// only once every priority-3 entry already is, but before any
// priority-2 entry), then priority-2 entries. Priority-1 entries are
// never dropped. Survivors always keep their original left-to-right
// order and are never truncated mid-entry. left is always left-aligned;
// the legend is always right-aligned — flush with the row's right edge
// whether or not left is present — so the legend's own reading position
// never jumps between alignments depending on whether left is present.
func TestLegendFit(t *testing.T) {
	entries := []LegendEntry{
		{Text: "[return] open", Priority: 1},
		{Text: "[tab] next", Priority: 2},
		{Text: "[ctrl+u] clear", Priority: 3},
		{Text: "[esc] close", Priority: 1},
	}
	full := LegendString(entries)                       // "[return] open  [tab] next  [ctrl+u] clear  [esc] close"
	tier2 := LegendString(KeepUpToPriority(entries, 2)) // drop tier 3: "[return] open  [tab] next  [esc] close"
	tier1 := LegendString(KeepUpToPriority(entries, 1)) // drop tier 2 and 3: "[return] open  [esc] close"
	left := "~/dirtree"

	tests := []struct {
		name       string
		w          int
		wantText   string
		wantLeftIn bool
	}{
		{"fits with label and full legend, exact width", len(left) + 1 + len(full), left + " " + full, true},
		{"fits with label and full legend, slack width pushes legend to the right edge", len(left) + 1 + len(full) + 10, left + strings.Repeat(" ", 11) + full, true},
		{"tier 3 dropped, label kept (priority 2.5 beats priority 3)", len(left) + 1 + len(tier2), left + " " + tier2, true},
		{"label dropped once tier 3 is already gone and still doesn't fit", len(tier2), tier2, false},
		{"legend stays right-aligned once left is dropped, slack width included", len(tier2) + len(left), strings.Repeat(" ", len(left)) + tier2, false},
		{"tier 2 also dropped, label stays out", len(tier1), tier1, false},
		{"tier 1 only, still narrower than tier1 text", len(tier1) - 1, tier1, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			text, leftIncluded := LegendFit(tc.w, left, entries)
			if text != tc.wantText {
				t.Errorf("LegendFit(%d) text = %q, want %q", tc.w, text, tc.wantText)
			}
			if leftIncluded != tc.wantLeftIn {
				t.Errorf("LegendFit(%d) leftIncluded = %v, want %v", tc.w, leftIncluded, tc.wantLeftIn)
			}
		})
	}
}

// TestLegendFitNeverReordersOrTruncatesMidEntry checks that whichever
// entries survive a drop keep their original relative order, and that no
// returned text ever contains a partial "[..." fragment cut off mid-entry.
func TestLegendFitNeverReordersOrTruncatesMidEntry(t *testing.T) {
	entries := []LegendEntry{
		{Text: "[a] alpha", Priority: 1},
		{Text: "[b] bravo", Priority: 3},
		{Text: "[c] charlie", Priority: 2},
		{Text: "[d] delta", Priority: 1},
	}
	for w := 0; w <= 60; w++ {
		text, _ := LegendFit(w, "left", entries)
		idxA := strings.Index(text, "[a] alpha")
		idxD := strings.Index(text, "[d] delta")
		if idxA != -1 && idxD != -1 && idxA > idxD {
			t.Fatalf("w=%d: priority-1 entries out of order in %q", w, text)
		}
		if strings.Count(text, "[") != strings.Count(text, "]") {
			t.Fatalf("w=%d: legend text %q has an unbalanced bracket, suggesting mid-entry truncation", w, text)
		}
	}
}

// newTestCanvas builds a Canvas backed by a fixed-size simulation
// screen, for tests that need to inspect actually-drawn cell styles
// rather than just the composed text LegendFit/LegendText return.
func newTestCanvas(t *testing.T, w, h int) (*Canvas, tcell.SimulationScreen) {
	t.Helper()
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(w, h)
	return New(sim), sim
}

// cellStyle returns the style of the cell at (x, y) after Show.
func cellStyle(sim tcell.SimulationScreen, x, y int) tcell.Style {
	cells, width, _ := sim.GetContents()
	return cells[y*width+x].Style
}

// TestDrawHeaderDimmedStylesOnlyTheMatchedEntry guards
// DrawHeaderDimmed's "compose once, overlay a styled sub-span"
// behavior: left's own columns get StyleHeaderMode (bolded, the same
// treatment DrawHeaderMode gives its own label), dimText's own columns
// get StyleHeaderDim, and every other column on the row keeps the
// plain StyleHeader, regardless of where in the fitted
// (right-aligned) legend dimText's substring happens to land.
func TestDrawHeaderDimmedStylesOnlyTheMatchedEntry(t *testing.T) {
	w, h := 40, 5
	c, sim := newTestCanvas(t, w, h)
	left := "root"
	entries := []LegendEntry{
		{Text: "[tab] switch files", Priority: 1},
		{Text: "[q] quit", Priority: 1},
	}
	c.DrawHeaderDimmed(w, left, entries, "[tab] switch files")
	sim.Show()

	text := LegendText(w, left, entries)
	runes := []rune(text)
	target := []rune("[tab] switch files")
	start := strings.Index(string(runes), "[tab] switch files")
	if start < 0 {
		t.Fatalf("test setup: %q not found in fitted legend %q", "[tab] switch files", text)
	}
	// start above is a byte offset from strings.Index on an ASCII-only
	// string here, so it doubles as the rune offset DrawText itself uses.
	for x := range w {
		style := cellStyle(sim, x, 0)
		inLeft := x < len(left)
		inTarget := x >= start && x < start+len(target)
		switch {
		case inLeft && style != StyleHeaderMode:
			t.Errorf("column %d (inside left) has style %v, want StyleHeaderMode", x, style)
		case inTarget && style != StyleHeaderDim:
			t.Errorf("column %d (inside dimText) has style %v, want StyleHeaderDim", x, style)
		case !inLeft && !inTarget && style != StyleHeader:
			t.Errorf("column %d (outside left/dimText) has style %v, want StyleHeader", x, style)
		}
	}
}

// TestDrawHeaderDimmedNoopsWhenDimTextIsDropped guards the case where
// dimText didn't survive LegendFit's own priority dropping at the
// given width (e.g. it's priority 2+ and the terminal is narrow): with
// no matching substring to overlay, the row is drawn exactly like
// DrawHeader would, entirely in StyleHeader, rather than panicking or
// styling the wrong span.
func TestDrawHeaderDimmedNoopsWhenDimTextIsDropped(t *testing.T) {
	w, h := 12, 5
	c, sim := newTestCanvas(t, w, h)
	entries := []LegendEntry{
		{Text: "[tab] switch files", Priority: 2},
		{Text: "[q] quit", Priority: 1},
	}
	c.DrawHeaderDimmed(w, "root", entries, "[tab] switch files")
	sim.Show()

	for x := range w {
		if style := cellStyle(sim, x, 0); style != StyleHeader {
			t.Errorf("column %d has style %v, want StyleHeader (dimText should have been dropped)", x, style)
		}
	}
}

// TestDrawHeaderAllDimmedStylesEverythingAfterLeft guards
// DrawHeaderAllDimmed's "left is bolded, everything after it dims"
// behavior when left survives LegendFit's own priority dropping and is
// included in the fitted text.
func TestDrawHeaderAllDimmedStylesEverythingAfterLeft(t *testing.T) {
	w, h := 40, 5
	c, sim := newTestCanvas(t, w, h)
	entries := []LegendEntry{
		{Text: "[tab] switch files", Priority: 1},
		{Text: "[q] quit", Priority: 1},
	}
	c.DrawHeaderAllDimmed(w, "root", entries)
	sim.Show()

	left := []rune("root")
	for x := range w {
		style := cellStyle(sim, x, 0)
		switch {
		case x < len(left) && style != StyleHeaderMode:
			t.Errorf("column %d (inside left) has style %v, want StyleHeaderMode", x, style)
		case x >= len(left) && style != StyleHeaderDim:
			t.Errorf("column %d (after left) has style %v, want StyleHeaderDim", x, style)
		}
	}
}

// TestDrawHeaderAllDimmedStylesWholeRowWhenLeftDropped guards the case
// where left itself didn't survive LegendFit's width-driven dropping
// (a narrow terminal): with no left-hand span to leave undimmed, the
// entire row — including the right-aligned padding preceding the
// legend — is dimmed.
func TestDrawHeaderAllDimmedStylesWholeRowWhenLeftDropped(t *testing.T) {
	w, h := 12, 5
	c, sim := newTestCanvas(t, w, h)
	entries := []LegendEntry{
		{Text: "[q] quit", Priority: 1},
	}
	c.DrawHeaderAllDimmed(w, "a very long root path that cannot fit", entries)
	sim.Show()

	for x := range w {
		if style := cellStyle(sim, x, 0); style != StyleHeaderDim {
			t.Errorf("column %d has style %v, want StyleHeaderDim (left should have been dropped)", x, style)
		}
	}
}

func TestKeepUpToPriority(t *testing.T) {
	entries := []LegendEntry{{Text: "a", Priority: 1}, {Text: "b", Priority: 2}, {Text: "c", Priority: 3}, {Text: "d", Priority: 1}}
	got := KeepUpToPriority(entries, 2)
	want := []LegendEntry{{Text: "a", Priority: 1}, {Text: "b", Priority: 2}, {Text: "d", Priority: 1}}
	if len(got) != len(want) {
		t.Fatalf("KeepUpToPriority = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("KeepUpToPriority = %v, want %v", got, want)
		}
	}
}
