package views

import (
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/preview"
	"github.com/nitti/dirtree/internal/ui/canvas"
)

// windowLines is how many source lines are fetched at once for a
// TierPlainText entry's on-screen window (docs/STREAMING_PREVIEW_
// DESIGN.md §8) — generous relative to any realistic terminal height, so
// ordinary scrolling within it doesn't need a refetch on every keypress.
// Deliberately a simple fixed size rather than an incremental margin
// scheme: the simplest thing that works, left to be tuned against real
// usage later rather than guessed upfront (the same stance the design
// doc's own §11 takes toward its still-unverified timing assumptions).
const windowLines = 2000

// windowMargin is how far above a newly-requested target line a
// TierPlainText window starts, so ordinary Up/Down scrolling has room to
// move before the window needs refetching again.
const windowMargin = 200

// handleGotoPromptKey handles input while the goto-line/goto-offset
// prompt is open (SPEC.md §2.1, §2.1a): shared prompt state (GotoInput)
// for both, since the two are mutually exclusive on which entry is
// displayed but otherwise identical — a text entry's Enter jumps to a
// line number, a TierBinary entry's jumps to a byte offset (accepting
// hex digits and an "0x" prefix in addition to plain decimal digits).
// Backspace, Ctrl+U (clear), and Escape (cancel without changing the
// viewport) behave the same either way.
func (v *Preview) handleGotoPromptKey(ev *tcell.EventKey) {
	e := v.Files.DisplayedEntry()
	isHex := e != nil && e.Tier == preview.TierBinary
	switch {
	case ev.Key() == tcell.KeyEscape:
		v.GotoPromptOpen = false
	case ev.Key() == tcell.KeyEnter:
		if isHex {
			v.gotoOffset(v.GotoInput)
		} else {
			v.gotoLine(v.GotoInput)
		}
		v.GotoPromptOpen = false
	case ev.Key() == tcell.KeyBackspace, ev.Key() == tcell.KeyBackspace2:
		if len(v.GotoInput) > 0 {
			v.GotoInput = v.GotoInput[:len(v.GotoInput)-1]
		}
	case ev.Key() == tcell.KeyCtrlU:
		v.GotoInput = ""
	case isHex && isHexOffsetRune(ev.Rune()):
		v.GotoInput += string(ev.Rune())
	case !isHex && ev.Rune() >= '0' && ev.Rune() <= '9':
		v.GotoInput += string(ev.Rune())
	}
}

// isHexOffsetRune reports whether r is acceptable input for the
// goto-offset prompt (SPEC.md §2.1a): decimal digits, hex digits, and
// 'x'/'X' so a "0x"-prefixed hex offset can be typed — parseOffset
// (hexview.go) decides afterward whether the full input is actually
// valid, the same "accept broadly while typing, reject on submit"
// approach the rest of the app's free-text prompts take.
func isHexOffsetRune(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') || r == 'x' || r == 'X'
}

// scroll scrolls the currently-displayed entry by delta display rows
// (SPEC.md §2.1), clamped so it never goes negative or past the point
// where the last display row would leave the viewport. A no-op at the
// empty state (no displayed entry) or while its content isn't ready yet
// (docs/STREAMING_PREVIEW_DESIGN.md §4, §8).
//
// For a TierPlainText entry, delta is instead treated as a number of
// source lines rather than display rows (§8's "a real change to the
// preview view's scroll model," flagged and accepted here as a
// deliberate simplification, since that tier's window only ever holds a
// slice of the file rather than a whole-file wrap cache to walk display
// rows against) — Up/Down and Page Up/Page Down all move by source line
// count for that tier.
func (v *Preview) scroll(delta int) {
	e := v.Files.DisplayedEntry()
	if e == nil || !contentReady(e) {
		return
	}
	width := v.computedWidth()
	if e.Tier == preview.TierPlainText {
		cur := currentTopLine(e)
		target := clamp(cur+delta, 1, bestLineCount(e))
		if target == cur {
			v.bumpEdge(e, delta)
		}
		v.ensureWindow(e, width, target)
		v.setScrollToLine(e, target)
		return
	}
	v.ensureWrapped(e, width)
	old := e.Scroll
	e.Scroll = clamp(e.Scroll+delta, 0, v.maxScroll(e, v.viewportHeight()))
	if e.Scroll == old {
		v.bumpEdge(e, delta)
	}
}

// bumpEdge records a scroll attempt (from scroll, above) that pushed
// further than e's content allows, in the direction delta indicates:
// negative means already at the top, positive means already at the
// bottom. A no-op for delta == 0, which scroll never actually passes.
func (v *Preview) bumpEdge(e *openfiles.Entry, delta int) {
	switch {
	case delta < 0:
		v.TopBumpPath = e.Path
		v.TopBumpFlashStart = time.Now()
	case delta > 0:
		v.BottomBumpPath = e.Path
		v.BottomBumpFlashStart = time.Now()
	}
}

// gotoLine jumps the currently-displayed entry's scroll to the source
// line's first display row (SPEC.md §2.1), clamped to [1, total source
// lines]. A no-op if input is empty or there's no displayed entry.
func (v *Preview) gotoLine(input string) {
	e := v.Files.DisplayedEntry()
	if input == "" || e == nil {
		return
	}
	n := 0
	for _, r := range input {
		n = n*10 + int(r-'0')
	}
	v.ScrollToLine(e, n)
}

// ScrollToLine jumps e's scroll to source line n's first display row
// (SPEC.md §2.1), clamped to [1, total source lines]. Used by both the
// goto-line prompt and, via App's dispatcher, content search's
// jump-to-hit (§9.2) — search has no access to this view's wrap-cache
// state, so App calls this directly rather than search calling it
// itself. A no-op while e's content isn't ready yet (goto-line's own
// gating already prevents this for the goto-line prompt itself, SPEC.md
// §2.1; this guards the same for a jump arriving some other way).
func (v *Preview) ScrollToLine(e *openfiles.Entry, n int) {
	if !contentReady(e) {
		return
	}
	width := v.computedWidth()
	n = clamp(n, 1, bestLineCount(e))
	if e.Tier == preview.TierPlainText {
		v.ensureWindow(e, width, n)
	} else {
		v.ensureWrapped(e, width)
	}
	v.setScrollToLine(e, n)
}

// setScrollToLine sets e.Scroll to source line n's first display row
// within e's currently-loaded content (the whole file for
// TierHighlighted, e's current window for TierPlainText — WindowStartLine
// is always 0 for the former, so this is n-1 there, matching the
// pre-windowing behavior exactly).
func (v *Preview) setScrollToLine(e *openfiles.Entry, n int) {
	if row, ok := e.FirstRow[n-1-e.WindowStartLine]; ok {
		e.Scroll = clamp(row, 0, v.maxScroll(e, v.viewportHeight()))
	}
}

func (v *Preview) maxScroll(e *openfiles.Entry, viewportHeight int) int {
	return max(len(e.Rows)-viewportHeight, 0)
}

// currentTopLine returns the 1-based source line currently at the top of
// e's viewport, derived from e.Scroll's row within e's currently-loaded
// window — used to compute a TierPlainText entry's scroll target in
// source-line units (§8).
func currentTopLine(e *openfiles.Entry) int {
	if e.Rows != nil && e.Scroll >= 0 && e.Scroll < len(e.Rows) {
		return e.WindowStartLine + e.Rows[e.Scroll].SourceLine + 1
	}
	return e.WindowStartLine + 1
}

// bestLineCount returns the best currently-known total line count for e:
// the full resident line count for TierHighlighted, or the background
// stream's line count for TierPlainText — exact once done, which is the
// only time this is consulted for that tier (content isn't considered
// ready, and so isn't scrolled/goto-lined, before then — §4, §8).
func bestLineCount(e *openfiles.Entry) int {
	if e.Tier == preview.TierHighlighted {
		return max(len(e.Lines), 1)
	}
	_, lineCount, _ := e.Stream.Snapshot()
	return max(lineCount, 1)
}

// contentReady syncs a TierHighlighted entry's content from its
// background stream if not already done (a cheap no-op once synced, or
// while the pass is still running) and reports whether e's
// tier-appropriate content is available to render, scroll, goto-line, or
// find against: for TierHighlighted, once Lines is populated; for
// TierPlainText, once the background pass has finished. This stage
// gates TierPlainText's windowed reading on that same "pass fully done"
// signal goto-line already gates on (SPEC.md §2.1, docs/STREAMING_
// PREVIEW_DESIGN.md §4), rather than the design's more ambitious
// progressive-availability aspiration ("a jump ahead of where the pass
// has reached can still show plain-text content immediately") — a
// deliberate simplification for this stage, flagged in SPEC.md.
func contentReady(e *openfiles.Entry) bool {
	if e.Tier == preview.TierHighlighted {
		e.SyncContent()
		return e.Lines != nil
	}
	return e.Stream != nil && e.Stream.Done()
}

// ensureWindow fetches (or reuses) a TierPlainText entry's on-screen
// window so it covers targetLine, then wraps it at width (§8). A no-op
// if targetLine is already within the currently-loaded window and width
// hasn't changed; if only width changed, the existing window is
// rewrapped without a fresh disk read.
func (v *Preview) ensureWindow(e *openfiles.Entry, width, targetLine int) {
	offsets, lineCount, done := e.Stream.Snapshot()
	if !done {
		return
	}
	targetLine = clamp(targetLine, 1, max(lineCount, 1))
	windowEnd := e.WindowStartLine + len(e.Lines)
	inWindow := e.Lines != nil && targetLine-1 >= e.WindowStartLine && targetLine-1 < windowEnd
	if inWindow {
		if e.RowsWidth != width {
			e.Rows, e.FirstRow = preview.BuildDisplayRows(e.Segs, width)
			e.RowsWidth = width
		}
		return
	}

	start := max(0, targetLine-1-windowMargin)
	if lineCount-start < windowLines {
		start = max(0, lineCount-windowLines)
	}
	count := min(windowLines, lineCount-start)
	lines, err := preview.ReadWindow(e.Path, offsets, start, count)
	if err != nil {
		lines = nil
	}
	segs := make([][]preview.Segment, len(lines))
	for i, l := range lines {
		segs[i] = []preview.Segment{{Text: l, Category: preview.CategoryText}}
	}
	e.Lines = lines
	e.Segs = segs
	e.WindowStartLine = start
	e.Rows, e.FirstRow = preview.BuildDisplayRows(e.Segs, width)
	e.RowsWidth = width
}

func (v *Preview) viewportHeight() int {
	_, h := v.Canvas.Size()
	height := h - 1 // header row
	if v.Files.DisplayedEntry() != nil {
		height-- // file title bar row, shown whenever a file is displayed
	}
	if v.GotoPromptOpen {
		height--
	}
	return height
}

// computedWidth returns the content width (in columns) available to
// the preview's wrapped text at the primary preview view when no
// overlay is active (full terminal width). This is only used by the
// scroll/goto-line key handlers, which are only reachable in that
// context.
func (v *Preview) computedWidth() int {
	w, _ := v.Canvas.Size()
	e := v.Files.DisplayedEntry()
	if e == nil {
		return w
	}
	return max(w-gutterWidth(e), 1)
}

// gutterWidth returns the line-number gutter's width for e:
// canvas.GutterWidth's normal computation, or 0 while e is in copy mode
// (SPEC.md §2.1) and stripping the gutter out of its preview entirely.
// Shared by computedWidth and Draw so both agree on the same content
// width — otherwise they'd race to invalidate each other's wrap cache
// every frame.
//
// For a TierPlainText entry, there's no exact line count until its
// background pass finishes — render against the best current lower
// bound instead (docs/STREAMING_PREVIEW_DESIGN.md §4), floored at a
// minimum width of 4 digits so the large majority of files (which end up
// under 10,000 lines) never visibly resize their gutter at all.
func gutterWidth(e *openfiles.Entry) int {
	if e.CopyMode {
		return 0
	}
	if e.Tier == preview.TierPlainText {
		_, lineCount, done := e.Stream.Snapshot()
		if !done && lineCount < 9999 {
			lineCount = 9999
		}
		return canvas.GutterWidth(lineCount)
	}
	return canvas.GutterWidth(len(e.Lines))
}

// ensureWrapped recomputes e's wrapped display rows if width has
// changed since they were last computed (SPEC.md §2.1: "wrapping must
// be recomputed whenever the available width changes"), caching the
// result on the entry so it's not redone every frame. Copy mode wraps
// the same way normal display does — an earlier version of copy mode
// disabled wrapping entirely and clipped long lines instead, but that
// meant anything past the pane's right edge was never drawn at all,
// making it impossible to select the rest of the line by any means;
// word-wrapping (SPEC.md §2.1) keeps every character of the line
// visible and selectable somewhere on screen, which matters more than
// avoiding the extra line break a multi-row selection can pick up at a
// wrap point — an inherent limitation of any fixed-width terminal grid,
// not something copy mode can fully solve either way.
func (v *Preview) ensureWrapped(e *openfiles.Entry, width int) {
	if e.RowsWidth == width && e.Rows != nil {
		return
	}
	e.Rows, e.FirstRow = preview.BuildDisplayRows(e.Segs, width)
	e.RowsWidth = width
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// gotoLineBlocked reports whether goto-line should be blocked because
// the displayed entry's background stream pass hasn't finished yet
// (SPEC.md §2.1, docs/STREAMING_PREVIEW_DESIGN.md §4): pressing `g`
// doesn't open the prompt at all in that case. A pure function of just
// "is a stream present" and "is it done" (rather than taking
// *preview.StreamIndex directly) so it's testable without spinning up a
// real background pass — streamPresent false (no stream tracked at all)
// never blocks.
func gotoLineBlocked(streamPresent, streamDone bool) bool {
	return streamPresent && !streamDone
}
