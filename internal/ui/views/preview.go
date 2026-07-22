package views

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/find"
	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/preview"
	"github.com/nitti/dirtree/internal/spinner"
	"github.com/nitti/dirtree/internal/tree"
	"github.com/nitti/dirtree/internal/ui/canvas"
)

// streamSpinnerMinDisplayDuration mirrors the corner badge's own
// minimum-display-duration floor (internal/ui's spinnerMinDisplayDuration,
// SPEC.md §5.2/§5.3): once the file-legend "building…" indicator has
// crossed the perceptibility threshold, it stays legible for at least
// this long even if the background stream pass finishes sooner. Kept as
// its own constant rather than importing the ui package's (which would
// create an import cycle, since ui already imports views) — same value,
// same rationale, one per animated indicator.
const streamSpinnerMinDisplayDuration = 1 * time.Second

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

// goto-line's block-flash color reuses canvas.FlashDuration (SPEC.md
// §5.2's red-flash convention, applied here to the file title bar rather
// than a list row).

// fileLegend lists actions specific to the currently-displayed file (as
// opposed to app-wide navigation), shown in the file title bar rather
// than the global menu bar (§5.2) — new file-specific actions belong
// here going forward.
var fileLegend = []canvas.LegendEntry{
	{Text: "[/] find", Priority: 1},
	{Text: "[g] goto line", Priority: 2},
	{Text: "[c] copy mode", Priority: 2},
}

// fileLegendCopyModeOn replaces fileLegend once copy mode is active
// (§2.1): goto-line and find are omitted since the point of copy mode
// is a screen with nothing on it but the file's own text, and
// scrolling/goto/find remain reachable via their own keys regardless of
// whether they're listed here, the same way arrow-key scrolling already
// is.
var fileLegendCopyModeOn = []canvas.LegendEntry{
	{Text: "[c] normal view", Priority: 1},
}

// gotoLegend documents the goto-line prompt's own actions (SPEC.md
// §5.2): Return jumps to the entered line and closes the prompt,
// Ctrl+U clears the entered digits, Escape cancels without changing
// scroll.
var gotoLegend = []canvas.LegendEntry{
	{Text: "[return] jump", Priority: 1},
	{Text: "[ctrl+u] clear", Priority: 2},
	{Text: "[esc] cancel", Priority: 1},
}

// findPromptLegend documents the in-file find prompt's own actions
// (SPEC.md §2.4): Return executes the search and closes the prompt,
// Ctrl+U clears the query, Escape cancels leaving any existing find
// state unchanged.
var findPromptLegend = []canvas.LegendEntry{
	{Text: "[return] search", Priority: 1},
	{Text: "[ctrl+u] clear", Priority: 2},
	{Text: "[esc] cancel", Priority: 1},
}

var findLegend = []canvas.LegendEntry{
	{Text: "[n] next", Priority: 1},
	{Text: "[N] prev", Priority: 1},
	{Text: "[esc] clear", Priority: 1},
}

// findLegendNoMatches is shown instead of findLegend when a find's
// query matched nothing — there's no next/previous to step between, but
// esc still clears it back to the idle file title bar.
var findLegendNoMatches = []canvas.LegendEntry{
	{Text: "[esc] clear", Priority: 1},
}

// Action is a one-shot signal Preview.HandleKey returns for the
// handful of keys that need something only App's dispatcher can do —
// transitioning to a view Preview holds no reference to (browser,
// open-files), running QuickOpen/Search's own Open() setup, or quitting
// (App.quit isn't part of Shared, since nothing else needs to touch it).
// Every other view's HandleKey can perform its own transitions directly
// through the shared *Overlay pointer; this return-value form is
// specific to the primary view because it's the one place five
// different exits converge, and a return value scales to that better
// than five more shared pointer fields on Preview would.
type Action int

// The Action values: ActionNone means HandleKey handled the key itself
// and there's nothing further for App to do.
const (
	ActionNone Action = iota
	ActionOpenBrowser
	ActionOpenFiles
	ActionOpenQuickOpen
	ActionOpenSearch
	ActionQuit
)

// Preview holds the primary preview view's own state (SPEC.md §2.1,
// §2.4): the goto-line prompt and in-file find prompt. The displayed
// entry's scroll position and wrapped-row cache live per-entry on
// openfiles.Entry instead, since they're tracked per open file rather
// than per view.
type Preview struct {
	*Shared

	GotoPromptOpen bool
	GotoInput      string

	// GotoBlockedPath/GotoBlockedFlashStart track a `g` pressed while the
	// displayed entry's background stream pass (docs/STREAMING_PREVIEW_
	// DESIGN.md §4) isn't done yet: the goto-line prompt doesn't open at
	// all, and the file title bar instead flashes red and shows an
	// inline "still indexing" message, the same red-flash-plus-inline-
	// message convention SPEC.md §2.2/§5.2 already uses for a failed file
	// open. GotoBlockedPath is keyed by path so switching to a different
	// entry doesn't carry a stale flash/message over; the message itself
	// is shown only while that same entry's stream is still not done —
	// once the pass finishes, the reason for it no longer holds, so it
	// stops being shown without needing anything to explicitly clear it.
	GotoBlockedPath       string
	GotoBlockedFlashStart time.Time

	FindPromptOpen bool
	FindInput      string
}

// HandleKey handles input at the primary preview view when no overlay
// is active: reaching the browser, quick open, and open-files overlays,
// preview scrolling/goto-line/in-file-find, and quitting (SPEC.md §7).
// `b` only opens the browser overlay (SPEC.md §5.1); closing it back to
// this view is Escape's job alone, handled by the browser view itself
// (quick open, opened by `o`, is likewise not a toggle). Escape does
// not quit and there is no overlay to back out of here (`q` is the only
// way to quit, so an accidental Escape press can't lose the session's
// open-files state) — its only effect at this view is clearFind,
// clearing an active in-file find if there is one, and otherwise
// remaining a no-op. Reaching another view (`b`/Tab/`o`/`s`) and
// quitting (`q`) are reported via the returned Action so App's
// dispatcher, which owns Overlay transitions and QuickOpen/Search's own
// Open() setup, can perform them.
func (v *Preview) HandleKey(ev *tcell.EventKey) Action {
	if v.GotoPromptOpen {
		v.handleGotoPromptKey(ev)
		return ActionNone
	}
	if v.FindPromptOpen {
		v.handleFindPromptKey(ev)
		return ActionNone
	}

	switch {
	case ev.Rune() == 'b':
		return ActionOpenBrowser
	case ev.Key() == tcell.KeyTab:
		return ActionOpenFiles
	case ev.Rune() == 'o':
		return ActionOpenQuickOpen
	case ev.Rune() == 's':
		return ActionOpenSearch
	case ev.Rune() == 'q':
		return ActionQuit
	case ev.Key() == tcell.KeyUp:
		v.scroll(-1)
	case ev.Key() == tcell.KeyDown:
		v.scroll(1)
	case ev.Key() == tcell.KeyPgUp:
		v.scroll(-v.viewportHeight())
	case ev.Key() == tcell.KeyPgDn:
		v.scroll(v.viewportHeight())
	case ev.Rune() == 'g':
		if e := v.Files.DisplayedEntry(); e != nil {
			if gotoLineBlocked(e.Stream != nil, e.Stream != nil && e.Stream.Done()) {
				v.GotoBlockedPath = e.Path
				v.GotoBlockedFlashStart = time.Now()
			} else {
				v.GotoPromptOpen = true
				v.GotoInput = ""
			}
		}
	case ev.Rune() == '/':
		// In-file find over a TierPlainText entry is not yet implemented
		// (docs/STREAMING_PREVIEW_DESIGN.md §9 scopes it to a later
		// stage) — `/` is a no-op there for now, the same "don't offer a
		// key that doesn't do anything yet" rule the rest of the app
		// follows for unavailable actions.
		if e := v.Files.DisplayedEntry(); e != nil && e.Tier != preview.TierPlainText {
			v.FindPromptOpen = true
			v.FindInput = ""
		}
	case ev.Rune() == 'c':
		if e := v.Files.DisplayedEntry(); e != nil {
			e.CopyMode = !e.CopyMode
		}
	case ev.Rune() == 'n':
		v.findStep(1)
	case ev.Rune() == 'N':
		v.findStep(-1)
	case ev.Key() == tcell.KeyEscape:
		v.clearFind()
	}
	return ActionNone
}

// clearFind clears the displayed entry's in-file find state (SPEC.md
// §2.4), if any — its query, matches, current index, and wrap note —
// so its highlighting and file-title-bar status disappear, leaving the
// idle file title bar in their place. Bound to Escape at the primary
// preview view: this does not conflict with Escape's deliberate
// no-op-when-nothing-to-back-out-of behavior there (it still never
// quits — only `q` does), it just gives find an explicit way out, since
// otherwise it would persist until superseded by a new search on the
// same entry. A no-op if there's no displayed entry or no active find,
// so Escape stays inert exactly when there was nothing to clear.
func (v *Preview) clearFind() {
	e := v.Files.DisplayedEntry()
	if e == nil || e.FindQuery == "" {
		return
	}
	e.FindQuery = ""
	e.FindMatches = nil
	e.FindCurrent = -1
	e.FindWrapNote = ""
}

// handleGotoPromptKey handles input while the goto-line prompt is open
// (SPEC.md §2.1): only digits, backspace, and Ctrl+U (clear) are
// accepted, Enter jumps to the entered line, Escape cancels without
// changing scroll.
func (v *Preview) handleGotoPromptKey(ev *tcell.EventKey) {
	switch {
	case ev.Key() == tcell.KeyEscape:
		v.GotoPromptOpen = false
	case ev.Key() == tcell.KeyEnter:
		v.gotoLine(v.GotoInput)
		v.GotoPromptOpen = false
	case ev.Key() == tcell.KeyBackspace, ev.Key() == tcell.KeyBackspace2:
		if len(v.GotoInput) > 0 {
			v.GotoInput = v.GotoInput[:len(v.GotoInput)-1]
		}
	case ev.Key() == tcell.KeyCtrlU:
		v.GotoInput = ""
	case ev.Rune() >= '0' && ev.Rune() <= '9':
		v.GotoInput += string(ev.Rune())
	}
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
		target := clamp(currentTopLine(e)+delta, 1, bestLineCount(e))
		v.ensureWindow(e, width, target)
		v.setScrollToLine(e, target)
		return
	}
	v.ensureWrapped(e, width)
	e.Scroll = clamp(e.Scroll+delta, 0, v.maxScroll(e, v.viewportHeight()))
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

// handleFindPromptKey handles input while the in-file find prompt is
// open (SPEC.md §2.4): any printable character is accepted (unlike
// goto-line's digits-only prompt, since a search query is free text),
// Enter executes the search, Escape cancels the prompt without
// changing the entry's existing find state (if any).
func (v *Preview) handleFindPromptKey(ev *tcell.EventKey) {
	switch {
	case ev.Key() == tcell.KeyEscape:
		v.FindPromptOpen = false
	case ev.Key() == tcell.KeyEnter:
		v.performFind(v.FindInput)
		v.FindPromptOpen = false
	case ev.Key() == tcell.KeyBackspace, ev.Key() == tcell.KeyBackspace2:
		if len(v.FindInput) > 0 {
			r := []rune(v.FindInput)
			v.FindInput = string(r[:len(r)-1])
		}
	case ev.Key() == tcell.KeyCtrlU:
		v.FindInput = ""
	case ev.Rune() != 0 && ev.Key() == tcell.KeyRune:
		v.FindInput += string(ev.Rune())
	}
}

// performFind executes an in-file find (SPEC.md §2.4): locates every
// case-insensitive match of query in the displayed entry's lines, then
// jumps to the first one at or after the source line currently at the
// top of the viewport — the same "search forward from here" behavior
// as `less` — wrapping to the very first match (and noting the wrap) if
// none exists at or after that point. A no-op if there's no displayed
// entry; an empty query clears any existing find state instead of
// searching (mirroring a bare "/" + Enter in `less`).
func (v *Preview) performFind(query string) {
	e := v.Files.DisplayedEntry()
	if e == nil {
		return
	}
	v.ensureWrapped(e, v.computedWidth())

	e.FindQuery = query
	e.FindMatches = find.InLines(e.Lines, query)
	e.FindWrapNote = ""
	if len(e.FindMatches) == 0 {
		e.FindCurrent = -1
		return
	}

	startLine := 0
	if e.Scroll >= 0 && e.Scroll < len(e.Rows) {
		startLine = e.Rows[e.Scroll].SourceLine
	}
	idx := 0
	for i, m := range e.FindMatches {
		if m.Line >= startLine {
			idx = i
			break
		}
	}
	if e.FindMatches[idx].Line < startLine {
		e.FindWrapNote = "wrapped to top"
	}
	e.FindCurrent = idx
	v.scrollToFindMatch(e)
}

// findStep moves the current match by delta (+1 for `n`/next, -1 for
// `N`/previous), wrapping around at either end and noting the wrap
// (SPEC.md §2.4) — the same wraparound stepper the browser and finder
// overlays already use (internal/tree.MoveSelection). A no-op if
// there's no displayed entry or it has no matches.
func (v *Preview) findStep(delta int) {
	e := v.Files.DisplayedEntry()
	if e == nil || len(e.FindMatches) == 0 {
		return
	}
	next := tree.MoveSelection(e.FindCurrent, delta, len(e.FindMatches))
	switch {
	case delta > 0 && next < e.FindCurrent:
		e.FindWrapNote = "wrapped to top"
	case delta < 0 && next > e.FindCurrent:
		e.FindWrapNote = "wrapped to bottom"
	default:
		e.FindWrapNote = ""
	}
	e.FindCurrent = next
	v.scrollToFindMatch(e)
}

// scrollToFindMatch scrolls e just enough to bring its current find
// match into view (the same "only scroll if it's not already visible"
// rule the browser uses for its own selection, SPEC.md §5.2), a no-op
// if there is no current match.
func (v *Preview) scrollToFindMatch(e *openfiles.Entry) {
	if e.FindCurrent < 0 || e.FindCurrent >= len(e.FindMatches) {
		return
	}
	row := findMatchRow(e, e.FindMatches[e.FindCurrent])
	if row < 0 {
		return
	}
	h := v.viewportHeight()
	if row < e.Scroll {
		e.Scroll = row
	}
	if row >= e.Scroll+h {
		e.Scroll = row - h + 1
	}
	e.Scroll = clamp(e.Scroll, 0, v.maxScroll(e, h))
}

// findMatchRow returns the index into e.Rows of the specific wrapped
// row m falls in (not just its source line's first row, since a long
// line's match may land in a continuation row) — ensureWrapped must
// already have been called. Falls back to the source line's first row
// if m's column can't be located in any of its rows (shouldn't happen
// for a match found in e.Lines, but degrades safely rather than losing
// the jump entirely).
func findMatchRow(e *openfiles.Entry, m find.Match) int {
	start, ok := e.FirstRow[m.Line]
	if !ok {
		return -1
	}
	for i := start; i < len(e.Rows); i++ {
		r := e.Rows[i]
		if r.SourceLine != m.Line {
			break
		}
		rowLen := preview.SegmentsRuneLen(r.Segments)
		if m.Col >= r.ColStart && m.Col < r.ColStart+max(rowLen, 1) {
			return i
		}
	}
	return start
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

// Draw renders the primary preview view's file title bar and content
// (SPEC.md §2.1) into the (x0, y0)-(x0+w, y0+h) rectangle: the title
// bar occupies its own row when a file is displayed, and the content
// below it is a line-number gutter plus wrapped, highlighted rows for
// the currently-displayed entry, or an explanatory empty-state message
// if none is displayed. interactive is false while another overlay
// (e.g. open-files-list, SPEC.md §2.3) owns input and this pane is
// read-only, accepting neither scrolling nor goto-line — in which case
// file-specific action keys like goto-line don't apply and their
// legend is omitted rather than advertising a key that won't do
// anything right now. The goto-line prompt, when open, occupies the
// bottom row — reachable only when this is the primary (non-overlaid)
// view, since no overlay leaves the goto-line key handled while this is
// showing.
func (v *Preview) Draw(x0, y0, w, h int, interactive bool) {
	titleRows := v.drawFileTitleBar(x0, y0, w, interactive)
	v.drawContent(x0, y0+titleRows, w, h-titleRows)
}

// drawFileTitleBar renders the currently-displayed file's own title bar
// (its root-relative path) in the row above the preview content, when a
// file is displayed. Returns the number of rows it occupied (0 or 1) so
// Draw can shrink the content rectangle accordingly.
func (v *Preview) drawFileTitleBar(x0, y0, w int, interactive bool) int {
	e := v.Files.DisplayedEntry()
	if e == nil {
		return 0
	}
	rel := tree.RelativeDisplayPath(v.RootPath, e.Path)

	// copyModeTag prefixes rel whenever e is in copy mode, so that state
	// is always legible in this row regardless of which case below fires
	// (find status/prompt text otherwise has no room to also mention
	// it) — the row's own distinct style (below) reinforces this further.
	left := rel
	if e.CopyMode {
		left = "[copy mode] " + rel
	}

	gotoBlocked := interactive && v.GotoBlockedPath == e.Path && gotoLineBlocked(e.Stream != nil, e.Stream != nil && e.Stream.Done())

	var text string
	switch {
	case interactive && v.FindPromptOpen:
		text = canvas.LegendText(w, "/"+v.FindInput, findPromptLegend)
	case !interactive:
		text = left
	case gotoBlocked:
		text = canvas.LegendText(w, left, withStatus("still indexing, try again shortly", nil))
	case e.FindQuery != "" && len(e.FindMatches) > 0:
		text = canvas.LegendText(w, left, withStatus(findStatusText(e), findLegend))
	case e.FindQuery != "":
		text = canvas.LegendText(w, left, withStatus(findStatusText(e), findLegendNoMatches))
	case e.CopyMode:
		text = canvas.LegendText(w, left, fileLegendCopyModeOn)
	default:
		text = canvas.LegendText(w, rel, v.fileLegendForIdle(e))
	}

	style := canvas.StyleFileTitle
	switch {
	case e.CopyMode:
		style = canvas.StyleCopyModeTitle
	case gotoBlocked && time.Since(v.GotoBlockedFlashStart) < canvas.FlashDuration:
		style = canvas.StyleFlashError
	}
	v.Canvas.DrawText(x0, y0, w, text, style)
	return 1
}

// fileLegendForIdle returns the idle file title bar's legend for e: the
// normal fileLegend, or fileLegend with its `[g] goto line` entry
// replaced by a "building…" spinner while e's background stream pass is
// running and has crossed the perceptibility threshold
// (docs/STREAMING_PREVIEW_DESIGN.md §7) — reusing the corner badge's own
// threshold/minimum-display-duration discipline (SPEC.md §5.3) rather
// than inventing new timing rules for this second indicator.
func (v *Preview) fileLegendForIdle(e *openfiles.Entry) []canvas.LegendEntry {
	if e.Stream == nil {
		return fileLegend
	}
	elapsed, sinceDone, done := e.Stream.Elapsed(), e.Stream.SinceDone(), e.Stream.Done()
	if !streamBuildingVisible(elapsed, sinceDone, done, canvas.SpinnerThreshold, streamSpinnerMinDisplayDuration) {
		return fileLegend
	}
	frame := spinner.Frame(elapsed, canvas.SpinnerFPS, spinner.DefaultFrames)
	return buildingLegend(frame)
}

// buildingLegend is fileLegend with its goto-line entry swapped for a
// "building…" spinner (docs/STREAMING_PREVIEW_DESIGN.md §7) — goto-line
// isn't available while this is showing (SPEC.md §2.1), so advertising
// it here would be misleading the same way the rest of the app never
// lists a key that currently does nothing.
func buildingLegend(frame rune) []canvas.LegendEntry {
	return []canvas.LegendEntry{
		{Text: "[/] find", Priority: 1},
		{Text: "building " + string(frame), Priority: 2},
		{Text: "[c] copy mode", Priority: 2},
	}
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

// streamBuildingVisible is fileLegendForIdle's show/hide decision as a
// pure function of elapsed/sinceDone durations (SPEC.md §5.3's "unit-
// testable without real elapsed wall-clock time" discipline), the same
// shape spinner.BadgeDecision uses for the corner badge's own spinner
// phase — simplified since this indicator has no completion-message/
// fade-out phase of its own (docs/STREAMING_PREVIEW_DESIGN.md §7): once
// both done and the minimum display duration have been satisfied, it
// just reverts straight to the normal legend.
func streamBuildingVisible(elapsed, sinceDone time.Duration, done bool, threshold, minDisplayDuration time.Duration) bool {
	doneAt := elapsed - sinceDone
	var crossedThreshold bool
	if done {
		crossedThreshold = doneAt >= threshold
	} else {
		crossedThreshold = elapsed >= threshold
	}
	if !crossedThreshold {
		return false
	}
	if !done {
		return true
	}
	if doneAt < minDisplayDuration {
		doneAt = minDisplayDuration
	}
	return elapsed < doneAt
}

// withStatus prepends a synthetic, always-shown (priority 1) legend
// entry carrying the in-file find's live status text (query, match
// position/count, wrap note — findStatusText below) ahead of legend's
// own entries, so status and legend tier together through
// canvas.LegendFit the same way the path and the legend used to be
// concatenated directly: status is never dropped for width, exactly
// like a priority-1 legend key, while legend's own lower-priority
// entries (e.g. findLegend's, all priority 1 today) still drop first if
// it ever came to that.
func withStatus(status string, legend []canvas.LegendEntry) []canvas.LegendEntry {
	return append([]canvas.LegendEntry{{Text: status, Priority: 1}}, legend...)
}

// findStatusText renders in-file find's live status (SPEC.md §2.4): the
// query, how many matches it found and which one is current, and a
// transient note when the most recent next/previous step wrapped
// around either end of the match list.
func findStatusText(e *openfiles.Entry) string {
	if len(e.FindMatches) == 0 {
		return "/" + e.FindQuery + "  no matches"
	}
	status := fmt.Sprintf("/%s  %d/%d", e.FindQuery, e.FindCurrent+1, len(e.FindMatches))
	if e.FindWrapNote != "" {
		status += " (" + e.FindWrapNote + ")"
	}
	return status
}

// drawContent renders the primary preview view's content (SPEC.md
// §2.1) into the (x0, y0)-(x0+w, y0+h) rectangle: a line-number gutter
// plus wrapped, highlighted rows for the currently-displayed entry, or
// an explanatory empty-state message if none is displayed. The
// goto-line prompt, when open, occupies the bottom row.
func (v *Preview) drawContent(x0, y0, w, h int) {
	e := v.Files.DisplayedEntry()
	if e == nil {
		msg := "no files open — press b to browse, o to quick-open, s to search contents"
		row := y0 + max(h/2, 1)
		v.Canvas.DrawText(x0, row, w, canvas.CenterPad(msg, w), canvas.StyleNormal)
		return
	}
	if !contentReady(e) {
		msg := "building preview…"
		row := y0 + max(h/2, 1)
		v.Canvas.DrawText(x0, row, w, canvas.CenterPad(msg, w), canvas.StyleNormal)
		return
	}

	gw := gutterWidth(e)
	contentWidth := max(w-gw, 1)
	if e.Tier == preview.TierPlainText {
		v.ensureWindow(e, contentWidth, currentTopLine(e))
	} else {
		v.ensureWrapped(e, contentWidth)
	}

	viewportHeight := h
	if v.GotoPromptOpen {
		viewportHeight--
	}
	digits := gw - 2

	for row := range viewportHeight {
		y := y0 + row
		i := e.Scroll + row
		if i >= len(e.Rows) {
			break
		}
		dr := e.Rows[i]
		if gw > 0 {
			numField := strings.Repeat(" ", digits)
			if dr.HasNumber {
				numField = fmt.Sprintf("%*d", digits, e.WindowStartLine+dr.SourceLine+1)
			}
			v.Canvas.DrawText(x0, y, gw, numField+"  ", canvas.StyleNormal)
		}
		v.drawSegments(x0+gw, y, contentWidth, dr.Segments, findHighlightsForRow(e, dr), e.CopyMode)
	}

	if v.GotoPromptOpen {
		v.Canvas.DrawText(x0, y0+h-1, w, canvas.LegendText(w, "goto line: "+v.GotoInput, gotoLegend), canvas.StyleNormal)
	}
}

// findHighlight is one in-file find match's column range within a
// single wrapped display row, in row-relative rune columns (SPEC.md
// §2.4) — Current picks canvas.StyleFindCurrent over canvas.StyleFindMatch
// so the active match stands out from the rest.
type findHighlight struct {
	Start, End int
	Current    bool
}

// findHighlightsForRow returns row's portion of every in-file find
// match that overlaps it, converting each match's source-line-relative
// column range (found against e.Lines, independent of wrapping) into
// row-relative columns via the row's ColStart — a match split across
// two wrapped rows by a mid-token wrap naturally yields one highlight
// per row it touches.
func findHighlightsForRow(e *openfiles.Entry, row preview.DisplayRow) []findHighlight {
	if len(e.FindMatches) == 0 {
		return nil
	}
	rowLen := preview.SegmentsRuneLen(row.Segments)
	var out []findHighlight
	for i, m := range e.FindMatches {
		if m.Line != row.SourceLine {
			continue
		}
		start := m.Col - row.ColStart
		end := start + m.Len
		if end <= 0 || start >= rowLen {
			continue
		}
		start = max(start, 0)
		end = min(end, rowLen)
		out = append(out, findHighlight{Start: start, End: end, Current: i == e.FindCurrent})
	}
	return out
}

// drawSegments draws seg fragments left to right starting at (x, y),
// each in its category's style (SPEC.md §2.1), clipped and padded with
// spaces to exactly w columns — unless copy mode is active, in which
// case every fragment uses the plain style instead, since copy mode's
// whole point is a screen with nothing on it but the file's own
// characters. Any column covered by a highlights entry (SPEC.md §2.4's
// in-file find) still overrides that with the find-match style, copy
// mode or not — it's not literal selectable text either way, so it
// doesn't undermine copy mode's purpose, and staying visible there is
// more useful than not.
func (v *Preview) drawSegments(x, y, w int, segs []preview.Segment, highlights []findHighlight, plain bool) {
	col := 0
	for _, seg := range segs {
		style := canvas.StyleNormal
		if !plain {
			style = canvas.StyleFor(seg.Category)
		}
		for _, r := range seg.Text {
			if col >= w {
				return
			}
			v.Canvas.SetContent(x+col, y, r, highlightStyleAt(col, highlights, style))
			col++
		}
	}
	for ; col < w; col++ {
		v.Canvas.SetContent(x+col, y, ' ', canvas.StyleNormal)
	}
}

// highlightStyleAt returns the find-match style covering col, if any,
// else base.
func highlightStyleAt(col int, highlights []findHighlight, base tcell.Style) tcell.Style {
	for _, h := range highlights {
		if col >= h.Start && col < h.End {
			if h.Current {
				return canvas.StyleFindCurrent
			}
			return canvas.StyleFindMatch
		}
	}
	return base
}
