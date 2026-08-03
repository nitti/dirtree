package views

import (
	"fmt"
	"strconv"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/hexfind"
	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/preview"
	"github.com/nitti/dirtree/internal/spinner"
	"github.com/nitti/dirtree/internal/tree"
	"github.com/nitti/dirtree/internal/ui/canvas"
)

// maxHexBytesPerRow bounds bytesPerRowFor's search (below) — far more
// than any realistic terminal width would ever fit, just a sane ceiling
// so the search has a fixed starting point.
const maxHexBytesPerRow = 64

// hexBytesPerGroup is the byte-group size the hex-byte grid's extra
// inter-group gap (hexBytesWidth) breaks on, and the granularity
// bytesPerRowFor picks a row's byte count at: a row always holds a whole
// number of groups, never a partial one — splitting a group across two
// rows read worse than giving up a little width to the ASCII column
// instead (drawHexContent), which absorbs whatever width a partial
// group would have used.
const hexBytesPerGroup = 8

// hexFileLegend documents the hex view's own file-specific actions
// (SPEC.md §2.1a), the TierBinary analog of fileLegend (draw.go):
// goto-offset in place of goto-line, no copy-mode entry, since copy mode
// does not apply to a hex view.
var hexFileLegend = []canvas.LegendEntry{
	{Text: "[/] find", Priority: 1},
	{Text: "[g] goto offset", Priority: 2},
}

// hexGotoLegend documents the goto-offset prompt (SPEC.md §2.1a) —
// identical wording to gotoLegend (draw.go), since Return/Ctrl+U/Escape
// behave the same way regardless of whether the prompt is jumping to a
// line or a byte offset; kept as its own variable anyway so the two
// prompts' legends can diverge later without one silently affecting the
// other.
var hexGotoLegend = gotoLegend

var hexFindPromptLegend = []canvas.LegendEntry{
	{Text: "[return] search", Priority: 1},
	{Text: "[ctrl+u] clear", Priority: 2},
	{Text: "[esc] cancel", Priority: 1},
}

var hexFindLegend = []canvas.LegendEntry{
	{Text: "[n] next", Priority: 1},
	{Text: "[N] prev", Priority: 1},
	{Text: "[esc] clear", Priority: 1},
}

var hexFindLegendNoMatches = []canvas.LegendEntry{
	{Text: "[esc] clear", Priority: 1},
}

// hexBytesWidth returns the column width n bytes occupy in the hex-byte
// grid (SPEC.md §2.1a): two hex digits per byte, one separating space
// between adjacent bytes, plus one extra gap space after every 8th byte
// (excluding the row's very last byte) for readability.
func hexBytesWidth(n int) int {
	if n <= 0 {
		return 0
	}
	w := n*2 + (n - 1)
	if n > 8 {
		w += (n - 1) / 8
	}
	return w
}

// hexRowWidth returns the total column width one hex-view row occupies:
// the offset gutter, a 2-column separator, the hex-byte grid, a second
// 2-column separator, and the n-column ASCII column (SPEC.md §2.1a).
func hexRowWidth(gutterWidth, n int) int {
	return gutterWidth + 2 + hexBytesWidth(n) + 2 + n
}

// bytesPerRowFor returns the largest whole number of hexBytesPerGroup-
// sized byte groups whose hex-view row fits within width given
// gutterWidth, recomputed fresh from the available width every time
// (SPEC.md §2.1a, §6.2) rather than cached — the hex view's analog of
// text wrapping adapting to a live resize. A row is always a whole
// number of groups (never a partial one split awkwardly across the
// grid); any width left over from not fitting one more full group is
// picked up by the ASCII column instead (drawHexContent), not wasted.
// Never returns less than one full group, so a hex view remains
// navigable (if visually cramped) even at a width too narrow to fit one
// comfortably.
func bytesPerRowFor(width, gutterWidth int) int {
	for n := maxHexBytesPerRow; n > hexBytesPerGroup; n -= hexBytesPerGroup {
		if hexRowWidth(gutterWidth, n) <= width {
			return n
		}
	}
	return hexBytesPerGroup
}

// hexGutterWidth returns the offset gutter's column width for a file of
// the given size (SPEC.md §2.1a): enough hex digits for the largest
// offset the file can contain, plus a two-column separator — the hex
// view's analog of canvas.GutterWidth, floored at 4 digits the same way
// a TierPlainText entry's line-number gutter is, so a small file's
// gutter isn't distractingly narrow.
func hexGutterWidth(size int64) int {
	n := size
	digits := 0
	for n > 0 {
		digits++
		n /= 16
	}
	if digits < 4 {
		digits = 4
	}
	return digits + 2
}

// lastRowStart returns the byte offset of the last row a file of the
// given size has, for a hex view laid out at bytesPerRow bytes per row —
// 0 for an empty (or invalid) size.
func lastRowStart(size int64, bytesPerRow int) int64 {
	if size <= 0 || bytesPerRow <= 0 {
		return 0
	}
	return (size - 1) / int64(bytesPerRow) * int64(bytesPerRow)
}

// hexMaxOffset returns the largest top-of-viewport byte offset for a
// file of the given size, laid out at bytesPerRow bytes/row within a
// viewportHeight-row-tall viewport: the offset that puts the file's
// last row at the bottom of a full viewport, rather than leaving most
// of the screen blank below a lone final row — the hex view's analog
// of the text tiers' own maxScroll (scroll.go), which keeps a long
// file's last line flush to the viewport's bottom the same way. Falls
// back to the last row's own start directly when the file has fewer
// total rows than the viewport is tall (or the viewport is at most one
// row), since there's nothing to fill the rest of the screen with
// either way.
func hexMaxOffset(size int64, bytesPerRow, viewportHeight int) int64 {
	last := lastRowStart(size, bytesPerRow)
	if viewportHeight <= 1 {
		return last
	}
	max := last - int64(viewportHeight-1)*int64(bytesPerRow)
	if max < 0 {
		max = 0
	}
	return max
}

// clampHexOffset clamps offset to a valid top-of-viewport byte offset
// for a file of the given size laid out at bytesPerRow bytes per row
// within a viewportHeight-row viewport (SPEC.md §2.1a): never negative,
// never past hexMaxOffset (above), and always itself a multiple of
// bytesPerRow, since a hex view's viewport always starts at a row
// boundary.
func clampHexOffset(offset, size int64, bytesPerRow, viewportHeight int) int64 {
	if bytesPerRow <= 0 {
		bytesPerRow = 1
	}
	if offset < 0 {
		offset = 0
	}
	if max := hexMaxOffset(size, bytesPerRow, viewportHeight); offset > max {
		offset = max
	}
	return offset - offset%int64(bytesPerRow)
}

// formatSize renders a byte count as precisely as it can within width
// columns (SPEC.md §2.1a's file title bar) — the hex view's analog of
// the text tiers' "NL" line-count tag, sized to the gutter (below)
// rather than to a fixed shape: a plain integer for a count under 1024
// (bytes are already exact, so there's no more precision to spend width
// on), otherwise an integer plus a single unit letter, widened with as
// many decimal places as still fit in width, tried from the most
// precise down rather than computed digit-by-digit — the simplest way
// to stay correct across a rounding carry that bumps the integer part's
// own digit count (e.g. 1023.97 rounding up to "1024.0"). Never wider
// than width; may be narrower when even one more decimal place would
// overflow it (or trivially, for the sub-1024 case, once the exact byte
// count is already shown in full).
func formatSize(n int64, width int) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	unitChar := "KMGTPE"[exp]
	value := float64(n) / float64(div)
	for decimals := width; decimals >= 0; decimals-- {
		s := fmt.Sprintf("%.*f%c", decimals, value, unitChar)
		if len(s) <= width {
			return s
		}
	}
	return fmt.Sprintf("%.0f%c", value, unitChar)
}

// hexBytesPerRow returns e's current bytes-per-row, derived from the
// canvas's actual current width (SPEC.md §6.2) — the same value drawHex-
// Content used most recently, kept as a small helper since navigation
// (hexScroll and friends) needs it independent of a draw call.
func (v *Preview) hexBytesPerRow(e *openfiles.Entry) int {
	w, _ := v.Canvas.Size()
	return bytesPerRowFor(w, hexGutterWidth(e.Size))
}

// hexScroll moves e's hex-view viewport by deltaRows rows (SPEC.md
// §2.1a), clamped so it never goes negative or past the file's last
// row. A no-op at the empty state (no displayed entry). Mirrors scroll's
// (scroll.go) own edge-bump behavior: a move that clamps back to
// exactly where it started (already at the top/bottom) calls bumpEdge,
// the same path-keyed flash-request mechanism the text tiers use, so
// Up/Page Up past offset 0 (or Down/Page Down past the last row) gets
// the same "you've hit the end" cue there — reused as-is rather than
// duplicated, since it already operates in terms of the entry and a
// signed delta, neither of which is tier-specific.
func (v *Preview) hexScroll(deltaRows int) {
	e := v.Files.DisplayedEntry()
	if e == nil {
		return
	}
	n := v.hexBytesPerRow(e)
	old := e.Hex.HexOffset
	e.Hex.HexOffset = clampHexOffset(e.Hex.HexOffset+int64(deltaRows)*int64(n), e.Size, n, v.viewportHeight())
	if e.Hex.HexOffset == old {
		v.bumpEdge(e, deltaRows)
	}
}

// hexJumpStart jumps e's hex-view viewport to offset 0 (SPEC.md §2.1a's
// Home binding).
func (v *Preview) hexJumpStart() {
	if e := v.Files.DisplayedEntry(); e != nil {
		e.Hex.HexOffset = 0
	}
}

// hexJumpEnd jumps e's hex-view viewport to hexMaxOffset — the file's
// last row at the bottom of a full viewport, rather than just its own
// start with the rest of the screen left blank (SPEC.md §2.1a's End
// binding).
func (v *Preview) hexJumpEnd() {
	e := v.Files.DisplayedEntry()
	if e == nil {
		return
	}
	e.Hex.HexOffset = hexMaxOffset(e.Size, v.hexBytesPerRow(e), v.viewportHeight())
}

// parseOffset parses a goto-offset prompt's input (SPEC.md §2.1a) as a
// hexadecimal number — the prompt's input is always hex (isHexOffsetRune,
// scroll.go, only accepts hex digits, and the displayed "0x" ahead of it
// is a fixed label rather than something the user types or this parses),
// reporting ok=false for anything that doesn't parse cleanly (including
// an empty input) — the goto-offset prompt's Enter handler (gotoOffset)
// leaves the viewport unchanged in that case, mirroring goto-line's own
// "malformed input is simply not actioned" behavior.
func parseOffset(input string) (int64, bool) {
	if input == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(input, 16, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// handleHexFindPromptKey handles input while the hex view's find prompt
// is open (SPEC.md §2.1a): any printable character is accepted (a byte
// pattern/ASCII substring, like in-file find's free-text query), Enter
// executes the search, Escape cancels without changing the entry's
// existing hex-find state.
func (v *Preview) handleHexFindPromptKey(ev *tcell.EventKey) {
	switch {
	case ev.Key() == tcell.KeyEscape:
		v.HexFindPromptOpen = false
	case ev.Key() == tcell.KeyEnter:
		v.performHexFind(v.HexFindInput)
		v.HexFindPromptOpen = false
	case ev.Key() == tcell.KeyBackspace, ev.Key() == tcell.KeyBackspace2:
		if len(v.HexFindInput) > 0 {
			r := []rune(v.HexFindInput)
			v.HexFindInput = string(r[:len(r)-1])
		}
	case ev.Key() == tcell.KeyCtrlU:
		v.HexFindInput = ""
	case ev.Rune() != 0 && ev.Key() == tcell.KeyRune:
		v.HexFindInput += string(ev.Rune())
	}
}

// clearHexFind clears the displayed entry's hex-find state (SPEC.md
// §2.1a), if any, canceling a still-running scan first — the hex view's
// analog of clearFind (find.go). A no-op if there's no displayed entry
// and no active query or in-progress scan.
func (v *Preview) clearHexFind() {
	e := v.Files.DisplayedEntry()
	if e == nil || (e.Hex.HexFindQuery == "" && e.Hex.HexFindScan == nil) {
		return
	}
	if e.Hex.HexFindScan != nil {
		e.Hex.HexFindScan.Cancel()
		e.Hex.HexFindScan = nil
	}
	e.Hex.HexFindQuery = ""
	e.Hex.HexFindMatches = nil
	e.Hex.HexFindCurrent = -1
	e.Hex.HexFindWrapNote = ""
}

// performHexFind executes a hex-view find (SPEC.md §2.1a): always a
// background scan (hexfind.StartScan), since a TierBinary entry never
// holds its file's full byte content resident regardless of size, unlike
// text find's TierHighlighted/TierPlainText split (performFind, find.go).
// A no-op if there's no displayed entry; an empty query clears any
// existing hex-find state instead of searching, mirroring performFind's
// own empty-query behavior.
func (v *Preview) performHexFind(query string) {
	e := v.Files.DisplayedEntry()
	if e == nil {
		return
	}
	if e.Hex.HexFindScan != nil {
		e.Hex.HexFindScan.Cancel()
		e.Hex.HexFindScan = nil
	}
	e.Hex.HexFindQuery = query
	e.Hex.HexFindMatches = nil
	e.Hex.HexFindCurrent = -1
	e.Hex.HexFindWrapNote = ""
	if query == "" {
		return
	}
	e.Hex.HexFindScan = hexfind.StartScan(e.Path, query)
}

// syncHexFindScan picks up a finished hex-find scan's result (SPEC.md
// §2.1a), mirroring syncFindScan (find.go): once e.Hex.HexFindScan reports
// done, its matches are copied into HexFindMatches and the current match
// is seeded, then HexFindScan is cleared so this only ever runs once per
// scan. A no-op while no scan is running or it hasn't finished yet.
func (v *Preview) syncHexFindScan(e *openfiles.Entry) {
	if e == nil || e.Hex == nil || e.Hex.HexFindScan == nil {
		return
	}
	matches, done := e.Hex.HexFindScan.Snapshot()
	if !done {
		return
	}
	e.Hex.HexFindScan = nil
	e.Hex.HexFindMatches = matches
	if len(matches) == 0 {
		return
	}
	v.seedHexFindCurrent(e)
}

// seedHexFindCurrent picks e's initial current hex-find match — the
// first one at or after the offset currently at the top of the
// viewport, wrapping to the very first match (and noting the wrap) if
// none exists at or after that point — and scrolls to it. Mirrors
// seedFindCurrent (find.go).
func (v *Preview) seedHexFindCurrent(e *openfiles.Entry) {
	idx := 0
	for i, m := range e.Hex.HexFindMatches {
		if m.Offset >= e.Hex.HexOffset {
			idx = i
			break
		}
	}
	if e.Hex.HexFindMatches[idx].Offset < e.Hex.HexOffset {
		e.Hex.HexFindWrapNote = "wrapped to top"
	}
	e.Hex.HexFindCurrent = idx
	v.scrollToHexFindMatch(e)
}

// hexFindStep moves the current hex-find match by delta (+1/-1),
// wrapping around at either end and noting the wrap (SPEC.md §2.1a),
// mirroring findStep (find.go). A no-op if there's no displayed entry
// or it has no matches.
func (v *Preview) hexFindStep(delta int) {
	e := v.Files.DisplayedEntry()
	if e == nil || len(e.Hex.HexFindMatches) == 0 {
		return
	}
	next := tree.MoveSelection(e.Hex.HexFindCurrent, delta, len(e.Hex.HexFindMatches))
	switch {
	case delta > 0 && next < e.Hex.HexFindCurrent:
		e.Hex.HexFindWrapNote = "wrapped to top"
	case delta < 0 && next > e.Hex.HexFindCurrent:
		e.Hex.HexFindWrapNote = "wrapped to bottom"
	default:
		e.Hex.HexFindWrapNote = ""
	}
	e.Hex.HexFindCurrent = next
	v.scrollToHexFindMatch(e)
}

// scrollToHexFindMatch scrolls e's hex-view viewport just enough to
// bring its current hex-find match into view (SPEC.md §2.1a), the same
// "only scroll if it's not already visible" rule scrollToFindMatch
// (find.go) uses. A no-op if there is no current match.
func (v *Preview) scrollToHexFindMatch(e *openfiles.Entry) {
	if e.Hex.HexFindCurrent < 0 || e.Hex.HexFindCurrent >= len(e.Hex.HexFindMatches) {
		return
	}
	m := e.Hex.HexFindMatches[e.Hex.HexFindCurrent]
	n := v.hexBytesPerRow(e)
	rowStart := m.Offset - m.Offset%int64(n)
	h := int64(max(v.viewportHeight(), 1))
	if rowStart < e.Hex.HexOffset {
		e.Hex.HexOffset = rowStart
	}
	if rowStart >= e.Hex.HexOffset+h*int64(n) {
		e.Hex.HexOffset = rowStart - (h-1)*int64(n)
	}
	e.Hex.HexOffset = clampHexOffset(e.Hex.HexOffset, e.Size, n, v.viewportHeight())
}

// hexFindStatusText renders the hex view's find status (SPEC.md
// §2.1a), mirroring findStatusText (find.go): the query, a "searching…"
// spinner while a scan is in flight, match position/count once one has
// finished, and the transient wrap note.
func hexFindStatusText(e *openfiles.Entry) string {
	if e.Hex.HexFindScan != nil {
		elapsed := e.Hex.HexFindScan.Elapsed()
		if elapsed < canvas.SpinnerThreshold {
			return "/" + e.Hex.HexFindQuery
		}
		frame := spinner.Frame(elapsed, canvas.SpinnerFPS, spinner.DefaultFrames)
		return fmt.Sprintf("/%s  searching %c", e.Hex.HexFindQuery, frame)
	}
	if len(e.Hex.HexFindMatches) == 0 {
		return "/" + e.Hex.HexFindQuery + "  no matches"
	}
	status := fmt.Sprintf("/%s  %d/%d", e.Hex.HexFindQuery, e.Hex.HexFindCurrent+1, len(e.Hex.HexFindMatches))
	if e.Hex.HexFindWrapNote != "" {
		status += " (" + e.Hex.HexFindWrapNote + ")"
	}
	return status
}

// drawHexFileTitleBar renders the hex view's own file title bar (SPEC.md
// §2.1a): the file's total size in place of a line count, and goto-
// offset/hex-find in place of goto-line/find/copy-mode in the legend.
// Mirrors drawFileTitleBar's (draw.go) state precedence, minus the
// goto-blocked/copy-mode cases, neither of which apply to a TierBinary
// entry (it starts no background stream to block on, and copy mode does
// not apply to a hex view, SPEC.md §2.1a).
func (v *Preview) drawHexFileTitleBar(x0, y0, w int, interactive bool, e *openfiles.Entry) int {
	path := tree.RelativeDisplayPath(v.RootPath, e.Path)
	// sizeField is sized to gutterWidth-1 columns: formatSize spends
	// that whole budget on precision (as many decimal places as fit)
	// rather than settling for a fixed shape, so it fills the budget on
	// its own for most files — the trailing %-*s pad is only a safety
	// net for whatever's left (the sub-1024-byte case, where there's no
	// more precision to add, or a rounding carry that costs a column).
	// Either way, the single space joining it to path always lands
	// path's own first column at x0+gutterWidth — the same column the
	// hex-byte grid itself starts at (drawHexContent) — regardless of
	// the file's size. This is the hex view's analog of §2.1's "NL"-tag-
	// plus-single-space alignment trick, which instead gets this for
	// free since its tag length and its gutter's digit width are both
	// derived from the same line count; here the two aren't naturally
	// coupled (hex digit count in the file's size vs. formatSize's own
	// decimal-with-unit-letter rendering), so sizing formatSize's own
	// output to the budget (and padding whatever's left) takes its place.
	gw := hexGutterWidth(e.Size)
	sizeField := fmt.Sprintf("%-*s", gw-1, formatSize(e.Size, gw-1))
	left := sizeField + " " + path

	legend := func(entries []canvas.LegendEntry) []canvas.LegendEntry {
		if v.HelpVisible {
			return nil
		}
		return entries
	}

	var text string
	switch {
	case interactive && v.GotoPromptOpen:
		fv := hexFileView{}
		text = canvas.LegendText(w, fv.gotoLabel()+v.GotoInput+" ("+fv.gotoRangeHint(e)+")", legend(fv.gotoLegend()))
	case interactive && v.HexFindPromptOpen:
		text = canvas.LegendText(w, "/"+v.HexFindInput, legend(hexFindPromptLegend))
	case !interactive:
		text = left
	case e.Hex.HexFindScan != nil:
		text = canvas.LegendText(w, left, withStatus(hexFindStatusText(e), legend(hexFindLegendNoMatches)))
	case e.Hex.HexFindQuery != "" && len(e.Hex.HexFindMatches) > 0:
		text = canvas.LegendText(w, left, withStatus(hexFindStatusText(e), legend(hexFindLegend)))
	case e.Hex.HexFindQuery != "":
		text = canvas.LegendText(w, left, withStatus(hexFindStatusText(e), legend(hexFindLegendNoMatches)))
	default:
		text = canvas.LegendText(w, left, legend(hexFileLegend))
	}

	style := canvas.StyleFileTitle
	v.Canvas.DrawText(x0, y0, w, text, style)
	v.boldPathInFileTitleBar(x0, y0, text, path, style)
	return 1
}

// hexFindHighlight is one hex-find match's byte-index range within a
// single drawn row (SPEC.md §2.1a) — the hex view's analog of
// findHighlight (draw.go), in row-relative byte indices rather than
// rune columns, since it's applied to both the hex-byte grid and the
// ASCII column, which render the same underlying byte at two different
// screen columns.
type hexFindHighlight struct {
	Start, End int
	Current    bool
}

// hexFindHighlightsForRow returns rowLen bytes' worth of every hex-find
// match overlapping the row starting at rowOffset, in row-relative byte
// indices — mirrors findHighlightsForRow (draw.go).
func hexFindHighlightsForRow(e *openfiles.Entry, rowOffset int64, rowLen int) []hexFindHighlight {
	if len(e.Hex.HexFindMatches) == 0 {
		return nil
	}
	var out []hexFindHighlight
	for i, m := range e.Hex.HexFindMatches {
		start := int(m.Offset - rowOffset)
		end := start + int(m.Len)
		if end <= 0 || start >= rowLen {
			continue
		}
		out = append(out, hexFindHighlight{Start: max(start, 0), End: min(end, rowLen), Current: i == e.Hex.HexFindCurrent})
	}
	return out
}

// hexHighlightStyleAt returns the hex-find match style covering byte
// index i, if any, else base — mirrors highlightStyleAt (draw.go).
func hexHighlightStyleAt(i int, highlights []hexFindHighlight, base tcell.Style) tcell.Style {
	for _, h := range highlights {
		if i >= h.Start && i < h.End {
			if h.Current {
				return canvas.StyleFindCurrent
			}
			return canvas.StyleFindMatch
		}
	}
	return base
}

// drawHexRow renders one hex-view row at (x0, y): the offset gutter and
// hex-byte grid left-aligned starting at x0, and the ASCII column
// right-aligned flush against x0+rowWidth — the row's own two
// representations of the same bytes take different amounts of width to
// show (bytesPerRow of them, at 3-4 columns apiece in the hex grid vs.
// exactly 1 apiece in the ASCII column), and the more compact one won't
// generally span the same width the other does, so rather than pick an
// arbitrary side to absorb that difference, both blocks anchor to their
// own edge of the row and whatever width is left over falls as a gap
// between them (SPEC.md §2.1a) — never inside either block itself. For
// the file's final, partial row (fewer than bytesPerRow actual bytes,
// data), the shortfall renders as blank space within both columns
// rather than changing either block's position.
func (v *Preview) drawHexRow(x0, y, rowWidth, gutterWidth, bytesPerRow int, rowOffset int64, data []byte, e *openfiles.Entry) {
	offsetStr := fmt.Sprintf("%0*x", gutterWidth-2, rowOffset)
	v.Canvas.DrawText(x0, y, gutterWidth, offsetStr+"  ", canvas.StyleNormal)

	highlights := hexFindHighlightsForRow(e, rowOffset, len(data))
	hexX := x0 + gutterWidth
	asciiX := x0 + rowWidth - bytesPerRow

	col := 0
	for i := range bytesPerRow {
		style := canvas.StyleNormal
		text := "  "
		if i < len(data) {
			style = hexHighlightStyleAt(i, highlights, style)
			text = fmt.Sprintf("%02x", data[i])
		}
		v.Canvas.DrawText(hexX+col, y, 2, text, style)
		col += 2
		if i < bytesPerRow-1 {
			v.Canvas.SetContent(hexX+col, y, ' ', canvas.StyleNormal)
			col++
			if (i+1)%hexBytesPerGroup == 0 {
				v.Canvas.SetContent(hexX+col, y, ' ', canvas.StyleNormal)
				col++
			}
		}
	}
	for i := range bytesPerRow {
		x := asciiX + i
		ch, style := ' ', canvas.StyleNormal
		if i < len(data) {
			b := data[i]
			ch = '.'
			if b >= 0x20 && b < 0x7f {
				ch = rune(b)
			}
			style = hexHighlightStyleAt(i, highlights, canvas.StyleNormal)
		}
		v.Canvas.SetContent(x, y, ch, style)
	}
}

// drawHexContent renders the hex view's content (SPEC.md §2.1a) into
// the (x0, y0)-(x0+w, y0+h) rectangle: the offset gutter, hex-byte grid,
// and ASCII column for the currently-displayed TierBinary entry's
// viewport, reading only the bytes actually needed for it (preview.
// ReadRange) rather than holding the file's content resident. The
// goto-offset prompt, when open, replaces the title bar's own content
// instead (drawHexFileTitleBar), the same as drawFileTitleBar's
// goto-line prompt — this rectangle is unaffected by it either way.
func (v *Preview) drawHexContent(x0, y0, w, h int) {
	e := v.Files.DisplayedEntry()
	if e == nil {
		return
	}
	gw := hexGutterWidth(e.Size)
	n := bytesPerRowFor(w, gw)

	viewportHeight := h
	if viewportHeight < 0 {
		viewportHeight = 0
	}

	e.Hex.HexOffset = clampHexOffset(e.Hex.HexOffset, e.Size, n, viewportHeight)

	data, err := preview.ReadRange(e.Path, e.Hex.HexOffset, viewportHeight*n)
	if err != nil {
		data = nil
	}

	topFlash := e.Path == v.TopBumpPath && time.Since(v.TopBumpFlashStart) < canvas.FlashDuration
	bottomFlash := e.Path == v.BottomBumpPath && time.Since(v.BottomBumpFlashStart) < canvas.FlashDuration
	lastDrawnY := -1

	for row := range viewportHeight {
		rowStart := row * n
		if rowStart >= len(data) {
			break
		}
		rowEnd := min(rowStart+n, len(data))
		y := y0 + row
		v.drawHexRow(x0, y, w, gw, n, e.Hex.HexOffset+int64(rowStart), data[rowStart:rowEnd], e)
		lastDrawnY = y
	}
	if topFlash {
		v.Canvas.FlashRow(x0, y0, w)
	}
	if bottomFlash && lastDrawnY >= 0 && lastDrawnY != y0 {
		// lastDrawnY == y0 means the whole file fits in one visible row;
		// skip so a simultaneous top+bottom flash doesn't reverse the
		// same row twice and cancel itself back out (drawContent, above,
		// guards the same case for the text tiers).
		v.Canvas.FlashRow(x0, lastDrawnY, w)
	}
}
