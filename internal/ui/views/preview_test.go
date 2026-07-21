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

func TestTierPlainTextFindKeyIsNoOp(t *testing.T) {
	files, _ := openTierPlainText(t, 10)
	v := newTestPreview(files, 60, 10)

	v.HandleKey(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone))
	if v.FindPromptOpen {
		t.Fatal("expected `/` to be a no-op on a TierPlainText entry (find not yet implemented for it)")
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
