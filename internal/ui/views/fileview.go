package views

import (
	"fmt"

	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/preview"
	"github.com/nitti/dirtree/internal/ui/canvas"
)

// fileView captures the pieces of the goto prompt's behavior that
// differ between the text and hex tiers of the primary preview view
// (SPEC.md §2.1, §2.1a): which characters are valid input (decimal
// digits addressing a source line, vs. hex digits addressing a byte
// offset), how a submitted input is applied to the displayed entry's
// viewport, and how the prompt itself is labeled/legended/range-
// hinted. This is the shared "file view" abstraction proposed in #114,
// replacing the several `if e.Tier == preview.TierBinary { ... } else
// { ... }` branches drawFileTitleBar/drawHexFileTitleBar and
// handleGotoPromptKey used to carry directly for the goto prompt
// specifically — the title bar's other states (find, copy mode) and
// the file-legend/line-count-vs-size questions #114 also raises remain
// their own separate branch points for now, left for further follow-up
// rather than folded in all at once.
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
// the label without duplicating the tier check drawFileTitleBar/
// drawHexFileTitleBar already make via fileViewFor.
func (v *Preview) GotoLabel() string {
	return fileViewFor(v.Files.DisplayedEntry()).gotoLabel()
}
