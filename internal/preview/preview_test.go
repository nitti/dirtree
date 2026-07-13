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
