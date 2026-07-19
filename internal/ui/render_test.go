package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/openfiles"
)

// TestLegendTier1FitsMinTerminalWidth guards SPEC.md §6.4's minimum
// terminal size against §5.2's legend tiering: every legend's
// priority-1 (never-dropped) text must fit within minTerminalWidth on
// its own, with no left-hand label — otherwise a terminal at exactly
// the enforced minimum would still see a clipped legend, the same class
// of bug a too-long jumpLegend/searchLegend priority-1 entry caused
// before those entries were demoted to priority 2.
func TestLegendTier1FitsMinTerminalWidth(t *testing.T) {
	named := map[string][]legendEntry{
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
		"quickOpenLegend":        quickOpenLegend,
		"openFilesLegend(false)": openFilesLegend(false),
		"openFilesLegend(true)":  openFilesLegend(true),
	}
	for name, entries := range named {
		tier1 := legendString(keepUpToPriority(entries, 1))
		if n := len([]rune(tier1)); n > minTerminalWidth {
			t.Errorf("%s's priority-1 text is %d runes, exceeding minTerminalWidth (%d): %q", name, n, minTerminalWidth, tier1)
		}
	}
}

// TestLegendFit covers legendFit's narrow-terminal drop order (SPEC.md
// §5.2): the full label+legend, then legend-only (label dropped), then
// whole priority-3 entries dropped, then priority-2 entries additionally
// dropped — survivors always keep their original left-to-right order and
// are never truncated mid-entry.
func TestLegendFit(t *testing.T) {
	entries := []legendEntry{
		{"[return] open", 1},
		{"[tab] next", 2},
		{"[ctrl+u] clear", 3},
		{"[esc] close", 1},
	}
	full := legendString(entries)                        // "[return] open  [tab] next  [ctrl+u] clear  [esc] close"
	tier12 := legendString(keepUpToPriority(entries, 2)) // drop tier 3
	tier1 := legendString(keepUpToPriority(entries, 1))  // drop tier 2 and 3
	left := "~/dirtree"

	tests := []struct {
		name       string
		w          int
		wantText   string
		wantLeftIn bool
	}{
		{"fits with label", len(left) + 1 + len(full), left + " " + full, true},
		{"label dropped, full legend fits", len(full), full, false},
		{"tier 3 dropped", len(tier12), tier12, false},
		{"tier 2 and 3 dropped", len(tier1), tier1, false},
		{"tier 1 only, still narrower than tier1 text", len(tier1) - 1, tier1, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			text, leftIncluded := legendFit(tc.w, left, entries)
			if text != tc.wantText {
				t.Errorf("legendFit(%d) text = %q, want %q", tc.w, text, tc.wantText)
			}
			if leftIncluded != tc.wantLeftIn {
				t.Errorf("legendFit(%d) leftIncluded = %v, want %v", tc.w, leftIncluded, tc.wantLeftIn)
			}
		})
	}
}

// TestLegendFitNeverReordersOrTruncatesMidEntry checks that whichever
// entries survive a drop keep their original relative order, and that no
// returned text ever contains a partial "[..." fragment cut off mid-entry.
func TestLegendFitNeverReordersOrTruncatesMidEntry(t *testing.T) {
	entries := []legendEntry{
		{"[a] alpha", 1},
		{"[b] bravo", 3},
		{"[c] charlie", 2},
		{"[d] delta", 1},
	}
	for w := 0; w <= 60; w++ {
		text, _ := legendFit(w, "left", entries)
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

// TestKeepUpToPriority checks the tier-filtering building block directly.
func TestKeepUpToPriority(t *testing.T) {
	entries := []legendEntry{{"a", 1}, {"b", 2}, {"c", 3}, {"d", 1}}
	got := keepUpToPriority(entries, 2)
	want := []legendEntry{{"a", 1}, {"b", 2}, {"d", 1}}
	if len(got) != len(want) {
		t.Fatalf("keepUpToPriority = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("keepUpToPriority = %v, want %v", got, want)
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
		screen: sim,
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
