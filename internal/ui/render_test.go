package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/ui/canvas"
	"github.com/nitti/dirtree/internal/ui/views"
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

// cellStyle returns the style of the cell at (x, y) after Show.
func cellStyle(sim tcell.SimulationScreen, x, y int) tcell.Style {
	cells, width, _ := sim.GetContents()
	return cells[y*width+x].Style
}

// TestDrawPreviewHeaderDimsSwitchFilesWhenEmpty guards the "switch
// files" legend entry's dim-when-empty behavior: with no open files,
// its own columns render in canvas.StyleHeaderDim while the rest of
// the row stays canvas.StyleHeader; with at least one file open, the
// entire row (including that entry) renders in the plain
// canvas.StyleHeader, matching the row's behavior before this feature.
func TestDrawPreviewHeaderDimsSwitchFilesWhenEmpty(t *testing.T) {
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	w, h := 60, 5
	sim.SetSize(w, h)

	a := &App{rootPath: "/root", shared: &views.Shared{Files: openfiles.New(), Canvas: canvas.New(sim)}}

	a.drawPreviewHeader(w)
	sim.Show()

	text := canvas.LegendText(w, a.rootLabel(), previewLegend)
	runes := []rune(text)
	target := []rune(switchFilesLegendText)
	idx := -1
	for i := 0; i+len(target) <= len(runes); i++ {
		if string(runes[i:i+len(target)]) == switchFilesLegendText {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("test setup: %q not found in fitted legend %q", switchFilesLegendText, text)
	}
	for x := range w {
		style := cellStyle(sim, x, 0)
		inTarget := x >= idx && x < idx+len(target)
		switch {
		case inTarget && style != canvas.StyleHeaderDim:
			t.Errorf("empty list: column %d (inside %q) has style %v, want StyleHeaderDim", x, switchFilesLegendText, style)
		case !inTarget && style != canvas.StyleHeader:
			t.Errorf("empty list: column %d (outside %q) has style %v, want StyleHeader", x, switchFilesLegendText, style)
		}
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "one.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := a.shared.Files.Open(path, 1<<20); res.Outcome != openfiles.Opened {
		t.Fatalf("Open failed: %s", res.Message)
	}

	a.drawPreviewHeader(w)
	sim.Show()

	for x := range w {
		if style := cellStyle(sim, x, 0); style != canvas.StyleHeader {
			t.Errorf("non-empty list: column %d has style %v, want plain StyleHeader (nothing dimmed)", x, style)
		}
	}
}

// TestDrawPreviewHeaderShowsHideKeysWhenHelpVisible guards SPEC.md
// §5.4: while the help overlay is open, the main title bar's own
// legend collapses to the single canvas.HideKeysLegend entry — this
// takes precedence over (and bypasses) the dim-when-empty behavior
// TestDrawPreviewHeaderDimsSwitchFilesWhenEmpty covers, since there's
// no "switch files" entry left to dim once the legend is replaced.
func TestDrawPreviewHeaderShowsHideKeysWhenHelpVisible(t *testing.T) {
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	w, h := 60, 5
	sim.SetSize(w, h)

	a := &App{rootPath: "/root", shared: &views.Shared{Files: openfiles.New(), Canvas: canvas.New(sim), HelpVisible: true}}
	a.drawPreviewHeader(w)
	sim.Show()

	row := rowText(sim, 0, w)
	if !strings.HasSuffix(strings.TrimRight(row, " "), "[?] hide keys") {
		t.Errorf("header = %q, want it to end with [?] hide keys", row)
	}
	if strings.Contains(row, "switch files") || strings.Contains(row, "browse") {
		t.Errorf("header = %q, want no other legend entries while HelpVisible", row)
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
