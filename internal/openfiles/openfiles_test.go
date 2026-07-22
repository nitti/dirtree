package openfiles

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nitti/dirtree/internal/find"
	"github.com/nitti/dirtree/internal/preview"
)

// newListWithN returns a List with n bare entries (no file I/O), for
// exercising the dropdown's paging/bulk-reorder math, which only cares
// about entry count and position, not content.
func newListWithN(n int) *List {
	l := New()
	for i := range n {
		l.Entries = append(l.Entries, &Entry{Path: fmt.Sprintf("/entry%02d", i)})
	}
	return l
}

// waitSynced blocks until e's background stream has finished and its
// content has been pulled into Lines/Segs (SyncContent), for tests that
// need to observe a TierHighlighted entry's actual content rather than
// just that Open/Reload started the background pass.
func waitSynced(t *testing.T, e *Entry) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		e.SyncContent()
		if e.Lines != nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("entry's background stream did not finish in time")
}

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

func TestOpenStartsBackgroundStreamForNewEntry(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "ok.txt", []byte("hello\n"))

	l := New()
	l.Open(path, preview.DefaultByteCap)
	if l.Entries[0].Stream == nil {
		t.Fatal("expected a background stream to be started for a newly-opened entry")
	}
}

func TestOpenAtOrUnderCeilingIsTierHighlighted(t *testing.T) {
	orig := preview.HighlightCeiling
	defer func() { preview.HighlightCeiling = orig }()
	preview.HighlightCeiling = 100

	dir := t.TempDir()
	path := writeFile(t, dir, "small.txt", []byte("hello\n"))

	l := New()
	l.Open(path, preview.DefaultByteCap)
	if got := l.Entries[0].Tier; got != preview.TierHighlighted {
		t.Fatalf("expected TierHighlighted for a file under the ceiling, got %v", got)
	}
}

func TestOpenOverCeilingIsTierPlainText(t *testing.T) {
	orig := preview.HighlightCeiling
	defer func() { preview.HighlightCeiling = orig }()
	preview.HighlightCeiling = 4

	dir := t.TempDir()
	path := writeFile(t, dir, "big.txt", []byte("hello\n"))

	l := New()
	l.Open(path, preview.DefaultByteCap)
	if got := l.Entries[0].Tier; got != preview.TierPlainText {
		t.Fatalf("expected TierPlainText for a file over the ceiling, got %v", got)
	}
}

func TestReloadPromotesEntryWhenShrunkUnderCeiling(t *testing.T) {
	orig := preview.HighlightCeiling
	defer func() { preview.HighlightCeiling = orig }()
	preview.HighlightCeiling = 4

	dir := t.TempDir()
	// Opened over the ceiling (TierPlainText)...
	path := writeFile(t, dir, "a.txt", []byte("hello\n"))
	l := New()
	l.Open(path, preview.DefaultByteCap)
	if l.Entries[0].Tier != preview.TierPlainText {
		t.Fatalf("expected TierPlainText at open, got %v", l.Entries[0].Tier)
	}

	// ...then rewritten small enough to now be under the ceiling.
	rewriteWithNewerMtime(t, path, []byte("hi\n"))
	l.Reload(preview.DefaultByteCap)
	if l.Entries[0].Tier != preview.TierHighlighted {
		t.Fatalf("expected reload to re-decide tier from the new size (promotion to TierHighlighted), got %v", l.Entries[0].Tier)
	}
}

func TestReloadDemotesEntryWhenGrownOverCeiling(t *testing.T) {
	orig := preview.HighlightCeiling
	defer func() { preview.HighlightCeiling = orig }()
	preview.HighlightCeiling = 4

	dir := t.TempDir()
	// Opened at/under the ceiling (TierHighlighted)...
	path := writeFile(t, dir, "a.txt", []byte("hi\n"))
	l := New()
	l.Open(path, preview.DefaultByteCap)
	if l.Entries[0].Tier != preview.TierHighlighted {
		t.Fatalf("expected TierHighlighted at open, got %v", l.Entries[0].Tier)
	}

	// ...then rewritten large enough to now be over the ceiling.
	rewriteWithNewerMtime(t, path, []byte("hello world\n"))
	l.Reload(preview.DefaultByteCap)
	if l.Entries[0].Tier != preview.TierPlainText {
		t.Fatalf("expected reload to re-decide tier from the new size (demotion to TierPlainText), got %v", l.Entries[0].Tier)
	}
}

func TestReloadResetsScrollWhenTierFlips(t *testing.T) {
	orig := preview.HighlightCeiling
	defer func() { preview.HighlightCeiling = orig }()
	preview.HighlightCeiling = 4

	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", []byte("hello\n"))
	l := New()
	l.Open(path, preview.DefaultByteCap)
	l.Entries[0].Scroll = 42

	rewriteWithNewerMtime(t, path, []byte("hi\n"))
	l.Reload(preview.DefaultByteCap)
	if l.Entries[0].Scroll != 0 {
		t.Fatalf("expected scroll reset to 0 across a tier flip, got %d", l.Entries[0].Scroll)
	}
}

func TestReloadLeavesScrollAloneWhenTierUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", []byte("old\n"))
	l := New()
	l.Open(path, preview.DefaultByteCap)
	l.Entries[0].Scroll = 7

	rewriteWithNewerMtime(t, path, []byte("new\n"))
	l.Reload(preview.DefaultByteCap)
	if l.Entries[0].Scroll != 7 {
		t.Fatalf("expected scroll left as-is when tier doesn't change, got %d", l.Entries[0].Scroll)
	}
}

func TestReloadRestartsBackgroundStream(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", []byte("old\n"))

	l := New()
	l.Open(path, preview.DefaultByteCap)
	streamBefore := l.Entries[0].Stream

	rewriteWithNewerMtime(t, path, []byte("new\n"))
	l.Reload(preview.DefaultByteCap)

	if l.Entries[0].Stream == nil {
		t.Fatal("expected a background stream after reload")
	}
	if l.Entries[0].Stream == streamBefore {
		t.Fatal("expected reload to start a fresh stream rather than reuse the stale one")
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

func TestIsOpenReflectsCurrentEntries(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.txt", []byte("a\n"))
	b := writeFile(t, dir, "b.txt", []byte("b\n"))

	l := New()
	if l.IsOpen(a) {
		t.Fatal("expected a not open before it's ever opened")
	}

	l.Open(a, preview.DefaultByteCap)
	if !l.IsOpen(a) {
		t.Fatal("expected a open after Open")
	}
	if l.IsOpen(b) {
		t.Fatal("expected b not open, it was never opened")
	}

	l.Remove(0)
	if l.IsOpen(a) {
		t.Fatal("expected a no longer open after Remove")
	}
}

// --- Open-files dropdown paging (TESTING.md "Open files list (§2.2, §2.3)") ---

func TestPageComputesZeroBasedPageFromIndex(t *testing.T) {
	cases := []struct{ i, want int }{{0, 0}, {9, 0}, {10, 1}, {19, 1}, {20, 2}, {23, 2}}
	for _, c := range cases {
		if got := Page(c.i, 10); got != c.want {
			t.Errorf("Page(%d, 10) = %d, want %d", c.i, got, c.want)
		}
	}
}

func TestPageCountRoundsUpAndIsAtLeastOne(t *testing.T) {
	cases := []struct{ count, want int }{{0, 1}, {1, 1}, {10, 1}, {11, 2}, {20, 2}, {23, 3}}
	for _, c := range cases {
		if got := PageCount(c.count, 10); got != c.want {
			t.Errorf("PageCount(%d, 10) = %d, want %d", c.count, got, c.want)
		}
	}
}

func TestPageBoundsReturnsCorrectSlice(t *testing.T) {
	if start, end := PageBounds(0, 10, 23); start != 0 || end != 10 {
		t.Fatalf("page 0 of 23 = [%d,%d), want [0,10)", start, end)
	}
	if start, end := PageBounds(2, 10, 23); start != 20 || end != 23 {
		t.Fatalf("page 2 of 23 = [%d,%d), want [20,23) (short final page)", start, end)
	}
}

func TestSelectPageJumpsToFirstEntryOfAdjacentPage(t *testing.T) {
	if got := SelectPage(15, 1, 10, 23); got != 20 {
		t.Fatalf("SelectPage(15, +1) = %d, want 20 (first entry of page 2)", got)
	}
	if got := SelectPage(15, -1, 10, 23); got != 0 {
		t.Fatalf("SelectPage(15, -1) = %d, want 0 (first entry of page 0)", got)
	}
}

func TestSelectPageClampsAtEndsRatherThanWrapping(t *testing.T) {
	if got := SelectPage(22, 1, 10, 23); got != 22 {
		t.Fatalf("SelectPage past the last page = %d, want unchanged 22", got)
	}
	if got := SelectPage(3, -1, 10, 23); got != 3 {
		t.Fatalf("SelectPage before the first page = %d, want unchanged 3", got)
	}
}

func TestSelectPageNoOpOnEmptyList(t *testing.T) {
	if got := SelectPage(0, 1, 10, 0); got != 0 {
		t.Fatalf("SelectPage on empty list = %d, want unchanged 0", got)
	}
}

func TestSelectDigitMapsToPageRelativePosition(t *testing.T) {
	idx, ok := SelectDigit(15, 3, 10, 23) // page 1 (10-19), digit 3 -> index 13
	if !ok || idx != 13 {
		t.Fatalf("SelectDigit(15, 3) = (%d, %v), want (13, true)", idx, ok)
	}
}

func TestSelectDigitNoOpPastShortFinalPage(t *testing.T) {
	// count=23: last page is [20,23), only digits 0-2 have an entry.
	if _, ok := SelectDigit(20, 5, 10, 23); ok {
		t.Fatal("expected SelectDigit past the final page's last row to report no entry")
	}
	if idx, ok := SelectDigit(20, 2, 10, 23); !ok || idx != 22 {
		t.Fatalf("SelectDigit(20, 2) = (%d, %v), want (22, true)", idx, ok)
	}
}

func TestMoveDownPageMovesUpToPageSizePositions(t *testing.T) {
	l := newListWithN(15)
	l.Display(0)
	newSel := l.MoveDownPage(0, 10)
	if newSel != 10 {
		t.Fatalf("MoveDownPage(0, 10) = %d, want 10", newSel)
	}
	if l.Entries[10].Path != "/entry00" {
		t.Fatalf("expected entry00 to land at index 10, got %+v", l.Entries[10])
	}
	if l.DisplayedEntry().Path != "/entry00" {
		t.Fatal("expected displayed entry to follow the bulk move")
	}
}

func TestMoveDownPageClampsAtLastEntryRatherThanWrapping(t *testing.T) {
	l := newListWithN(5)
	newSel := l.MoveDownPage(0, 10)
	if newSel != 4 {
		t.Fatalf("MoveDownPage clamped = %d, want 4 (last index)", newSel)
	}
	if l.Entries[4].Path != "/entry00" {
		t.Fatalf("expected entry00 to land at the last index, got %+v", l.Entries)
	}
}

func TestMoveUpPageMovesUpToPageSizePositions(t *testing.T) {
	l := newListWithN(15)
	l.Display(14)
	newSel := l.MoveUpPage(14, 10)
	if newSel != 4 {
		t.Fatalf("MoveUpPage(14, 10) = %d, want 4", newSel)
	}
	if l.Entries[4].Path != "/entry14" {
		t.Fatalf("expected entry14 to land at index 4, got %+v", l.Entries[4])
	}
	if l.DisplayedEntry().Path != "/entry14" {
		t.Fatal("expected displayed entry to follow the bulk move")
	}
}

func TestMoveUpPageClampsAtFirstEntryRatherThanWrapping(t *testing.T) {
	l := newListWithN(5)
	newSel := l.MoveUpPage(4, 10)
	if newSel != 0 {
		t.Fatalf("MoveUpPage clamped = %d, want 0 (first index)", newSel)
	}
	if l.Entries[0].Path != "/entry04" {
		t.Fatalf("expected entry04 to land at the first index, got %+v", l.Entries)
	}
}

// --- Live reload of open files on disk change (TESTING.md "Live
// reload of open files (§6.1a)") ---

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

func TestReloadPicksUpChangedContentOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", []byte("old\n"))

	l := New()
	l.Open(path, preview.DefaultByteCap)

	rewriteWithNewerMtime(t, path, []byte("new\n"))

	reloaded := l.Reload(preview.DefaultByteCap)
	if len(reloaded) != 1 || reloaded[0] != "a.txt" {
		t.Fatalf("expected [\"a.txt\"] reloaded, got %v", reloaded)
	}
	waitSynced(t, l.Entries[0])
	if got := l.Entries[0].Lines; len(got) != 1 || got[0] != "new" {
		t.Fatalf("expected reloaded content, got %v", got)
	}
}

func TestReloadLeavesUnchangedFilesAlone(t *testing.T) {
	dir := t.TempDir()
	changed := writeFile(t, dir, "changed.txt", []byte("old\n"))
	untouched := writeFile(t, dir, "untouched.txt", []byte("stays\n"))

	l := New()
	l.Open(changed, preview.DefaultByteCap)
	l.Open(untouched, preview.DefaultByteCap)

	rewriteWithNewerMtime(t, changed, []byte("new\n"))

	reloaded := l.Reload(preview.DefaultByteCap)
	if len(reloaded) != 1 || reloaded[0] != "changed.txt" {
		t.Fatalf("expected only changed.txt reloaded, got %v", reloaded)
	}
	waitSynced(t, l.Entries[1])
	if got := l.Entries[1].Lines; len(got) != 1 || got[0] != "stays" {
		t.Fatalf("expected untouched entry's content unchanged, got %v", got)
	}
}

func TestReloadIsNoOpWhenNothingChangedOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", []byte("same\n"))

	l := New()
	l.Open(path, preview.DefaultByteCap)

	if reloaded := l.Reload(preview.DefaultByteCap); reloaded != nil {
		t.Fatalf("expected no reload when mtime hasn't changed, got %v", reloaded)
	}
}

func TestReloadPreservesEntryIdentityListOrderAndDisplayed(t *testing.T) {
	dir := t.TempDir()
	first := writeFile(t, dir, "first.txt", []byte("a\n"))
	second := writeFile(t, dir, "second.txt", []byte("b\n"))

	l := New()
	l.Open(first, preview.DefaultByteCap)
	l.Open(second, preview.DefaultByteCap)
	l.Display(0)
	entryBefore := l.Entries[0]

	rewriteWithNewerMtime(t, first, []byte("a changed\n"))
	l.Reload(preview.DefaultByteCap)

	if l.Entries[0] != entryBefore {
		t.Fatal("expected the same *Entry object to be mutated in place, not replaced")
	}
	if l.Entries[0].Path != first || l.Entries[1].Path != second {
		t.Fatalf("expected list order unchanged, got %+v", l.Entries)
	}
	if l.Displayed != 0 {
		t.Fatalf("expected displayed index unchanged, got %d", l.Displayed)
	}
}

func TestReloadLeavesDeletedFileEntryStale(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", []byte("still here\n"))

	l := New()
	l.Open(path, preview.DefaultByteCap)
	waitSynced(t, l.Entries[0])
	linesBefore := l.Entries[0].Lines

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	reloaded := l.Reload(preview.DefaultByteCap)
	if reloaded != nil {
		t.Fatalf("expected no reload reported for a deleted file, got %v", reloaded)
	}
	if got := l.Entries[0].Lines; len(got) != len(linesBefore) || got[0] != linesBefore[0] {
		t.Fatalf("expected last-known content left untouched, got %v want %v", got, linesBefore)
	}
}

func TestReloadInvalidatesWrapCacheAndFindState(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", []byte("old\n"))

	l := New()
	l.Open(path, preview.DefaultByteCap)
	e := l.Entries[0]
	e.RowsWidth = 80
	e.Rows = []preview.DisplayRow{{}}
	e.FirstRow = map[int]int{0: 0}
	e.FindQuery = "old"
	e.FindMatches = []find.Match{{Line: 0}}
	e.FindCurrent = 0
	e.FindWrapNote = "wrapped to top"

	rewriteWithNewerMtime(t, path, []byte("new\n"))
	l.Reload(preview.DefaultByteCap)

	if e.RowsWidth != 0 || e.Rows != nil || e.FirstRow != nil {
		t.Fatalf("expected wrap cache invalidated after reload, got RowsWidth=%d Rows=%v FirstRow=%v", e.RowsWidth, e.Rows, e.FirstRow)
	}
	if e.FindQuery != "" || e.FindMatches != nil || e.FindCurrent != -1 || e.FindWrapNote != "" {
		t.Fatalf("expected find state cleared after reload, got query=%q matches=%v current=%d wrapNote=%q",
			e.FindQuery, e.FindMatches, e.FindCurrent, e.FindWrapNote)
	}
}

func TestReloadCancelsAndClearsInFlightFindScan(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.txt", []byte("old\n"))

	l := New()
	l.Open(path, preview.DefaultByteCap)
	e := l.Entries[0]
	e.FindScan = find.StartScan(path, "old")

	rewriteWithNewerMtime(t, path, []byte("new\n"))
	l.Reload(preview.DefaultByteCap)

	if e.FindScan != nil {
		t.Fatalf("expected reload to cancel and clear an in-flight find scan, got %v", e.FindScan)
	}
}
