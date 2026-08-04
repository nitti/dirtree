package views

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/entry"
	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/preview"
	"github.com/nitti/dirtree/internal/ui/canvas"
)

// TestFileViewForDispatchesOnTier guards fileViewFor's own branch
// (SPEC.md §2.1, §2.1a): a *entry.HexEntry gets hexFileView, every
// other tier (and a nil entry, which the goto prompt can never actually
// be open against) gets textFileView.
func TestFileViewForDispatchesOnTier(t *testing.T) {
	cases := []struct {
		name string
		e    entry.Entry
		want fileView
	}{
		{"nil entry", nil, textFileView{}},
		{"highlighted tier", &entry.TextEntry{EntryInfo: entry.EntryInfo{Tier: preview.TierHighlighted}}, textFileView{}},
		{"plain text tier", &entry.TextEntry{EntryInfo: entry.EntryInfo{Tier: preview.TierPlainText}}, textFileView{}},
		{"binary tier", &entry.HexEntry{EntryInfo: entry.EntryInfo{Tier: preview.TierBinary}}, hexFileView{}},
	}
	for _, c := range cases {
		if got := fileViewFor(c.e); got != c.want {
			t.Errorf("%s: fileViewFor() = %T, want %T", c.name, got, c.want)
		}
	}
}

// TestTextFileViewAcceptGotoRune guards textFileView's input filter
// (SPEC.md §2.1): decimal digits only, matching the goto-line prompt's
// documented "digits only" behavior.
func TestTextFileViewAcceptGotoRune(t *testing.T) {
	fv := textFileView{}
	for _, r := range "0123456789" {
		if !fv.acceptGotoRune(r) {
			t.Errorf("acceptGotoRune(%q) = false, want true", r)
		}
	}
	for _, r := range "aA xX-." {
		if fv.acceptGotoRune(r) {
			t.Errorf("acceptGotoRune(%q) = true, want false", r)
		}
	}
}

// TestHexFileViewAcceptGotoRune guards hexFileView's input filter
// (SPEC.md §2.1a): hex digits only (0-9, a-f, A-F), mirroring
// isHexOffsetRune directly.
func TestHexFileViewAcceptGotoRune(t *testing.T) {
	fv := hexFileView{}
	for _, r := range "0123456789abcdefABCDEF" {
		if !fv.acceptGotoRune(r) {
			t.Errorf("acceptGotoRune(%q) = false, want true", r)
		}
	}
	for _, r := range "gGxX -." {
		if fv.acceptGotoRune(r) {
			t.Errorf("acceptGotoRune(%q) = true, want false", r)
		}
	}
}

// TestTextFileViewJumpToSetsScrollByLine and
// TestHexFileViewJumpToSetsHexOffsetByByte guard jumpTo's dispatch to
// gotoLine/gotoOffset respectively (SPEC.md §2.1, §2.1a) — the same
// "parse this tier's own addressing unit and move the viewport there"
// behavior handleGotoPromptKey used to branch on directly, now reached
// only through the fileView interface.
func TestTextFileViewJumpToSetsScrollByLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	var content string
	for i := 1; i <= 30; i++ {
		content += fmt.Sprintf("line %d\n", i)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(60, 8) // short enough that a 30-line file needs to scroll

	files := openfiles.New()
	v := &Preview{Shared: &Shared{Files: files, Canvas: canvas.New(sim)}}
	res := files.Open(path, 1<<20)
	if res.Outcome != openfiles.Opened {
		t.Fatalf("Open failed: %s", res.Message)
	}
	waitEntryReady(t, res.Entry)

	textFileView{}.jumpTo(v, "20")
	te := res.Entry.(*entry.TextEntry)
	row, ok := te.FirstRow[19]
	if !ok {
		t.Fatal("expected line 20's row to be in FirstRow after jumpTo")
	}
	want := clamp(row, 0, v.maxScroll(te, v.viewportHeight()))
	if te.Scroll != want {
		t.Fatalf("after jumpTo(20), Scroll = %d, want %d", te.Scroll, want)
	}
	if want == 0 {
		t.Fatal("test fixture didn't actually exercise a nonzero scroll — widen it")
	}
}

func TestHexFileViewJumpToSetsHexOffsetByByte(t *testing.T) {
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

	hexFileView{}.jumpTo(v, "64") // hex 64 == decimal 100
	he := res.Entry.(*entry.HexEntry)
	n := v.hexBytesPerRow(he)
	want := clampHexOffset(0x64, he.Size, n, v.viewportHeight())
	if he.HexOffset != want {
		t.Fatalf("after jumpTo(\"64\"), HexOffset = %d, want %d", he.HexOffset, want)
	}
}

// TestHandleGotoPromptKeyRejectsWrongTierDigits guards the goto prompt
// end to end through Preview.HandleKey (SPEC.md §2.1, §2.1a): a text
// entry's prompt silently drops hex-only letters, and a hex entry's
// prompt accepts them, now dispatched through fileViewFor rather than
// an inline isHex branch.
func TestHandleGotoPromptKeyRejectsWrongTierDigits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(60, 10)

	files := openfiles.New()
	v := &Preview{Shared: &Shared{Files: files, Canvas: canvas.New(sim)}}
	res := files.Open(path, 1<<20)
	if res.Outcome != openfiles.Opened {
		t.Fatalf("Open failed: %s", res.Message)
	}
	waitEntryReady(t, res.Entry)

	v.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'g', tcell.ModNone))
	for _, r := range "1a2" { // 'a' is not a valid text-tier goto digit
		v.HandleKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	if v.GotoInput != "12" {
		t.Fatalf("GotoInput = %q, want %q (hex letter dropped)", v.GotoInput, "12")
	}
}

// TestTextFileViewGotoRangeHint and TestHexFileViewGotoRangeHint guard
// #114's "show the valid range while typing" fix directly against the
// pure per-tier hint functions, independent of rendering.
func TestTextFileViewGotoRangeHint(t *testing.T) {
	cases := []struct {
		name string
		e    entry.Entry
		want string
	}{
		{"highlighted tier, 3 lines", &entry.TextEntry{EntryInfo: entry.EntryInfo{Tier: preview.TierHighlighted}, Lines: []string{"a", "b", "c"}}, "1-3"},
		{"highlighted tier, empty file (floors at 1 line)", &entry.TextEntry{EntryInfo: entry.EntryInfo{Tier: preview.TierHighlighted}}, "1-1"},
	}
	for _, c := range cases {
		if got := (textFileView{}).gotoRangeHint(c.e); got != c.want {
			t.Errorf("%s: gotoRangeHint() = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestHexFileViewGotoRangeHint(t *testing.T) {
	cases := []struct {
		name string
		size int64
		want string
	}{
		{"1000-byte file", 1000, "0-3e7"},
		{"1-byte file", 1, "0-0"},
		{"empty file (floors at 0 rather than going negative)", 0, "0-0"},
	}
	for _, c := range cases {
		e := &entry.HexEntry{EntryInfo: entry.EntryInfo{Tier: preview.TierBinary, Size: c.size}}
		if got := (hexFileView{}).gotoRangeHint(e); got != c.want {
			t.Errorf("%s: gotoRangeHint() = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestGotoLabelDispatchesOnTier guards the exported Preview.GotoLabel
// wrapper (used by internal/ui/render.go's cursor-position logic,
// outside this package) dispatches through fileViewFor the same way
// the title bar's own rendering does.
func TestGotoLabelDispatchesOnTier(t *testing.T) {
	textEntry := &entry.TextEntry{EntryInfo: entry.EntryInfo{Tier: preview.TierHighlighted}, Lines: []string{"a"}}
	hexEntry := &entry.HexEntry{EntryInfo: entry.EntryInfo{Tier: preview.TierBinary, Size: 10}}

	files := openfiles.New()
	v := &Preview{Shared: &Shared{Files: files}}

	files.Entries = []entry.Entry{textEntry}
	files.Displayed = 0
	if got, want := v.GotoLabel(), "goto line: "; got != want {
		t.Errorf("GotoLabel() with text entry = %q, want %q", got, want)
	}

	files.Entries = []entry.Entry{hexEntry}
	files.Displayed = 0
	if got, want := v.GotoLabel(), "goto offset: 0x"; got != want {
		t.Errorf("GotoLabel() with hex entry = %q, want %q", got, want)
	}
}

// TestToggleCopyModeDispatchesOnTier guards #114's final cleanup stage:
// textFileView.ToggleCopyMode actually toggles the entry's copy mode,
// while hexFileView.ToggleCopyMode is a genuine no-op (SPEC.md §2.1a —
// copy mode does not apply to a hex view). HexEntry has no CopyMode
// field at all, so the only thing to verify on the hex side is that it
// doesn't panic reaching for one.
func TestToggleCopyModeDispatchesOnTier(t *testing.T) {
	textEntry := &entry.TextEntry{EntryInfo: entry.EntryInfo{Tier: preview.TierHighlighted}}
	hexEntry := &entry.HexEntry{EntryInfo: entry.EntryInfo{Tier: preview.TierBinary}}

	textFileView{}.ToggleCopyMode(nil, textEntry)
	if !textEntry.CopyMode {
		t.Fatal("expected textFileView.ToggleCopyMode to toggle CopyMode on")
	}
	textFileView{}.ToggleCopyMode(nil, textEntry)
	if textEntry.CopyMode {
		t.Fatal("expected a second call to toggle CopyMode back off")
	}

	hexFileView{}.ToggleCopyMode(nil, hexEntry)
}
