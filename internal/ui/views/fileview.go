package views

import (
	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/preview"
)

// fileView captures the pieces of the goto prompt's behavior that
// differ between the text and hex tiers of the primary preview view
// (SPEC.md §2.1, §2.1a): which characters are valid input (decimal
// digits addressing a source line, vs. hex digits addressing a byte
// offset) and how a submitted input is applied to the displayed
// entry's viewport. This is the first slice of the shared "file view"
// abstraction proposed in #114, replacing the `if e.Tier ==
// preview.TierBinary { ... } else { ... }` branch handleGotoPromptKey
// (scroll.go) used to carry directly with a single dispatch to one of
// the two implementations below. More of the title-bar/goto-prompt
// commonality catalogued in #114 (shared placement, a "valid range"
// hint) is expected to move behind this interface in follow-up work,
// not all at once.
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

func (textFileView) jumpTo(v *Preview, input string) {
	v.gotoLine(input)
}

func (hexFileView) acceptGotoRune(r rune) bool {
	return isHexOffsetRune(r)
}

func (hexFileView) jumpTo(v *Preview, input string) {
	v.gotoOffset(input)
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
