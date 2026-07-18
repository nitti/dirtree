package preview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, content []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadLinesNormalFile(t *testing.T) {
	path := writeTemp(t, []byte("a\n\nb\n"))
	lines := ReadLines(path, DefaultByteCap)
	want := []string{"a", "", "b"}
	if len(lines) != len(want) {
		t.Fatalf("got %v, want %v", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("got %v, want %v", lines, want)
		}
	}
}

func TestReadLinesBinaryFile(t *testing.T) {
	path := writeTemp(t, []byte("abc\x00def"))
	lines := ReadLines(path, DefaultByteCap)
	if len(lines) != 1 || !strings.Contains(lines[0], "binary") {
		t.Fatalf("expected single binary-file placeholder line, got %v", lines)
	}
}

func TestReadLinesTruncatesAboveCap(t *testing.T) {
	content := strings.Repeat("x", 100)
	path := writeTemp(t, []byte(content))
	lines := ReadLines(path, 10)
	last := lines[len(lines)-1]
	if !strings.Contains(last, "truncated") {
		t.Fatalf("expected truncation marker, got %v", lines)
	}
	// The content line(s) before the marker must not exceed the cap.
	contentLen := 0
	for _, l := range lines[:len(lines)-1] {
		contentLen += len(l)
	}
	if contentLen > 10 {
		t.Fatalf("expected at most 10 bytes of content read, got %d", contentLen)
	}
}

func TestReadLinesNonexistentFile(t *testing.T) {
	lines := ReadLines(filepath.Join(t.TempDir(), "nope.txt"), DefaultByteCap)
	if len(lines) != 1 {
		t.Fatalf("expected single explanatory line, got %v", lines)
	}
}

func TestReadLinesEmptyFile(t *testing.T) {
	path := writeTemp(t, []byte(""))
	lines := ReadLines(path, DefaultByteCap)
	if len(lines) != 1 || lines[0] != "" {
		t.Fatalf("expected single empty line, got %v", lines)
	}
}

func TestReadLinesExpandsTabsToNextTabStop(t *testing.T) {
	path := writeTemp(t, []byte("a\tb\n"))
	lines := ReadLines(path, DefaultByteCap)
	want := "a       b" // 'a' at col 0, tab expands to col 8, 'b' at col 8
	if len(lines) != 1 || lines[0] != want {
		t.Fatalf("got %q, want %q", lines, []string{want})
	}
}

func TestReadLinesTabStopsResetPerLine(t *testing.T) {
	path := writeTemp(t, []byte("\tx\nyy\ty\n"))
	lines := ReadLines(path, DefaultByteCap)
	want := []string{
		"        x", // tab at col 0 expands to col 8
		"yy      y", // "yy" at cols 0-1, tab expands to col 8
	}
	if len(lines) != len(want) {
		t.Fatalf("got %v, want %v", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("line %d: got %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestHighlightNoRuleSetReturnsNil(t *testing.T) {
	segs := Highlight("weird.unknownext", []string{"hello"})
	if segs != nil {
		t.Fatal("expected nil when no rule-set matches")
	}
}

func TestHighlightOneSegmentListPerLine(t *testing.T) {
	lines := []string{"# comment", "x = 1", ""}
	segs := Highlight("script.py", lines)
	if segs == nil || len(segs) != len(lines) {
		t.Fatalf("expected %d segment lists, got %d", len(lines), len(segs))
	}
}

// TestHighlightSegmentsReconstructEachLineExactly guards against a
// token-to-line attribution bug where a token's value spans a newline
// (e.g. a whitespace token like "\n    " covering an end-of-line plus
// the next line's leading indentation): the text after the newline
// must land on the new line, not get appended to the line that just
// ended (which silently drops one line's indentation and shifts
// everything after it by one line — this is exactly the "mangled YAML
// indentation" bug it was written to catch).
func TestHighlightSegmentsReconstructEachLineExactly(t *testing.T) {
	lines := []string{
		"builds:",
		"  - id: dirtree",
		"    main: ./cmd/dirtree",
		"    env:",
		"      - CGO_ENABLED=0",
	}
	segs := Highlight("build.yaml", lines)
	if segs == nil {
		t.Fatal("expected a lexer match for build.yaml")
	}
	if len(segs) != len(lines) {
		t.Fatalf("expected %d segment lists, got %d", len(lines), len(segs))
	}
	for i, want := range lines {
		var got strings.Builder
		for _, seg := range segs[i] {
			got.WriteString(seg.Text)
		}
		if got.String() != want {
			t.Fatalf("line %d: got %q, want %q", i, got.String(), want)
		}
	}
}

func TestAlignSegmentsToLinesPadsAndTruncates(t *testing.T) {
	segs := [][]Segment{{{Text: "a", Category: CategoryText}}}
	aligned := AlignSegmentsToLines(segs, 3)
	if len(aligned) != 3 {
		t.Fatalf("expected padded to 3, got %d", len(aligned))
	}
	aligned2 := AlignSegmentsToLines(segs, 0)
	if len(aligned2) != 0 {
		t.Fatalf("expected truncated to 0, got %d", len(aligned2))
	}
}

func TestWrapLineSplitsAtWidth(t *testing.T) {
	segs := []Segment{{Text: "abcdefghij", Category: CategoryText}}
	rows := wrapLineSegments(segs, 4)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows (4+4+2), got %d: %v", len(rows), rows)
	}
	for _, row := range rows {
		total := 0
		for _, s := range row {
			total += len([]rune(s.Text))
		}
		if total > 4 {
			t.Fatalf("row exceeds width: %v", row)
		}
	}
}

func TestWrapPreservesCategoryBoundaries(t *testing.T) {
	segs := []Segment{
		{Text: "ab", Category: CategoryKeyword},
		{Text: "cd", Category: CategoryString},
	}
	rows := wrapLineSegments(segs, 3)
	// width 3 forces a split inside the combined 4-char content.
	var flatCats []Category
	for _, row := range rows {
		for _, s := range row {
			flatCats = append(flatCats, s.Category)
		}
	}
	if len(flatCats) < 2 || flatCats[0] != CategoryKeyword {
		t.Fatalf("expected first segment category preserved, got %v", flatCats)
	}
}

// rowText concatenates a wrapped row's segment text, for readable
// assertions against the plain content a row ends up containing.
func rowText(row []Segment) string {
	var b strings.Builder
	for _, s := range row {
		b.WriteString(s.Text)
	}
	return b.String()
}

func TestWrapPrefersBreakingAtSpaceOverMidWord(t *testing.T) {
	segs := []Segment{{Text: "hello world", Category: CategoryText}}
	rows := wrapLineSegments(segs, 8)
	want := []string{"hello", "world"}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows %v, want %v", len(rows), rowsText(rows), want)
	}
	for i, w := range want {
		if rowText(rows[i]) != w {
			t.Fatalf("row %d: got %q, want %q", i, rowText(rows[i]), w)
		}
	}
}

func TestWrapMovesWordToNextRowRatherThanSplittingIt(t *testing.T) {
	// "friend" (6 chars) doesn't fit after "hi " within width 6, but
	// fits exactly on a fresh row — it must move there whole rather than
	// being split (a naive fixed-width wrap would produce "hi frie"/"nd").
	segs := []Segment{{Text: "hi friend", Category: CategoryText}}
	rows := wrapLineSegments(segs, 6)
	want := []string{"hi", "friend"}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows %v, want %v", len(rows), rowsText(rows), want)
	}
	for i, w := range want {
		if rowText(rows[i]) != w {
			t.Fatalf("row %d: got %q, want %q", i, rowText(rows[i]), w)
		}
	}
}

func TestWrapAllowsBreakingAfterDash(t *testing.T) {
	segs := []Segment{{Text: "auto-complete", Category: CategoryText}}
	rows := wrapLineSegments(segs, 6)
	if len(rows) == 0 || rowText(rows[0]) != "auto-" {
		t.Fatalf("expected first row to break after the dash as \"auto-\", got %v", rowsText(rows))
	}
	// The dash must be kept (not dropped like a triggering space).
	full := rowsText(rows)
	joined := ""
	for _, r := range full {
		joined += r
	}
	if joined != "auto-complete" {
		t.Fatalf("expected rows to reconstruct to %q, got %q", "auto-complete", joined)
	}
}

func TestWrapHardBreaksWordLongerThanFullWidth(t *testing.T) {
	// No space or dash anywhere: a word longer than the width has no
	// legal break point at all, so it must still hard-break rather than
	// overflow the row.
	segs := []Segment{{Text: "supercalifragilistic", Category: CategoryText}}
	rows := wrapLineSegments(segs, 5)
	want := []string{"super", "calif", "ragil", "istic"}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows %v, want %v", len(rows), rowsText(rows), want)
	}
	for i, w := range want {
		if rowText(rows[i]) != w {
			t.Fatalf("row %d: got %q, want %q", i, rowText(rows[i]), w)
		}
	}
}

func TestWrapDropsTriggeringSpaceButKeepsRealTrailingSpace(t *testing.T) {
	// The space that causes the wrap disappears; a real trailing space
	// at the actual end of the line's content does not.
	segs := []Segment{{Text: "ab cd ", Category: CategoryText}}
	rows := wrapLineSegments(segs, 3)
	want := []string{"ab", "cd "}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows %v, want %v", len(rows), rowsText(rows), want)
	}
	for i, w := range want {
		if rowText(rows[i]) != w {
			t.Fatalf("row %d: got %q, want %q", i, rowText(rows[i]), w)
		}
	}
}

func rowsText(rows [][]Segment) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = rowText(r)
	}
	return out
}

func TestBuildDisplayRowsAssignsNumberOnlyToFirstWrappedRow(t *testing.T) {
	lines := [][]Segment{
		{{Text: "abcdefgh", Category: CategoryText}}, // wraps into 2 rows at width 4
		{{Text: "xy", Category: CategoryText}},
	}
	rows, _ := BuildDisplayRows(lines, 4)
	if len(rows) != 3 {
		t.Fatalf("expected 3 display rows, got %d", len(rows))
	}
	if !rows[0].HasNumber {
		t.Fatal("expected first row of line 0 to have a number")
	}
	if rows[1].HasNumber {
		t.Fatal("expected continuation row of line 0 to have no number")
	}
	if !rows[2].HasNumber {
		t.Fatal("expected first row of line 1 to have a number")
	}
}

func TestBuildDisplayRowsFirstRowIndex(t *testing.T) {
	lines := [][]Segment{
		{{Text: "abcdefgh", Category: CategoryText}},
		{{Text: "xy", Category: CategoryText}},
	}
	_, firstRow := BuildDisplayRows(lines, 4)
	if firstRow[0] != 0 {
		t.Fatalf("expected line 0's first row at index 0, got %d", firstRow[0])
	}
	if firstRow[1] != 2 {
		t.Fatalf("expected line 1's first row at index 2, got %d", firstRow[1])
	}
}

func TestBuildDisplayRowsColStart(t *testing.T) {
	lines := [][]Segment{
		{{Text: "abcdefgh", Category: CategoryText}},
	}
	rows, _ := BuildDisplayRows(lines, 4)
	if len(rows) != 2 {
		t.Fatalf("expected 2 wrapped rows, got %d", len(rows))
	}
	if rows[0].ColStart != 0 {
		t.Fatalf("expected first row's ColStart 0, got %d", rows[0].ColStart)
	}
	if rows[1].ColStart != 4 {
		t.Fatalf("expected second row's ColStart 4, got %d", rows[1].ColStart)
	}
}

func TestLoadBinaryFileFails(t *testing.T) {
	path := writeTemp(t, []byte("abc\x00def"))
	res := Load(path, DefaultByteCap)
	if !res.Failed || res.Message != "binary file, preview not available" {
		t.Fatalf("expected failed binary result, got %+v", res)
	}
}

func TestLoadNonexistentFileFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nope.txt")
	res := Load(path, DefaultByteCap)
	if !res.Failed || res.Message == "" {
		t.Fatalf("expected failed result with a message, got %+v", res)
	}
	if strings.Contains(res.Message, path) {
		t.Fatalf("expected message not to repeat the already-visible path, got %q", res.Message)
	}
}

func TestLoadOrdinaryFileSucceeds(t *testing.T) {
	path := writeTemp(t, []byte("package main\n\nfunc main() {}\n"))
	res := Load(path, DefaultByteCap)
	if res.Failed {
		t.Fatalf("expected success, got failed result: %+v", res)
	}
	if len(res.Lines) != 3 {
		t.Fatalf("expected 3 lines, got %v", res.Lines)
	}
	if len(res.Segs) != len(res.Lines) {
		t.Fatalf("expected one segment list per line, got %d segs for %d lines", len(res.Segs), len(res.Lines))
	}
}

func TestLoadFallsBackToPlainTextWhenNoRulesMatch(t *testing.T) {
	path := writeTemp(t, []byte("just some text\n"))
	res := Load(path, DefaultByteCap)
	if res.Failed {
		t.Fatalf("expected success, got failed result: %+v", res)
	}
	for _, segs := range res.Segs {
		for _, s := range segs {
			if s.Category != CategoryText {
				t.Fatalf("expected plain-text fallback category, got %v", s.Category)
			}
		}
	}
}
