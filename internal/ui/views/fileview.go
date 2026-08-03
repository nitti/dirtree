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
// (scroll.go) used to carry directly: each tier's own addressing logic
// (formerly the standalone gotoLine/gotoOffset methods) now lives
// directly in its jumpTo implementation below, rather than jumpTo being
// a thin pass-through to a same-shaped method elsewhere. More of the
// title-bar/goto-prompt commonality catalogued in #114 (shared
// placement, a "valid range" hint) is expected to move behind this
// interface in follow-up work, not all at once.
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
	e.HexOffset = clampHexOffset(offset, e.Size, v.hexBytesPerRow(e), v.viewportHeight())
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
