package views

import (
	"fmt"
	"time"

	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/preview"
	"github.com/nitti/dirtree/internal/tree"
	"github.com/nitti/dirtree/internal/ui/canvas"
)

// fileView captures the pieces of the primary preview view's behavior
// that differ between the text and hex tiers (SPEC.md §2.1, §2.1a):
// the goto prompt (which characters are valid input, how a submitted
// input is applied, its label/legend/range hint), the file title bar's
// drawing and state precedence, its help-overlay legend accessor, and
// background find-scan syncing. This is the shared "file view"
// abstraction proposed in #114, replacing the several `if e.Tier ==
// preview.TierBinary { ... } else { ... }` branches that used to sit
// directly in Draw, CurrentFileLegend, handleGotoPromptKey, and the
// now-deleted drawFileTitleBar/drawHexFileTitleBar/syncFindScan/
// syncHexFindScan functions. Content drawing and scroll/navigation
// (drawContent/drawHexContent, scroll/hexScroll) and find's own
// perform/step/clear actions remain their own separate branch points
// for now, left for further follow-up rather than folded in all at
// once.
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
	// hex-byte grid itself starts at (drawHexContent) — regardless of
	// the file's size. This is the hex view's analog of textFileView.
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
