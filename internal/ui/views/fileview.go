package views

import (
	"fmt"
	"strings"
	"time"

	"github.com/nitti/dirtree/internal/entry"
	"github.com/nitti/dirtree/internal/find"
	"github.com/nitti/dirtree/internal/hexfind"
	"github.com/nitti/dirtree/internal/preview"
	"github.com/nitti/dirtree/internal/spinner"
	"github.com/nitti/dirtree/internal/tree"
	"github.com/nitti/dirtree/internal/ui/canvas"
)

// fileView captures the pieces of the primary preview view's behavior
// that differ between the text and hex tiers (SPEC.md §2.1, §2.1a): the
// goto prompt (which characters are valid input, how a submitted input
// is applied, its label/legend/range hint), the file title bar's
// drawing and state precedence, its help-overlay legend accessor,
// background find-scan syncing, content drawing, scroll/navigation,
// find's own perform/step/clear actions, and the copy-mode toggle. This
// is the shared "file view" abstraction proposed in #114, replacing the
// several `if e.Tier == preview.TierBinary { ... } else { ... }`
// branches that used to sit directly in Draw, CurrentFileLegend,
// handleGotoPromptKey, and HandleKey's scroll/Home/End/find-step/
// clear-find/copy-mode cases, and the now-deleted drawFileTitleBar/
// drawHexFileTitleBar/syncFindScan/syncHexFindScan/drawContent/
// drawHexContent/scroll/hexScroll/hexJumpStart/hexJumpEnd/performFind/
// performHexFind/findStep/hexFindStep/clearFind/clearHexFind functions.
//
// e is always fileEntry (below) now, not a pointer to a single flat
// struct (a follow-up to #114 extracting internal/entry: that package
// exports no interface of its own — TextEntry/HexEntry are concrete
// types, and this package declares fileEntry itself, sized to exactly
// what it needs, the same "accept interfaces, return structs" pattern
// internal/openfiles.entryHandle uses for its own, differently-shaped
// needs). Every method below type-asserts e to the concrete
// *entry.TextEntry/*entry.HexEntry it expects once, at the top, exactly
// the same shape the prior Entry.Text/Entry.Hex-pointer design already
// used — only what's being asserted against changed.
//
// This completes #114's core ask: after this, fileViewFor itself is
// the only remaining tier-branch point in this package for behavior
// this interface covers. HandleKey's `g` and `/` cases still branch on
// tier too, but for a different reason than the branches above — the
// branch itself isn't the same action reached two ways, it reflects
// genuine per-tier asymmetry that doesn't fit this interface's shape:
// `g`'s text-tier case has an extra gotoLineBlocked check hex has no
// equivalent of, and `/` sets one of two still-separate
// Preview.FindPromptOpen/HexFindPromptOpen fields. Collapsing those two
// fields into one (making `/`'s branch removable the same way copy
// mode's was) is a smaller, opportunistic follow-up, not folded in
// here.
// fileEntry is the minimal contract this package needs from an entry
// before dispatching to its concrete tier — declared here, by the
// consumer, mirroring internal/openfiles.entryHandle's same rationale:
// ask for exactly what's needed rather than programming against a
// producer-blessed abstraction. entry.TextEntry/entry.HexEntry satisfy
// this structurally; internal/entry never declares or knows about it.
// This package never calls Close()/Evict() (those are internal/
// openfiles.List's own bookkeeping), so unlike entryHandle, Path() is
// all this side needs.
type fileEntry interface {
	Path() string
}

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
	gotoRangeHint(e fileEntry) string
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
	DrawTitleBar(v *Preview, e fileEntry, x0, y0, w int, interactive bool) int

	// CurrentLegend returns the keybinding legend e's file title bar is
	// currently showing, mirroring DrawTitleBar's own state precedence
	// exactly — for the help overlay (§5.4) to reuse. ok is false when
	// the title bar is showing a transient state with no keybinding
	// legend of its own (e.g. text-tier's blocked-on-indexing case).
	CurrentLegend(v *Preview, e fileEntry) (entries []canvas.LegendEntry, ok bool)

	// SyncFindScan picks up a finished background find scan's result
	// for e, if any (docs/STREAMING_PREVIEW_DESIGN.md §9, SPEC.md
	// §2.1a) — a no-op while no scan is running, it hasn't finished
	// yet, or e is nil. Called once per frame (Draw) so the
	// "searching…" spinner and, once ready, match highlighting/status
	// both reflect current state without any caller needing to poll
	// for it explicitly.
	SyncFindScan(v *Preview, e fileEntry)

	// DrawContent renders e's content into the (x0, y0)-(x0+w, y0+h)
	// rectangle (SPEC.md §2.1, §2.1a) — assumes e is non-nil; Draw's own
	// empty-state message (drawEmptyState) handles the no-entry case
	// directly rather than either implementation needing to.
	DrawContent(v *Preview, e fileEntry, x0, y0, w, h int)

	// Scroll moves e's viewport by delta (SPEC.md §2.1, §2.1a) — display
	// rows for TierHighlighted, source lines for TierPlainText, or hex
	// rows for TierBinary. A no-op at the empty state or while e's
	// content isn't ready yet.
	Scroll(v *Preview, e fileEntry, delta int)

	// JumpStart moves e's viewport to its very start (Home binding).
	JumpStart(v *Preview, e fileEntry)
	// JumpEnd moves e's viewport to its very end (End binding).
	JumpEnd(v *Preview, e fileEntry)

	// PerformFind executes an in-file find for query against e (SPEC.md
	// §2.4, §2.1a), replacing any previous find state — synchronous or a
	// background scan, and matched by whatever coordinate system this
	// tier's content addresses (line/rune vs. byte offset), per the
	// implementation. An empty query clears any existing find state
	// instead of searching. A no-op if e is nil.
	PerformFind(v *Preview, e fileEntry, query string)
	// FindStep moves e's current find match by delta (+1/-1), wrapping
	// around at either end and noting the wrap. A no-op if e is nil or
	// has no matches.
	FindStep(v *Preview, e fileEntry, delta int)
	// ClearFind clears e's find state (query, matches, current index,
	// wrap note), canceling a still-running scan first. A no-op if
	// there's nothing to clear.
	ClearFind(v *Preview, e fileEntry)

	// ToggleCopyMode toggles e's copy mode (SPEC.md §2.1) — a no-op for
	// a TierBinary entry, which copy mode does not apply to (SPEC.md
	// §2.1a). A no-op if e is nil.
	ToggleCopyMode(v *Preview, e fileEntry)
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
	he, ok := e.(*entry.HexEntry)
	if !ok {
		return
	}
	offset, ok := parseOffset(input)
	if !ok {
		return
	}
	he.HexOffset = clampHexOffset(offset, he.Size, v.hexBytesPerRow(he), v.viewportHeight())
}

func (textFileView) gotoLabel() string { return "goto line: " }

// gotoRangeHint renders the valid goto-line range as "1-<total lines>"
// (#114) — bestLineCount (scroll.go) is the same lower bound the goto
// prompt itself is already gated/clamped against (gotoLineBlocked,
// ScrollToLine), so the hint never promises a target the prompt would
// then reject.
func (textFileView) gotoRangeHint(e fileEntry) string {
	te := e.(*entry.TextEntry)
	return fmt.Sprintf("1-%d", bestLineCount(te))
}

func (textFileView) gotoLegend() []canvas.LegendEntry { return gotoLegend }

func (hexFileView) gotoLabel() string { return "goto offset: 0x" }

// gotoRangeHint renders the valid goto-offset range as "0-<last valid
// offset>" in hex (#114), matching the prompt's own always-hex input
// (parseOffset) and the file title bar's hex offset gutter. An empty
// (zero-size) file has no valid offset at all; the hint floors at 0
// rather than showing a negative range in that edge case.
func (hexFileView) gotoRangeHint(e fileEntry) string {
	he := e.(*entry.HexEntry)
	last := he.Size - 1
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
func (textFileView) DrawTitleBar(v *Preview, e fileEntry, x0, y0, w int, interactive bool) int {
	te := e.(*entry.TextEntry)
	path := tree.RelativeDisplayPath(v.RootPath, te.Path())
	rel := path

	lineCount := bestLineCount(te)
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
	if te.CopyMode {
		left = "[copy mode] " + rel
	}

	gotoBlocked := interactive && v.GotoBlockedPath == te.Path() && gotoLineBlocked(te.Stream != nil, te.Stream != nil && te.Stream.Done())

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
	case te.FindScan != nil:
		text = canvas.LegendText(w, left, withStatus(findStatusText(te), legend(findLegendNoMatches)))
	case te.FindQuery != "" && len(te.FindMatches) > 0:
		text = canvas.LegendText(w, left, withStatus(findStatusText(te), legend(findLegend)))
	case te.FindQuery != "":
		text = canvas.LegendText(w, left, withStatus(findStatusText(te), legend(findLegendNoMatches)))
	case te.CopyMode:
		text = canvas.LegendText(w, left, legend(fileLegendCopyModeOn))
	default:
		text = canvas.LegendText(w, rel, legend(v.fileLegendForIdle(te)))
	}

	style := canvas.StyleFileTitle
	switch {
	case te.CopyMode:
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
func (hexFileView) DrawTitleBar(v *Preview, e fileEntry, x0, y0, w int, interactive bool) int {
	he := e.(*entry.HexEntry)
	path := tree.RelativeDisplayPath(v.RootPath, he.Path())
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
	// textFileView.DrawTitleBar's "NL"-tag-plus-single-space alignment
	// trick, which instead gets this for free since its tag length and
	// its gutter's digit width are both derived from the same line
	// count; here the two aren't naturally coupled (hex digit count in
	// the file's size vs. formatSize's own decimal-with-unit-letter
	// rendering), so sizing formatSize's own output to the budget (and
	// padding whatever's left) takes its place.
	gw := hexGutterWidth(he.Size)
	sizeField := fmt.Sprintf("%-*s", gw-1, formatSize(he.Size, gw-1))
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
	case he.HexFindScan != nil:
		text = canvas.LegendText(w, left, withStatus(hexFindStatusText(he), legend(hexFindLegendNoMatches)))
	case he.HexFindQuery != "" && len(he.HexFindMatches) > 0:
		text = canvas.LegendText(w, left, withStatus(hexFindStatusText(he), legend(hexFindLegend)))
	case he.HexFindQuery != "":
		text = canvas.LegendText(w, left, withStatus(hexFindStatusText(he), legend(hexFindLegendNoMatches)))
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
func (textFileView) CurrentLegend(v *Preview, e fileEntry) (entries []canvas.LegendEntry, ok bool) {
	te := e.(*entry.TextEntry)
	gotoBlocked := v.GotoBlockedPath == te.Path() && gotoLineBlocked(te.Stream != nil, te.Stream != nil && te.Stream.Done())
	switch {
	case v.GotoPromptOpen:
		return gotoLegend, true
	case v.FindPromptOpen:
		return findPromptLegend, true
	case gotoBlocked:
		return nil, false
	case te.FindScan != nil:
		return findLegendNoMatches, true
	case te.FindQuery != "" && len(te.FindMatches) > 0:
		return findLegend, true
	case te.FindQuery != "":
		return findLegendNoMatches, true
	case te.CopyMode:
		return fileLegendCopyModeOn, true
	default:
		return fileLegend, true
	}
}

// CurrentLegend returns the keybinding legend the hex-tier file title
// bar is currently showing, mirroring DrawTitleBar's own state
// precedence exactly.
func (hexFileView) CurrentLegend(v *Preview, e fileEntry) (entries []canvas.LegendEntry, ok bool) {
	he := e.(*entry.HexEntry)
	switch {
	case v.GotoPromptOpen:
		return hexGotoLegend, true
	case v.HexFindPromptOpen:
		return hexFindPromptLegend, true
	case he.HexFindScan != nil:
		return hexFindLegendNoMatches, true
	case he.HexFindQuery != "" && len(he.HexFindMatches) > 0:
		return hexFindLegend, true
	case he.HexFindQuery != "":
		return hexFindLegendNoMatches, true
	default:
		return hexFileLegend, true
	}
}

// SyncFindScan picks up a finished TierPlainText find scan's result
// (docs/STREAMING_PREVIEW_DESIGN.md §9): once te.FindScan reports
// done, its matches are copied into FindMatches and the current match
// is seeded and scrolled to exactly like a synchronous find's result
// would be, then FindScan is cleared so this only ever runs once per
// scan. A no-op while e isn't a text-tier entry, or no scan is running,
// or it hasn't finished yet.
func (textFileView) SyncFindScan(v *Preview, e fileEntry) {
	te, ok := e.(*entry.TextEntry)
	if !ok || te.FindScan == nil {
		return
	}
	matches, done := te.FindScan.Snapshot()
	if !done {
		return
	}
	te.FindScan = nil
	te.FindMatches = matches
	if len(matches) == 0 {
		return
	}
	v.seedFindCurrent(te)
}

// SyncFindScan picks up a finished hex-find scan's result (SPEC.md
// §2.1a), mirroring textFileView.SyncFindScan: once he.HexFindScan
// reports done, its matches are copied into HexFindMatches and the
// current match is seeded, then HexFindScan is cleared so this only
// ever runs once per scan. A no-op while e isn't a hex-tier entry, or
// no scan is running, or it hasn't finished yet.
func (hexFileView) SyncFindScan(v *Preview, e fileEntry) {
	he, ok := e.(*entry.HexEntry)
	if !ok || he.HexFindScan == nil {
		return
	}
	matches, done := he.HexFindScan.Snapshot()
	if !done {
		return
	}
	he.HexFindScan = nil
	he.HexFindMatches = matches
	if len(matches) == 0 {
		return
	}
	v.seedHexFindCurrent(he)
}

// DrawContent renders the currently-displayed text-tier entry's content
// (SPEC.md §2.1): a line-number gutter plus wrapped, highlighted rows,
// or a "building preview…" placeholder while its background pass is
// still running. Assumes e is non-nil — Draw's own empty-state message
// (drawEmptyState) handles the no-entry case before this is ever
// reached.
func (textFileView) DrawContent(v *Preview, e fileEntry, x0, y0, w, h int) {
	te := e.(*entry.TextEntry)
	if !te.ContentReady() {
		msg := "building preview…"
		if te.Stream != nil {
			elapsed := te.Stream.Elapsed()
			if spinner.ShouldShow(te.Stream.Done(), elapsed, canvas.SpinnerThreshold) {
				frame := spinner.Frame(elapsed, canvas.SpinnerFPS, spinner.DefaultFrames)
				msg = "building preview " + string(frame)
			}
		}
		row := y0 + max(h/2, 1)
		v.Canvas.DrawText(x0, row, w, canvas.CenterPad(msg, w), canvas.StyleNormal)
		return
	}

	gw := gutterWidth(te)
	contentWidth := max(w-gw, 1)
	if te.Tier == preview.TierPlainText {
		v.ensureWindow(te, contentWidth, currentTopLine(te))
	} else {
		v.ensureWrapped(te, contentWidth)
	}

	viewportHeight := h
	digits := gw - 2

	topFlash := te.Path() == v.TopBumpPath && time.Since(v.TopBumpFlashStart) < canvas.FlashDuration
	bottomFlash := te.Path() == v.BottomBumpPath && time.Since(v.BottomBumpFlashStart) < canvas.FlashDuration
	lastDrawnY := -1

	for row := range viewportHeight {
		y := y0 + row
		i := te.Scroll + row
		if i >= len(te.Rows) {
			break
		}
		dr := te.Rows[i]
		if gw > 0 {
			numField := strings.Repeat(" ", digits)
			if dr.HasNumber {
				numField = fmt.Sprintf("%*d", digits, te.WindowStartLine+dr.SourceLine+1)
			}
			v.Canvas.DrawText(x0, y, gw, numField+"  ", canvas.StyleNormal)
		}
		v.drawSegments(x0+gw, y, contentWidth, dr.Segments, findHighlightsForRow(te, dr), te.CopyMode)
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
func (hexFileView) DrawContent(v *Preview, e fileEntry, x0, y0, w, h int) {
	he := e.(*entry.HexEntry)
	gw := hexGutterWidth(he.Size)
	n := bytesPerRowFor(w, gw)

	viewportHeight := h
	if viewportHeight < 0 {
		viewportHeight = 0
	}

	he.HexOffset = clampHexOffset(he.HexOffset, he.Size, n, viewportHeight)

	data, err := preview.ReadRange(he.Path(), he.HexOffset, viewportHeight*n)
	if err != nil {
		data = nil
	}

	topFlash := he.Path() == v.TopBumpPath && time.Since(v.TopBumpFlashStart) < canvas.FlashDuration
	bottomFlash := he.Path() == v.BottomBumpPath && time.Since(v.BottomBumpFlashStart) < canvas.FlashDuration
	lastDrawnY := -1

	for row := range viewportHeight {
		rowStart := row * n
		if rowStart >= len(data) {
			break
		}
		rowEnd := min(rowStart+n, len(data))
		y := y0 + row
		v.drawHexRow(x0, y, w, gw, n, he.HexOffset+int64(rowStart), data[rowStart:rowEnd], he)
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
// display row would leave the viewport. A no-op if e isn't a text-tier
// entry or its content isn't ready yet (docs/STREAMING_PREVIEW_
// DESIGN.md §4, §8).
//
// For a TierPlainText entry, delta is instead treated as a number of
// source lines rather than display rows (§8's "a real change to the
// preview view's scroll model," flagged and accepted here as a
// deliberate simplification, since that tier's window only ever holds a
// slice of the file rather than a whole-file wrap cache to walk display
// rows against) — Up/Down and Page Up/Page Down all move by source line
// count for that tier.
func (textFileView) Scroll(v *Preview, e fileEntry, delta int) {
	te, ok := e.(*entry.TextEntry)
	if !ok || !te.ContentReady() {
		return
	}
	width := v.computedWidth()
	if te.Tier == preview.TierPlainText {
		cur := currentTopLine(te)
		target := clamp(cur+delta, 1, bestLineCount(te))
		if target == cur {
			v.bumpEdge(te, delta)
		}
		v.ensureWindow(te, width, target)
		v.setScrollToLine(te, target)
		return
	}
	v.ensureWrapped(te, width)
	old := te.Scroll
	te.Scroll = clamp(te.Scroll+delta, 0, v.maxScroll(te, v.viewportHeight()))
	if te.Scroll == old {
		v.bumpEdge(te, delta)
	}
}

// Scroll moves e's hex-view viewport by delta rows (SPEC.md §2.1a),
// clamped so it never goes negative or past the file's last row. A
// no-op if e isn't a hex-tier entry. Mirrors textFileView.Scroll's own
// edge-bump behavior: a move that clamps back to exactly where it
// started (already at the top/bottom) calls bumpEdge, the same
// path-keyed flash-request mechanism the text tiers use, so Up/Page Up
// past offset 0 (or Down/Page Down past the last row) gets the same
// "you've hit the end" cue there — reused as-is rather than duplicated,
// since it already operates in terms of the entry and a signed delta,
// neither of which is tier-specific.
func (hexFileView) Scroll(v *Preview, e fileEntry, delta int) {
	he, ok := e.(*entry.HexEntry)
	if !ok {
		return
	}
	n := v.hexBytesPerRow(he)
	old := he.HexOffset
	he.HexOffset = clampHexOffset(he.HexOffset+int64(delta)*int64(n), he.Size, n, v.viewportHeight())
	if he.HexOffset == old {
		v.bumpEdge(he, delta)
	}
}

// JumpStart is a no-op for the text tiers: Home currently has no bound
// behavior for a text-tier entry — preview.go's HandleKey only ever
// reached the pre-interface hexJumpStart for a hex-tier entry, never a
// text-tier equivalent. Kept as an explicit no-op now that the
// key-to-action mapping is unified across tiers, preserving that
// existing behavior rather than silently changing it.
func (textFileView) JumpStart(v *Preview, e fileEntry) {}

// JumpEnd is a no-op for the text tiers; see JumpStart.
func (textFileView) JumpEnd(v *Preview, e fileEntry) {}

// JumpStart jumps e's hex-view viewport to offset 0 (SPEC.md §2.1a's
// Home binding).
func (hexFileView) JumpStart(v *Preview, e fileEntry) {
	if he, ok := e.(*entry.HexEntry); ok {
		he.HexOffset = 0
	}
}

// JumpEnd jumps e's hex-view viewport to hexMaxOffset — the file's last
// row at the bottom of a full viewport, rather than just its own start
// with the rest of the screen left blank (SPEC.md §2.1a's End binding).
func (hexFileView) JumpEnd(v *Preview, e fileEntry) {
	he, ok := e.(*entry.HexEntry)
	if !ok {
		return
	}
	he.HexOffset = hexMaxOffset(he.Size, v.hexBytesPerRow(he), v.viewportHeight())
}

// PerformFind executes an in-file find (SPEC.md §2.4): locates every
// case-insensitive match of query, then jumps to the first one at or
// after the source line currently at the top of the viewport — the same
// "search forward from here" behavior as `less` — wrapping to the very
// first match (and noting the wrap) if none exists at or after that
// point. A no-op if e isn't a text-tier entry; an empty query clears
// any existing find state instead of searching (mirroring a bare "/" +
// Enter in `less`).
//
// For a TierPlainText entry, whose full content isn't resident
// (docs/STREAMING_PREVIEW_DESIGN.md §9), matches can't be located
// synchronously — this instead cancels any previous scan for the entry
// and starts a new background one (find.StartScan), leaving FindMatches
// empty and FindCurrent at -1 until SyncFindScan picks up its result on
// a later frame; the file title bar's status area shows a "searching…"
// spinner in the meantime (findStatusText) rather than blocking this
// keystroke.
func (textFileView) PerformFind(v *Preview, e fileEntry, query string) {
	te, ok := e.(*entry.TextEntry)
	if !ok {
		return
	}

	if te.FindScan != nil {
		te.FindScan.Cancel()
		te.FindScan = nil
	}
	te.FindQuery = query
	te.FindMatches = nil
	te.FindCurrent = -1
	te.FindWrapNote = ""
	if query == "" {
		return
	}

	if te.Tier == preview.TierPlainText {
		te.FindScan = find.StartScan(te.Path(), query)
		return
	}

	v.ensureWrapped(te, v.computedWidth())
	te.FindMatches = find.InLines(te.Lines, query)
	if len(te.FindMatches) == 0 {
		return
	}
	v.seedFindCurrent(te)
}

// PerformFind executes a hex-view find (SPEC.md §2.1a): always a
// background scan (hexfind.StartScan), since a TierBinary entry never
// holds its file's full byte content resident regardless of size,
// unlike text find's TierHighlighted/TierPlainText split
// (textFileView.PerformFind). A no-op if e isn't a hex-tier entry; an
// empty query clears any existing hex-find state instead of searching,
// mirroring textFileView.PerformFind's own empty-query behavior.
func (hexFileView) PerformFind(v *Preview, e fileEntry, query string) {
	he, ok := e.(*entry.HexEntry)
	if !ok {
		return
	}
	if he.HexFindScan != nil {
		he.HexFindScan.Cancel()
		he.HexFindScan = nil
	}
	he.HexFindQuery = query
	he.HexFindMatches = nil
	he.HexFindCurrent = -1
	he.HexFindWrapNote = ""
	if query == "" {
		return
	}
	he.HexFindScan = hexfind.StartScan(he.Path(), query)
}

// FindStep moves the current match by delta (+1 for `n`/next, -1 for
// `N`/previous), wrapping around at either end and noting the wrap
// (SPEC.md §2.4) — the same wraparound stepper the browser and finder
// overlays already use (internal/tree.MoveSelection). A no-op if e
// isn't a text-tier entry or has no matches.
func (textFileView) FindStep(v *Preview, e fileEntry, delta int) {
	te, ok := e.(*entry.TextEntry)
	if !ok || len(te.FindMatches) == 0 {
		return
	}
	next := tree.MoveSelection(te.FindCurrent, delta, len(te.FindMatches))
	switch {
	case delta > 0 && next < te.FindCurrent:
		te.FindWrapNote = "wrapped to top"
	case delta < 0 && next > te.FindCurrent:
		te.FindWrapNote = "wrapped to bottom"
	default:
		te.FindWrapNote = ""
	}
	te.FindCurrent = next
	v.scrollToFindMatch(te)
}

// FindStep moves the current hex-find match by delta (+1/-1), wrapping
// around at either end and noting the wrap (SPEC.md §2.1a), mirroring
// textFileView.FindStep. A no-op if e isn't a hex-tier entry or has no
// matches.
func (hexFileView) FindStep(v *Preview, e fileEntry, delta int) {
	he, ok := e.(*entry.HexEntry)
	if !ok || len(he.HexFindMatches) == 0 {
		return
	}
	next := tree.MoveSelection(he.HexFindCurrent, delta, len(he.HexFindMatches))
	switch {
	case delta > 0 && next < he.HexFindCurrent:
		he.HexFindWrapNote = "wrapped to top"
	case delta < 0 && next > he.HexFindCurrent:
		he.HexFindWrapNote = "wrapped to bottom"
	default:
		he.HexFindWrapNote = ""
	}
	he.HexFindCurrent = next
	v.scrollToHexFindMatch(he)
}

// ClearFind clears e's in-file find state (SPEC.md §2.4), if any — its
// query, matches, current index, and wrap note — so its highlighting
// and file-title-bar status disappear, leaving the idle file title bar
// in their place. Bound to Escape at the primary preview view: this
// does not conflict with Escape's deliberate no-op-when-nothing-to-
// back-out-of behavior there (it still never quits — only `q` does), it
// just gives find an explicit way out, since otherwise it would persist
// until superseded by a new search on the same entry. A no-op if e
// isn't a text-tier entry, or there's no active query or in-progress
// scan, so Escape stays inert exactly when there was nothing to clear.
// Also cancels a still-running TierPlainText find scan
// (docs/STREAMING_PREVIEW_DESIGN.md §9) rather than leaving it to
// finish unread.
func (textFileView) ClearFind(v *Preview, e fileEntry) {
	te, ok := e.(*entry.TextEntry)
	if !ok || (te.FindQuery == "" && te.FindScan == nil) {
		return
	}
	if te.FindScan != nil {
		te.FindScan.Cancel()
		te.FindScan = nil
	}
	te.FindQuery = ""
	te.FindMatches = nil
	te.FindCurrent = -1
	te.FindWrapNote = ""
}

// ClearFind clears e's hex-find state (SPEC.md §2.1a), if any,
// canceling a still-running scan first — the hex view's analog of
// textFileView.ClearFind. A no-op if e isn't a hex-tier entry, or
// there's no active query or in-progress scan.
func (hexFileView) ClearFind(v *Preview, e fileEntry) {
	he, ok := e.(*entry.HexEntry)
	if !ok || (he.HexFindQuery == "" && he.HexFindScan == nil) {
		return
	}
	if he.HexFindScan != nil {
		he.HexFindScan.Cancel()
		he.HexFindScan = nil
	}
	he.HexFindQuery = ""
	he.HexFindMatches = nil
	he.HexFindCurrent = -1
	he.HexFindWrapNote = ""
}

// ToggleCopyMode toggles e's copy mode (SPEC.md §2.1): strips the
// preview's line-number gutter and syntax-color styling for e
// specifically, so a terminal mouse selection over its content grabs
// exactly the file's own characters. A no-op if e isn't a text-tier
// entry.
func (textFileView) ToggleCopyMode(v *Preview, e fileEntry) {
	if te, ok := e.(*entry.TextEntry); ok {
		te.CopyMode = !te.CopyMode
	}
}

// ToggleCopyMode is a no-op for the hex tier: copy mode does not apply
// to a hex view (SPEC.md §2.1a) — pressing `c` while one is displayed
// has no effect.
func (hexFileView) ToggleCopyMode(v *Preview, e fileEntry) {}

// fileViewFor returns the fileView implementation for e's tier —
// hexFileView for a *entry.HexEntry, textFileView otherwise —
// mirroring every other tier-branch point in this package (draw.go,
// scroll.go, hexview.go). e may be nil: the type assertion below
// safely reports ok=false for a nil interface value, so textFileView is
// a harmless default, since the goto prompt is never opened without a
// displayed entry (preview.go's HandleKey only sets GotoPromptOpen when
// e != nil).
func fileViewFor(e fileEntry) fileView {
	if _, ok := e.(*entry.HexEntry); ok {
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
