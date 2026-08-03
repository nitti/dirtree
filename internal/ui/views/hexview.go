package views

import (
	"fmt"
	"strconv"

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

// bytesPerRowFor returns the largest byte count whose hex-view row fits
// within width given gutterWidth, recomputed fresh from the available
// width every time (SPEC.md §2.1a, §6.2) rather than cached — the hex
// view's analog of text wrapping adapting to a live resize. Never
// returns less than 1, so a hex view remains navigable even at a width
// too narrow to comfortably fit anything.
func bytesPerRowFor(width, gutterWidth int) int {
	for n := maxHexBytesPerRow; n >= 1; n-- {
		if hexRowWidth(gutterWidth, n) <= width {
			return n
		}
	}
	return 1
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

// clampHexOffset clamps offset to a valid row-start byte offset for a
// file of the given size laid out at bytesPerRow bytes per row (SPEC.md
// §2.1a): never negative, never past the last row, and always itself a
// multiple of bytesPerRow, since a hex view's viewport always starts at
// a row boundary.
func clampHexOffset(offset, size int64, bytesPerRow int) int64 {
	if bytesPerRow <= 0 {
		bytesPerRow = 1
	}
	if offset < 0 {
		offset = 0
	}
	if max := lastRowStart(size, bytesPerRow); offset > max {
		offset = max
	}
	return offset - offset%int64(bytesPerRow)
}

// formatSize renders a byte count in human-readable form (SPEC.md
// §2.1a's file title bar, e.g. "4.2 KB"), the hex view's analog of the
// text tiers' "NL" line-count tag.
func formatSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
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
// §2.1a), clamped so it never goes negative or past the file's last row.
// A no-op at the empty state (no displayed entry).
func (v *Preview) hexScroll(deltaRows int) {
	e := v.Files.DisplayedEntry()
	if e == nil {
		return
	}
	n := v.hexBytesPerRow(e)
	e.HexOffset = clampHexOffset(e.HexOffset+int64(deltaRows)*int64(n), e.Size, n)
}

// hexJumpStart jumps e's hex-view viewport to offset 0 (SPEC.md §2.1a's
// Home binding).
func (v *Preview) hexJumpStart() {
	if e := v.Files.DisplayedEntry(); e != nil {
		e.HexOffset = 0
	}
}

// hexJumpEnd jumps e's hex-view viewport to the row containing the
// file's last byte (SPEC.md §2.1a's End binding).
func (v *Preview) hexJumpEnd() {
	e := v.Files.DisplayedEntry()
	if e == nil {
		return
	}
	e.HexOffset = lastRowStart(e.Size, v.hexBytesPerRow(e))
}

// parseOffset parses a goto-offset prompt's input (SPEC.md §2.1a) as
// either a "0x"-prefixed hexadecimal number or a plain decimal number,
// reporting ok=false for anything else (an empty input, a negative
// number, or text that doesn't parse cleanly under the selected base) —
// the goto-offset prompt's Enter handler (gotoOffset) leaves the
// viewport unchanged in that case, mirroring goto-line's own "malformed
// input is simply not actioned" behavior.
func parseOffset(input string) (int64, bool) {
	s := input
	base := 10
	if len(s) > 2 && (s[:2] == "0x" || s[:2] == "0X") {
		s = s[2:]
		base = 16
	}
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, base, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// gotoOffset jumps the currently-displayed entry's hex-view viewport to
// the row containing the parsed byte offset (SPEC.md §2.1a), clamped to
// the file's valid range. A no-op if input is empty, doesn't parse, or
// there's no displayed entry.
func (v *Preview) gotoOffset(input string) {
	e := v.Files.DisplayedEntry()
	if input == "" || e == nil {
		return
	}
	offset, ok := parseOffset(input)
	if !ok {
		return
	}
	e.HexOffset = clampHexOffset(offset, e.Size, v.hexBytesPerRow(e))
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
	if e == nil || (e.HexFindQuery == "" && e.HexFindScan == nil) {
		return
	}
	if e.HexFindScan != nil {
		e.HexFindScan.Cancel()
		e.HexFindScan = nil
	}
	e.HexFindQuery = ""
	e.HexFindMatches = nil
	e.HexFindCurrent = -1
	e.HexFindWrapNote = ""
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
	if e.HexFindScan != nil {
		e.HexFindScan.Cancel()
		e.HexFindScan = nil
	}
	e.HexFindQuery = query
	e.HexFindMatches = nil
	e.HexFindCurrent = -1
	e.HexFindWrapNote = ""
	if query == "" {
		return
	}
	e.HexFindScan = hexfind.StartScan(e.Path, query)
}

// syncHexFindScan picks up a finished hex-find scan's result (SPEC.md
// §2.1a), mirroring syncFindScan (find.go): once e.HexFindScan reports
// done, its matches are copied into HexFindMatches and the current match
// is seeded, then HexFindScan is cleared so this only ever runs once per
// scan. A no-op while no scan is running or it hasn't finished yet.
func (v *Preview) syncHexFindScan(e *openfiles.Entry) {
	if e == nil || e.HexFindScan == nil {
		return
	}
	matches, done := e.HexFindScan.Snapshot()
	if !done {
		return
	}
	e.HexFindScan = nil
	e.HexFindMatches = matches
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
	for i, m := range e.HexFindMatches {
		if m.Offset >= e.HexOffset {
			idx = i
			break
		}
	}
	if e.HexFindMatches[idx].Offset < e.HexOffset {
		e.HexFindWrapNote = "wrapped to top"
	}
	e.HexFindCurrent = idx
	v.scrollToHexFindMatch(e)
}

// hexFindStep moves the current hex-find match by delta (+1/-1),
// wrapping around at either end and noting the wrap (SPEC.md §2.1a),
// mirroring findStep (find.go). A no-op if there's no displayed entry
// or it has no matches.
func (v *Preview) hexFindStep(delta int) {
	e := v.Files.DisplayedEntry()
	if e == nil || len(e.HexFindMatches) == 0 {
		return
	}
	next := tree.MoveSelection(e.HexFindCurrent, delta, len(e.HexFindMatches))
	switch {
	case delta > 0 && next < e.HexFindCurrent:
		e.HexFindWrapNote = "wrapped to top"
	case delta < 0 && next > e.HexFindCurrent:
		e.HexFindWrapNote = "wrapped to bottom"
	default:
		e.HexFindWrapNote = ""
	}
	e.HexFindCurrent = next
	v.scrollToHexFindMatch(e)
}

// scrollToHexFindMatch scrolls e's hex-view viewport just enough to
// bring its current hex-find match into view (SPEC.md §2.1a), the same
// "only scroll if it's not already visible" rule scrollToFindMatch
// (find.go) uses. A no-op if there is no current match.
func (v *Preview) scrollToHexFindMatch(e *openfiles.Entry) {
	if e.HexFindCurrent < 0 || e.HexFindCurrent >= len(e.HexFindMatches) {
		return
	}
	m := e.HexFindMatches[e.HexFindCurrent]
	n := v.hexBytesPerRow(e)
	rowStart := m.Offset - m.Offset%int64(n)
	h := int64(max(v.viewportHeight(), 1))
	if rowStart < e.HexOffset {
		e.HexOffset = rowStart
	}
	if rowStart >= e.HexOffset+h*int64(n) {
		e.HexOffset = rowStart - (h-1)*int64(n)
	}
	e.HexOffset = clampHexOffset(e.HexOffset, e.Size, n)
}

// hexFindStatusText renders the hex view's find status (SPEC.md
// §2.1a), mirroring findStatusText (find.go): the query, a "searching…"
// spinner while a scan is in flight, match position/count once one has
// finished, and the transient wrap note.
func hexFindStatusText(e *openfiles.Entry) string {
	if e.HexFindScan != nil {
		elapsed := e.HexFindScan.Elapsed()
		if elapsed < canvas.SpinnerThreshold {
			return "/" + e.HexFindQuery
		}
		frame := spinner.Frame(elapsed, canvas.SpinnerFPS, spinner.DefaultFrames)
		return fmt.Sprintf("/%s  searching %c", e.HexFindQuery, frame)
	}
	if len(e.HexFindMatches) == 0 {
		return "/" + e.HexFindQuery + "  no matches"
	}
	status := fmt.Sprintf("/%s  %d/%d", e.HexFindQuery, e.HexFindCurrent+1, len(e.HexFindMatches))
	if e.HexFindWrapNote != "" {
		status += " (" + e.HexFindWrapNote + ")"
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
	left := formatSize(e.Size) + " " + path

	legend := func(entries []canvas.LegendEntry) []canvas.LegendEntry {
		if v.HelpVisible {
			return nil
		}
		return entries
	}

	var text string
	switch {
	case interactive && v.HexFindPromptOpen:
		text = canvas.LegendText(w, "/"+v.HexFindInput, legend(hexFindPromptLegend))
	case !interactive:
		text = left
	case e.HexFindScan != nil:
		text = canvas.LegendText(w, left, withStatus(hexFindStatusText(e), legend(hexFindLegendNoMatches)))
	case e.HexFindQuery != "" && len(e.HexFindMatches) > 0:
		text = canvas.LegendText(w, left, withStatus(hexFindStatusText(e), legend(hexFindLegend)))
	case e.HexFindQuery != "":
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
	if len(e.HexFindMatches) == 0 {
		return nil
	}
	var out []hexFindHighlight
	for i, m := range e.HexFindMatches {
		start := int(m.Offset - rowOffset)
		end := start + int(m.Len)
		if end <= 0 || start >= rowLen {
			continue
		}
		out = append(out, hexFindHighlight{Start: max(start, 0), End: min(end, rowLen), Current: i == e.HexFindCurrent})
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

// drawHexRow renders one hex-view row at (x0, y): the offset gutter,
// the hex-byte grid, and the ASCII column (SPEC.md §2.1a), for the
// bytesPerRow-byte row starting at rowOffset whose actual bytes are
// data (fewer than bytesPerRow at the file's final, partial row — the
// remainder renders as blank space in both columns).
func (v *Preview) drawHexRow(x0, y, gutterWidth, bytesPerRow int, rowOffset int64, data []byte, e *openfiles.Entry) {
	offsetStr := fmt.Sprintf("%0*x", gutterWidth-2, rowOffset)
	v.Canvas.DrawText(x0, y, gutterWidth, offsetStr+"  ", canvas.StyleNormal)

	highlights := hexFindHighlightsForRow(e, rowOffset, len(data))
	hexX := x0 + gutterWidth
	asciiX := hexX + hexBytesWidth(bytesPerRow) + 2

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
			if (i+1)%8 == 0 {
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
// goto-offset prompt, when open, occupies the bottom row, the same as
// drawContent's goto-line prompt row.
func (v *Preview) drawHexContent(x0, y0, w, h int) {
	e := v.Files.DisplayedEntry()
	if e == nil {
		return
	}
	gw := hexGutterWidth(e.Size)
	n := bytesPerRowFor(w, gw)
	e.HexOffset = clampHexOffset(e.HexOffset, e.Size, n)

	viewportHeight := h
	if v.GotoPromptOpen {
		viewportHeight--
	}
	if viewportHeight < 0 {
		viewportHeight = 0
	}

	data, err := preview.ReadRange(e.Path, e.HexOffset, viewportHeight*n)
	if err != nil {
		data = nil
	}

	for row := range viewportHeight {
		rowStart := row * n
		if rowStart >= len(data) {
			break
		}
		rowEnd := min(rowStart+n, len(data))
		v.drawHexRow(x0, y0+row, gw, n, e.HexOffset+int64(rowStart), data[rowStart:rowEnd], e)
	}

	if v.GotoPromptOpen {
		gotoRowLegend := hexGotoLegend
		if v.HelpVisible {
			gotoRowLegend = nil
		}
		v.Canvas.DrawText(x0, y0+h-1, w, canvas.LegendText(w, "goto offset: "+v.GotoInput, gotoRowLegend), canvas.StyleNormal)
	}
}
