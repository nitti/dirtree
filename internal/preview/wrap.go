package preview

// DisplayRow is one wrapped row ready for rendering. SourceLine is the
// 0-based index of the source line it came from; HasNumber is true only
// for a source line's first wrapped row (SPEC.md §8's gutter rule).
type DisplayRow struct {
	SourceLine int
	HasNumber  bool
	Segments   []Segment
}

// AlignSegmentsToLines defensively pads or truncates segs so it has
// exactly numLines entries, guarding against a lexing pass producing a
// mismatched row count (SPEC.md §8, TESTING.md "Preview" group).
func AlignSegmentsToLines(segs [][]Segment, numLines int) [][]Segment {
	out := make([][]Segment, numLines)
	for i := 0; i < numLines; i++ {
		if i < len(segs) {
			out[i] = segs[i]
		} else {
			out[i] = []Segment{{Text: "", Category: CategoryText}}
		}
	}
	return out
}

// wrapLineSegments wraps one source line's segments to fit width
// columns, splitting fragments mid-token as needed so no row exceeds
// width, and preserving each fragment's category across the split
// (SPEC.md §8).
func wrapLineSegments(segments []Segment, width int) [][]Segment {
	if width <= 0 {
		width = 1
	}
	var rows [][]Segment
	var current []Segment
	col := 0

	pushRow := func() {
		if current == nil {
			current = []Segment{}
		}
		rows = append(rows, current)
		current = nil
		col = 0
	}

	for _, seg := range segments {
		text := []rune(seg.Text)
		for len(text) > 0 {
			remaining := width - col
			if remaining <= 0 {
				pushRow()
				remaining = width
			}
			take := remaining
			if take > len(text) {
				take = len(text)
			}
			chunk := string(text[:take])
			text = text[take:]
			current = append(current, Segment{Text: chunk, Category: seg.Category})
			col += take
		}
	}
	if current != nil || len(rows) == 0 {
		pushRow()
	}
	return rows
}

// BuildDisplayRows wraps every source line's segments to width and
// returns the flattened rows plus an index from source-line number to
// the display-row index of that line's first wrapped row (SPEC.md §8).
func BuildDisplayRows(lines [][]Segment, width int) ([]DisplayRow, map[int]int) {
	var rows []DisplayRow
	firstRow := make(map[int]int, len(lines))

	for lineIdx, segs := range lines {
		wrapped := wrapLineSegments(segs, width)
		firstRow[lineIdx] = len(rows)
		for i, w := range wrapped {
			rows = append(rows, DisplayRow{
				SourceLine: lineIdx,
				HasNumber:  i == 0,
				Segments:   w,
			})
		}
	}
	return rows, firstRow
}
