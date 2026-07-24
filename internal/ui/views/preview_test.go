package views

import (
	"fmt"
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

// TestGotoLineBlockedOnlyWhenStreamPresentAndNotDone verifies the pure
// decision behind goto-line's block/allow gating (SPEC.md §2.1,
// docs/STREAMING_PREVIEW_DESIGN.md §4): blocked only while a stream is
// present and hasn't finished; absent a stream at all, or once it's
// done, goto-line proceeds normally.
func TestGotoLineBlockedOnlyWhenStreamPresentAndNotDone(t *testing.T) {
	cases := []struct {
		name                      string
		streamPresent, streamDone bool
		want                      bool
	}{
		{"no stream tracked", false, false, false},
		{"stream present, not done", true, false, true},
		{"stream present, done", true, true, false},
	}
	for _, c := range cases {
		if got := gotoLineBlocked(c.streamPresent, c.streamDone); got != c.want {
			t.Errorf("%s: gotoLineBlocked(%v, %v) = %v, want %v", c.name, c.streamPresent, c.streamDone, got, c.want)
		}
	}
}

// TestStreamBuildingVisible exercises the file-legend spinner's
// show/hide decision (SPEC.md §5.3's perceptibility-threshold and
// minimum-display-duration discipline, applied here the same way
// spinner.BadgeDecision applies it to the corner badge).
func TestStreamBuildingVisible(t *testing.T) {
	const threshold = 250 * time.Millisecond
	const minDisplay = 1 * time.Second

	cases := []struct {
		name               string
		elapsed, sinceDone time.Duration
		done               bool
		want               bool
	}{
		{"running, under threshold", 100 * time.Millisecond, 0, false, false},
		{"running, at threshold", threshold, 0, false, true},
		{"running, well past threshold", 5 * time.Second, 0, false, true},
		{"done before threshold ever crossed", 100 * time.Millisecond, 100 * time.Millisecond, true, false},
		{"done just after threshold, before min display elapses", 300 * time.Millisecond, 50 * time.Millisecond, true, true},
		{"done, min display duration fully elapsed", 2 * time.Second, 1500 * time.Millisecond, true, false},
	}
	for _, c := range cases {
		if got := streamBuildingVisible(c.elapsed, c.sinceDone, c.done, threshold, minDisplay); got != c.want {
			t.Errorf("%s: streamBuildingVisible(elapsed=%v, sinceDone=%v, done=%v) = %v, want %v", c.name, c.elapsed, c.sinceDone, c.done, got, c.want)
		}
	}
}

// TestPreviewLegendsTier1FitMinTerminalWidth guards SPEC.md §6.4's
// minimum terminal size against §5.2's legend tiering: each of the
// preview view's own legends (the idle file title bar, copy mode, the
// goto-line prompt, and the in-file find prompt and its two states)
// must have priority-1 (never-dropped) text that fits within
// canvas.MinTerminalWidth on its own, with no left-hand label —
// otherwise a terminal at exactly the enforced minimum would still see
// a clipped legend.
func TestPreviewLegendsTier1FitMinTerminalWidth(t *testing.T) {
	named := map[string][]canvas.LegendEntry{
		"fileLegend":           fileLegend,
		"fileLegendCopyModeOn": fileLegendCopyModeOn,
		"gotoLegend":           gotoLegend,
		"findPromptLegend":     findPromptLegend,
		"findLegend":           findLegend,
		"findLegendNoMatches":  findLegendNoMatches,
	}
	for name, entries := range named {
		tier1 := canvas.LegendString(canvas.KeepUpToPriority(entries, 1))
		if n := len([]rune(tier1)); n > canvas.MinTerminalWidth {
			t.Errorf("%s's priority-1 text is %d runes, exceeding MinTerminalWidth (%d): %q", name, n, canvas.MinTerminalWidth, tier1)
		}
	}
}

// TestDrawPreviewShowsGotoLegend verifies the goto-line prompt row
// renders a keybinding legend (SPEC.md §5.2) alongside the "goto
// line: " input.
// TestDrawContentEmptyStateHintMatchesLegendOrder guards SPEC.md §2.1's
// empty-state hint: with no displayed entry, it names quick open,
// browse, then search — the same left-to-right order previewLegend
// (internal/ui/render.go) uses — rather than an order that drifted out
// of sync with the header legend the user actually sees above it.
func TestDrawContentEmptyStateHintMatchesLegendOrder(t *testing.T) {
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	w, h := 90, 10
	sim.SetSize(w, h)

	v := &Preview{Shared: &Shared{Files: openfiles.New(), Canvas: canvas.New(sim)}}
	v.drawContent(0, 0, w, h)
	sim.Show()

	row := strings.TrimSpace(rowText(sim, h/2, w))
	oIdx := strings.Index(row, "o to quick open")
	bIdx := strings.Index(row, "b to browse")
	sIdx := strings.Index(row, "s to search")
	if oIdx < 0 || bIdx < 0 || sIdx < 0 {
		t.Fatalf("empty-state hint = %q, missing one of the three mode hints", row)
	}
	if oIdx >= bIdx || bIdx >= sIdx {
		t.Errorf("empty-state hint = %q, want quick-open before browse before search", row)
	}
}

func TestDrawPreviewShowsGotoLegend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	w, h := 60, 10
	sim.SetSize(w, h)

	files := openfiles.New()
	v := &Preview{Shared: &Shared{Files: files, Canvas: canvas.New(sim)}}
	if res := files.Open(path, 1<<20); res.Outcome != openfiles.Opened {
		t.Fatalf("Open failed: %s", res.Message)
	}
	waitEntryReady(t, files.DisplayedEntry())
	v.GotoPromptOpen = true
	v.GotoInput = "2"

	v.drawContent(0, 0, w, h)
	sim.Show()

	row := rowText(sim, h-1, w)
	if !strings.HasPrefix(row, "goto line: 2") {
		t.Fatalf("goto-line row = %q, want it to start with the prompt text", row)
	}
	for _, want := range []string{"[return] jump", "[esc] cancel"} {
		if !strings.Contains(row, want) {
			t.Errorf("goto-line row = %q, missing legend entry %q", row, want)
		}
	}
}

func TestDrawFileTitleBarShowsLineCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "three.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	w, h := 60, 10
	sim.SetSize(w, h)

	files := openfiles.New()
	v := &Preview{Shared: &Shared{Files: files, Canvas: canvas.New(sim), RootPath: dir}}
	if res := files.Open(path, 1<<20); res.Outcome != openfiles.Opened {
		t.Fatalf("Open failed: %s", res.Message)
	}
	waitEntryReady(t, files.DisplayedEntry())

	v.Draw(0, 0, w, h, true)
	sim.Show()

	row := rowText(sim, 0, w)
	if !strings.HasPrefix(row, "3 lines  three.txt") {
		t.Fatalf("title row = %q, want it to start with the file's line count and name", row)
	}
}

func TestDrawFileTitleBarShowsSingularLineCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "one.txt")
	if err := os.WriteFile(path, []byte("only line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	w, h := 60, 10
	sim.SetSize(w, h)

	files := openfiles.New()
	v := &Preview{Shared: &Shared{Files: files, Canvas: canvas.New(sim), RootPath: dir}}
	if res := files.Open(path, 1<<20); res.Outcome != openfiles.Opened {
		t.Fatalf("Open failed: %s", res.Message)
	}
	waitEntryReady(t, files.DisplayedEntry())

	v.Draw(0, 0, w, h, true)
	sim.Show()

	row := rowText(sim, 0, w)
	if !strings.HasPrefix(row, "1 line  one.txt") {
		t.Fatalf("title row = %q, want singular \"1 line\"", row)
	}
}

// waitEntryReady blocks until e's content is ready to render (its
// background stream pass has finished and, for TierHighlighted, been
// synced into Lines/Segs) — the real app never opens the goto-line
// prompt or scrolls before this point either (contentReady/gotoLineBlocked
// gate on the same signal), so tests exercising rendering/scrolling
// against a freshly-opened entry wait for it the same way.
func waitEntryReady(t *testing.T, e *openfiles.Entry) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if contentReady(e) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("entry's background stream did not finish in time")
}

// openTierPlainText forces path (which is created with numLines lines
// "line 1".."line N") to open as TierPlainText regardless of its actual
// (tiny, test-fixture-sized) content, by temporarily zeroing
// preview.HighlightCeiling — the same technique the openfiles package's
// own tier tests use, so Tier B's windowed-read/scroll logic can be
// exercised without a real multi-megabyte fixture. Restores the ceiling
// via t.Cleanup.
func openTierPlainText(t *testing.T, numLines int) (*openfiles.List, *openfiles.Entry) {
	t.Helper()
	orig := preview.HighlightCeiling
	preview.HighlightCeiling = 0
	t.Cleanup(func() { preview.HighlightCeiling = orig })

	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	var content strings.Builder
	for i := 1; i <= numLines; i++ {
		fmt.Fprintf(&content, "line %d\n", i)
	}
	if err := os.WriteFile(path, []byte(content.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	files := openfiles.New()
	if res := files.Open(path, 1<<20); res.Outcome != openfiles.Opened {
		t.Fatalf("Open failed: %s", res.Message)
	}
	e := files.DisplayedEntry()
	if e.Tier != preview.TierPlainText {
		t.Fatalf("expected TierPlainText, got %v", e.Tier)
	}
	waitEntryReady(t, e)
	return files, e
}

func newTestPreview(files *openfiles.List, w, h int) *Preview {
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		panic(err)
	}
	sim.SetSize(w, h)
	return &Preview{Shared: &Shared{Files: files, Canvas: canvas.New(sim)}}
}

func TestTierPlainTextWindowStartsAtFirstLine(t *testing.T) {
	files, e := openTierPlainText(t, 10)
	v := newTestPreview(files, 60, 10)

	v.drawContent(0, 0, 60, 10)
	if e.WindowStartLine != 0 {
		t.Fatalf("expected window to start at line 0, got %d", e.WindowStartLine)
	}
	if len(e.Lines) == 0 || e.Lines[0] != "line 1" {
		t.Fatalf("expected window to start with 'line 1', got %v", e.Lines)
	}
}

func TestTierPlainTextScrollMovesBySourceLine(t *testing.T) {
	files, e := openTierPlainText(t, 50)
	v := newTestPreview(files, 60, 10)
	v.drawContent(0, 0, 60, 10)

	v.scroll(3)
	if got := currentTopLine(e); got != 4 {
		t.Fatalf("expected top line 4 after scrolling 3, got %d", got)
	}
}

func TestTierPlainTextGotoLineJumpsViaWindow(t *testing.T) {
	files, e := openTierPlainText(t, 50)
	v := newTestPreview(files, 60, 10)

	v.ScrollToLine(e, 7)
	if got := currentTopLine(e); got != 7 {
		t.Fatalf("expected top line 7 after ScrollToLine(7), got %d", got)
	}
	if e.Lines[e.Scroll] != "line 7" {
		t.Fatalf("expected line 7 at the scrolled-to row, got %q", e.Lines[e.Scroll])
	}
}

func TestTierPlainTextFindKeyOpensPrompt(t *testing.T) {
	files, _ := openTierPlainText(t, 10)
	v := newTestPreview(files, 60, 10)

	v.HandleKey(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone))
	if !v.FindPromptOpen {
		t.Fatal("expected `/` to open the find prompt on a TierPlainText entry (docs/STREAMING_PREVIEW_DESIGN.md §9)")
	}
}

// waitFindScanDone blocks until e's in-progress find scan has been
// picked up by syncFindScan (i.e. FindScan cleared back to nil), the
// same signal the real Draw loop's per-frame sync relies on.
func waitFindScanDone(t *testing.T, v *Preview, e *openfiles.Entry) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		v.syncFindScan(e)
		if e.FindScan == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("find scan did not finish in time")
}

func TestTierPlainTextFindStartsBackgroundScan(t *testing.T) {
	files, e := openTierPlainText(t, 10)
	v := newTestPreview(files, 60, 10)

	v.performFind("line 7")
	if e.FindScan == nil {
		t.Fatal("expected performFind to start a background scan for a TierPlainText entry")
	}
	if len(e.FindMatches) != 0 || e.FindCurrent != -1 {
		t.Fatalf("expected no matches yet while the scan is in flight, got FindMatches=%v FindCurrent=%d", e.FindMatches, e.FindCurrent)
	}
}

func TestTierPlainTextFindScanResultsSyncOnceDone(t *testing.T) {
	files, e := openTierPlainText(t, 10)
	v := newTestPreview(files, 60, 10)

	v.performFind("line 7")
	waitFindScanDone(t, v, e)

	if len(e.FindMatches) != 1 || e.FindMatches[0].Line != 6 {
		t.Fatalf("expected a single match on source line 6 (the 0-based index of the text \"line 7\"), got %v", e.FindMatches)
	}
	if e.FindCurrent != 0 {
		t.Fatalf("expected the single match to be current, got %d", e.FindCurrent)
	}
	matchRow := e.FindMatches[0].Line - e.WindowStartLine
	if matchRow < 0 || matchRow >= len(e.Lines) || e.Lines[matchRow] != "line 7" {
		t.Fatalf("expected the match's window-relative row to hold \"line 7\", got %v (row %d)", e.Lines, matchRow)
	}
	if matchRow < e.Scroll || matchRow >= e.Scroll+v.viewportHeight() {
		t.Fatalf("expected the match's row (%d) to be scrolled into view (Scroll=%d, viewportHeight=%d)", matchRow, e.Scroll, v.viewportHeight())
	}
}

func TestTierPlainTextFindClearCancelsInFlightScan(t *testing.T) {
	files, e := openTierPlainText(t, 10)
	v := newTestPreview(files, 60, 10)

	v.performFind("line 7")
	if e.FindScan == nil {
		t.Fatal("expected a scan to be in flight")
	}
	v.clearFind()
	if e.FindScan != nil || e.FindQuery != "" {
		t.Fatalf("expected clearFind to cancel and clear the in-flight scan, got FindScan=%v FindQuery=%q", e.FindScan, e.FindQuery)
	}
}

func TestGutterWidthUsesExactCountOnceTierPlainTextStreamDone(t *testing.T) {
	_, e := openTierPlainText(t, 10)
	if got := gutterWidth(e); got != canvas.GutterWidth(10) {
		t.Fatalf("expected gutter sized off the exact final line count once done, got %d want %d", got, canvas.GutterWidth(10))
	}
}

func rowText(sim tcell.SimulationScreen, y, w int) string {
	cells, width, _ := sim.GetContents()
	var b strings.Builder
	for x := 0; x < w && x < width; x++ {
		c := cells[y*width+x]
		if len(c.Runes) > 0 {
			b.WriteRune(c.Runes[0])
		} else {
			b.WriteRune(' ')
		}
	}
	return b.String()
}
