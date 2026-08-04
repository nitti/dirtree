package views

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/entry"
	"github.com/nitti/dirtree/internal/find"
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

// TestHandleKeyQReturnsActionQuitKey guards the primary preview view's
// half of the hold-to-quit gesture (SPEC.md §5.2): `q` never quits by
// itself — it only reports ActionQuitKey so App's dispatcher can
// progress its own hold timer, actually quitting only once `q` has been
// held continuously for the full hold duration.
func TestHandleKeyQReturnsActionQuitKey(t *testing.T) {
	v := &Preview{Shared: &Shared{Files: openfiles.New()}}
	ev := tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone)
	if got := v.HandleKey(ev); got != ActionQuitKey {
		t.Fatalf("HandleKey('q') = %v, want ActionQuitKey", got)
	}
}

// TestHandleKeyTogglesCopyMode exercises 'c' end to end through
// Preview.HandleKey for a text-tier entry (SPEC.md §2.1), now dispatched
// through fileView.ToggleCopyMode rather than an inline isHex check.
func TestHandleKeyTogglesCopyMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := openfiles.New()
	v := newTestPreview(files, 60, 10)
	res := files.Open(path, 1<<20)
	if res.Outcome != openfiles.Opened {
		t.Fatalf("Open failed: %s", res.Message)
	}
	e := res.Entry.(*entry.TextEntry)

	v.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModNone))
	if !e.CopyMode {
		t.Fatal("expected 'c' to toggle copy mode on")
	}
	v.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModNone))
	if e.CopyMode {
		t.Fatal("expected a second 'c' to toggle copy mode back off")
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
	v.drawEmptyState(0, 0, w, h)
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

// TestDrawFileTitleBarShowsGotoPrompt guards #114's title-bar placement
// fix: the goto-line prompt now renders in the file title bar (same row
// as the find prompt) instead of its own row at the bottom of the
// content area, and shows the file's valid line range alongside the
// typed input while typing.
func TestDrawFileTitleBarShowsGotoPrompt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	// Wide enough that "goto line: 2 (1-3)" plus the full legend both
	// fit without the fit/drop rule (SPEC.md §5.2) dropping the left
	// side in favor of the legend.
	w, h := 70, 10
	sim.SetSize(w, h)

	files := openfiles.New()
	v := &Preview{Shared: &Shared{Files: files, Canvas: canvas.New(sim)}}
	if res := files.Open(path, 1<<20); res.Outcome != openfiles.Opened {
		t.Fatalf("Open failed: %s", res.Message)
	}
	waitEntryReady(t, files.DisplayedEntry())
	v.GotoPromptOpen = true
	v.GotoInput = "2"

	textFileView{}.DrawTitleBar(v, files.DisplayedEntry(), 0, 0, w, true)
	sim.Show()

	row := rowText(sim, 0, w)
	if !strings.HasPrefix(row, "goto line: 2 (1-3)") {
		t.Fatalf("file title bar row = %q, want it to start with the prompt text and range hint", row)
	}
	for _, want := range []string{"[return] jump", "[esc] cancel"} {
		if !strings.Contains(row, want) {
			t.Errorf("file title bar row = %q, missing legend entry %q", row, want)
		}
	}
}

// TestDrawFileTitleBarSuppressesGotoLegendWhenHelpVisible guards
// SPEC.md §5.4: while the help overlay is showing, the goto prompt
// keeps its own left-hand content (the typed digits and range hint)
// but drops its trailing keybinding legend, since the help overlay is
// the one place that legend now lives.
func TestDrawFileTitleBarSuppressesGotoLegendWhenHelpVisible(t *testing.T) {
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
	v := &Preview{Shared: &Shared{Files: files, Canvas: canvas.New(sim), HelpVisible: true}}
	if res := files.Open(path, 1<<20); res.Outcome != openfiles.Opened {
		t.Fatalf("Open failed: %s", res.Message)
	}
	waitEntryReady(t, files.DisplayedEntry())
	v.GotoPromptOpen = true
	v.GotoInput = "2"

	textFileView{}.DrawTitleBar(v, files.DisplayedEntry(), 0, 0, w, true)
	sim.Show()

	row := rowText(sim, 0, w)
	if !strings.HasPrefix(row, "goto line: 2 (1-3)") {
		t.Fatalf("file title bar row = %q, want it to still start with the prompt text and range hint", row)
	}
	if strings.Contains(row, "[return]") || strings.Contains(row, "[esc]") {
		t.Errorf("file title bar row = %q, want no keybinding legend while HelpVisible", row)
	}
}

// TestPreviewCurrentFileLegendPrecedence guards CurrentFileLegend's
// state precedence (SPEC.md §5.4), which must exactly mirror
// textFileView.DrawTitleBar's own switch so the help overlay never
// shows a legend the title bar itself isn't actually offering.
func TestPreviewCurrentFileLegendPrecedence(t *testing.T) {
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
	if res := files.Open(path, 1<<20); res.Outcome != openfiles.Opened {
		t.Fatalf("Open failed: %s", res.Message)
	}
	waitEntryReady(t, files.DisplayedEntry())
	e := files.DisplayedEntry().(*entry.TextEntry)

	reset := func() {
		v.GotoPromptOpen = false
		v.FindPromptOpen = false
		e.CopyMode = false
		e.FindQuery = ""
		e.FindMatches = nil
		e.FindScan = nil
	}

	t.Run("no displayed entry", func(t *testing.T) {
		empty := &Preview{Shared: &Shared{Files: openfiles.New(), Canvas: canvas.New(sim)}}
		if _, ok := empty.CurrentFileLegend(); ok {
			t.Error("expected ok=false with no displayed entry")
		}
	})

	t.Run("idle", func(t *testing.T) {
		reset()
		got, ok := v.CurrentFileLegend()
		if !ok || &got[0] != &fileLegend[0] {
			t.Errorf("CurrentFileLegend() = (%v, %v), want fileLegend", got, ok)
		}
	})

	// #114: the goto prompt now renders in the file title bar (same as
	// find), so CurrentFileLegend must offer its legend too, taking
	// precedence over every other title-bar state the same way
	// textFileView.DrawTitleBar's own switch does.
	t.Run("goto prompt open", func(t *testing.T) {
		reset()
		v.GotoPromptOpen = true
		got, ok := v.CurrentFileLegend()
		if !ok || &got[0] != &gotoLegend[0] {
			t.Errorf("CurrentFileLegend() = (%v, %v), want gotoLegend", got, ok)
		}
	})

	t.Run("find prompt open", func(t *testing.T) {
		reset()
		v.FindPromptOpen = true
		got, ok := v.CurrentFileLegend()
		if !ok || &got[0] != &findPromptLegend[0] {
			t.Errorf("CurrentFileLegend() = (%v, %v), want findPromptLegend", got, ok)
		}
	})

	t.Run("find scan running", func(t *testing.T) {
		reset()
		e.FindScan = &find.Scan{}
		got, ok := v.CurrentFileLegend()
		if !ok || &got[0] != &findLegendNoMatches[0] {
			t.Errorf("CurrentFileLegend() = (%v, %v), want findLegendNoMatches", got, ok)
		}
	})

	t.Run("find query with matches", func(t *testing.T) {
		reset()
		e.FindQuery = "one"
		e.FindMatches = []find.Match{{Line: 0, Col: 0, Len: 3}}
		got, ok := v.CurrentFileLegend()
		if !ok || &got[0] != &findLegend[0] {
			t.Errorf("CurrentFileLegend() = (%v, %v), want findLegend", got, ok)
		}
	})

	t.Run("find query no matches", func(t *testing.T) {
		reset()
		e.FindQuery = "zzz"
		got, ok := v.CurrentFileLegend()
		if !ok || &got[0] != &findLegendNoMatches[0] {
			t.Errorf("CurrentFileLegend() = (%v, %v), want findLegendNoMatches", got, ok)
		}
	})

	t.Run("copy mode", func(t *testing.T) {
		reset()
		e.CopyMode = true
		got, ok := v.CurrentFileLegend()
		if !ok || &got[0] != &fileLegendCopyModeOn[0] {
			t.Errorf("CurrentFileLegend() = (%v, %v), want fileLegendCopyModeOn", got, ok)
		}
	})

	reset()
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
	if !strings.HasPrefix(row, "3L three.txt") {
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
	if !strings.HasPrefix(row, "1L one.txt") {
		t.Fatalf("title row = %q, want \"1L\"", row)
	}
}

// TestDrawFileTitleBarPathAlignsWithContent guards the file title bar's
// path against drifting out of column alignment with the preview
// content below it: both start at x0+gutterWidth(e), so a viewer's eye
// can track a single vertical line from the path straight down into the
// code.
func TestDrawFileTitleBarPathAlignsWithContent(t *testing.T) {
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
	e := files.DisplayedEntry()
	waitEntryReady(t, e)

	v.Draw(0, 0, w, h, true)
	sim.Show()

	titleRow := rowText(sim, 0, w)
	pathStart := strings.Index(titleRow, "three.txt")
	if pathStart < 0 {
		t.Fatalf("test setup: %q not found in title row %q", "three.txt", titleRow)
	}
	contentRow := rowText(sim, 1, w)
	contentStart := strings.Index(contentRow, "one")
	if contentStart < 0 {
		t.Fatalf("test setup: %q not found in content row %q", "one", contentRow)
	}
	if pathStart != contentStart {
		t.Fatalf("path starts at column %d, content starts at column %d, want equal (gutterWidth=%d)", pathStart, contentStart, gutterWidth(e.(*entry.TextEntry)))
	}
}

// TestDrawFileTitleBarBoldsPath guards the file title bar's own
// root-relative path rendering bolded, distinct from the plain-weight
// line-count prefix ahead of it: "NL" stays normal weight while
// "three.txt" itself bolds.
func TestDrawFileTitleBarBoldsPath(t *testing.T) {
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
	start := strings.Index(row, "three.txt")
	if start < 0 {
		t.Fatalf("test setup: %q not found in title row %q", "three.txt", row)
	}
	for x := range start {
		if _, _, attr := cellStyle(sim, x, 0).Decompose(); attr&tcell.AttrBold != 0 {
			t.Errorf("column %d (before path) is bold, want not bold", x)
		}
	}
	for x := start; x < start+len("three.txt"); x++ {
		if _, _, attr := cellStyle(sim, x, 0).Decompose(); attr&tcell.AttrBold == 0 {
			t.Errorf("column %d (inside path) not bold, want bold", x)
		}
	}
}

// waitEntryReady blocks until e's content is ready to render (its
// background stream pass has finished and, for TierHighlighted, been
// synced into Lines/Segs) — the real app never opens the goto-line
// prompt or scrolls before this point either (contentReady/gotoLineBlocked
// gate on the same signal), so tests exercising rendering/scrolling
// against a freshly-opened entry wait for it the same way.
func waitEntryReady(t *testing.T, e entry.Entry) {
	t.Helper()
	te, ok := e.(*entry.TextEntry)
	if !ok {
		return
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if te.ContentReady() {
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
func openTierPlainText(t *testing.T, numLines int) (*openfiles.List, *entry.TextEntry) {
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
	e := files.DisplayedEntry().(*entry.TextEntry)
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

	textFileView{}.DrawContent(v, e, 0, 0, 60, 10)
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
	textFileView{}.DrawContent(v, e, 0, 0, 60, 10)

	textFileView{}.Scroll(v, e, 3)
	if got := currentTopLine(e); got != 4 {
		t.Fatalf("expected top line 4 after scrolling 3, got %d", got)
	}
}

// TestScrollBumpsAtRestEdges guards the scroll-edge "bump" cue (SPEC.md
// §2.1): scrolling further in a direction that's already fully clamped
// records a flash for that edge, but ordinary in-bounds scrolling — and
// the scroll that merely reaches an edge for the first time, rather than
// pushing past an already-at-rest one — does not.
func TestScrollBumpsAtRestEdges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "many.txt")
	var content strings.Builder
	for i := 1; i <= 20; i++ {
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
	waitEntryReady(t, e)
	v := newTestPreview(files, 60, 5)
	textFileView{}.DrawContent(v, e, 0, 0, 60, 5)

	textFileView{}.Scroll(v, e, -1) // already at the top: pushing further up bumps
	if v.TopBumpPath != e.Path() {
		t.Fatalf("expected top bump after scrolling up from the top, got TopBumpPath=%q", v.TopBumpPath)
	}
	if v.BottomBumpPath != "" {
		t.Fatalf("expected no bottom bump yet, got BottomBumpPath=%q", v.BottomBumpPath)
	}

	v.TopBumpPath = ""
	textFileView{}.Scroll(v, e, 1) // ordinary downward scroll within bounds: no bump
	if v.TopBumpPath != "" || v.BottomBumpPath != "" {
		t.Fatalf("expected no bump from an in-bounds scroll, got top=%q bottom=%q", v.TopBumpPath, v.BottomBumpPath)
	}

	textFileView{}.Scroll(v, e, 100) // reaches the bottom for the first time: no bump yet
	if v.BottomBumpPath != "" {
		t.Fatalf("expected no bottom bump on first reaching the bottom, got BottomBumpPath=%q", v.BottomBumpPath)
	}
	textFileView{}.Scroll(v, e, 1) // pushes past the bottom again: bumps
	if v.BottomBumpPath != e.Path() {
		t.Fatalf("expected bottom bump after scrolling past the last line, got BottomBumpPath=%q", v.BottomBumpPath)
	}
}

// TestScrollBumpsAtRestEdgesForTierPlainText mirrors
// TestScrollBumpsAtRestEdges for TierPlainText, whose scroll target is
// source lines rather than display rows (SPEC.md §2.1's TierPlainText
// carve-out) — the bump condition (target == cur) applies identically
// either way.
func TestScrollBumpsAtRestEdgesForTierPlainText(t *testing.T) {
	files, e := openTierPlainText(t, 20)
	v := newTestPreview(files, 60, 5)
	textFileView{}.DrawContent(v, e, 0, 0, 60, 5)

	textFileView{}.Scroll(v, e, -1)
	if v.TopBumpPath != e.Path() {
		t.Fatalf("expected top bump for TierPlainText already at line 1, got TopBumpPath=%q", v.TopBumpPath)
	}
}

// TestDrawContentFlashesEdgeRowOnBump guards the actual visual cue: once
// a bump is recorded, drawContent reverse-video flashes the
// corresponding edge row (canvas.FlashRow) for the currently-displayed
// entry.
func TestDrawContentFlashesEdgeRowOnBump(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "many.txt")
	var content strings.Builder
	for i := 1; i <= 20; i++ {
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
	waitEntryReady(t, e)

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	w, h := 60, 5
	sim.SetSize(w, h)
	v := &Preview{Shared: &Shared{Files: files, Canvas: canvas.New(sim)}}

	textFileView{}.DrawContent(v, e, 0, 0, w, h)
	textFileView{}.Scroll(v, e, -1) // already at the top: bumps
	textFileView{}.DrawContent(v, e, 0, 0, w, h)
	sim.Show()

	_, _, attr := cellStyle(sim, 0, 0).Decompose()
	if attr&tcell.AttrReverse == 0 {
		t.Fatalf("expected the top row to be reverse-video flashed after a top bump")
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
// picked up by textFileView.SyncFindScan (i.e. FindScan cleared back
// to nil), the same signal the real Draw loop's per-frame sync relies
// on.
func waitFindScanDone(t *testing.T, v *Preview, e *entry.TextEntry) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		textFileView{}.SyncFindScan(v, e)
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

	textFileView{}.PerformFind(v, e, "line 7")
	if e.FindScan == nil {
		t.Fatal("expected PerformFind to start a background scan for a TierPlainText entry")
	}
	if len(e.FindMatches) != 0 || e.FindCurrent != -1 {
		t.Fatalf("expected no matches yet while the scan is in flight, got FindMatches=%v FindCurrent=%d", e.FindMatches, e.FindCurrent)
	}
}

func TestTierPlainTextFindScanResultsSyncOnceDone(t *testing.T) {
	files, e := openTierPlainText(t, 10)
	v := newTestPreview(files, 60, 10)

	textFileView{}.PerformFind(v, e, "line 7")
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

	textFileView{}.PerformFind(v, e, "line 7")
	if e.FindScan == nil {
		t.Fatal("expected a scan to be in flight")
	}
	textFileView{}.ClearFind(v, e)
	if e.FindScan != nil || e.FindQuery != "" {
		t.Fatalf("expected ClearFind to cancel and clear the in-flight scan, got FindScan=%v FindQuery=%q", e.FindScan, e.FindQuery)
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
