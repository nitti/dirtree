package views

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/search"
	"github.com/nitti/dirtree/internal/ui/canvas"
)

// TestSearchLegendTier1FitsMinTerminalWidth guards SPEC.md §6.4's
// minimum terminal size against §5.2's legend tiering: content search's
// priority-1 (never-dropped) legend text must fit within
// canvas.MinTerminalWidth on its own, with no left-hand label —
// otherwise a terminal at exactly the enforced minimum would still see
// a clipped legend.
func TestSearchLegendTier1FitsMinTerminalWidth(t *testing.T) {
	tier1 := canvas.LegendString(canvas.KeepUpToPriority(searchLegend, 1))
	if n := len([]rune(tier1)); n > canvas.MinTerminalWidth {
		t.Errorf("searchLegend's priority-1 text is %d runes, exceeding MinTerminalWidth (%d): %q", n, canvas.MinTerminalWidth, tier1)
	}
}

// TestSearchDrawSuppressesHeaderLegendWhenHelpVisible guards SPEC.md
// §5.4: while the help overlay is showing, the header keeps its
// "SEARCH" mode label but its legend collapses to the single
// canvas.HideKeysLegend entry.
func TestSearchDrawSuppressesHeaderLegendWhenHelpVisible(t *testing.T) {
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	w, h := 60, 10
	sim.SetSize(w, h)

	v := &Search{Shared: &Shared{Files: openfiles.New(), Canvas: canvas.New(sim), HelpVisible: true}}
	v.Draw(w, h)
	sim.Show()

	row := rowText(sim, 0, w)
	if !strings.Contains(row, "SEARCH") || !strings.HasSuffix(strings.TrimRight(row, " "), "[?] hide keys") {
		t.Errorf("header = %q, want SEARCH label plus only [?] hide keys", row)
	}
	if got := v.CurrentLegend(); &got[0] != &searchLegend[0] {
		t.Errorf("CurrentLegend() = %v, want searchLegend", got)
	}
}

// TestSearchSummaryCountsHitsAndFilesWithHits guards searchSummary's
// counting rule (SPEC.md §9.2): only files that actually contributed
// at least one hit count toward either total, so a result present
// solely because of a scan issue (§9.1) with zero hits contributes to
// neither the hit count nor the file count. Singular/plural wording is
// also exercised at the 1-vs-many boundary for both nouns.
func TestSearchSummaryCountsHitsAndFilesWithHits(t *testing.T) {
	cases := []struct {
		name    string
		results []search.FileResult
		want    string
	}{
		{"no results", nil, "0 hits across 0 files"},
		{
			"single hit in single file",
			[]search.FileResult{{AbsPath: "a", Hits: []search.Hit{{LineNum: 1, LineText: "x"}}}},
			"1 hit across 1 file",
		},
		{
			"multiple hits across multiple files",
			[]search.FileResult{
				{AbsPath: "a", Hits: []search.Hit{{LineNum: 1, LineText: "x"}, {LineNum: 2, LineText: "y"}}},
				{AbsPath: "b", Hits: []search.Hit{{LineNum: 1, LineText: "z"}}},
			},
			"3 hits across 2 files",
		},
		{
			"scan-issue file with no hits doesn't count as a file",
			[]search.FileResult{{AbsPath: "a", Issue: "timed out"}},
			"0 hits across 0 files",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := searchSummary(c.results); got != c.want {
				t.Errorf("searchSummary(%v) = %q, want %q", c.results, got, c.want)
			}
		})
	}
}

// TestSearchDrawBoldsFileNamesWithHits guards a file row's own name
// (as opposed to its disclosure marker, open-indicator, or match-count
// suffix) rendering bolded when the file actually contributed at least
// one hit, and staying plain when it didn't (a result present solely
// because of a scan issue, §9.1) — so a scanned-but-empty file is
// visually distinguishable from one with real matches at a glance.
// Hit rows (indented under a file) never bold anything, since a hit row
// has no file name of its own to bold.
func TestSearchDrawBoldsFileNamesWithHits(t *testing.T) {
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	w, h := 60, 10
	sim.SetSize(w, h)

	v := &Search{
		Shared: &Shared{Files: openfiles.New(), Canvas: canvas.New(sim)},
		Query:  "needle",
		Results: []search.FileResult{
			{AbsPath: "a", RelPath: "hit.txt", Hits: []search.Hit{{LineNum: 1, LineText: "needle"}}},
			{AbsPath: "b", RelPath: "empty.txt", Issue: "timed out"},
		},
	}
	v.Draw(w, h)
	sim.Show()

	// runeIndex finds target's rune-column start within row — a plain
	// byte-offset search (strings.Index) would misalign against cell
	// columns here, since the disclosure marker and open-indicator
	// ahead of the name are multi-byte runes.
	runeIndex := func(t *testing.T, row, target string) int {
		t.Helper()
		runes, targetRunes := []rune(row), []rune(target)
		for i := 0; i+len(targetRunes) <= len(runes); i++ {
			if string(runes[i:i+len(targetRunes)]) == target {
				return i
			}
		}
		t.Fatalf("test setup: %q not found in row %q", target, row)
		return -1
	}

	// Row 2 (below the header and query rows): the "hit.txt" file row.
	rowY := 2
	row := rowText(sim, rowY, w)
	start := runeIndex(t, row, "hit.txt")
	for x := start; x < start+len([]rune("hit.txt")); x++ {
		_, _, attr := cellStyle(sim, x, rowY).Decompose()
		if attr&tcell.AttrBold == 0 {
			t.Errorf("hit.txt: column %d not bold, want bold", x)
		}
	}

	// Row 3 is hit.txt's own (expanded-by-default) hit row; row 4 is
	// the "empty.txt" scan-issue-only file row, which has no hits, so
	// its own name must stay plain (not bold).
	rowY = 4
	row = rowText(sim, rowY, w)
	start = runeIndex(t, row, "empty.txt")
	for x := start; x < start+len([]rune("empty.txt")); x++ {
		_, _, attr := cellStyle(sim, x, rowY).Decompose()
		if attr&tcell.AttrBold != 0 {
			t.Errorf("empty.txt: column %d is bold, want not bold", x)
		}
	}
}

// TestSearchDrawShowsHitSummaryRightAligned guards SPEC.md §9.2: once
// results exist (even zero matches), the query row's right side shows
// "N hits across N files", right-aligned with at least one space of
// separation from the query text on its left.
func TestSearchDrawShowsHitSummaryRightAligned(t *testing.T) {
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	w, h := 60, 10
	sim.SetSize(w, h)

	v := &Search{
		Shared: &Shared{Files: openfiles.New(), Canvas: canvas.New(sim)},
		Query:  "needle",
		Results: []search.FileResult{
			{AbsPath: "a", RelPath: "a.txt", Hits: []search.Hit{{LineNum: 1, LineText: "needle"}}},
			{AbsPath: "b", RelPath: "b.txt", Hits: []search.Hit{{LineNum: 1, LineText: "needle"}, {LineNum: 2, LineText: "needle"}}},
		},
	}
	v.Draw(w, h)
	sim.Show()

	row := rowText(sim, 1, w)
	if !strings.HasPrefix(row, "> needle") {
		t.Fatalf("query row = %q, want it to start with the prompt and query", row)
	}
	if !strings.HasSuffix(strings.TrimRight(row, " "), "3 hits across 2 files") {
		t.Fatalf("query row = %q, want it to end with the hit summary", row)
	}
}
