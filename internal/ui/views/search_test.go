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
