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
// terminal size against §5.2's legend tiering: every legend's
// priority-1 (never-dropped) text must fit within canvas.MinTerminalWidth
// on its own, with no left-hand label — otherwise a terminal at exactly
// the enforced minimum would still see a clipped legend, the same class
// of bug a too-long jumpLegend/searchLegend priority-1 entry caused
// before those entries were demoted to priority 2. Quick open's own
// legend has an equivalent test alongside its own package
// (views.TestQuickOpenLegendTier1FitsMinTerminalWidth), since it no
// longer lives here.
func TestLegendTier1FitsMinTerminalWidth(t *testing.T) {
	named := map[string][]canvas.LegendEntry{
		"previewLegend":          previewLegend,
		"browserLegend":          browserLegend,
		"jumpLegend":             jumpLegend,
		"searchLegend":           searchLegend,
		"fileLegend":             fileLegend,
		"fileLegendCopyModeOn":   fileLegendCopyModeOn,
		"gotoLegend":             gotoLegend,
		"findPromptLegend":       findPromptLegend,
		"findLegend":             findLegend,
		"findLegendNoMatches":    findLegendNoMatches,
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

// TestDrawPreviewShowsGotoLegend verifies the goto-line prompt row now
// renders a keybinding legend (SPEC.md §5.2) alongside the "goto line: "
// input, closing what was previously a bar with no legend at all.
func TestDrawPreviewShowsGotoLegend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	w, h := 60, 10
	sim.SetSize(w, h)

	a := &App{
		shared: &views.Shared{Canvas: canvas.New(sim)},
		files:  openfiles.New(),
	}
	if res := a.files.Open(path, 1<<20); res.Outcome != openfiles.Opened {
		t.Fatalf("Open failed: %s", res.Message)
	}
	a.gotoPromptOpen = true
	a.gotoInput = "2"

	a.drawPreview(0, 0, w, h)
	sim.Show()

	row := rowText(sim, h-1, w)
	if !strings.HasPrefix(row, "goto line: 2") {
		t.Fatalf("goto-line row = %q, want it to start with the prompt text", row)
	}
	for _, want := range []string{"[return] jump", "[esc] cancel"} {
		if !strings.Contains(row, want) {
			t.Errorf("goto-line row = %q, missing legend entry %q", row, want)
		}
	}
}

func rowText(sim tcell.SimulationScreen, y, w int) string {
	cells, width, _ := sim.GetContents()
	var b strings.Builder
	for x := 0; x < w && x < width; x++ {
		c := cells[y*width+x]
		if len(c.Runes) > 0 {
			b.WriteRune(c.Runes[0])
		} else {
			b.WriteRune(' ')
		}
	}
	return b.String()
}
