package views

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/entry"
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

		mainc, _, _ := sim.Get(w-1, 2)
		if mainc == "" || mainc == " " {
			t.Errorf("width %d: rightmost cell of the first content row is blank, want the ASCII column flush to the right edge", w)
		}
	}
}

// TestClampHexOffsetBounds guards SPEC.md §2.1a's viewport clamping: an
// offset is clamped to [0, hexMaxOffset], never negative, never past it,
// and always itself a row-boundary multiple of bytesPerRow. A
// viewportHeight of 1 makes hexMaxOffset equal to the file's raw last
// row start (below), isolating this test from the viewport-filling
// behavior TestHexMaxOffsetFillsViewport covers separately.
func TestClampHexOffsetBounds(t *testing.T) {
	const bytesPerRow = 16
	const size = 100 // last row starts at 96 (bytes 96-99)
	const viewportHeight = 1

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
		if got := clampHexOffset(c.offset, size, bytesPerRow, viewportHeight); got != c.want {
			t.Errorf("%s: clampHexOffset(%d, %d, %d, %d) = %d, want %d", c.name, c.offset, size, bytesPerRow, viewportHeight, got, c.want)
		}
	}
}

// TestClampHexOffsetEmptyFile guards the size<=0 edge case: everything
// clamps to offset 0 rather than producing a negative or nonsensical
// last-row bound.
func TestClampHexOffsetEmptyFile(t *testing.T) {
	if got := clampHexOffset(10, 0, 16, 1); got != 0 {
		t.Fatalf("clampHexOffset on an empty file = %d, want 0", got)
	}
}

// TestHexMaxOffsetFillsViewport guards SPEC.md §2.1a's viewport-filling
// rule (the hex view's analog of the text tiers' own maxScroll): once a
// file has more rows than the viewport is tall, the largest valid
// offset pulls back from the file's raw last row start by enough rows
// to keep a full viewport of content on screen, with the last row
// landing at the bottom rather than leaving most of the screen blank
// beneath a single lone row. A viewport taller than the file's total
// rows (or exactly one row tall) falls back to the raw last row start
// directly, since there's nothing to fill the rest of the screen with
// either way.
func TestHexMaxOffsetFillsViewport(t *testing.T) {
	const bytesPerRow = 16
	const size = 1000 // last row starts at 992 (63 rows total, 0-62)

	cases := []struct {
		name           string
		viewportHeight int
		want           int64
	}{
		{"one-row viewport: raw last row start", 1, 992},
		{"taller than the file has rows: whole file fits, offset 0", 100, 0},
		{"exactly as tall as the file has rows: whole file fits, offset 0", 63, 0},
		{"shorter than the file: pulls back to keep the tail flush to the bottom", 10, 992 - 9*bytesPerRow},
	}
	for _, c := range cases {
		if got := hexMaxOffset(size, bytesPerRow, c.viewportHeight); got != c.want {
			t.Errorf("%s: hexMaxOffset(%d, %d, %d) = %d, want %d", c.name, size, bytesPerRow, c.viewportHeight, got, c.want)
		}
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

// TestParseOffsetAlwaysHex guards goto-offset's input parsing (SPEC.md
// §2.1a): the input is always interpreted as hexadecimal — there's no
// decimal fallback and no "0x" prefix to type or parse, since the
// prompt shows that prefix itself as a fixed label — and malformed
// input is rejected rather than guessed at.
func TestParseOffsetAlwaysHex(t *testing.T) {
	cases := []struct {
		input  string
		want   int64
		wantOK bool
	}{
		{"1a", 0x1a, true},
		{"1A", 0x1A, true},
		{"26", 0x26, true}, // hex, not decimal: 0x26 == 38
		{"0", 0, true},
		{"", 0, false},
		{"-5", 0, false},
		{"0x1a", 0, false}, // "x" isn't a hex digit; isHexOffsetRune never admits it either
		{"zz", 0, false},
	}
	for _, c := range cases {
		got, ok := parseOffset(c.input)
		if ok != c.wantOK || (ok && got != c.want) {
			t.Errorf("parseOffset(%q) = (%d, %v), want (%d, %v)", c.input, got, ok, c.want, c.wantOK)
		}
	}
}

// TestIsHexOffsetRuneOnlyAcceptsHexDigits guards the goto-offset
// prompt's input filter (SPEC.md §2.1a): only 0-9/a-f/A-F are
// accepted — no "x", since the prompt's "0x" is a fixed label the user
// never types, and no other punctuation.
func TestIsHexOffsetRuneOnlyAcceptsHexDigits(t *testing.T) {
	for _, r := range "0123456789abcdefABCDEF" {
		if !isHexOffsetRune(r) {
			t.Errorf("isHexOffsetRune(%q) = false, want true", r)
		}
	}
	for _, r := range "xXgG .-+\n" {
		if isHexOffsetRune(r) {
			t.Errorf("isHexOffsetRune(%q) = true, want false", r)
		}
	}
}

// TestFormatSize guards the file title bar's size tag (SPEC.md §2.1a):
// a plain integer under 1024 bytes (already exact, nothing more precise
// to show), otherwise an integer plus a single unit letter, spending
// whatever width is left on decimal precision rather than settling for
// a fixed shape — never wider than width, and as precise as width
// allows.
func TestFormatSize(t *testing.T) {
	cases := []struct {
		n     int64
		width int
		want  string
	}{
		{0, 5, "0"},
		{1023, 5, "1023"},
		{1024, 5, "1.00K"},
		{1024, 6, "1.000K"},
		{256 * 1024, 5, "256K"}, // no room left for even one decimal place
		{256 * 1024, 7, "256.00K"},
		{1 << 20, 5, "1.00M"},
	}
	for _, c := range cases {
		if got := formatSize(c.n, c.width); got != c.want {
			t.Errorf("formatSize(%d, %d) = %q, want %q", c.n, c.width, got, c.want)
		}
	}
}

// TestFormatSizeNeverExceedsWidth is a property check across a spread
// of sizes and widths: whatever precision formatSize picks, the result
// never runs past the width budget it was given (SPEC.md §2.1a's "the
// size indicator should always be sized to fit within the gutter").
// Widths below 5 aren't exercised here — hexGutterWidth's own 4-hex-
// digit floor means hexFileView.DrawTitleBar never actually offers
// formatSize a budget narrower than that in practice.
func TestFormatSizeNeverExceedsWidth(t *testing.T) {
	sizes := []int64{1, 1023, 1024, 1025, 1536, 999_999, 1 << 20, 1 << 30, 1<<40 + 12345}
	for _, n := range sizes {
		for width := 5; width <= 16; width++ {
			if got := formatSize(n, width); len(got) > width {
				t.Errorf("formatSize(%d, %d) = %q (len %d), exceeds width %d", n, width, got, len(got), width)
			}
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
		he := res.Entry.(*entry.HexEntry)

		v.Draw(0, 1, w, h-1, true)
		sim.Show()

		titleRow := rowText(sim, 1, w)
		relPath := tree.RelativeDisplayPath(dir, path)
		pathCol := strings.Index(titleRow, relPath)
		if pathCol < 0 {
			t.Fatalf("size %d: path %q not found in title bar row %q", size, relPath, titleRow)
		}

		wantCol := hexGutterWidth(he.Size)
		if pathCol != wantCol {
			t.Errorf("size %d: path starts at column %d, want %d (the hex grid's own start column)", size, pathCol, wantCol)
		}
	}
}

// TestDrawHexFileTitleBarShowsGotoPrompt guards #114's title-bar
// placement fix for the hex view: the goto-offset prompt renders in the
// file title bar (same as hex-find) instead of its own row at the
// bottom of the content area, and shows the file's valid hex offset
// range alongside the typed input while typing.
func TestDrawHexFileTitleBarShowsGotoPrompt(t *testing.T) {
	dir := t.TempDir()
	content := append([]byte{0}, make([]byte, 999)...) // size 1000, last valid offset 0x3e7
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
	e := res.Entry
	v.GotoPromptOpen = true
	v.GotoInput = "64"

	hexFileView{}.DrawTitleBar(v, e, 0, 0, w, true)
	sim.Show()

	row := rowText(sim, 0, w)
	if !strings.HasPrefix(row, "goto offset: 0x64 (0-3e7)") {
		t.Fatalf("file title bar row = %q, want it to start with the prompt text and range hint", row)
	}
	for _, want := range []string{"[return] jump", "[esc] cancel"} {
		if !strings.Contains(row, want) {
			t.Errorf("file title bar row = %q, missing legend entry %q", row, want)
		}
	}
}

// TestHexScrollBumpsAtRestEdges guards the hex view's edge-bump cue
// (SPEC.md §2.1a), mirroring TestScrollBumpsAtRestEdges (preview_test.go)
// for the text tiers: scrolling further in a direction that's already
// fully clamped records a flash for that edge (reusing bumpEdge and the
// same TopBumpPath/BottomBumpPath fields, since neither is tier-specific),
// but ordinary in-bounds scrolling — and the scroll that merely reaches
// an edge for the first time, rather than pushing past an already-at-
// rest one — does not.
func TestHexScrollBumpsAtRestEdges(t *testing.T) {
	dir := t.TempDir()
	content := append([]byte{0}, make([]byte, 999)...)
	path := writeBinaryFile(t, dir, content)

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(60, 10)

	files := openfiles.New()
	v := &Preview{Shared: &Shared{Files: files, Canvas: canvas.New(sim)}}
	res := files.Open(path, preview.DefaultByteCap)
	if res.Outcome != openfiles.Opened {
		t.Fatalf("Open failed: %+v", res)
	}
	e := res.Entry
	he := e.(*entry.HexEntry)

	hexFileView{}.Scroll(v, e, -1) // already at the top: pushing further up bumps
	if v.TopBumpPath != he.Path() {
		t.Fatalf("expected top bump after scrolling up from the top, got TopBumpPath=%q", v.TopBumpPath)
	}
	if v.BottomBumpPath != "" {
		t.Fatalf("expected no bottom bump yet, got BottomBumpPath=%q", v.BottomBumpPath)
	}

	v.TopBumpPath = ""
	hexFileView{}.Scroll(v, e, 1) // ordinary downward scroll within bounds: no bump
	if v.TopBumpPath != "" || v.BottomBumpPath != "" {
		t.Fatalf("expected no bump from an ordinary in-bounds scroll, got TopBumpPath=%q BottomBumpPath=%q", v.TopBumpPath, v.BottomBumpPath)
	}

	he.HexOffset = 0
	v.BottomBumpPath = ""
	hexFileView{}.Scroll(v, e, 1000) // scroll far past the bottom: first landing on the last row doesn't bump
	if v.BottomBumpPath != "" {
		t.Fatalf("expected no bottom bump on first reaching the last row, got BottomBumpPath=%q", v.BottomBumpPath)
	}
	hexFileView{}.Scroll(v, e, 1) // already at the bottom: pushing further down bumps
	if v.BottomBumpPath != he.Path() {
		t.Fatalf("expected bottom bump after scrolling down from the bottom, got BottomBumpPath=%q", v.BottomBumpPath)
	}
}

// TestDrawHexContentFlashesEdgeRowOnBump guards the actual visual cue,
// mirroring TestDrawContentFlashesEdgeRowOnBump (preview_test.go): once a
// bump is recorded, drawHexContent reverse-video flashes the
// corresponding edge row (canvas.FlashRow) for the currently-displayed
// entry.
func TestDrawHexContentFlashesEdgeRowOnBump(t *testing.T) {
	dir := t.TempDir()
	content := append([]byte{0}, make([]byte, 999)...)
	path := writeBinaryFile(t, dir, content)

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	w, h := 60, 5
	sim.SetSize(w, h)

	files := openfiles.New()
	v := &Preview{Shared: &Shared{Files: files, Canvas: canvas.New(sim)}}
	res := files.Open(path, preview.DefaultByteCap)
	if res.Outcome != openfiles.Opened {
		t.Fatalf("Open failed: %+v", res)
	}
	e := res.Entry

	hexFileView{}.DrawContent(v, e, 0, 0, w, h)
	hexFileView{}.Scroll(v, e, -1) // already at the top: bumps
	hexFileView{}.DrawContent(v, e, 0, 0, w, h)
	sim.Show()

	_, _, attr := cellStyle(sim, 0, 0).Decompose()
	if attr&tcell.AttrReverse == 0 {
		t.Fatalf("expected the top row to be reverse-video flashed after a top bump")
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
	he, ok := res.Entry.(*entry.HexEntry)
	if res.Outcome != openfiles.Opened || !ok {
		t.Fatalf("expected an opened TierBinary entry, got %+v", res)
	}

	n := v.hexBytesPerRow(he)

	v.HandleKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	if he.HexOffset != int64(n) {
		t.Fatalf("after Down, HexOffset = %d, want %d", he.HexOffset, n)
	}

	v.HandleKey(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone))
	if he.HexOffset != 0 {
		t.Fatalf("after Up, HexOffset = %d, want 0", he.HexOffset)
	}

	v.HandleKey(tcell.NewEventKey(tcell.KeyEnd, 0, tcell.ModNone))
	if want := hexMaxOffset(he.Size, n, v.viewportHeight()); he.HexOffset != want {
		t.Fatalf("after End, HexOffset = %d, want %d", he.HexOffset, want)
	}

	v.HandleKey(tcell.NewEventKey(tcell.KeyHome, 0, tcell.ModNone))
	if he.HexOffset != 0 {
		t.Fatalf("after Home, HexOffset = %d, want 0", he.HexOffset)
	}
}

// TestHandleKeyHexGotoOffset exercises the goto-offset prompt end to end
// through Preview.HandleKey (SPEC.md §2.1a): 'g' opens it, typing hex
// digits (no "0x" — the prompt supplies that itself) and Enter jumps
// the viewport, interpreting the input as hexadecimal, and closes the
// prompt.
// TestHandleKeyCIsNoOpOnHexTier guards SPEC.md §2.1a: copy mode does
// not apply to a hex view, so pressing 'c' while one is displayed has
// no effect — now dispatched through fileView.ToggleCopyMode
// (hexFileView's own implementation is a genuine no-op) rather than an
// inline isHex check, and must not panic reaching for a CopyMode field
// HexState doesn't have.
func TestHandleKeyCIsNoOpOnHexTier(t *testing.T) {
	dir := t.TempDir()
	content := append([]byte{0}, make([]byte, 99)...)
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

	v.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModNone))
}

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
	he := res.Entry.(*entry.HexEntry)

	v.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'g', tcell.ModNone))
	if !v.GotoPromptOpen {
		t.Fatal("expected goto prompt open after 'g'")
	}
	for _, r := range "64" { // hex 64 == decimal 100
		v.HandleKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	v.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if v.GotoPromptOpen {
		t.Fatal("expected goto prompt closed after Enter")
	}

	n := v.hexBytesPerRow(he)
	want := clampHexOffset(0x64, he.Size, n, v.viewportHeight())
	if he.HexOffset != want {
		t.Fatalf("HexOffset = %d, want %d", he.HexOffset, want)
	}
}

// TestHandleKeyHexFindEndToEnd exercises the hex-find prompt through
// Preview.HandleKey and hexFileView.SyncFindScan (SPEC.md §2.1a): '/'
// opens the prompt, typing a query and Enter starts a background scan,
// and once it finishes the match is found and seeded as current.
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
	he := e.(*entry.HexEntry)

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
	if he.HexFindScan == nil {
		t.Fatal("expected a background hex-find scan to have started")
	}

	deadline := time.Now().Add(2 * time.Second)
	for he.HexFindScan != nil && time.Now().Before(deadline) {
		hexFileView{}.SyncFindScan(v, e)
		time.Sleep(time.Millisecond)
	}
	if he.HexFindScan != nil {
		t.Fatal("hex-find scan did not finish in time")
	}
	if len(he.HexFindMatches) != 1 || he.HexFindMatches[0].Offset != 3 {
		t.Fatalf("expected one match at offset 3, got %v", he.HexFindMatches)
	}
	if he.HexFindCurrent != 0 {
		t.Fatalf("expected HexFindCurrent = 0, got %d", he.HexFindCurrent)
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
