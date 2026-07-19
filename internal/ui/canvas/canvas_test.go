package canvas

import (
	"strings"
	"testing"
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

// TestKeepUpToPriority checks the tier-filtering building block directly.
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
