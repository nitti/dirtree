package views

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

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
	v.syncFindScan(v.Files.DisplayedEntry())
	titleRows := v.drawFileTitleBar(x0, y0, w, interactive)
	v.drawContent(x0, y0+titleRows, w, h-titleRows)
}

// CurrentFileLegend returns the keybinding legend the file title bar
// is currently showing, mirroring drawFileTitleBar's own state
// precedence exactly (find prompt, an async plain-text-tier scan, an
// active find with or without matches, copy mode, else idle) — for the
// help overlay (§5.4) to reuse. Returns ok=false when there is no
// displayed entry, or when the title bar is showing a transient state
// with no keybinding legend of its own (blocked-on-indexing), since
// there is nothing meaningful to list in either case.
func (v *Preview) CurrentFileLegend() (entries []canvas.LegendEntry, ok bool) {
	e := v.Files.DisplayedEntry()
	if e == nil {
		return nil, false
	}
	gotoBlocked := v.GotoBlockedPath == e.Path && gotoLineBlocked(e.Stream != nil, e.Stream != nil && e.Stream.Done())
	switch {
	case v.FindPromptOpen:
		return findPromptLegend, true
	case gotoBlocked:
		return nil, false
	case e.FindScan != nil:
		return findLegendNoMatches, true
	case e.FindQuery != "" && len(e.FindMatches) > 0:
		return findLegend, true
	case e.FindQuery != "":
		return findLegendNoMatches, true
	case e.CopyMode:
		return fileLegendCopyModeOn, true
	default:
		return fileLegend, true
	}
}

// GotoPromptLegend returns the goto-line prompt's own legend while
// it's open, for the help overlay (§5.4) to reuse — it renders on its
// own bottom row (drawContent), independent of the file title bar.
func (v *Preview) GotoPromptLegend() (entries []canvas.LegendEntry, ok bool) {
	if !v.GotoPromptOpen {
		return nil, false
	}
	return gotoLegend, true
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
	path := tree.RelativeDisplayPath(v.RootPath, e.Path)
	rel := path

	lineCount := bestLineCount(e)
	lineTag := fmt.Sprintf("%d line", lineCount)
	if lineCount != 1 {
		lineTag += "s"
	}
	rel = lineTag + "  " + rel

	// copyModeTag prefixes rel whenever e is in copy mode, so that state
	// is always legible in this row regardless of which case below fires
	// (find status/prompt text otherwise has no room to also mention
	// it) — the row's own distinct style (below) reinforces this further.
	left := rel
	if e.CopyMode {
		left = "[copy mode] " + rel
	}

	gotoBlocked := interactive && v.GotoBlockedPath == e.Path && gotoLineBlocked(e.Stream != nil, e.Stream != nil && e.Stream.Done())

	// legend suppresses entries entirely while the help overlay (§5.4)
	// is showing, so this row's own legend doesn't compete with the
	// full keybinding reference drawn separately — the left-hand
	// content (path, find/goto status text) is untouched either way.
	legend := func(entries []canvas.LegendEntry) []canvas.LegendEntry {
		if v.HelpVisible {
			return nil
		}
		return entries
	}

	var text string
	switch {
	case interactive && v.FindPromptOpen:
		text = canvas.LegendText(w, "/"+v.FindInput, legend(findPromptLegend))
	case !interactive:
		text = left
	case gotoBlocked:
		text = canvas.LegendText(w, left, withStatus("still indexing, try again shortly", nil))
	case e.FindScan != nil:
		text = canvas.LegendText(w, left, withStatus(findStatusText(e), legend(findLegendNoMatches)))
	case e.FindQuery != "" && len(e.FindMatches) > 0:
		text = canvas.LegendText(w, left, withStatus(findStatusText(e), legend(findLegend)))
	case e.FindQuery != "":
		text = canvas.LegendText(w, left, withStatus(findStatusText(e), legend(findLegendNoMatches)))
	case e.CopyMode:
		text = canvas.LegendText(w, left, legend(fileLegendCopyModeOn))
	default:
		text = canvas.LegendText(w, rel, legend(v.fileLegendForIdle(e)))
	}

	style := canvas.StyleFileTitle
	switch {
	case e.CopyMode:
		style = canvas.StyleCopyModeTitle
	case gotoBlocked && time.Since(v.GotoBlockedFlashStart) < canvas.FlashDuration:
		style = canvas.StyleFlashError
	}
	v.Canvas.DrawText(x0, y0, w, text, style)
	v.boldPathInFileTitleBar(x0, y0, text, path, style)
	return 1
}

// boldPathInFileTitleBar re-draws path (the displayed entry's own
// root-relative path) in style.Bold(true), if it appears in text — it
// may not: the find prompt's own input replaces the path in this row
// while open, and left-hand content (including the path) can be
// dropped entirely for width under the fit/drop rule (SPEC.md §5.2),
// in either of which cases this is a no-op rather than styling the
// wrong span.
func (v *Preview) boldPathInFileTitleBar(x0, y0 int, text, path string, style tcell.Style) {
	runes, target := []rune(text), []rune(path)
	for i := 0; i+len(target) <= len(runes); i++ {
		if string(runes[i:i+len(target)]) == path {
			v.Canvas.DrawText(x0+i, y0, len(target), path, style.Bold(true))
			return
		}
	}
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

// drawContent renders the primary preview view's content (SPEC.md
// §2.1) into the (x0, y0)-(x0+w, y0+h) rectangle: a line-number gutter
// plus wrapped, highlighted rows for the currently-displayed entry, or
// an explanatory empty-state message if none is displayed. The
// goto-line prompt, when open, occupies the bottom row.
func (v *Preview) drawContent(x0, y0, w, h int) {
	e := v.Files.DisplayedEntry()
	if e == nil {
		msg := "no files open — press o to quick open, b to browse, s to search contents"
		row := y0 + max(h/2, 1)
		v.Canvas.DrawText(x0, row, w, canvas.CenterPad(msg, w), canvas.StyleNormal)
		return
	}
	if !contentReady(e) {
		msg := "building preview…"
		if e.Stream != nil {
			elapsed := e.Stream.Elapsed()
			if spinner.ShouldShow(e.Stream.Done(), elapsed, canvas.SpinnerThreshold) {
				frame := spinner.Frame(elapsed, canvas.SpinnerFPS, spinner.DefaultFrames)
				msg = "building preview " + string(frame)
			}
		}
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
		gotoRowLegend := gotoLegend
		if v.HelpVisible {
			gotoRowLegend = nil
		}
		v.Canvas.DrawText(x0, y0+h-1, w, canvas.LegendText(w, "goto line: "+v.GotoInput, gotoRowLegend), canvas.StyleNormal)
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
		if m.Line-e.WindowStartLine != row.SourceLine {
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
