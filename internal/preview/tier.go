package preview

// Tier is the highlighting/residency strategy for an open file, decided
// from its size (docs/STREAMING_PREVIEW_DESIGN.md §2, §5): at open time,
// and re-decided the same way on every reload that changes the file
// (openfiles.List.Reload) rather than treated as sticky for the entry's
// lifetime — the tier bounds memory/highlighting cost against the
// file's *current* size, not whatever size it happened to be when first
// opened.
type Tier int

const (
	// TierHighlighted files are fully read, decoded, and highlighted in
	// the background, then kept fully resident for the entry's lifetime
	// — today's model, just made non-blocking (§2's "Tier A").
	TierHighlighted Tier = iota
	// TierPlainText files are never fully resident: no highlighting is
	// attempted, and only the lines actually needed for the current
	// viewport are read on demand, seeking via the background pass's
	// line-offset index (§2's "Tier B").
	TierPlainText
	// TierBinary files are files ReadCapped flagged as binary (a NUL
	// byte in the leading capped peek). They're never decoded into
	// lines/runes at all: only the byte range needed for the current
	// hex-view viewport is read on demand, directly via ReadAt at
	// row*bytesPerRow — no line-offset index is needed since hex-view
	// rows are fixed-width by construction rather than line-delimited.
	TierBinary
)

// HighlightCeiling is the size (in bytes) at or under which a file is
// TierHighlighted; larger files are TierPlainText (§5). A var, not a
// const, so it can be tuned later against real timing data without a
// code change, and so tests can exercise both tiers without a real
// multi-megabyte fixture — the same reasoning search.go's own
// maxConcurrentScans/perFileTimeout vars already follow.
var HighlightCeiling int64 = 25 * 1024 * 1024

// TierFor decides a file's tier from its size, per HighlightCeiling.
func TierFor(size int64) Tier {
	if size <= HighlightCeiling {
		return TierHighlighted
	}
	return TierPlainText
}
