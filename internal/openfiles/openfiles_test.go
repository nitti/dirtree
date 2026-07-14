package openfiles

import (
	"os"
	"path/filepath"
	"testing"

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

// --- Open-failure detection at open time (TESTING.md "Open-failure
// detection at open time (§2.2)") ---

func TestOpenBinaryFileFails(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "bin", []byte("abc\x00def"))

	l := New()
	res := l.Open(path, preview.DefaultByteCap)
	if res.Outcome != Failed || res.Message != "binary file, preview not available" {
		t.Fatalf("expected failed binary result, got %+v", res)
	}
	if len(l.Entries) != 0 {
		t.Fatalf("expected no entry created, got %d", len(l.Entries))
	}
	if l.Displayed != -1 {
		t.Fatalf("expected no displayed entry, got %d", l.Displayed)
	}
}

func TestOpenNonexistentFileFails(t *testing.T) {
	l := New()
	res := l.Open(filepath.Join(t.TempDir(), "nope.txt"), preview.DefaultByteCap)
	if res.Outcome != Failed || res.Message == "" {
		t.Fatalf("expected failed result with a message, got %+v", res)
	}
	if len(l.Entries) != 0 {
		t.Fatalf("expected no entry created, got %d", len(l.Entries))
	}
}

func TestOpenFailureDoesNotDisturbPreviouslyDisplayedEntry(t *testing.T) {
	dir := t.TempDir()
	good := writeFile(t, dir, "good.txt", []byte("hello\n"))
	bad := writeFile(t, dir, "bad.bin", []byte("x\x00y"))

	l := New()
	l.Open(good, preview.DefaultByteCap)
	displayedBefore := l.Displayed

	res := l.Open(bad, preview.DefaultByteCap)
	if res.Outcome != Failed {
		t.Fatalf("expected failed result, got %+v", res)
	}
	if l.Displayed != displayedBefore {
		t.Fatalf("expected displayed entry unchanged, got %d want %d", l.Displayed, displayedBefore)
	}
	if len(l.Entries) != 1 {
		t.Fatalf("expected still exactly one entry, got %d", len(l.Entries))
	}
}

func TestOpenOrdinaryFileSucceeds(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "ok.txt", []byte("hello\n"))

	l := New()
	res := l.Open(path, preview.DefaultByteCap)
	if res.Outcome != Opened {
		t.Fatalf("expected opened result, got %+v", res)
	}
	if len(l.Entries) != 1 || l.Displayed != 0 {
		t.Fatalf("expected one entry displayed, got entries=%d displayed=%d", len(l.Entries), l.Displayed)
	}
}

func TestOpenReusesExistingEntryWithoutRereading(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "ok.txt", []byte("hello\n"))

	l := New()
	first := l.Open(path, preview.DefaultByteCap)
	first.Entry.Scroll = 7

	// Mutate the file on disk so a re-read would behave differently
	// (it would fail entirely, since the file is now gone) — proving
	// the second Open call reuses the cached entry rather than
	// re-reading.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	second := l.Open(path, preview.DefaultByteCap)
	if second.Outcome != Opened {
		t.Fatalf("expected reused entry to still be an opened result, got %+v", second)
	}
	if second.Entry != first.Entry {
		t.Fatal("expected the same entry object to be reused, not a new one")
	}
	if second.Entry.Scroll != 7 {
		t.Fatalf("expected scroll state preserved across reuse, got %d", second.Entry.Scroll)
	}
	if len(l.Entries) != 1 {
		t.Fatalf("expected no duplicate entry, got %d", len(l.Entries))
	}
}

func TestOpenFailureDoesNotBlockSubsequentOpens(t *testing.T) {
	dir := t.TempDir()
	l := New()

	bad := l.Open(filepath.Join(dir, "nope.txt"), preview.DefaultByteCap)
	if bad.Outcome != Failed {
		t.Fatalf("expected failed result, got %+v", bad)
	}

	good := writeFile(t, dir, "ok.txt", []byte("hi\n"))
	res := l.Open(good, preview.DefaultByteCap)
	if res.Outcome != Opened {
		t.Fatalf("expected subsequent open to succeed independently, got %+v", res)
	}
}

// --- Open files list (TESTING.md "Open files list (§2.2, §2.3)") ---

func TestOpenAppendsNewEntryAtEndWithScrollReset(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.txt", []byte("a\n"))
	b := writeFile(t, dir, "b.txt", []byte("b\n"))

	l := New()
	l.Open(a, preview.DefaultByteCap)
	l.DisplayedEntry().Scroll = 3
	res := l.Open(b, preview.DefaultByteCap)

	if len(l.Entries) != 2 || l.Entries[1].Path != b {
		t.Fatalf("expected new entry appended at end, got %+v", l.Entries)
	}
	if res.Entry.Scroll != 0 {
		t.Fatalf("expected new entry's scroll reset to top, got %d", res.Entry.Scroll)
	}
	if l.Displayed != 1 {
		t.Fatalf("expected new entry displayed, got %d", l.Displayed)
	}
}

func TestDisplayingExistingEntryNeverChangesOrder(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.txt", []byte("a\n"))
	b := writeFile(t, dir, "b.txt", []byte("b\n"))

	l := New()
	l.Open(a, preview.DefaultByteCap)
	l.Open(b, preview.DefaultByteCap)
	l.Display(0)

	if l.Entries[0].Path != a || l.Entries[1].Path != b {
		t.Fatalf("expected order unchanged, got %+v", l.Entries)
	}
	if l.Displayed != 0 {
		t.Fatalf("expected entry 0 displayed, got %d", l.Displayed)
	}
}

func TestEachEntryScrollIsIndependent(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.txt", []byte("a\n"))
	b := writeFile(t, dir, "b.txt", []byte("b\n"))

	l := New()
	l.Open(a, preview.DefaultByteCap)
	l.Open(b, preview.DefaultByteCap)
	l.Entries[1].Scroll = 5

	if l.Entries[0].Scroll != 0 {
		t.Fatalf("expected entry 0's scroll unaffected, got %d", l.Entries[0].Scroll)
	}
}

func TestRemoveNonDisplayedEntryLeavesDisplayedUnaffected(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.txt", []byte("a\n"))
	b := writeFile(t, dir, "b.txt", []byte("b\n"))
	c := writeFile(t, dir, "c.txt", []byte("c\n"))

	l := New()
	l.Open(a, preview.DefaultByteCap)
	l.Open(b, preview.DefaultByteCap)
	l.Open(c, preview.DefaultByteCap)
	l.Display(2) // c displayed
	displayed := l.DisplayedEntry()

	l.Remove(0) // remove a, not displayed

	if l.DisplayedEntry() != displayed {
		t.Fatal("expected displayed entry unaffected by removing a non-displayed entry")
	}
	if len(l.Entries) != 2 {
		t.Fatalf("expected list to shrink to 2, got %d", len(l.Entries))
	}
}

func TestRemoveDisplayedEntryPromotesNext(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.txt", []byte("a\n"))
	b := writeFile(t, dir, "b.txt", []byte("b\n"))
	c := writeFile(t, dir, "c.txt", []byte("c\n"))

	l := New()
	l.Open(a, preview.DefaultByteCap)
	l.Open(b, preview.DefaultByteCap)
	l.Open(c, preview.DefaultByteCap)
	l.Display(1) // b displayed

	newSel := l.Remove(1) // remove b

	if l.DisplayedEntry().Path != c {
		t.Fatalf("expected c promoted to displayed, got %s", l.DisplayedEntry().Path)
	}
	if newSel != 1 {
		t.Fatalf("expected overlay selection to follow to index 1, got %d", newSel)
	}
}

func TestRemoveDisplayedLastEntryPromotesPrevious(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.txt", []byte("a\n"))
	b := writeFile(t, dir, "b.txt", []byte("b\n"))

	l := New()
	l.Open(a, preview.DefaultByteCap)
	l.Open(b, preview.DefaultByteCap)
	l.Display(1) // b displayed, and last

	newSel := l.Remove(1)

	if l.DisplayedEntry().Path != a {
		t.Fatalf("expected a promoted to displayed, got %s", l.DisplayedEntry().Path)
	}
	if newSel != 0 {
		t.Fatalf("expected overlay selection to follow to index 0, got %d", newSel)
	}
}

func TestRemoveLastRemainingEntryEmptiesList(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.txt", []byte("a\n"))

	l := New()
	l.Open(a, preview.DefaultByteCap)
	l.Remove(0)

	if len(l.Entries) != 0 {
		t.Fatalf("expected empty list, got %d entries", len(l.Entries))
	}
	if l.Displayed != -1 {
		t.Fatalf("expected no displayed entry, got %d", l.Displayed)
	}
}

func TestRemoveClampsSelectionToNearestRemaining(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.txt", []byte("a\n"))
	b := writeFile(t, dir, "b.txt", []byte("b\n"))
	c := writeFile(t, dir, "c.txt", []byte("c\n"))

	l := New()
	l.Open(a, preview.DefaultByteCap)
	l.Open(b, preview.DefaultByteCap)
	l.Open(c, preview.DefaultByteCap)
	l.Display(0)

	newSel := l.Remove(2) // remove last entry (c), not displayed

	if newSel != 1 {
		t.Fatalf("expected selection clamped to new last index 1, got %d", newSel)
	}
}

func TestMoveDownSwapsWithSuccessorAndFollowsSelection(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.txt", []byte("a\n"))
	b := writeFile(t, dir, "b.txt", []byte("b\n"))

	l := New()
	l.Open(a, preview.DefaultByteCap)
	l.Open(b, preview.DefaultByteCap)
	l.Display(0) // a displayed

	newSel := l.MoveDown(0)

	if newSel != 1 || l.Entries[1].Path != a || l.Entries[0].Path != b {
		t.Fatalf("expected a moved to index 1, got sel=%d entries=%+v", newSel, l.Entries)
	}
	if l.DisplayedEntry().Path != a {
		t.Fatal("expected displayed entry to still be a after reorder")
	}
}

func TestMoveUpSwapsWithPredecessorAndFollowsSelection(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.txt", []byte("a\n"))
	b := writeFile(t, dir, "b.txt", []byte("b\n"))

	l := New()
	l.Open(a, preview.DefaultByteCap)
	l.Open(b, preview.DefaultByteCap)
	l.Display(1) // b displayed

	newSel := l.MoveUp(1)

	if newSel != 0 || l.Entries[0].Path != b || l.Entries[1].Path != a {
		t.Fatalf("expected b moved to index 0, got sel=%d entries=%+v", newSel, l.Entries)
	}
	if l.DisplayedEntry().Path != b {
		t.Fatal("expected displayed entry to still be b after reorder")
	}
}

func TestMoveDownOnLastIsNoOp(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.txt", []byte("a\n"))
	b := writeFile(t, dir, "b.txt", []byte("b\n"))

	l := New()
	l.Open(a, preview.DefaultByteCap)
	l.Open(b, preview.DefaultByteCap)

	newSel := l.MoveDown(1)

	if newSel != 1 || l.Entries[0].Path != a || l.Entries[1].Path != b {
		t.Fatalf("expected no-op, got sel=%d entries=%+v", newSel, l.Entries)
	}
}

func TestMoveUpOnFirstIsNoOp(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.txt", []byte("a\n"))
	b := writeFile(t, dir, "b.txt", []byte("b\n"))

	l := New()
	l.Open(a, preview.DefaultByteCap)
	l.Open(b, preview.DefaultByteCap)

	newSel := l.MoveUp(0)

	if newSel != 0 || l.Entries[0].Path != a || l.Entries[1].Path != b {
		t.Fatalf("expected no-op, got sel=%d entries=%+v", newSel, l.Entries)
	}
}

func TestReorderDoesNotResetMovedEntryScrollState(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.txt", []byte("a\n"))
	b := writeFile(t, dir, "b.txt", []byte("b\n"))

	l := New()
	l.Open(a, preview.DefaultByteCap)
	l.Open(b, preview.DefaultByteCap)
	l.Entries[0].Scroll = 4

	l.MoveDown(0)

	if l.Entries[1].Scroll != 4 {
		t.Fatalf("expected moved entry's scroll preserved, got %d", l.Entries[1].Scroll)
	}
}

func TestOpeningAfterReorderStillAppendsAtEnd(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.txt", []byte("a\n"))
	b := writeFile(t, dir, "b.txt", []byte("b\n"))
	c := writeFile(t, dir, "c.txt", []byte("c\n"))

	l := New()
	l.Open(a, preview.DefaultByteCap)
	l.Open(b, preview.DefaultByteCap)
	l.MoveDown(0) // order is now b, a

	l.Open(c, preview.DefaultByteCap)

	if len(l.Entries) != 3 || l.Entries[2].Path != c {
		t.Fatalf("expected c appended at end regardless of reordering, got %+v", l.Entries)
	}
}
