package views

import (
	"fmt"
	"strings"
	"time"

	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/preview"
	"github.com/nitti/dirtree/internal/spinner"
	"github.com/nitti/dirtree/internal/tree"
	"github.com/nitti/dirtree/internal/ui/canvas"
)

// fileView captures the pieces of the primary preview view's behavior
// that differ between the text and hex tiers (SPEC.md §2.1, §2.1a):
// the goto prompt (which characters are valid input, how a submitted
// input is applied, its label/legend/range hint), the file title bar's
// drawing and state precedence, its help-overlay legend accessor,
// background find-scan syncing, content drawing, and scroll/navigation.
// This is the shared "file view" abstraction proposed in #114,
// replacing the several `if e.Tier == preview.TierBinary { ... } else
// { ... }` branches that used to sit directly in Draw, CurrentFileLegend,
// handleGotoPromptKey, HandleKey's scroll/Home/End cases, and the
// now-deleted drawFileTitleBar/drawHexFileTitleBar/syncFindScan/
// syncHexFindScan/drawContent/drawHexContent/scroll/hexScroll/
// hexJumpStart/hexJumpEnd functions. Find's own perform/step/clear
// actions remain their own separate branch point for now, left for
// further follow-up rather than folded in all at once.
type fileView interface {
	// acceptGotoRune reports whether r is valid input for the goto
	// prompt while this tier's entry is displayed.
	acceptGotoRune(r rune) bool
	// jumpTo parses input and, if it parses to a valid target, moves
	// v's displayed entry's viewport there (clamped to its valid
	// range). A no-op for empty or malformed input, mirroring
	// gotoLine/gotoOffset's existing "bad input is simply not
	// actioned" behavior.
	jumpTo(v *Preview, input string)
	// gotoLabel is the fixed label shown ahead of the goto prompt's
	// typed input ("goto line: " / "goto offset: 0x") — a label, not
	// something the user types themselves.
	gotoLabel() string
	// gotoRangeHint renders e's valid goto-target range for display
	// alongside the prompt while typing (#114), so a user doesn't have
	// to already know the file's length/size to know what a reasonable
	// target is.
	gotoRangeHint(e *openfiles.Entry) string
	// gotoLegend is the goto prompt's own keybinding legend.
	gotoLegend() []canvas.LegendEntry

	// DrawTitleBar renders e's file title bar into the row at (x0, y0)
	// (SPEC.md §5.2, §2.1/§2.1a) — the tier-specific left-hand content
	// (line count vs. size tag), state precedence (goto/find prompts,
	// active find, copy mode where applicable), and legend. interactive
	// is false while another overlay (e.g. open-files-list) owns input
	// and the row is read-only. Always returns 1 (the row it occupied);
	// the signature returns int rather than nothing so callers don't
	// need a tier check of their own to know how much vertical space it
	// used, mirroring the pre-interface drawFileTitleBar/
	// drawHexFileTitleBar functions this replaces.
	DrawTitleBar(v *Preview, e *openfiles.Entry, x0, y0, w int, interactive bool) int

	// CurrentLegend returns the keybinding legend e's file title bar is
	// currently showing, mirroring DrawTitleBar's own state precedence
	// exactly — for the help overlay (§5.4) to reuse. ok is false when
	// the title bar is showing a transient state with no keybinding
	// legend of its own (e.g. text-tier's blocked-on-indexing case).
	CurrentLegend(v *Preview, e *openfiles.Entry) (entries []canvas.LegendEntry, ok bool)

	// SyncFindScan picks up a finished background find scan's result
	// for e, if any (docs/STREAMING_PREVIEW_DESIGN.md §9, SPEC.md
	// §2.1a) — a no-op while no scan is running, it hasn't finished
	// yet, or e is nil. Called once per frame (Draw) so the
	// "searching…" spinner and, once ready, match highlighting/status
	// both reflect current state without any caller needing to poll
	// for it explicitly.
	SyncFindScan(v *Preview, e *openfiles.Entry)

	// DrawContent renders e's content into the (x0, y0)-(x0+w, y0+h)
	// rectangle (SPEC.md §2.1, §2.1a) — assumes e is non-nil; Draw's own
	// empty-state message (drawEmptyState) handles the no-entry case
	// directly rather than either implementation needing to.
	DrawContent(v *Preview, e *openfiles.Entry, x0, y0, w, h int)

	// Scroll moves e's viewport by delta (SPEC.md §2.1, §2.1a) — display
	// rows for TierHighlighted, source lines for TierPlainText, or hex
	// rows for TierBinary. A no-op at the empty state or while e's
	// content isn't ready yet.
	Scroll(v *Preview, e *openfiles.Entry, delta int)

	// JumpStart moves e's viewport to its very start (Home binding).
	JumpStart(v *Preview, e *openfiles.Entry)
	// JumpEnd moves e's viewport to its very end (End binding).
	JumpEnd(v *Preview, e *openfiles.Entry)
}

// textFileView is the fileView for every tier except TierBinary
// (TierHighlighted and TierPlainText both address content by source
// line, the only axis the goto prompt cares about).
type textFileView struct{}

// hexFileView is the fileView for a TierBinary entry, addressing
// content by byte offset instead of source line.
type hexFileView struct{}

func (textFileView) acceptGotoRune(r rune) bool {
	return r >= '0' && r <= '9'
}

// jumpTo parses input as a decimal source line and jumps v's displayed
// entry's scroll to that line's first display row (SPEC.md §2.1),
// clamped to [1, total source lines]. A no-op if input is empty or
// there's no displayed entry.
func (textFileView) jumpTo(v *Preview, input string) {
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

func (hexFileView) acceptGotoRune(r rune) bool {
	return isHexOffsetRune(r)
}

// jumpTo parses input as a hexadecimal byte offset and jumps v's
// displayed entry's hex-view viewport to the row containing it
// (SPEC.md §2.1a), clamped to the file's valid range. A no-op if input
// is empty, doesn't parse, or there's no displayed entry.
func (hexFileView) jumpTo(v *Preview, input string) {
	e := v.Files.DisplayedEntry()
	if input == "" || e == nil {
		return
	}
	offset, ok := parseOffset(input)
	if !ok {
		return
	}
	e.Hex.HexOffset = clampHexOffset(offset, e.Size, v.hexBytesPerRow(e), v.viewportHeight())
}

func (textFileView) gotoLabel() string { return "goto line: " }

// gotoRangeHint renders the valid goto-line range as "1-<total lines>"
// (#114) — bestLineCount (scroll.go) is the same lower bound the goto
// prompt itself is already gated/clamped against (gotoLineBlocked,
// ScrollToLine), so the hint never promises a target the prompt would
// then reject.
func (textFileView) gotoRangeHint(e *openfiles.Entry) string {
	return fmt.Sprintf("1-%d", bestLineCount(e))
}

func (textFileView) gotoLegend() []canvas.LegendEntry { return gotoLegend }

func (hexFileView) gotoLabel() string { return "goto offset: 0x" }

// gotoRangeHint renders the valid goto-offset range as "0-<last valid
// offset>" in hex (#114), matching the prompt's own always-hex input
// (parseOffset) and the file title bar's hex offset gutter. An empty
// (zero-size) file has no valid offset at all; the hint floors at 0
// rather than showing a negative range in that edge case.
func (hexFileView) gotoRangeHint(e *openfiles.Entry) string {
	last := e.Size - 1
	if last < 0 {
		last = 0
	}
	return fmt.Sprintf("0-%x", last)
}

func (hexFileView) gotoLegend() []canvas.LegendEntry { return hexGotoLegend }

// DrawTitleBar renders the currently-displayed text-tier entry's file
// title bar (SPEC.md §5.2, §2.1): its root-relative path prefixed with
// its line count as "NL", the goto-line/find prompts (in place of the
// path/legend while open), an active find's live status, copy mode's
// `[copy mode]` tag and distinct style, and the idle legend otherwise
// (with a "building…" spinner in place of goto-line while the
// background stream pass is still running and perceptible,
// fileLegendForIdle) — see the fileView interface doc for the overall
// state-precedence shape this and hexFileView.DrawTitleBar share.
func (textFileView) DrawTitleBar(v *Preview, e *openfiles.Entry, x0, y0, w int, interactive bool) int {
	path := tree.RelativeDisplayPath(v.RootPath, e.Path)
	rel := path

	lineCount := bestLineCount(e)
	lineTag := fmt.Sprintf("%dL", lineCount)
	// One space, not two: unlike the gutter's own numField+"  " (draw-
	// Content, below), lineTag already carries a trailing "L" in place of
	// the gutter's second padding column, so a single space here lines
	// the path up with content starting at x0+gutterWidth(e).
	rel = lineTag + " " + rel

	// copyModeTag prefixes rel whenever e is in copy mode, so that state
	// is always legible in this row regardless of which case below fires
	// (find status/prompt text otherwise has no room to also mention
	// it) — the row's own distinct style (below) reinforces this further.
	left := rel
	if e.Text.CopyMode {
		left = "[copy mode] " + rel
	}

	gotoBlocked := interactive && v.GotoBlockedPath == e.Path && gotoLineBlocked(e.Text.Stream != nil, e.Text.Stream != nil && e.Text.Stream.Done())

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
	case interactive && v.GotoPromptOpen:
		fv := textFileView{}
		text = canvas.LegendText(w, fv.gotoLabel()+v.GotoInput+" ("+fv.gotoRangeHint(e)+")", legend(fv.gotoLegend()))
	case interactive && v.FindPromptOpen:
		text = canvas.LegendText(w, "/"+v.FindInput, legend(findPromptLegend))
	case !interactive:
		text = left
	case gotoBlocked:
		text = canvas.LegendText(w, left, withStatus("still indexing, try again shortly", nil))
	case e.Text.FindScan != nil:
		text = canvas.LegendText(w, left, withStatus(findStatusText(e), legend(findLegendNoMatches)))
	case e.Text.FindQuery != "" && len(e.Text.FindMatches) > 0:
		text = canvas.LegendText(w, left, withStatus(findStatusText(e), legend(findLegend)))
	case e.Text.FindQuery != "":
		text = canvas.LegendText(w, left, withStatus(findStatusText(e), legend(findLegendNoMatches)))
	case e.Text.CopyMode:
		text = canvas.LegendText(w, left, legend(fileLegendCopyModeOn))
	default:
		text = canvas.LegendText(w, rel, legend(v.fileLegendForIdle(e)))
	}

	style := canvas.StyleFileTitle
	switch {
	case e.Text.CopyMode:
		style = canvas.StyleCopyModeTitle
	case gotoBlocked && time.Since(v.GotoBlockedFlashStart) < canvas.FlashDuration:
		style = canvas.StyleFlashError
	}
	v.Canvas.DrawText(x0, y0, w, text, style)
	v.boldPathInFileTitleBar(x0, y0, text, path, style)
	return 1
}

// DrawTitleBar renders the currently-displayed hex-tier entry's file
// title bar (SPEC.md §2.1a): the file's total size in place of a line
// count, and goto-offset/hex-find in place of goto-line/find/copy-mode
// in the legend. Mirrors textFileView.DrawTitleBar's own state
// precedence, minus the goto-blocked/copy-mode cases, neither of which
// apply to a TierBinary entry (it starts no background stream to block
// on, and copy mode does not apply to a hex view, SPEC.md §2.1a).
func (hexFileView) DrawTitleBar(v *Preview, e *openfiles.Entry, x0, y0, w int, interactive bool) int {
	path := tree.RelativeDisplayPath(v.RootPath, e.Path)
	// sizeField is sized to gutterWidth-1 columns: formatSize spends
	// that whole budget on precision (as many decimal places as fit)
	// rather than settling for a fixed shape, so it fills the budget on
	// its own for most files — the trailing %-*s pad is only a safety
	// net for whatever's left (the sub-1024-byte case, where there's no
	// more precision to add, or a rounding carry that costs a column).
	// Either way, the single space joining it to path always lands
	// path's own first column at x0+gutterWidth — the same column the
	// hex-byte grid itself starts at (hexFileView.DrawContent) —
	// regardless of the file's size. This is the hex view's analog of
	// textFileView.
	// DrawTitleBar's "NL"-tag-plus-single-space alignment trick, which
	// instead gets this for free since its tag length and its gutter's
	// digit width are both derived from the same line count; here the
	// two aren't naturally
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

// CurrentLegend returns the keybinding legend the text-tier file title
// bar is currently showing, mirroring DrawTitleBar's own state
// precedence exactly (goto prompt, find prompt, blocked-on-indexing,
// an async plain-text-tier scan, an active find with or without
// matches, copy mode, else idle).
func (textFileView) CurrentLegend(v *Preview, e *openfiles.Entry) (entries []canvas.LegendEntry, ok bool) {
	gotoBlocked := v.GotoBlockedPath == e.Path && gotoLineBlocked(e.Text.Stream != nil, e.Text.Stream != nil && e.Text.Stream.Done())
	switch {
	case v.GotoPromptOpen:
		return gotoLegend, true
	case v.FindPromptOpen:
		return findPromptLegend, true
	case gotoBlocked:
		return nil, false
	case e.Text.FindScan != nil:
		return findLegendNoMatches, true
	case e.Text.FindQuery != "" && len(e.Text.FindMatches) > 0:
		return findLegend, true
	case e.Text.FindQuery != "":
		return findLegendNoMatches, true
	case e.Text.CopyMode:
		return fileLegendCopyModeOn, true
	default:
		return fileLegend, true
	}
}

// CurrentLegend returns the keybinding legend the hex-tier file title
// bar is currently showing, mirroring DrawTitleBar's own state
// precedence exactly.
func (hexFileView) CurrentLegend(v *Preview, e *openfiles.Entry) (entries []canvas.LegendEntry, ok bool) {
	switch {
	case v.GotoPromptOpen:
		return hexGotoLegend, true
	case v.HexFindPromptOpen:
		return hexFindPromptLegend, true
	case e.Hex.HexFindScan != nil:
		return hexFindLegendNoMatches, true
	case e.Hex.HexFindQuery != "" && len(e.Hex.HexFindMatches) > 0:
		return hexFindLegend, true
	case e.Hex.HexFindQuery != "":
		return hexFindLegendNoMatches, true
	default:
		return hexFileLegend, true
	}
}

// SyncFindScan picks up a finished TierPlainText find scan's result
// (docs/STREAMING_PREVIEW_DESIGN.md §9): once e.Text.FindScan reports
// done, its matches are copied into FindMatches and the current match
// is seeded and scrolled to exactly like a synchronous find's result
// would be, then FindScan is cleared so this only ever runs once per
// scan. A no-op while no scan is running or it hasn't finished yet.
func (textFileView) SyncFindScan(v *Preview, e *openfiles.Entry) {
	if e == nil || e.Text == nil || e.Text.FindScan == nil {
		return
	}
	matches, done := e.Text.FindScan.Snapshot()
	if !done {
		return
	}
	e.Text.FindScan = nil
	e.Text.FindMatches = matches
	if len(matches) == 0 {
		return
	}
	v.seedFindCurrent(e)
}

// SyncFindScan picks up a finished hex-find scan's result (SPEC.md
// §2.1a), mirroring textFileView.SyncFindScan: once e.Hex.HexFindScan
// reports done, its matches are copied into HexFindMatches and the
// current match is seeded, then HexFindScan is cleared so this only
// ever runs once per scan. A no-op while no scan is running or it
// hasn't finished yet.
func (hexFileView) SyncFindScan(v *Preview, e *openfiles.Entry) {
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

// DrawContent renders the currently-displayed text-tier entry's content
// (SPEC.md §2.1): a line-number gutter plus wrapped, highlighted rows,
// or a "building preview…" placeholder while its background pass is
// still running. Assumes e is non-nil — Draw's own empty-state message
// (drawEmptyState) handles the no-entry case before this is ever
// reached.
func (textFileView) DrawContent(v *Preview, e *openfiles.Entry, x0, y0, w, h int) {
	if !contentReady(e) {
		msg := "building preview…"
		if e.Text.Stream != nil {
			elapsed := e.Text.Stream.Elapsed()
			if spinner.ShouldShow(e.Text.Stream.Done(), elapsed, canvas.SpinnerThreshold) {
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
	digits := gw - 2

	topFlash := e.Path == v.TopBumpPath && time.Since(v.TopBumpFlashStart) < canvas.FlashDuration
	bottomFlash := e.Path == v.BottomBumpPath && time.Since(v.BottomBumpFlashStart) < canvas.FlashDuration
	lastDrawnY := -1

	for row := range viewportHeight {
		y := y0 + row
		i := e.Text.Scroll + row
		if i >= len(e.Text.Rows) {
			break
		}
		dr := e.Text.Rows[i]
		if gw > 0 {
			numField := strings.Repeat(" ", digits)
			if dr.HasNumber {
				numField = fmt.Sprintf("%*d", digits, e.Text.WindowStartLine+dr.SourceLine+1)
			}
			v.Canvas.DrawText(x0, y, gw, numField+"  ", canvas.StyleNormal)
		}
		v.drawSegments(x0+gw, y, contentWidth, dr.Segments, findHighlightsForRow(e, dr), e.Text.CopyMode)
		lastDrawnY = y
	}
	if topFlash {
		v.Canvas.FlashRow(x0, y0, w)
	}
	if bottomFlash && lastDrawnY >= 0 && lastDrawnY != y0 {
		// lastDrawnY == y0 means the document is short enough that its
		// only visible row is both the top and bottom edge; skip so a
		// simultaneous top+bottom flash doesn't reverse the same row
		// twice and cancel itself back out.
		v.Canvas.FlashRow(x0, lastDrawnY, w)
	}
}

// DrawContent renders the currently-displayed hex-tier entry's content
// (SPEC.md §2.1a): the offset gutter, hex-byte grid, and ASCII column
// for e's viewport, reading only the bytes actually needed for it
// (preview.ReadRange) rather than holding the file's content resident.
// Assumes e is non-nil — Draw's own empty-state message (drawEmptyState)
// handles the no-entry case before this is ever reached.
func (hexFileView) DrawContent(v *Preview, e *openfiles.Entry, x0, y0, w, h int) {
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
		// same row twice and cancel itself back out (textFileView.
		// DrawContent, above, guards the same case for the text tiers).
		v.Canvas.FlashRow(x0, lastDrawnY, w)
	}
}

// Scroll moves e's display-row scroll by delta rows (SPEC.md §2.1),
// clamped so it never goes negative or past the point where the last
// display row would leave the viewport. A no-op if e is nil or its
// content isn't ready yet (docs/STREAMING_PREVIEW_DESIGN.md §4, §8).
//
// For a TierPlainText entry, delta is instead treated as a number of
// source lines rather than display rows (§8's "a real change to the
// preview view's scroll model," flagged and accepted here as a
// deliberate simplification, since that tier's window only ever holds a
// slice of the file rather than a whole-file wrap cache to walk display
// rows against) — Up/Down and Page Up/Page Down all move by source line
// count for that tier.
func (textFileView) Scroll(v *Preview, e *openfiles.Entry, delta int) {
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
	old := e.Text.Scroll
	e.Text.Scroll = clamp(e.Text.Scroll+delta, 0, v.maxScroll(e, v.viewportHeight()))
	if e.Text.Scroll == old {
		v.bumpEdge(e, delta)
	}
}

// Scroll moves e's hex-view viewport by delta rows (SPEC.md §2.1a),
// clamped so it never goes negative or past the file's last row. A
// no-op if e is nil. Mirrors textFileView.Scroll's own edge-bump
// behavior: a move that clamps back to exactly where it started
// (already at the top/bottom) calls bumpEdge, the same path-keyed
// flash-request mechanism the text tiers use, so Up/Page Up past offset
// 0 (or Down/Page Down past the last row) gets the same "you've hit the
// end" cue there — reused as-is rather than duplicated, since it
// already operates in terms of the entry and a signed delta, neither of
// which is tier-specific.
func (hexFileView) Scroll(v *Preview, e *openfiles.Entry, delta int) {
	if e == nil {
		return
	}
	n := v.hexBytesPerRow(e)
	old := e.Hex.HexOffset
	e.Hex.HexOffset = clampHexOffset(e.Hex.HexOffset+int64(delta)*int64(n), e.Size, n, v.viewportHeight())
	if e.Hex.HexOffset == old {
		v.bumpEdge(e, delta)
	}
}

// JumpStart is a no-op for the text tiers: Home currently has no bound
// behavior for a text-tier entry — preview.go's HandleKey only ever
// reached the pre-interface hexJumpStart for a hex-tier entry, never a
// text-tier equivalent. Kept as an explicit no-op now that the
// key-to-action mapping is unified across tiers, preserving that
// existing behavior rather than silently changing it.
func (textFileView) JumpStart(v *Preview, e *openfiles.Entry) {}

// JumpEnd is a no-op for the text tiers; see JumpStart.
func (textFileView) JumpEnd(v *Preview, e *openfiles.Entry) {}

// JumpStart jumps e's hex-view viewport to offset 0 (SPEC.md §2.1a's
// Home binding).
func (hexFileView) JumpStart(v *Preview, e *openfiles.Entry) {
	if e != nil {
		e.Hex.HexOffset = 0
	}
}

// JumpEnd jumps e's hex-view viewport to hexMaxOffset — the file's last
// row at the bottom of a full viewport, rather than just its own start
// with the rest of the screen left blank (SPEC.md §2.1a's End binding).
func (hexFileView) JumpEnd(v *Preview, e *openfiles.Entry) {
	if e == nil {
		return
	}
	e.Hex.HexOffset = hexMaxOffset(e.Size, v.hexBytesPerRow(e), v.viewportHeight())
}

// fileViewFor returns the fileView implementation for e's tier —
// hexFileView for TierBinary, textFileView otherwise — mirroring every
// other `e.Tier == preview.TierBinary` branch point in this package
// (draw.go, scroll.go, hexview.go). e may be nil: textFileView is a
// harmless default, since the goto prompt is never opened without a
// displayed entry (preview.go's HandleKey only sets GotoPromptOpen when
// e != nil).
func fileViewFor(e *openfiles.Entry) fileView {
	if e != nil && e.Tier == preview.TierBinary {
		return hexFileView{}
	}
	return textFileView{}
}

// GotoLabel returns the fixed label the goto prompt shows ahead of its
// typed input for the currently-displayed entry's tier ("goto line: " /
// "goto offset: 0x") — exported for App's cursor-position logic
// (internal/ui/render.go, a different package) to place the caret past
// the label without duplicating the tier check DrawTitleBar already
// makes via fileViewFor.
func (v *Preview) GotoLabel() string {
	return fileViewFor(v.Files.DisplayedEntry()).gotoLabel()
}
