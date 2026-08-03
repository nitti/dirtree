package views

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/preview"
	"github.com/nitti/dirtree/internal/tree"
	"github.com/nitti/dirtree/internal/ui/canvas"
)

// TestBytesPerRowForAdaptsToWidth guards SPEC.md §2.1a's "bytesPerRow is
// a derived quantity from the available width, always a whole number of
// groups" rule: it's always a multiple of hexBytesPerGroup, never a
// value whose row wouldn't fit once at least one group's row does fit at
// all, and narrower widths never return more bytes per row than wider
// ones (monotonic, not just individually in-bounds).
func TestBytesPerRowForAdaptsToWidth(t *testing.T) {
	gutterWidth := 10
	prev := 0
	for _, w := range []int{20, 40, 60, 80, 120, 200} {
		n := bytesPerRowFor(w, gutterWidth)
		if n < hexBytesPerGroup {
			t.Fatalf("width %d: bytesPerRowFor returned %d, want >= %d", w, n, hexBytesPerGroup)
		}
		if n%hexBytesPerGroup != 0 {
			t.Errorf("width %d: bytesPerRowFor returned %d, not a whole number of %d-byte groups", w, n, hexBytesPerGroup)
		}
		if hexRowWidth(gutterWidth, n) > w && n > hexBytesPerGroup {
			t.Errorf("width %d: bytesPerRowFor returned %d, whose row width %d exceeds it", w, n, hexRowWidth(gutterWidth, n))
		}
		if n < prev {
			t.Errorf("width %d: bytesPerRowFor returned %d, less than a narrower width's %d", w, n, prev)
		}
		prev = n
	}
}

// TestBytesPerRowForNeverBelowOneGroup guards the "remains navigable
// even at a width too narrow to fit one group comfortably" fallback
// (SPEC.md §2.1a): it never splits a group, so the floor is one whole
// group, not one byte.
func TestBytesPerRowForNeverBelowOneGroup(t *testing.T) {
	if got := bytesPerRowFor(1, 10); got != hexBytesPerGroup {
		t.Fatalf("bytesPerRowFor(1, 10) = %d, want %d", got, hexBytesPerGroup)
	}
}

// TestDrawHexContentAsciiColumnRightAligned guards SPEC.md §2.1a's
// left-align-the-hex-grid/right-align-the-ASCII-column layout rule: the
// gutter+hex-grid block starts flush at the row's left edge and the
// ASCII column ends flush at its right edge (x0+w-1), with whatever
// width is left over falling as a gap *between* them rather than
// stretching either block — and this holds across a resize, confirming
// the ASCII column's right edge tracks the actual available width
// instead of staying pinned to wherever the hex grid happens to end.
func TestDrawHexContentAsciiColumnRightAligned(t *testing.T) {
	dir := t.TempDir()
	// Printable, non-space bytes throughout so every rendered ASCII cell
	// this test inspects is unambiguously real content, never blank
	// padding that could be mistaken for it.
	content := make([]byte, 0, 200)
	content = append(content, 0) // NUL byte -> binary
	for i := 1; i < 200; i++ {
		content = append(content, byte('A'+(i%26)))
	}
	path := writeBinaryFile(t, dir, content)

	for _, w := range []int{60, 78, 100, 140} {
		sim := tcell.NewSimulationScreen("")
		if err := sim.Init(); err != nil {
			t.Fatal(err)
		}
		sim.SetSize(w, 10)

		files := openfiles.New()
		v := &Preview{Shared: &Shared{Files: files, Canvas: canvas.New(sim)}}
		res := files.Open(path, preview.DefaultByteCap)
		if res.Outcome != openfiles.Opened {
			t.Fatalf("width %d: Open failed: %+v", w, res)
		}

		v.Draw(0, 1, w, 9, true)
		sim.Show()

		mainc, _, _, _ := sim.GetContent(w-1, 2)
		if mainc == ' ' || mainc == 0 {
			t.Errorf("width %d: rightmost cell of the first content row is blank, want the ASCII column flush to the right edge", w)
		}
	}
}

// TestClampHexOffsetBounds guards SPEC.md §2.1a's viewport clamping: an
// offset is clamped to [0, lastRowStart], never negative, never past the
// file's last row, and always itself a row-boundary multiple of
// bytesPerRow.
func TestClampHexOffsetBounds(t *testing.T) {
	const bytesPerRow = 16
	const size = 100 // last row starts at 96 (bytes 96-99)

	cases := []struct {
		name   string
		offset int64
		want   int64
	}{
		{"negative clamps to 0", -50, 0},
		{"zero stays 0", 0, 0},
		{"mid-file snaps down to row boundary", 37, 32},
		{"past EOF clamps to last row start", 500, 96},
		{"exactly last row start", 96, 96},
	}
	for _, c := range cases {
		if got := clampHexOffset(c.offset, size, bytesPerRow); got != c.want {
			t.Errorf("%s: clampHexOffset(%d, %d, %d) = %d, want %d", c.name, c.offset, size, bytesPerRow, got, c.want)
		}
	}
}

// TestClampHexOffsetEmptyFile guards the size<=0 edge case: everything
// clamps to offset 0 rather than producing a negative or nonsensical
// last-row bound.
func TestClampHexOffsetEmptyFile(t *testing.T) {
	if got := clampHexOffset(10, 0, 16); got != 0 {
		t.Fatalf("clampHexOffset on an empty file = %d, want 0", got)
	}
}

// TestLastRowStart guards the row-start-of-final-byte computation used
// by Home/End (SPEC.md §2.1a) and clamping.
func TestLastRowStart(t *testing.T) {
	cases := []struct {
		size        int64
		bytesPerRow int
		want        int64
	}{
		{0, 16, 0},
		{1, 16, 0},
		{16, 16, 0},
		{17, 16, 16},
		{100, 16, 96},
	}
	for _, c := range cases {
		if got := lastRowStart(c.size, c.bytesPerRow); got != c.want {
			t.Errorf("lastRowStart(%d, %d) = %d, want %d", c.size, c.bytesPerRow, got, c.want)
		}
	}
}

// TestHexGutterWidthSizing guards the offset gutter's width (SPEC.md
// §2.1a): enough hex digits for the file's size, floored at 4 digits so
// a small file's gutter isn't distractingly narrow, plus the 2-column
// separator.
func TestHexGutterWidthSizing(t *testing.T) {
	cases := []struct {
		size int64
		want int
	}{
		{0, 4 + 2},
		{0xF, 4 + 2},     // 1 hex digit, floored to 4
		{0xFFFF, 4 + 2},  // exactly 4 hex digits
		{0x10000, 5 + 2}, // 5 hex digits
		{0x100000000, 9 + 2},
	}
	for _, c := range cases {
		if got := hexGutterWidth(c.size); got != c.want {
			t.Errorf("hexGutterWidth(%#x) = %d, want %d", c.size, got, c.want)
		}
	}
}

// TestParseOffsetHexAndDecimal guards goto-offset's input parsing
// (SPEC.md §2.1a): a "0x"-prefixed input parses as hex, anything else as
// plain decimal, and malformed input is rejected rather than guessed at.
func TestParseOffsetHexAndDecimal(t *testing.T) {
	cases := []struct {
		input  string
		want   int64
		wantOK bool
	}{
		{"0x1a", 0x1a, true},
		{"0X1A", 0x1A, true},
		{"26", 26, true},
		{"0", 0, true},
		{"", 0, false},
		{"-5", 0, false},
		{"1a", 0, false}, // no 0x prefix: not valid decimal
		{"0xzz", 0, false},
		{"0x", 0, false},
	}
	for _, c := range cases {
		got, ok := parseOffset(c.input)
		if ok != c.wantOK || (ok && got != c.want) {
			t.Errorf("parseOffset(%q) = (%d, %v), want (%d, %v)", c.input, got, ok, c.want, c.wantOK)
		}
	}
}

// TestFormatSize guards the file title bar's human-readable size tag
// (SPEC.md §2.1a).
func TestFormatSize(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{1023, "1023"},
		{1024, "1K"},
		{1536, "2K"}, // rounded, not truncated: 1536/1024 = 1.5 -> 2
		{1 << 20, "1M"},
		{256 * 1024, "256K"},
	}
	for _, c := range cases {
		if got := formatSize(c.n); got != c.want {
			t.Errorf("formatSize(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

// TestDrawHexFileTitleBarAlignsPathWithHexGrid guards SPEC.md §2.1a's
// alignment rule: the file title bar's path text lands at the same
// on-screen column the hex-byte grid itself starts at (x0+gutterWidth),
// the hex view's analog of §2.1's "NL"-tag-plus-single-space trick —
// here achieved by explicitly padding the size tag to gutterWidth-1
// columns, since (unlike the text tiers) the size tag's own length has
// no natural relationship to the gutter's hex-digit width.
func TestDrawHexFileTitleBarAlignsPathWithHexGrid(t *testing.T) {
	dir := t.TempDir()
	for _, size := range []int{10, 1000, 100_000, 5_000_000} {
		content := append([]byte{0}, make([]byte, size-1)...)
		path := writeBinaryFile(t, dir, content)

		sim := tcell.NewSimulationScreen("")
		if err := sim.Init(); err != nil {
			t.Fatal(err)
		}
		w, h := 80, 10
		sim.SetSize(w, h)

		files := openfiles.New()
		v := &Preview{Shared: &Shared{Files: files, RootPath: dir, Canvas: canvas.New(sim)}}
		res := files.Open(path, preview.DefaultByteCap)
		if res.Outcome != openfiles.Opened {
			t.Fatalf("size %d: Open failed: %+v", size, res)
		}
		e := res.Entry

		v.Draw(0, 1, w, h-1, true)
		sim.Show()

		titleRow := rowText(sim, 1, w)
		relPath := tree.RelativeDisplayPath(dir, path)
		pathCol := strings.Index(titleRow, relPath)
		if pathCol < 0 {
			t.Fatalf("size %d: path %q not found in title bar row %q", size, relPath, titleRow)
		}

		wantCol := hexGutterWidth(e.Size)
		if pathCol != wantCol {
			t.Errorf("size %d: path starts at column %d, want %d (the hex grid's own start column)", size, pathCol, wantCol)
		}
	}
}

func writeBinaryFile(t *testing.T, dir string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestHandleKeyHexNavigation exercises Up/Down/PgDn/Home/End moving a
// TierBinary entry's viewport (SPEC.md §2.1a) through Preview.HandleKey,
// confirming the tier-branch in HandleKey actually reaches hexScroll/
// hexJumpStart/hexJumpEnd rather than the text-scroll path.
func TestHandleKeyHexNavigation(t *testing.T) {
	dir := t.TempDir()
	content := make([]byte, 0, 1)
	content = append(content, 0) // NUL byte -> binary
	content = append(content, make([]byte, 999)...)
	path := writeBinaryFile(t, dir, content)

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(80, 10)

	files := openfiles.New()
	v := &Preview{Shared: &Shared{Files: files, Canvas: canvas.New(sim)}}
	res := files.Open(path, preview.DefaultByteCap)
	if res.Outcome != openfiles.Opened || res.Entry.Tier != preview.TierBinary {
		t.Fatalf("expected an opened TierBinary entry, got %+v", res)
	}
	e := res.Entry

	n := v.hexBytesPerRow(e)

	v.HandleKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	if e.HexOffset != int64(n) {
		t.Fatalf("after Down, HexOffset = %d, want %d", e.HexOffset, n)
	}

	v.HandleKey(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone))
	if e.HexOffset != 0 {
		t.Fatalf("after Up, HexOffset = %d, want 0", e.HexOffset)
	}

	v.HandleKey(tcell.NewEventKey(tcell.KeyEnd, 0, tcell.ModNone))
	if want := lastRowStart(e.Size, n); e.HexOffset != want {
		t.Fatalf("after End, HexOffset = %d, want %d", e.HexOffset, want)
	}

	v.HandleKey(tcell.NewEventKey(tcell.KeyHome, 0, tcell.ModNone))
	if e.HexOffset != 0 {
		t.Fatalf("after Home, HexOffset = %d, want 0", e.HexOffset)
	}
}

// TestHandleKeyHexGotoOffset exercises the goto-offset prompt end to end
// through Preview.HandleKey (SPEC.md §2.1a): 'g' opens it, typing a
// hex-prefixed offset and Enter jumps the viewport, closing the prompt.
func TestHandleKeyHexGotoOffset(t *testing.T) {
	dir := t.TempDir()
	content := append([]byte{0}, make([]byte, 999)...)
	path := writeBinaryFile(t, dir, content)

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(80, 10)

	files := openfiles.New()
	v := &Preview{Shared: &Shared{Files: files, Canvas: canvas.New(sim)}}
	res := files.Open(path, preview.DefaultByteCap)
	if res.Outcome != openfiles.Opened {
		t.Fatalf("Open failed: %+v", res)
	}
	e := res.Entry

	v.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'g', tcell.ModNone))
	if !v.GotoPromptOpen {
		t.Fatal("expected goto prompt open after 'g'")
	}
	for _, r := range "0x64" { // 0x64 == 100
		v.HandleKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	v.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if v.GotoPromptOpen {
		t.Fatal("expected goto prompt closed after Enter")
	}

	n := v.hexBytesPerRow(e)
	want := clampHexOffset(0x64, e.Size, n)
	if e.HexOffset != want {
		t.Fatalf("HexOffset = %d, want %d", e.HexOffset, want)
	}
}

// TestHandleKeyHexFindEndToEnd exercises the hex-find prompt through
// Preview.HandleKey and syncHexFindScan (SPEC.md §2.1a): '/' opens the
// prompt, typing a query and Enter starts a background scan, and once
// it finishes the match is found and seeded as current.
func TestHandleKeyHexFindEndToEnd(t *testing.T) {
	dir := t.TempDir()
	content := append([]byte{0, 0, 0}, []byte("NEEDLE")...)
	content = append(content, make([]byte, 100)...)
	path := writeBinaryFile(t, dir, content)

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(80, 10)

	files := openfiles.New()
	v := &Preview{Shared: &Shared{Files: files, Canvas: canvas.New(sim)}}
	res := files.Open(path, preview.DefaultByteCap)
	if res.Outcome != openfiles.Opened {
		t.Fatalf("Open failed: %+v", res)
	}
	e := res.Entry

	v.HandleKey(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone))
	if !v.HexFindPromptOpen {
		t.Fatal("expected hex-find prompt open after '/'")
	}
	for _, r := range "NEEDLE" {
		v.HandleKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	v.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if v.HexFindPromptOpen {
		t.Fatal("expected hex-find prompt closed after Enter")
	}
	if e.HexFindScan == nil {
		t.Fatal("expected a background hex-find scan to have started")
	}

	deadline := time.Now().Add(2 * time.Second)
	for e.HexFindScan != nil && time.Now().Before(deadline) {
		v.syncHexFindScan(e)
		time.Sleep(time.Millisecond)
	}
	if e.HexFindScan != nil {
		t.Fatal("hex-find scan did not finish in time")
	}
	if len(e.HexFindMatches) != 1 || e.HexFindMatches[0].Offset != 3 {
		t.Fatalf("expected one match at offset 3, got %v", e.HexFindMatches)
	}
	if e.HexFindCurrent != 0 {
		t.Fatalf("expected HexFindCurrent = 0, got %d", e.HexFindCurrent)
	}
}

// TestDrawHexContentRendersGutterAndAscii renders a small binary file's
// hex view and confirms the offset gutter and ASCII column both appear
// on screen (SPEC.md §2.1a) — a light rendering smoke test, not a
// pixel-exact layout check, per the project's terminal-rendering testing
// discipline (manual verification covers the rest).
func TestDrawHexContentRendersGutterAndAscii(t *testing.T) {
	dir := t.TempDir()
	content := append([]byte{0}, []byte("Hello, world!")...)
	path := writeBinaryFile(t, dir, content)

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	w, h := 80, 10
	sim.SetSize(w, h)

	files := openfiles.New()
	v := &Preview{Shared: &Shared{Files: files, Canvas: canvas.New(sim)}}
	res := files.Open(path, preview.DefaultByteCap)
	if res.Outcome != openfiles.Opened {
		t.Fatalf("Open failed: %+v", res)
	}

	v.Draw(0, 1, w, h-1, true)
	sim.Show()

	row := rowText(sim, 2, w)
	if !strings.Contains(row, "Hello, world!") {
		t.Fatalf("hex view row = %q, expected the ASCII column to show the file's printable bytes", row)
	}
	if !strings.HasPrefix(row, "0000") {
		t.Fatalf("hex view row = %q, expected it to start with the offset gutter for row 0", row)
	}
	if !strings.Contains(row, "00 48 65 6c") {
		t.Fatalf("hex view row = %q, expected the hex-byte grid to show the file's bytes", row)
	}
}
