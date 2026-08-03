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
	"github.com/nitti/dirtree/internal/ui/canvas"
)

// TestBytesPerRowForAdaptsToWidth guards SPEC.md §2.1a's "bytesPerRow is
// a derived quantity from the available width" rule: it never returns a
// value whose row wouldn't fit, and narrower widths never return more
// bytes per row than wider ones (monotonic, not just individually
// in-bounds).
func TestBytesPerRowForAdaptsToWidth(t *testing.T) {
	gutterWidth := 10
	prev := 0
	for _, w := range []int{20, 40, 60, 80, 120, 200} {
		n := bytesPerRowFor(w, gutterWidth)
		if n < 1 {
			t.Fatalf("width %d: bytesPerRowFor returned %d, want >= 1", w, n)
		}
		if hexRowWidth(gutterWidth, n) > w {
			t.Errorf("width %d: bytesPerRowFor returned %d, whose row width %d exceeds it", w, n, hexRowWidth(gutterWidth, n))
		}
		if n < prev {
			t.Errorf("width %d: bytesPerRowFor returned %d, less than a narrower width's %d", w, n, prev)
		}
		prev = n
	}
}

// TestBytesPerRowForNeverBelowOne guards the "remains navigable even at
// a width too narrow to comfortably fit anything" fallback (SPEC.md
// §2.1a).
func TestBytesPerRowForNeverBelowOne(t *testing.T) {
	if got := bytesPerRowFor(1, 10); got != 1 {
		t.Fatalf("bytesPerRowFor(1, 10) = %d, want 1", got)
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
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1 << 20, "1.0 MB"},
	}
	for _, c := range cases {
		if got := formatSize(c.n); got != c.want {
			t.Errorf("formatSize(%d) = %q, want %q", c.n, got, c.want)
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
