package views

import (
	"fmt"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/find"
	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/preview"
	"github.com/nitti/dirtree/internal/spinner"
	"github.com/nitti/dirtree/internal/tree"
	"github.com/nitti/dirtree/internal/ui/canvas"
)

// clearFind clears the displayed entry's in-file find state (SPEC.md
// §2.4), if any — its query, matches, current index, and wrap note —
// so its highlighting and file-title-bar status disappear, leaving the
// idle file title bar in their place. Bound to Escape at the primary
// preview view: this does not conflict with Escape's deliberate
// no-op-when-nothing-to-back-out-of behavior there (it still never
// quits — only `q` does), it just gives find an explicit way out, since
// otherwise it would persist until superseded by a new search on the
// same entry. A no-op if there's no displayed entry and no active find
// or in-progress scan, so Escape stays inert exactly when there was
// nothing to clear. Also cancels a still-running TierPlainText find scan
// (docs/STREAMING_PREVIEW_DESIGN.md §9) rather than leaving it to finish
// unread.
func (v *Preview) clearFind() {
	e := v.Files.DisplayedEntry()
	if e == nil || (e.FindQuery == "" && e.FindScan == nil) {
		return
	}
	if e.FindScan != nil {
		e.FindScan.Cancel()
		e.FindScan = nil
	}
	e.FindQuery = ""
	e.FindMatches = nil
	e.FindCurrent = -1
	e.FindWrapNote = ""
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
// case-insensitive match of query, then jumps to the first one at or
// after the source line currently at the top of the viewport — the same
// "search forward from here" behavior as `less` — wrapping to the very
// first match (and noting the wrap) if none exists at or after that
// point. A no-op if there's no displayed entry; an empty query clears
// any existing find state instead of searching (mirroring a bare "/" +
// Enter in `less`).
//
// For a TierPlainText entry, whose full content isn't resident
// (docs/STREAMING_PREVIEW_DESIGN.md §9), matches can't be located
// synchronously — this instead cancels any previous scan for the entry
// and starts a new background one (find.StartScan), leaving FindMatches
// empty and FindCurrent at -1 until syncFindScan picks up its result on
// a later frame; the file title bar's status area shows a "searching…"
// spinner in the meantime (findStatusText) rather than blocking this
// keystroke.
func (v *Preview) performFind(query string) {
	e := v.Files.DisplayedEntry()
	if e == nil {
		return
	}

	if e.FindScan != nil {
		e.FindScan.Cancel()
		e.FindScan = nil
	}
	e.FindQuery = query
	e.FindMatches = nil
	e.FindCurrent = -1
	e.FindWrapNote = ""
	if query == "" {
		return
	}

	if e.Tier == preview.TierPlainText {
		e.FindScan = find.StartScan(e.Path, query)
		return
	}

	v.ensureWrapped(e, v.computedWidth())
	e.FindMatches = find.InLines(e.Lines, query)
	if len(e.FindMatches) == 0 {
		return
	}
	v.seedFindCurrent(e)
}

// seedFindCurrent picks e's initial current match — the first one at or
// after the source line currently at the top of the viewport, wrapping
// to the very first match (and noting the wrap) if none exists at or
// after that point — and scrolls to it. Shared by performFind's
// synchronous (TierHighlighted) path and syncFindScan's asynchronous
// (TierPlainText) one, once a match set actually exists either way.
func (v *Preview) seedFindCurrent(e *openfiles.Entry) {
	startLine := currentTopLine(e) - 1
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

// syncFindScan picks up a finished TierPlainText find scan's result
// (docs/STREAMING_PREVIEW_DESIGN.md §9): once e.FindScan reports done,
// its matches are copied into FindMatches and the current match is
// seeded and scrolled to exactly like a synchronous find's result would
// be, then FindScan is cleared so this only ever runs once per scan. A
// no-op while no scan is running or it hasn't finished yet. Called once
// per frame (Draw) so the "searching…" spinner and, once ready, the
// match highlighting/status both reflect current state without any
// caller needing to poll for it explicitly.
func (v *Preview) syncFindScan(e *openfiles.Entry) {
	if e == nil || e.FindScan == nil {
		return
	}
	matches, done := e.FindScan.Snapshot()
	if !done {
		return
	}
	e.FindScan = nil
	e.FindMatches = matches
	if len(matches) == 0 {
		return
	}
	v.seedFindCurrent(e)
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
// if there is no current match. For a TierPlainText entry, the match's
// (absolute) source line may fall outside the currently-loaded window
// (docs/STREAMING_PREVIEW_DESIGN.md §8, §9) — the window is fetched to
// cover it first, the same way goto-line already does.
func (v *Preview) scrollToFindMatch(e *openfiles.Entry) {
	if e.FindCurrent < 0 || e.FindCurrent >= len(e.FindMatches) {
		return
	}
	m := e.FindMatches[e.FindCurrent]
	if e.Tier == preview.TierPlainText {
		v.ensureWindow(e, v.computedWidth(), m.Line+1)
	}
	row := findMatchRow(e, m)
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
// line's match may land in a continuation row) — ensureWrapped/
// ensureWindow must already have been called. m.Line is an absolute
// source line number; e.Rows' SourceLine is relative to e.WindowStartLine
// (always 0 for TierHighlighted, so this is a no-op subtraction there).
// Falls back to the line's first row if m's column can't be located in
// any of its rows (shouldn't happen for a match found against the same
// content, but degrades safely rather than losing the jump entirely).
func findMatchRow(e *openfiles.Entry, m find.Match) int {
	line := m.Line - e.WindowStartLine
	start, ok := e.FirstRow[line]
	if !ok {
		return -1
	}
	for i := start; i < len(e.Rows); i++ {
		r := e.Rows[i]
		if r.SourceLine != line {
			break
		}
		rowLen := preview.SegmentsRuneLen(r.Segments)
		if m.Col >= r.ColStart && m.Col < r.ColStart+max(rowLen, 1) {
			return i
		}
	}
	return start
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
	if e.FindScan != nil {
		elapsed := e.FindScan.Elapsed()
		if elapsed < canvas.SpinnerThreshold {
			return "/" + e.FindQuery
		}
		frame := spinner.Frame(elapsed, canvas.SpinnerFPS, spinner.DefaultFrames)
		return fmt.Sprintf("/%s  searching %c", e.FindQuery, frame)
	}
	if len(e.FindMatches) == 0 {
		return "/" + e.FindQuery + "  no matches"
	}
	status := fmt.Sprintf("/%s  %d/%d", e.FindQuery, e.FindCurrent+1, len(e.FindMatches))
	if e.FindWrapNote != "" {
		status += " (" + e.FindWrapNote + ")"
	}
	return status
}
