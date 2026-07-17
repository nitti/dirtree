package preview

// DisplayRow is one wrapped row ready for rendering. SourceLine is the
// 0-based index of the source line it came from; HasNumber is true only
// for a source line's first wrapped row (SPEC.md §8's gutter rule).
// ColStart is the rune column, within SourceLine, where this row's text
// begins — 0 for a line's first wrapped row, and the cumulative rune
// length of that line's prior rows for any continuation row. This lets
// callers (e.g. in-file find highlighting, SPEC.md §2.4) map a
// source-line-relative match column back onto the specific wrapped row
// and row-relative column it falls in.
type DisplayRow struct {
	SourceLine int
	HasNumber  bool
	Segments   []Segment
	ColStart   int
}

// SegmentsRuneLen returns the total rune length of segs' concatenated
// text — the width, in columns, a row built from them occupies.
func SegmentsRuneLen(segs []Segment) int {
	n := 0
	for _, seg := range segs {
		n += len([]rune(seg.Text))
	}
	return n
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
// columns, preserving each fragment's category across the split
// (SPEC.md §8). It word-wraps: a row only ever breaks mid-word as a
// last resort, when the word doesn't fit within a full-width row even
// on its own — otherwise the break lands at the end of a run of spaces
// (the triggering spaces are dropped, the standard word-wrap
// convention, rather than re-appearing as leading whitespace on the
// next row) or immediately after a dash/hyphen (kept on the row that
// ends there, since a trailing "-" is itself the visual break marker,
// same as a hyphenated word wrapping in prose).
func wrapLineSegments(segments []Segment, width int) [][]Segment {
	if width <= 0 {
		width = 1
	}

	// Flatten to a single rune stream with a parallel per-rune category,
	// so break-point search isn't confined to one segment at a time —
	// a legal break can span a segment boundary (e.g. a keyword
	// immediately followed by a space in a different category).
	var runes []rune
	var cats []Category
	for _, seg := range segments {
		for _, r := range seg.Text {
			runes = append(runes, r)
			cats = append(cats, seg.Category)
		}
	}
	if len(runes) == 0 {
		return [][]Segment{{}}
	}

	var rows [][]Segment
	pos := 0
	for pos < len(runes) {
		end := wrapBreak(runes, pos, width)
		// A break caused by a run of spaces drops all of them from the
		// row (they're implicit in the row boundary itself, the same
		// way prose word-wrap elides the space it broke on) — but only
		// when `end` is an artificial wrap point, not the line's actual
		// end, so real trailing whitespace at the true end of a line's
		// content is preserved rather than eaten.
		contentEnd := end
		if end < len(runes) && runes[end-1] == ' ' {
			for contentEnd > pos && runes[contentEnd-1] == ' ' {
				contentEnd--
			}
		}
		rows = append(rows, buildRowSegments(runes[pos:contentEnd], cats[pos:contentEnd]))
		pos = end
	}
	return rows
}

// wrapBreak returns the index a row starting at start should end at,
// searching forward up to width runes for the last legal break point
// (wrapBreakable) in that range. If none exists — an unbroken run of
// non-space, non-dash characters at least width runes long — it falls
// back to a hard break at exactly start+width, the only case where a
// row can end mid-word.
func wrapBreak(runes []rune, start, width int) int {
	limit := start + width
	if limit >= len(runes) {
		return len(runes)
	}
	best := -1
	for i := start + 1; i <= limit; i++ {
		if wrapBreakable(runes, i) {
			best = i
		}
	}
	if best > start {
		return best
	}
	return limit
}

// wrapBreakable reports whether ending a row right before index i is a
// legal word-wrap break: i is at the end of a run of spaces (not
// between two spaces, so a run of indentation isn't fragmented into
// one-column rows) or immediately after a dash/hyphen.
func wrapBreakable(runes []rune, i int) bool {
	if i <= 0 || i > len(runes) {
		return false
	}
	switch runes[i-1] {
	case ' ':
		return i == len(runes) || runes[i] != ' '
	case '-':
		return true
	default:
		return false
	}
}

// buildRowSegments merges a row's flattened runes back into segments,
// combining consecutive runs of the same category into one Segment
// (the same merge rule Highlight's flush uses) rather than emitting one
// segment per source rune.
func buildRowSegments(runes []rune, cats []Category) []Segment {
	var segs []Segment
	for i, r := range runes {
		if i > 0 && cats[i] == cats[i-1] {
			segs[len(segs)-1].Text += string(r)
			continue
		}
		segs = append(segs, Segment{Text: string(r), Category: cats[i]})
	}
	if segs == nil {
		segs = []Segment{}
	}
	return segs
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
		colStart := 0
		for i, w := range wrapped {
			rows = append(rows, DisplayRow{
				SourceLine: lineIdx,
				HasNumber:  i == 0,
				Segments:   w,
				ColStart:   colStart,
			})
			colStart += SegmentsRuneLen(w)
		}
	}
	return rows, firstRow
}
