// Package canvas holds the tcell drawing primitives shared by every
// view: a screen wrapper, the style vocabulary, and the keybinding
// legend-fit engine (SPEC.md §5.2's header/title-bar convention). It's a
// concrete type, not an interface — there's exactly one implementation
// (backed by tcell.Screen) and no test-double need (this rendering layer
// is manually verified, not unit-tested, per the project's CLAUDE.md), so
// there's no reason to pay for an interface boundary here.
package canvas

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/preview"
)

// The style vocabulary every view draws with. StyleNormal is the
// baseline; StyleSelected is the cursor-position reverse-video used
// throughout every row-list view; StyleHeader is the top menu-bar/title
// row's background.
var (
	StyleNormal   = tcell.StyleDefault
	StyleSelected = tcell.StyleDefault.Reverse(true)
	StyleHeader   = tcell.StyleDefault.Background(tcell.ColorDarkBlue).Foreground(tcell.ColorWhite)
	// StyleHeaderMode is StyleHeader with bold applied, used only for the
	// mode-name label (e.g. "BROWSE", "SEARCH") that replaces the tree
	// root path on the header/title bar's left side while a mode other
	// than the primary preview view is active — bold sets the label
	// apart from the plain-weight legend sharing the same row, similar
	// to how editors like hx render their current mode name.
	StyleHeaderMode = StyleHeader.Bold(true)
	// StyleHeaderDim is StyleHeader with the dim attribute applied, used
	// to mark a single header legend entry as currently inert (e.g.
	// "switch files" while no file is open) without removing it from
	// the legend or dropping its keybinding hint — the entry is still
	// legible, just visually deprioritized against the rest of the row.
	StyleHeaderDim = StyleHeader.Dim(true)
	// StyleHeaderQuit replaces StyleHeader while the hold-to-quit
	// gesture (SPEC.md §5.2) is in progress: a saturated red background
	// deliberately breaks from the rest of the header/title bar's
	// otherwise subdued palette, since this is the one moment the app
	// is about to discard the session's state and is worth demanding
	// attention rather than staying quiet about it — the sole
	// intentional exception to §5.3's "subtle, not flashy" default.
	StyleHeaderQuit = tcell.StyleDefault.Background(tcell.ColorRed).Foreground(tcell.ColorWhite).Bold(true)
	StyleFileTitle  = tcell.StyleDefault.Background(tcell.ColorDarkSlateGray).Foreground(tcell.ColorWhite)
	StyleError      = tcell.StyleDefault.Foreground(tcell.ColorRed)
	StyleBadge      = tcell.StyleDefault.Background(tcell.ColorOrange).Foreground(tcell.ColorBlack)
	StyleFindMatch  = tcell.StyleDefault.Background(tcell.ColorYellow).Foreground(tcell.ColorBlack)
	// StyleFindCurrent marks in-file find's active match (§2.4), and
	// doubles as the "this query unambiguously identifies exactly one
	// row" highlight for quick open (§4.2) and jump to file (§4.3) —
	// both are the same underlying idea (the one row a search-like query
	// currently resolves to), distinct from both plain StyleSelected
	// (cursor position, with no implication the query alone singled it
	// out) and StyleFindMatch (one of several matches, not the sole one).
	StyleFindCurrent = tcell.StyleDefault.Background(tcell.ColorOrange).Foreground(tcell.ColorBlack).Bold(true)
	// StyleCopyModeTitle replaces StyleFileTitle whenever copy mode
	// (SPEC.md §2.1) is active, so the file title bar itself makes copy
	// mode's on/off state visually unmistakable, not just the legend
	// text.
	StyleCopyModeTitle = tcell.StyleDefault.Background(tcell.ColorDarkGreen).Foreground(tcell.ColorWhite)
	// StyleSearchInput sets the content search (SPEC.md §9.2) and quick
	// open (§4.2) query rows apart from the plain-background list below
	// them, so the "this is where you're typing" row is visually
	// unmistakable at a glance rather than blending into the rest of the
	// overlay.
	StyleSearchInput = tcell.StyleDefault.Background(tcell.ColorDarkSlateGray).Foreground(tcell.ColorWhite)
	// StyleQueryGhost is StyleSearchInput with the dim attribute applied,
	// used for the autocompleted "remainder of the path" ghost text a
	// query row shows immediately after what the user actually typed,
	// once that text unambiguously identifies a single match (quick
	// open, SPEC.md §4.2; jump to file, §4.3) — same background as the
	// query text it trails, dimmed so it reads as a suggestion rather
	// than something already entered.
	StyleQueryGhost = StyleSearchInput.Dim(true)
	// StyleFlash briefly replaces a just-opened file row's normal style,
	// as an on-open confirmation distinct from StyleSelected (cursor
	// position) and from the lasting "●" already-open indicator every
	// open file's row shows regardless of when it was opened.
	StyleFlash = tcell.StyleDefault.Background(tcell.ColorGreen).Foreground(tcell.ColorBlack).Bold(true)
	// StyleFlashError is StyleFlash's counterpart for a live-refresh
	// discovering a directory that newly failed to list (SPEC.md §6.1),
	// or any other failed operation: same brief-attention-grabbing role,
	// red instead of green so it reads as a problem rather than a
	// confirmation.
	StyleFlashError = tcell.StyleDefault.Background(tcell.ColorRed).Foreground(tcell.ColorWhite).Bold(true)
)

// FlashDuration is how long a brief on-open/on-error row flash (SPEC.md
// §2.2/§3.4/§9.2's "on-open confirmation", §6.1's error flash) stays
// visible before fading back to normal, shared by every view that shows
// one.
const FlashDuration = 400 * time.Millisecond

// SpinnerThreshold/SpinnerFPS drive the "still working" spinner shown
// once a background operation (content search's scan, the indexing
// badge) runs long enough to be noticeable, both by the badge and by
// any view showing its own in-place spinner (e.g. content search's
// "searching…" text).
const (
	SpinnerThreshold = 250 * time.Millisecond
	SpinnerFPS       = 10.0
)

// MinTerminalWidth/MinTerminalHeight (SPEC.md §6.4) gate every screen:
// below either threshold, no layout (header + legend, popups, split
// content) can render without truncating or overlapping in ways that
// are actively misleading rather than just cramped. Every legend's
// priority-1 (never-dropped) text is guaranteed to fit within
// MinTerminalWidth on its own — each view's own tests enforce this
// against its own legend tables.
const (
	MinTerminalWidth  = 58
	MinTerminalHeight = 15
)

var categoryStyles = map[preview.Category]tcell.Style{
	preview.CategoryComment:  tcell.StyleDefault.Foreground(tcell.ColorGray),
	preview.CategoryString:   tcell.StyleDefault.Foreground(tcell.ColorGreen),
	preview.CategoryNumber:   tcell.StyleDefault.Foreground(tcell.ColorPurple),
	preview.CategoryKeyword:  tcell.StyleDefault.Foreground(tcell.ColorTeal).Bold(true),
	preview.CategoryFunction: tcell.StyleDefault.Foreground(tcell.ColorBlue),
	preview.CategoryOperator: tcell.StyleDefault.Foreground(tcell.ColorYellow),
	preview.CategoryText:     tcell.StyleDefault,
}

// StyleFor returns the style a preview content category renders in
// (SPEC.md §2.1), falling back to StyleNormal for an unrecognized
// category.
func StyleFor(cat preview.Category) tcell.Style {
	if s, ok := categoryStyles[cat]; ok {
		return s
	}
	return StyleNormal
}

// LegendEntry is one "[key] action" segment of a keybinding legend,
// tagged with a drop priority for narrow terminals (SPEC.md §5.2):
// priority 1 entries are never dropped, priority 2 entries are dropped
// before priority 1, and priority 3 entries are dropped before priority
// 2 — see LegendFit below. Entries always render left-to-right in the
// order they're declared; dropping only ever omits an entry, it never
// reorders the survivors.
type LegendEntry struct {
	Text     string
	Priority int
}

// HideKeysLegend replaces every main title bar's own legend while the
// help overlay is showing (SPEC.md §5.4): every view's Draw checks its
// own Shared.HelpVisible and substitutes this in place of its usual
// legend, so the full keybinding reference (drawn separately by App)
// isn't competing with a second, now-redundant copy of the same
// information split across every title bar.
var HideKeysLegend = []LegendEntry{{Text: "[?] hide keys", Priority: 1}}

// LegendString joins entries' text left-to-right, in declaration order,
// with the app's standard two-space separator between legend segments.
func LegendString(entries []LegendEntry) string {
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = e.Text
	}
	return strings.Join(parts, "  ")
}

// KeepUpToPriority returns the subset of entries whose priority is
// maxPriority or lower, preserving their original order — the building
// block LegendFit uses to progressively drop the lowest-priority tier
// first (SPEC.md §5.2's narrow-terminal drop order).
func KeepUpToPriority(entries []LegendEntry, maxPriority int) []LegendEntry {
	out := make([]LegendEntry, 0, len(entries))
	for _, e := range entries {
		if e.Priority <= maxPriority {
			out = append(out, e)
		}
	}
	return out
}

// RightAlign right-pads text with leading spaces so its own right edge
// lands on column w-1 — used for the legend once left-hand content has
// been dropped entirely for width, so the legend is right-aligned there
// too, the same as it is whenever left-hand content precedes it (FitPair,
// below). Without this, DrawText's own padding (which always lands on
// the right of whatever it's given) would make the legend read as
// left-aligned the moment left-hand content drops out — a visible
// alignment jump on every resize crossing that boundary.
func RightAlign(w int, text string) string {
	if n := len([]rune(text)); n < w {
		return strings.Repeat(" ", w-n) + text
	}
	return text
}

// FitPair left-aligns left and right-aligns legend within w columns,
// with at least one space of separation between them, reporting whether
// they both fit at all.
func FitPair(w int, left, legend string) (text string, ok bool) {
	leftLen, legendLen := len([]rune(left)), len([]rune(legend))
	if gap := w - leftLen - legendLen; gap >= 1 {
		return left + strings.Repeat(" ", gap) + legend, true
	}
	return "", false
}

// LegendText composes left and entries the same way LegendFit does,
// discarding the leftIncluded flag for callers that don't need it (most
// don't — it exists only for DrawHeaderMode's bold-label styling).
func LegendText(w int, left string, entries []LegendEntry) string {
	text, _ := LegendFit(w, left, entries)
	return text
}

// LegendFit is LegendText's underlying fit computation (SPEC.md §5.2's
// header/title bar convention, applied everywhere a header combines some
// left-hand content with a keybinding legend), additionally reporting
// whether left was included (as opposed to dropped for width reasons),
// so a caller that needs to style left differently from the legend
// (DrawHeaderMode, below) knows whether left actually appears in the
// returned text.
//
// left and the legend's entries share one drop order, weakest first:
// priority-3 entries, then left itself (conceptually priority "2.5" —
// dropped only once every priority-3 entry already has been, but before
// any priority-2 entry is), then priority-2 entries. Priority-1 entries
// are never dropped. This means left survives being squeezed by a
// disposable priority-3 key hint (e.g. "[ctrl+u] clear") rather than
// being sacrificed first regardless of how little space that would
// actually free — losing the root path/mode label is a real loss of
// context, so it's worth less-essential legend entries going first if
// that's enough on its own. Entries are only ever omitted wholesale,
// never truncated mid-entry, and survivors keep their original
// left-to-right order. Priority-1 entries are never dropped; by
// construction every legend's priority-1 subset is short enough to fit
// any terminal wide enough to run the app at all (SPEC.md §6.4), but as
// a last-resort fallback if it somehow still doesn't, that priority-1
// text is returned anyway and left to DrawText's own column clip.
//
// The legend itself is always right-aligned — flush with the row's right
// edge whether or not left is present (RightAlign handles the no-left
// case; FitPair's variable-width gap handles it whenever left precedes
// the legend) — so its own right edge, and therefore its reading
// position, never jumps between alignments as left is dropped or
// restored across a resize.
func LegendFit(w int, left string, entries []LegendEntry) (text string, leftIncluded bool) {
	full := LegendString(entries)
	tier2 := LegendString(KeepUpToPriority(entries, 2))
	tier1 := LegendString(KeepUpToPriority(entries, 1))

	if fit, ok := FitPair(w, left, full); ok {
		return fit, true
	}
	if fit, ok := FitPair(w, left, tier2); ok {
		return fit, true
	}
	if len([]rune(tier2)) <= w {
		return RightAlign(w, tier2), false
	}
	return RightAlign(w, tier1), false
}

// CenterPad left-pads s with spaces so it reads as horizontally centered
// within w columns (the caller's own DrawText call then pads the
// remaining right-hand columns).
func CenterPad(s string, w int) string {
	if len(s) >= w {
		return s
	}
	pad := (w - len(s)) / 2
	return strings.Repeat(" ", pad) + s
}

// GutterWidth returns the line-number gutter's column width for a file
// with numLines source lines: wide enough for the largest line number,
// plus a two-column separator (SPEC.md §2.1).
func GutterWidth(numLines int) int {
	digits := max(len(fmt.Sprintf("%d", numLines)), 1)
	return digits + 2
}

// Canvas wraps a tcell.Screen with the drawing primitives every view
// shares. Constructed once (New) after the real terminal screen is
// initialized.
type Canvas struct {
	Screen tcell.Screen
}

// New builds a Canvas around an already-initialized screen.
func New(screen tcell.Screen) *Canvas {
	return &Canvas{Screen: screen}
}

// Size returns the current terminal dimensions.
func (c *Canvas) Size() (int, int) {
	return c.Screen.Size()
}

// Clear blanks the screen ahead of a new frame.
func (c *Canvas) Clear() {
	c.Screen.Clear()
}

// Show flushes the current frame to the terminal.
func (c *Canvas) Show() {
	c.Screen.Show()
}

// ShowCursor places the terminal's own cursor at (x, y), the caret
// position of whichever text-entry field currently owns keystrokes
// (quick open, search, jump-to-file, in-file find, goto-line) — takes
// effect on the next Show(), the same as any other draw call.
func (c *Canvas) ShowCursor(x, y int) {
	c.Screen.ShowCursor(x, y)
}

// HideCursor hides the terminal's own cursor, the default whenever no
// text-entry field is active.
func (c *Canvas) HideCursor() {
	c.Screen.HideCursor()
}

// SetContent places a single rune at (x, y) with no combining
// characters — no caller here ever needs them.
func (c *Canvas) SetContent(x, y int, r rune, style tcell.Style) {
	c.Screen.SetContent(x, y, r, nil, style)
}

// DrawText draws text starting at (x, y), clipped and padded with spaces
// to exactly w columns so style (e.g. selection reverse-video, the
// header/badge backgrounds) fills the full row width regardless of text
// length.
func (c *Canvas) DrawText(x, y, w int, text string, style tcell.Style) {
	runes := []rune(text)
	for i := range w {
		r := ' '
		if i < len(runes) {
			r = runes[i]
		}
		c.Screen.SetContent(x+i, y, r, nil, style)
	}
}

// DrawBox draws a bordered rectangle with an optional title embedded in
// the top border.
func (c *Canvas) DrawBox(x0, y0, w, h int, title string) {
	if w < 2 || h < 2 {
		return
	}
	for x := 1; x < w-1; x++ {
		c.Screen.SetContent(x0+x, y0, '─', nil, StyleNormal)
		c.Screen.SetContent(x0+x, y0+h-1, '─', nil, StyleNormal)
	}
	for y := 1; y < h-1; y++ {
		c.Screen.SetContent(x0, y0+y, '│', nil, StyleNormal)
		c.Screen.SetContent(x0+w-1, y0+y, '│', nil, StyleNormal)
	}
	c.Screen.SetContent(x0, y0, '┌', nil, StyleNormal)
	c.Screen.SetContent(x0+w-1, y0, '┐', nil, StyleNormal)
	c.Screen.SetContent(x0, y0+h-1, '└', nil, StyleNormal)
	c.Screen.SetContent(x0+w-1, y0+h-1, '┘', nil, StyleNormal)

	if title != "" && w > 4 {
		label := " " + title + " "
		if len(label) > w-2 {
			label = label[:w-2]
		}
		c.DrawText(x0+1, y0, len(label), label, StyleNormal)
	}
}

// FillRect blanks a rectangle at style, used to erase whatever was drawn
// underneath a popup before drawing its contents on top.
func (c *Canvas) FillRect(x0, y0, w, h int, style tcell.Style) {
	for y := range h {
		c.DrawText(x0, y0+y, w, "", style)
	}
}

// DrawHeader draws text as the top menu-bar row, in the standard header
// style.
func (c *Canvas) DrawHeader(w int, text string) {
	c.DrawText(0, 0, w, text, StyleHeader)
}

// DrawHeaderDimmed draws the header/title bar like DrawHeaderMode (left
// bolded, if it survived LegendFit's own width-driven dropping), then
// re-draws dimText in StyleHeaderDim if it appears in the fitted legend
// text — it may not, having been dropped for width per LegendFit's own
// priority order, in which case this is a no-op beyond the base draw.
// Generalizes DrawHeaderMode's "compose once, overlay a styled
// sub-span" pattern to a second, independent overlay span.
func (c *Canvas) DrawHeaderDimmed(w int, left string, entries []LegendEntry, dimText string) {
	text, leftIncluded := LegendFit(w, left, entries)
	c.DrawText(0, 0, w, text, StyleHeader)
	if leftIncluded {
		c.DrawText(0, 0, len([]rune(left)), left, StyleHeaderMode)
	}
	runes, dim := []rune(text), []rune(dimText)
	for i := 0; i+len(dim) <= len(runes); i++ {
		if string(runes[i:i+len(dim)]) == dimText {
			c.DrawText(i, 0, len(dim), dimText, StyleHeaderDim)
			return
		}
	}
}

// DrawHeaderAllDimmed draws the header/title bar like DrawHeaderMode
// (left bolded, if it survived LegendFit's own width-driven dropping),
// but renders its entire legend (everything to the right of left) in
// StyleHeaderDim — used when none of a header's own legend entries are
// actually invokable in the current context (e.g. the primary preview
// view's title bar, still drawn underneath the open-files-list
// overlay, SPEC.md §2.3, which owns every key while it's active), as
// opposed to DrawHeaderDimmed's single-entry dimming for the narrower
// case where only one entry is inert.
func (c *Canvas) DrawHeaderAllDimmed(w int, left string, entries []LegendEntry) {
	text, leftIncluded := LegendFit(w, left, entries)
	c.DrawText(0, 0, w, text, StyleHeader)
	start := 0
	if leftIncluded {
		start = len([]rune(left))
		c.DrawText(0, 0, start, left, StyleHeaderMode)
	}
	runes := []rune(text)
	if start < len(runes) {
		c.DrawText(start, 0, len(runes)-start, string(runes[start:]), StyleHeaderDim)
	}
}

// DrawHeaderMode renders the header/title bar with label — either a
// bold, all-caps mode name (e.g. "BROWSE" or "SEARCH") or, in the
// primary preview view, the bolded tree root path — standing in on the
// left, and legend right-aligned, using the same fit/drop rule
// LegendText uses elsewhere — label is dropped once legend can't buy
// back enough room some other way. Only label itself is bold; legend
// keeps the normal header weight.
func (c *Canvas) DrawHeaderMode(w int, label string, entries []LegendEntry) {
	text, included := LegendFit(w, label, entries)
	c.DrawText(0, 0, w, text, StyleHeader)
	if included {
		c.DrawText(0, 0, len([]rune(label)), label, StyleHeaderMode)
	}
}

// DrawHeaderQuitting draws the hold-to-quit gesture's header/title bar
// variant (SPEC.md §5.2): the full row in StyleHeaderQuit with message
// right-aligned, its left edge fading away over hiddenPrefix columns —
// the same "anchored, directional motion" fade convention DrawCornerBadge
// and internal/toast use (§5.3), just applied to the whole row's width
// rather than to a corner string's own rune count, so the row's right
// edge (where message sits) stays anchored while its left edge visibly
// erodes as the hold progresses. Columns below hiddenPrefix are left
// untouched, so whatever the caller already cleared/drew there (a blank
// frame, per draw()'s per-frame Clear) shows through.
func (c *Canvas) DrawHeaderQuitting(w int, message string, hiddenPrefix int) {
	for x := hiddenPrefix; x < w; x++ {
		c.Screen.SetContent(x, 0, ' ', nil, StyleHeaderQuit)
	}
	msg := []rune(message)
	start := w - len(msg)
	for i, r := range msg {
		x := start + i
		if x >= hiddenPrefix && x < w {
			c.Screen.SetContent(x, 0, r, nil, StyleHeaderQuit)
		}
	}
}

// DrawQuittingDim dims every already-drawn cell in a rectangle (applying
// the same StyleDim attribute StyleHeaderDim etc. already use elsewhere)
// for as long as the hold-to-quit gesture is in progress, the content-area
// counterpart to the header bar's own row-0 erosion — a plain on/off dim
// rather than a gradual blend, since no color-interpolation primitive
// exists anywhere in this codebase and one isn't worth inventing solely
// for this. Reads back whatever the caller already drew via Get so
// existing styling (syntax highlighting, the file title bar) dims in
// place rather than being erased.
func (c *Canvas) DrawQuittingDim(x0, y0, w, h int) {
	for y := y0; y < y0+h; y++ {
		for x := x0; x < x0+w; x++ {
			str, style, _ := c.Screen.Get(x, y)
			r, _ := utf8.DecodeRuneInString(str)
			c.Screen.SetContent(x, y, r, nil, style.Dim(true))
		}
	}
}

// DrawCornerBadge draws text right-anchored on the bottom row, the
// shared anchor point both the indexing badge and the error toast fade
// out from (SPEC.md §5.3's "anchored, directional motion").
func (c *Canvas) DrawCornerBadge(w, h int, text string, style tcell.Style) {
	visible := []rune(text)
	x := max(w-len(visible), 0)
	y := h - 1
	if y < 0 {
		return
	}
	c.DrawText(x, y, len(visible), text, style)
}
