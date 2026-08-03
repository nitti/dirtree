package views

import (
	"fmt"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/find"
	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/preview"
	"github.com/nitti/dirtree/internal/spinner"
	"github.com/nitti/dirtree/internal/ui/canvas"
)

// handleFindPromptKey handles input while the in-file find prompt is
// open (SPEC.md §2.4): any printable character is accepted (unlike
// goto-line's digits-only prompt, since a search query is free text),
// Enter executes the search, Escape cancels the prompt without
// changing the entry's existing find state (if any). FindPromptOpen is
// only ever set true for a text-tier entry (HandleKey's `/` case), so
// this reaches straight for textFileView rather than dispatching
// through fileViewFor.
func (v *Preview) handleFindPromptKey(ev *tcell.EventKey) {
	e := v.Files.DisplayedEntry()
	switch {
	case ev.Key() == tcell.KeyEscape:
		v.FindPromptOpen = false
	case ev.Key() == tcell.KeyEnter:
		textFileView{}.PerformFind(v, e, v.FindInput)
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

// seedFindCurrent picks e's initial current match — the first one at or
// after the source line currently at the top of the viewport, wrapping
// to the very first match (and noting the wrap) if none exists at or
// after that point — and scrolls to it. Shared by textFileView.
// PerformFind's synchronous (TierHighlighted) path and textFileView.
// SyncFindScan's asynchronous (TierPlainText) one, once a match set
// actually exists either way.
func (v *Preview) seedFindCurrent(e *openfiles.Entry) {
	startLine := currentTopLine(e) - 1
	idx := 0
	for i, m := range e.Text.FindMatches {
		if m.Line >= startLine {
			idx = i
			break
		}
	}
	if e.Text.FindMatches[idx].Line < startLine {
		e.Text.FindWrapNote = "wrapped to top"
	}
	e.Text.FindCurrent = idx
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
	if e.Text.FindCurrent < 0 || e.Text.FindCurrent >= len(e.Text.FindMatches) {
		return
	}
	m := e.Text.FindMatches[e.Text.FindCurrent]
	if e.Tier == preview.TierPlainText {
		v.ensureWindow(e, v.computedWidth(), m.Line+1)
	}
	row := findMatchRow(e, m)
	if row < 0 {
		return
	}
	h := v.viewportHeight()
	if row < e.Text.Scroll {
		e.Text.Scroll = row
	}
	if row >= e.Text.Scroll+h {
		e.Text.Scroll = row - h + 1
	}
	e.Text.Scroll = clamp(e.Text.Scroll, 0, v.maxScroll(e, h))
}

// findMatchRow returns the index into e.Text.Rows of the specific wrapped
// row m falls in (not just its source line's first row, since a long
// line's match may land in a continuation row) — ensureWrapped/
// ensureWindow must already have been called. m.Line is an absolute
// source line number; e.Text.Rows' SourceLine is relative to e.Text.WindowStartLine
// (always 0 for TierHighlighted, so this is a no-op subtraction there).
// Falls back to the line's first row if m's column can't be located in
// any of its rows (shouldn't happen for a match found against the same
// content, but degrades safely rather than losing the jump entirely).
func findMatchRow(e *openfiles.Entry, m find.Match) int {
	line := m.Line - e.Text.WindowStartLine
	start, ok := e.Text.FirstRow[line]
	if !ok {
		return -1
	}
	for i := start; i < len(e.Text.Rows); i++ {
		r := e.Text.Rows[i]
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
	if e.Text.FindScan != nil {
		elapsed := e.Text.FindScan.Elapsed()
		if elapsed < canvas.SpinnerThreshold {
			return "/" + e.Text.FindQuery
		}
		frame := spinner.Frame(elapsed, canvas.SpinnerFPS, spinner.DefaultFrames)
		return fmt.Sprintf("/%s  searching %c", e.Text.FindQuery, frame)
	}
	if len(e.Text.FindMatches) == 0 {
		return "/" + e.Text.FindQuery + "  no matches"
	}
	status := fmt.Sprintf("/%s  %d/%d", e.Text.FindQuery, e.Text.FindCurrent+1, len(e.Text.FindMatches))
	if e.Text.FindWrapNote != "" {
		status += " (" + e.Text.FindWrapNote + ")"
	}
	return status
}
