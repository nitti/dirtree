package views

import (
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/entry"
	"github.com/nitti/dirtree/internal/preview"
	"github.com/nitti/dirtree/internal/spinner"
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
// anything right now. The goto-line prompt, when open, replaces the
// title bar's own left/right content the same way the find prompt does
// (fileView.DrawTitleBar) — reachable only when this is the primary
// (non-overlaid) view, since no overlay leaves the goto-line key
// handled while this is showing. Find-scan syncing, title bar drawing,
// and content drawing are all dispatched through fileViewFor now — this
// function itself has no remaining `e.Tier == preview.TierBinary`
// branch of its own (#114).
func (v *Preview) Draw(x0, y0, w, h int, interactive bool) {
	e := v.Files.DisplayedEntry()
	fv := fileViewFor(e)
	fv.SyncFindScan(v, e)
	if e == nil {
		v.drawEmptyState(x0, y0, w, h)
		return
	}
	titleRows := fv.DrawTitleBar(v, e, x0, y0, w, interactive)
	fv.DrawContent(v, e, x0, y0+titleRows, w, h-titleRows)
}

// drawEmptyState renders the primary preview view's "no files open"
// placeholder (SPEC.md §2.1) — tier-agnostic, since there is no
// displayed entry at all in this state, so it's Draw's own
// responsibility rather than either fileView implementation's.
func (v *Preview) drawEmptyState(x0, y0, w, h int) {
	msg := "no files open — press o to quick open, b to browse, s to search contents"
	row := y0 + max(h/2, 1)
	v.Canvas.DrawText(x0, row, w, canvas.CenterPad(msg, w), canvas.StyleNormal)
}

// CurrentFileLegend returns the keybinding legend the file title bar
// is currently showing, dispatched through fileViewFor to mirror
// DrawTitleBar's own state precedence exactly (fileView interface doc)
// — for the help overlay (§5.4) to reuse. Returns ok=false when there
// is no displayed entry.
func (v *Preview) CurrentFileLegend() (entries []canvas.LegendEntry, ok bool) {
	e := v.Files.DisplayedEntry()
	if e == nil {
		return nil, false
	}
	return fileViewFor(e).CurrentLegend(v, e)
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
func (v *Preview) fileLegendForIdle(e *entry.TextEntry) []canvas.LegendEntry {
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
func findHighlightsForRow(e *entry.TextEntry, row preview.DisplayRow) []findHighlight {
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
