package entry

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nitti/dirtree/internal/find"
	"github.com/nitti/dirtree/internal/preview"
)

func writeFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// rewriteWithNewerMtime overwrites path's content and forces its mtime
// to be strictly after whatever it was before, sidestepping any
// filesystem's mtime resolution (e.g. 1s on some setups) so tests never
// flake on write speed.
func rewriteWithNewerMtime(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
}

// --- Open-failure detection at open time / tier decisions (TESTING.md
// "Open-failure detection at open time (§2.2)") ---

func TestOpenBinaryFileOpensAsTierBinary(t *testing.T) {
	dir := t.TempDir()
	content := []byte("abc\x00def")
	path := writeFile(t, dir, "bin", content)

	e, err := Open(path, preview.DefaultByteCap)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	he, ok := e.(*HexEntry)
	if !ok {
		t.Fatalf("expected *HexEntry, got %T", e)
	}
	if he.Tier != preview.TierBinary {
		t.Fatalf("expected TierBinary, got %v", he.Tier)
	}
	if he.Size != int64(len(content)) {
		t.Fatalf("expected Size %d, got %d", len(content), he.Size)
	}
	if he.HexFindCurrent != -1 {
		t.Fatalf("expected HexFindCurrent initialized to -1, got %d", he.HexFindCurrent)
	}
}

func TestOpenStartsBackgroundStreamForNewEntry(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "ok.txt", []byte("hello\n"))

	e, err := Open(path, preview.DefaultByteCap)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if e.(*TextEntry).Stream == nil {
		t.Fatal("expected a background stream to be started for a newly-opened entry")
	}
}

func TestOpenAtOrUnderCeilingIsTierHighlighted(t *testing.T) {
	orig := preview.HighlightCeiling
	defer func() { preview.HighlightCeiling = orig }()
	preview.HighlightCeiling = 100

	dir := t.TempDir()
	path := writeFile(t, dir, "small.txt", []byte("hello\n"))

	e, err := Open(path, preview.DefaultByteCap)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if got := e.(*TextEntry).Tier; got != preview.TierHighlighted {
		t.Fatalf("expected TierHighlighted for a file under the ceiling, got %v", got)
	}
}

func TestOpenOverCeilingIsTierPlainText(t *testing.T) {
	orig := preview.HighlightCeiling
	defer func() { preview.HighlightCeiling = orig }()
	preview.HighlightCeiling = 4

	dir := t.TempDir()
	path := writeFile(t, dir, "big.txt", []byte("hello\n"))

	e, err := Open(path, preview.DefaultByteCap)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if got := e.(*TextEntry).Tier; got != preview.TierPlainText {
		t.Fatalf("expected TierPlainText for a file over the ceiling, got %v", got)
	}
}

// --- Reload's tier-deciding/invalidation behavior (TESTING.md "Live
// reload of open files (§6.1a)") ---

func TestReloadPromotesEntryWhenShrunkUnderCeiling(t *testing.T) {
	orig := preview.HighlightCeiling
	defer func() { preview.HighlightCeiling = orig }()
	preview.HighlightCeiling = 4

	dir := t.TempDir()
	// Opened over the ceiling (TierPlainText)...
	path := writeFile(t, dir, "a.txt", []byte("hello\n"))
	e, err := Open(path, preview.DefaultByteCap)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if got := e.(*TextEntry).Tier; got != preview.TierPlainText {
		t.Fatalf("expected TierPlainText at open, got %v", got)
	}

	// ...then rewritten small enough to now be under the ceiling.
	rewriteWithNewerMtime(t, path, []byte("hi\n"))
	updated, changed, err := Reload(e, preview.DefaultByteCap)
	if err != nil || !changed {
		t.Fatalf("expected reload to report changed, got changed=%v err=%v", changed, err)
	}
	if got := updated.(*TextEntry).Tier; got != preview.TierHighlighted {
		t.Fatalf("expected reload to re-decide tier from the new size (promotion to TierHighlighted), got %v", got)
	}
}

func TestReloadDemotesEntryWhenGrownOverCeiling(t *testing.T) {
	orig := preview.HighlightCeiling
	defer func() { preview.HighlightCeiling = orig }()
	preview.HighlightCeiling = 4

	dir := t.TempDir()
	// Opened at/under the ceiling (TierHighlighted)...
	path := writeFile(t, dir, "a.txt", []byte("hi\n"))
	e, err := Open(path, preview.DefaultByteCap)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if got := e.(*TextEntry).Tier; got != preview.TierHighlighted {
		t.Fatalf("expected TierHighlighted at open, got %v", got)
	}

	// ...then rewritten large enough to now be over the ceiling.
	rewriteWithNewerMtime(t, path, []byte("hello world\n"))
	updated, changed, err := Reload(e, preview.DefaultByteCap)
	if err != nil || !changed {
		t.Fatalf("expected reload to report changed, got changed=%v err=%v", changed, err)
	}
	if got := updated.(*TextEntry).Tier; got != preview.TierPlainText {
		t.Fatalf("expected reload to re-decide tier from the new size (demotion to TierPlainText), got %v", got)
	}
}

func TestReloadResetsScrollWhenTierFlips(t *testing.T) {
	orig := preview.HighlightCeiling
	defer func() { preview.HighlightCeiling = orig }()
	preview.HighlightCeiling = 4

	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", []byte("hello\n"))
	e, err := Open(path, preview.DefaultByteCap)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	e.(*TextEntry).Scroll = 42

	rewriteWithNewerMtime(t, path, []byte("hi\n"))
	updated, _, err := Reload(e, preview.DefaultByteCap)
	if err != nil {
		t.Fatalf("Reload failed: %v", err)
	}
	if got := updated.(*TextEntry).Scroll; got != 0 {
		t.Fatalf("expected scroll reset to 0 across a tier flip, got %d", got)
	}
}

func TestReloadLeavesScrollAloneWhenTierUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", []byte("old\n"))
	e, err := Open(path, preview.DefaultByteCap)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	e.(*TextEntry).Scroll = 7

	rewriteWithNewerMtime(t, path, []byte("new\n"))
	updated, _, err := Reload(e, preview.DefaultByteCap)
	if err != nil {
		t.Fatalf("Reload failed: %v", err)
	}
	if got := updated.(*TextEntry).Scroll; got != 7 {
		t.Fatalf("expected scroll left as-is when tier doesn't change, got %d", got)
	}
}

func TestReloadRestartsBackgroundStream(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", []byte("old\n"))

	e, err := Open(path, preview.DefaultByteCap)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	streamBefore := e.(*TextEntry).Stream

	rewriteWithNewerMtime(t, path, []byte("new\n"))
	updated, _, err := Reload(e, preview.DefaultByteCap)
	if err != nil {
		t.Fatalf("Reload failed: %v", err)
	}
	te := updated.(*TextEntry)
	if te.Stream == nil {
		t.Fatal("expected a background stream after reload")
	}
	if te.Stream == streamBefore {
		t.Fatal("expected reload to start a fresh stream rather than reuse the stale one")
	}
}

// TestReloadChangesConcreteTypeOnFlip guards the TextEntry/HexEntry
// split itself (#114): a tier flip must hand back a genuinely different
// concrete type, not just a same-shaped struct with different field
// values — checked in both directions (text-tier entry becoming
// binary, and vice versa). Replaces the earlier Entry.Text/Entry.Hex-
// pointer-based TestReloadNilsInactiveTierStateOnFlip, which doesn't
// make sense anymore now that a flip produces a wholly different Go
// type rather than nil-ing out one of two fields on a shared struct —
// a stronger, more directly-expressible guarantee.
func TestReloadChangesConcreteTypeOnFlip(t *testing.T) {
	t.Run("text to binary", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, "a.txt", []byte("hello\n"))
		e, err := Open(path, preview.DefaultByteCap)
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		if _, ok := e.(*TextEntry); !ok {
			t.Fatalf("expected *TextEntry at open, got %T", e)
		}

		rewriteWithNewerMtime(t, path, []byte("abc\x00def"))
		updated, changed, err := Reload(e, preview.DefaultByteCap)
		if err != nil || !changed {
			t.Fatalf("expected reload to report changed, got changed=%v err=%v", changed, err)
		}
		if _, ok := updated.(*HexEntry); !ok {
			t.Fatalf("expected reload to flip to *HexEntry, got %T", updated)
		}
	})

	t.Run("binary to text", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, "a.bin", []byte("abc\x00def"))
		e, err := Open(path, preview.DefaultByteCap)
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		if _, ok := e.(*HexEntry); !ok {
			t.Fatalf("expected *HexEntry at open, got %T", e)
		}

		rewriteWithNewerMtime(t, path, []byte("hello\n"))
		updated, changed, err := Reload(e, preview.DefaultByteCap)
		if err != nil || !changed {
			t.Fatalf("expected reload to report changed, got changed=%v err=%v", changed, err)
		}
		if _, ok := updated.(*TextEntry); !ok {
			t.Fatalf("expected reload to flip to *TextEntry, got %T", updated)
		}
	})
}

func TestReloadInvalidatesWrapCacheAndFindState(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", []byte("old\n"))

	e, err := Open(path, preview.DefaultByteCap)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	te := e.(*TextEntry)
	te.RowsWidth = 80
	te.Rows = []preview.DisplayRow{{}}
	te.FirstRow = map[int]int{0: 0}
	te.FindQuery = "old"
	te.FindMatches = []find.Match{{Line: 0}}
	te.FindCurrent = 0
	te.FindWrapNote = "wrapped to top"

	rewriteWithNewerMtime(t, path, []byte("new\n"))
	updated, _, err := Reload(e, preview.DefaultByteCap)
	if err != nil {
		t.Fatalf("Reload failed: %v", err)
	}
	ute := updated.(*TextEntry)

	if ute.RowsWidth != 0 || ute.Rows != nil || ute.FirstRow != nil {
		t.Fatalf("expected wrap cache invalidated after reload, got RowsWidth=%d Rows=%v FirstRow=%v", ute.RowsWidth, ute.Rows, ute.FirstRow)
	}
	if ute.FindQuery != "" || ute.FindMatches != nil || ute.FindCurrent != -1 || ute.FindWrapNote != "" {
		t.Fatalf("expected find state cleared after reload, got query=%q matches=%v current=%d wrapNote=%q",
			ute.FindQuery, ute.FindMatches, ute.FindCurrent, ute.FindWrapNote)
	}
}

func TestReloadCancelsAndClearsInFlightFindScan(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", []byte("old\n"))

	e, err := Open(path, preview.DefaultByteCap)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	te := e.(*TextEntry)
	te.FindScan = find.StartScan(path, "old")

	rewriteWithNewerMtime(t, path, []byte("new\n"))
	updated, _, err := Reload(e, preview.DefaultByteCap)
	if err != nil {
		t.Fatalf("Reload failed: %v", err)
	}
	if got := updated.(*TextEntry).FindScan; got != nil {
		t.Fatalf("expected reload to cancel and clear an in-flight find scan, got %v", got)
	}
}
