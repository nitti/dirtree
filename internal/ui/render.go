package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/preview"
	"github.com/nitti/dirtree/internal/search"
	"github.com/nitti/dirtree/internal/spinner"
	"github.com/nitti/dirtree/internal/toast"
	"github.com/nitti/dirtree/internal/tree"
)

var (
	styleNormal   = tcell.StyleDefault
	styleSelected = tcell.StyleDefault.Reverse(true)
	styleHeader   = tcell.StyleDefault.Background(tcell.ColorDarkBlue).Foreground(tcell.ColorWhite)
	// styleHeaderMode is styleHeader with bold applied, used only for the
	// mode-name label (e.g. "BROWSE", "SEARCH") that replaces the tree
	// root path on the header/title bar's left side while a mode other
	// than the primary preview view is active — bold sets the label
	// apart from the plain-weight legend sharing the same row, similar
	// to how editors like hx render their current mode name.
	styleHeaderMode  = styleHeader.Bold(true)
	styleFileTitle   = tcell.StyleDefault.Background(tcell.ColorDarkSlateGray).Foreground(tcell.ColorWhite)
	styleError       = tcell.StyleDefault.Foreground(tcell.ColorRed)
	styleBadge       = tcell.StyleDefault.Background(tcell.ColorOrange).Foreground(tcell.ColorBlack)
	styleFindMatch   = tcell.StyleDefault.Background(tcell.ColorYellow).Foreground(tcell.ColorBlack)
	styleFindCurrent = tcell.StyleDefault.Background(tcell.ColorOrange).Foreground(tcell.ColorBlack).Bold(true)
	// styleCopyModeTitle replaces styleFileTitle whenever copy mode
	// (SPEC.md §2.1) is active, so the file title bar itself makes copy
	// mode's on/off state visually unmistakable, not just the legend
	// text.
	styleCopyModeTitle = tcell.StyleDefault.Background(tcell.ColorDarkGreen).Foreground(tcell.ColorWhite)
	// styleSearchInput sets the content search (SPEC.md §9.2) and quick
	// open (§4.2) query rows apart from the plain-background list below
	// them, so the "this is where you're typing" row is visually
	// unmistakable at a glance rather than blending into the rest of the
	// overlay.
	styleSearchInput = tcell.StyleDefault.Background(tcell.ColorDarkSlateGray).Foreground(tcell.ColorWhite)
	// styleFlash briefly replaces a just-opened file row's normal style
	// (browserOpen/browserFlashPath, performSearchOpen/searchFlashPath),
	// as an on-open confirmation distinct from styleSelected (cursor
	// position) and from the lasting "●" already-open indicator every
	// open file's row shows regardless of when it was opened.
	styleFlash = tcell.StyleDefault.Background(tcell.ColorGreen).Foreground(tcell.ColorBlack).Bold(true)
	// styleFlashError is styleFlash's counterpart for a live-refresh
	// discovering a directory that newly failed to list (SPEC.md §6.1):
	// same brief-attention-grabbing role, red instead of green so it
	// reads as a problem rather than a confirmation. Unlike styleFlash,
	// what it's drawing attention to (the inline `[error]` text
	// browserLabel already appends from tree.Node.Err) does not disappear
	// when the flash itself fades — only the highlight is transient.
	styleFlashError = tcell.StyleDefault.Background(tcell.ColorRed).Foreground(tcell.ColorWhite).Bold(true)
)

const (
	// minTerminalWidth/minTerminalHeight (SPEC.md §6.4) gate every other
	// screen: below either threshold, none of the app's other layouts
	// (header + legend, popups, split content) can render without
	// truncating or overlapping in ways that are actively misleading
	// rather than just cramped, so drawTooSmall takes over the whole
	// frame instead of drawing a mangled version of whatever view was
	// active. minTerminalWidth matches openFilesMinWidth's existing
	// "40 columns is the narrowest we design any component for"
	// precedent; minTerminalHeight is header + file title bar + a
	// handful of content/legend rows, the shortest any overlay's own
	// layout assumes it can rely on.
	minTerminalWidth  = 40
	minTerminalHeight = 10
)

// legendEntry is one "[key] action" segment of a keybinding legend,
// tagged with a drop priority for narrow terminals (SPEC.md §5.2):
// priority 1 entries are never dropped, priority 2 entries are dropped
// before priority 1, and priority 3 entries are dropped before priority
// 2 — see legendFit below. Entries always render left-to-right in the
// order they're declared; dropping only ever omits an entry, it never
// reorders the survivors.
type legendEntry struct {
	text     string
	priority int
}

var (
	// previewLegend is the primary preview view's app-wide legend
	// (SPEC.md §5.2): the four ways to leave the preview for another
	// view/overlay, plus quit. Browse and open-files are the two most
	// direct routes to opening something and are kept at priority 1
	// alongside quit (the only way out of the app); quick open and
	// search are alternate entry points to the same goal and drop first
	// on a narrow terminal.
	previewLegend = []legendEntry{
		{"[b] browse", 1},
		{"[tab] open files", 1},
		{"[o] quick open", 2},
		{"[s] search", 2},
		{"[q] quit", 1},
	}
	// browserLegend documents the browser overlay's own actions (SPEC.md
	// §3.4): Return opens the selected file, `/` enters jump to file
	// (§4.3), `b`/Escape close the overlay.
	browserLegend = []legendEntry{
		{"[return] open", 1},
		{"[/] jump to file", 2},
		{"[b/esc] close", 1},
	}
	// jumpLegend documents jump-to-file mode's own actions (SPEC.md
	// §4.3): Return leaves jump mode keeping the current selection,
	// Tab/Shift-Tab cycle the match set, `/` slash-to-expand narrows the
	// jump scope into a single-matching directory, Ctrl+U clears the
	// query, Escape cancels back to the selection/scroll jump mode was
	// entered with.
	jumpLegend = []legendEntry{
		{"[return] done", 1},
		{"[tab/shift-tab] next/prev match", 1},
		{"[/] expand", 2},
		{"[ctrl+u] clear", 2},
		{"[esc] cancel", 1},
	}
	// searchLegend documents the content search overlay's actions (SPEC.md
	// §9.2): Return opens the selected row (jumping to its line if it's a
	// hit row) and leaves the overlay open, for opening several hits in a
	// row without re-triggering the search each time — Escape is what
	// closes the overlay; Tab/Shift-Tab cycle the flattened row list;
	// left/right collapse/expand a file's hit rows; ctrl+r toggles regex
	// mode; ctrl+u clears the query.
	searchLegend = []legendEntry{
		{"[return] open", 1},
		{"[tab/shift-tab] next/prev", 1},
		{"[left/right] expand/collapse", 2},
		{"[ctrl+r] regex", 2},
		{"[ctrl+u] clear", 3},
		{"[esc] close", 1},
	}
	// fileLegend lists actions specific to the currently-displayed file
	// (as opposed to app-wide navigation), shown in the file title bar
	// rather than the global menu bar (§5.2) — new file-specific actions
	// belong here going forward.
	fileLegend = []legendEntry{
		{"[/] find", 1},
		{"[g] goto line", 2},
		{"[c] copy mode", 2},
	}
	// fileLegendCopyModeOn replaces fileLegend once copy mode is active
	// (§2.1): goto-line and find are omitted since the point of copy
	// mode is a screen with nothing on it but the file's own text, and
	// scrolling/goto/find remain reachable via their own keys regardless
	// of whether they're listed here, the same way arrow-key scrolling
	// already is.
	fileLegendCopyModeOn = []legendEntry{
		{"[c] normal view", 1},
	}
	// gotoLegend documents the goto-line prompt's own actions (SPEC.md
	// §5.2): Return jumps to the entered line and closes the prompt,
	// Ctrl+U clears the entered digits, Escape cancels without changing
	// scroll.
	gotoLegend = []legendEntry{
		{"[return] jump", 1},
		{"[ctrl+u] clear", 2},
		{"[esc] cancel", 1},
	}
	// findPromptLegend documents the in-file find prompt's own actions
	// (SPEC.md §2.4): Return executes the search and closes the prompt,
	// Ctrl+U clears the query, Escape cancels leaving any existing find
	// state unchanged.
	findPromptLegend = []legendEntry{
		{"[return] search", 1},
		{"[ctrl+u] clear", 2},
		{"[esc] cancel", 1},
	}
	findLegend = []legendEntry{
		{"[n] next", 1},
		{"[N] prev", 1},
		{"[esc] clear", 1},
	}
	// findLegendNoMatches is shown instead of findLegend when a find's
	// query matched nothing — there's no next/previous to step between,
	// but esc still clears it back to the idle file title bar.
	findLegendNoMatches = []legendEntry{
		{"[esc] clear", 1},
	}
	// quickOpenLegend documents the quick open overlay's actions
	// (SPEC.md §4.2): Return opens the selected match into the
	// open-files list, Tab/Shift-Tab and Page Up/Down move the
	// selection, Ctrl+U clears the query, Escape cancels back to the
	// primary preview view.
	quickOpenLegend = []legendEntry{
		{"[return] open", 1},
		{"[tab/shift-tab] next/prev", 2},
		{"[pgup/pgdn] page", 3},
		{"[ctrl+u] clear", 3},
		{"[esc] cancel", 1},
	}
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

func styleFor(cat preview.Category) tcell.Style {
	if s, ok := categoryStyles[cat]; ok {
		return s
	}
	return styleNormal
}

// draw renders one frame.
func (a *App) draw() {
	a.screen.Clear()
	w, h := a.screen.Size()

	if w < minTerminalWidth || h < minTerminalHeight {
		a.drawTooSmall(w, h)
		a.screen.Show()
		return
	}

	switch a.overlay {
	case overlayBrowser:
		a.drawBrowserOverlay(w, h)
	case overlayQuickOpen:
		a.drawQuickOpen(w, h)
		a.drawBadge(w, h)
	case overlayOpenFiles:
		a.drawOpenFiles(w, h)
	case overlaySearch:
		a.drawSearch(w, h)
		a.drawBadge(w, h)
	default:
		a.drawHeader(w, a.menuBarText(w, previewLegend))
		titleRows := a.drawFileTitleBar(0, 1, w, true)
		a.drawPreview(0, 1+titleRows, w, h-1-titleRows)
	}
	a.drawToast(w, h)

	a.screen.Show()
}

// drawTooSmall renders the too-small screen (SPEC.md §6.4), which
// supersedes every other view/overlay whenever the terminal is below
// minTerminalWidth or minTerminalHeight in either dimension: none of
// the app's other layouts can be trusted to degrade gracefully below
// that floor, so this replaces the frame outright rather than drawing
// a truncated/overlapping version of whatever was active. It only
// assumes it can address individual cells directly (SetContent), not
// that a full row (drawText's w-wide loop) or centered layout is safe
// at any particular size, since it must remain legible-as-possible
// even far below its own stated minimum.
//
// Rather than a block of text, it shows the single undersized
// dimension as a pair of red arrows pointing apart with that
// dimension's required size centered between them — "stretch this
// direction to at least this much" — since that's more compact than
// spelling out current/required sizes as prose and reads as a direct
// resize instruction rather than an error report. When both
// dimensions are short, only one is shown at a time (drawing both
// widths a horizontal and a vertical indicator into a space too small
// for either individually would just recreate the overlap problem this
// screen exists to avoid): whichever is proportionally further below
// its own minimum, on the theory that it's the bigger blocker and
// worth fixing first — ties favor width. Resizing far enough to clear
// that dimension makes the other one (if still short) take its place
// on the next frame.
func (a *App) drawTooSmall(w, h int) {
	widthShort := w < minTerminalWidth
	heightShort := h < minTerminalHeight

	showWidth := widthShort
	if widthShort && heightShort {
		widthRatio := float64(w) / float64(minTerminalWidth)
		heightRatio := float64(h) / float64(minTerminalHeight)
		showWidth = widthRatio <= heightRatio
	}

	if showWidth {
		a.drawTooSmallHorizontal(w, h, minTerminalWidth)
	} else {
		a.drawTooSmallVertical(w, h, minTerminalHeight)
	}
}

// drawTooSmallCell places a single rune at (x, y), silently doing
// nothing if that cell falls outside [0,w)x[0,h) — every caller here
// works from computed centering math that can land off-screen at
// sizes well below either minimum, and this is the one shared bounds
// check they all lean on rather than duplicating it inline.
func (a *App) drawTooSmallCell(x, y, w, h int, r rune) {
	if x < 0 || x >= w || y < 0 || y >= h {
		return
	}
	a.screen.SetContent(x, y, r, nil, styleError)
}

// drawTooSmallHorizontal renders "← need →" on the vertical-center
// row, horizontally centered.
func (a *App) drawTooSmallHorizontal(w, h, need int) {
	line := fmt.Sprintf("← %d →", need)
	y := h / 2
	x := (w - len([]rune(line))) / 2
	for i, r := range []rune(line) {
		a.drawTooSmallCell(x+i, y, w, h, r)
	}
}

// drawTooSmallVertical renders a three-row block — "↑", the required
// height, "↓" — centered as a unit both horizontally and vertically.
func (a *App) drawTooSmallVertical(w, h, need int) {
	numStr := fmt.Sprintf("%d", need)
	startY := (h - 3) / 2
	center := func(text string, y int) {
		x := (w - len([]rune(text))) / 2
		for i, r := range []rune(text) {
			a.drawTooSmallCell(x+i, y, w, h, r)
		}
	}
	center("↑", startY)
	center(numStr, startY+1)
	center("↓", startY+2)
}

// drawBrowserOverlay renders the browser full-screen (SPEC.md §5.1): a
// header with its mode label/legend, the jump-to-file query on its own
// input row directly below when active (the same convention quick open
// and content search use), and the flat row list filling the rest of
// the screen.
func (a *App) drawBrowserOverlay(w, h int) {
	browserTop := 1
	if a.jumpActive {
		// SPEC.md §5.2: while jump-to-file (§4.3) is active, the header's
		// left side shows the "JUMP" mode label rather than the literal
		// query — user-entered text has no business appearing in the
		// title bar — and the query itself moves to its own input row
		// directly below the header, the same convention quick open and
		// content search already use for their own queries.
		a.drawHeaderMode(w, "JUMP", jumpLegend)
		a.drawText(0, 1, w, "/"+a.jumpQuery, styleSearchInput)
		browserTop = 2
	} else {
		a.drawHeaderMode(w, "BROWSE", browserLegend)
	}

	a.drawBrowser(0, browserTop, w, h-browserTop)

	a.drawBadge(w, h)
}

// drawBox draws a bordered rectangle with an optional title embedded in
// the top border.
func (a *App) drawBox(x0, y0, w, h int, title string) {
	if w < 2 || h < 2 {
		return
	}
	for x := 1; x < w-1; x++ {
		a.screen.SetContent(x0+x, y0, '─', nil, styleNormal)
		a.screen.SetContent(x0+x, y0+h-1, '─', nil, styleNormal)
	}
	for y := 1; y < h-1; y++ {
		a.screen.SetContent(x0, y0+y, '│', nil, styleNormal)
		a.screen.SetContent(x0+w-1, y0+y, '│', nil, styleNormal)
	}
	a.screen.SetContent(x0, y0, '┌', nil, styleNormal)
	a.screen.SetContent(x0+w-1, y0, '┐', nil, styleNormal)
	a.screen.SetContent(x0, y0+h-1, '└', nil, styleNormal)
	a.screen.SetContent(x0+w-1, y0+h-1, '┘', nil, styleNormal)

	if title != "" && w > 4 {
		label := " " + title + " "
		if len(label) > w-2 {
			label = label[:w-2]
		}
		a.drawText(x0+1, y0, len(label), label, styleNormal)
	}
}

// fillRect blanks a rectangle at style, used to erase whatever was
// drawn underneath a popup before drawing its contents on top.
func (a *App) fillRect(x0, y0, w, h int, style tcell.Style) {
	for y := range h {
		a.drawText(x0, y0+y, w, "", style)
	}
}

// drawQuickOpen renders the quick open overlay (SPEC.md §4.2, §5.2): a
// header with its single action (Return opens the selected match), the
// query on its own row directly below (the same input-row convention
// content search uses, §9.2), and the flat match list.
func (a *App) drawQuickOpen(w, h int) {
	a.drawHeaderMode(w, "QUICK OPEN", quickOpenLegend)
	a.drawText(0, 1, w, "> "+a.finderQuery, styleSearchInput)
	a.drawFinderList(w, h)
}

// drawFinderList renders quick open's flat, root-relative match list
// (SPEC.md §4.1's index, §5.2's indexing/no-matches placeholder), with
// any failed-open match (§2.2) showing its failure message inline,
// appended the same way tree.Node.Err is in the browser, and flashing
// red the same brief window (§5.3).
func (a *App) drawFinderList(w, h int) {
	const listTop = 2
	listHeight := h - listTop

	_, done := a.idx.Snapshot()
	switch {
	case !done:
		// SPEC.md §5.2: during the pre-threshold grace period this is
		// indistinguishable from "still indexing" at the pure-decision
		// level, so the match-list area stays blank either way rather
		// than claiming "no matches" before indexing has even looked.
		a.drawText(0, listTop, w, centerPad("indexing…", w), styleNormal)
	case len(a.finderMatches) == 0:
		a.drawText(0, listTop, w, centerPad("no matches", w), styleNormal)
	default:
		if listHeight > 0 {
			if a.finderSelected < a.finderScroll {
				a.finderScroll = a.finderSelected
			}
			if a.finderSelected >= a.finderScroll+listHeight {
				a.finderScroll = a.finderSelected - listHeight + 1
			}
		}
		for row := range listHeight {
			i := a.finderScroll + row
			if i >= len(a.finderMatches) {
				break
			}
			match := a.finderMatches[i]
			label := match.RelPath
			errored := a.finderErrorPath != "" && match.AbsPath == a.finderErrorPath
			if errored {
				label += " [" + a.finderErrorMessage + "]"
			}
			style := styleNormal
			switch {
			case errored && time.Since(a.finderErrorFlashStart) < flashDuration:
				style = styleFlashError
			case i == a.finderSelected:
				style = styleSelected
			}
			a.drawText(0, listTop+row, w, label, style)
		}
	}
}

// drawOpenFiles renders the open-files-list overlay (SPEC.md §2.3): a
// dropdown-style popup over the (unmodified, last-rendered) primary
// preview view, showing at most openfiles.PageSize entries of the
// current page, each row labeled with its 0-9 position, the
// currently-displayed entry marked distinctly, or an explanatory
// message if the list is empty.
func (a *App) drawOpenFiles(w, h int) {
	a.drawHeader(w, a.menuBarText(w, previewLegend))
	titleRows := a.drawFileTitleBar(0, 1, w, false)
	a.drawPreview(0, 1+titleRows, w, h-1-titleRows)

	entries := a.files.Entries
	y0 := 1

	if len(entries) == 0 {
		boxW := min(max(openFilesMinWidth, 4), w)
		boxH := min(4, h)
		if boxW < 4 || boxH < 4 {
			return
		}
		x0 := (w - boxW) / 2
		a.drawOpenFilesBox(x0, y0, boxW, boxH, "open files", false, false)
		innerW := boxW - 2
		a.drawText(x0+1, y0+1, innerW, centerPad("no open files", innerW), styleNormal)
		a.drawText(x0+1, y0+2, innerW, centerPad("[esc] close", innerW), styleNormal)
		return
	}

	pageSize := openfiles.PageSize
	page := openfiles.Page(a.openFilesSelected, pageSize)
	start, end := openfiles.PageBounds(page, pageSize, len(entries))
	pageCount := openfiles.PageCount(len(entries), pageSize)
	counter := fmt.Sprintf("%d–%d/%d", start+1, end, len(entries))
	legendEntries := openFilesLegend(pageCount > 1)

	// Content-driven width: the header row (counter + a 1-column gap +
	// legend) needs only the border columns around it, since legendText
	// already accounts for its own internal spacing; row labels get 2
	// extra columns of breathing room around them. Sized off the full,
	// untiered legend text so the box is wide enough that its own
	// narrow-terminal tiering (legendText below) rarely has to kick in.
	longest := len([]rune(counter)) + 1 + len([]rune(legendString(legendEntries))) + 2
	for i := start; i < end; i++ {
		label := openFilesRowLabel(i-start, i == a.files.Displayed, tree.RelativeDisplayPath(a.rootPath, entries[i].Path))
		if n := len([]rune(label)) + 4; n > longest {
			longest = n
		}
	}
	boxW := min(max(longest, openFilesMinWidth), min(openFilesMaxWidth, w))
	itemRows := end - start
	boxH := min(3+itemRows, h)
	if boxW < 4 || boxH < 3 {
		return
	}
	x0 := (w - boxW) / 2
	innerW := boxW - 2

	a.drawOpenFilesBox(x0, y0, boxW, boxH, "open files", page > 0, page < pageCount-1)
	a.drawText(x0+1, y0+1, innerW, legendText(innerW, counter, legendEntries), styleNormal)

	for row := 0; row < itemRows && y0+2+row < y0+boxH-1; row++ {
		i := start + row
		style := styleNormal
		if i == a.openFilesSelected {
			style = styleSelected
		}
		label := openFilesRowLabel(row, i == a.files.Displayed, tree.RelativeDisplayPath(a.rootPath, entries[i].Path))
		a.drawText(x0+1, y0+2+row, innerW, label, style)
	}
}

// drawOpenFilesBox draws the open-files dropdown's bordered box: the
// title embedded in the top border like drawBox, plus a "▲" in the top
// border's right end when a previous page exists and a "▼" in the
// bottom border's right end when a next page exists — so "more items
// available" is visible right at the edge it refers to (above/below)
// without spending a content row on it.
func (a *App) drawOpenFilesBox(x0, y0, w, h int, title string, hasPrev, hasNext bool) {
	a.drawBox(x0, y0, w, h, title)
	if hasPrev && w > 2 {
		a.screen.SetContent(x0+w-2, y0, '▲', nil, styleNormal)
	}
	if hasNext && w > 2 {
		a.screen.SetContent(x0+w-2, y0+h-1, '▼', nil, styleNormal)
	}
	a.fillRect(x0+1, y0+1, w-2, h-2, styleNormal)
}

// openFilesRowLabel renders one dropdown row: its 0-9 on-page position,
// a marker for the currently-displayed entry, and the path.
func openFilesRowLabel(digit int, displayed bool, path string) string {
	marker := "  "
	if displayed {
		marker = "* "
	}
	return fmt.Sprintf("%d %s%s", digit, marker, path)
}

// openFilesLegend is the dropdown's keybinding legend (SPEC.md §2.3),
// kept deliberately terse since it has to fit inside a narrow popup
// rather than a full-width header row; pgup/pgdn is only listed when
// there's more than one page to page through, the same "don't advertise
// a key that does nothing right now" discipline the file title bar's
// legend already follows. Shift-Page-Up/Down (the bulk-reorder
// accelerator) is intentionally left off even on a multi-page list —
// it's a bulk variant of the already-listed shift+↑↓, the same way
// arrow-key scrolling is never listed at the primary view either.
func openFilesLegend(multiPage bool) []legendEntry {
	entries := []legendEntry{
		{"[return/0-9] open", 1},
		{"[x] remove", 2},
		{"[shift+↑↓] move", 3},
	}
	if multiPage {
		entries = append(entries, legendEntry{"[pgup/pgdn] page", 2})
	}
	return append(entries, legendEntry{"[esc] close", 1})
}

// drawSearch renders the content search overlay (SPEC.md §9.2): a
// header row (title plus keybinding legend), the query input on its own
// row directly below, and a two-level list — one row per matching file,
// disclosing (unless collapsed) its own matching-line rows below it —
// or a placeholder while there's nothing to show yet.
func (a *App) drawSearch(w, h int) {
	title := "SEARCH"
	if a.searchRegex {
		title = "SEARCH (REGEX)"
	}
	a.drawHeaderMode(w, title, searchLegend)
	a.drawText(0, 1, w, "> "+a.searchQuery, styleSearchInput)

	const listTop = 2
	listHeight := h - listTop

	switch {
	case a.searchQuery == "":
		a.drawText(0, listTop, w, centerPad("type to search file contents", w), styleNormal)
	case a.searchError != "":
		a.drawText(0, listTop, w, centerPad("invalid regex: "+a.searchError, w), styleError)
	case a.searchResults == nil:
		// SPEC.md §9.1: covers both "index not done yet" and "a scan for
		// this query is still running" — either way, nothing has been
		// found yet, which is a different state from genuinely zero
		// matches, so this must not render as "no matches." Scanning
		// itself always runs in a background goroutine (never on this
		// draw/input thread), so a slow scan over a large tree never
		// blocks keystrokes; this spinner is purely feedback that it's
		// still working, mirroring the background-index badge (§5.2).
		_, indexDone := a.idx.Snapshot()
		var msg string
		switch {
		case !indexDone:
			msg = "indexing…"
		case a.searchCancel == nil:
			// A scan hasn't been (re)started yet this draw (e.g. the very
			// first frame after a keystroke); avoid flashing a spinner
			// frame for a scan that isn't actually running.
			msg = "searching…"
		case time.Since(a.searchScanStart) < spinnerThreshold:
			msg = "searching…"
		default:
			frame := spinner.Frame(time.Since(a.searchScanStart), spinnerFPS, spinner.DefaultFrames)
			msg = string(frame) + " searching…"
		}
		a.drawText(0, listTop, w, centerPad(msg, w), styleNormal)
	case len(a.searchResults) == 0:
		a.drawText(0, listTop, w, centerPad("no matches", w), styleNormal)
	default:
		rows := a.searchRows()
		if listHeight > 0 {
			if a.searchSelected < a.searchScroll {
				a.searchScroll = a.searchSelected
			}
			if a.searchSelected >= a.searchScroll+listHeight {
				a.searchScroll = a.searchSelected - listHeight + 1
			}
		}
		for line := range listHeight {
			i := a.searchScroll + line
			if i >= len(rows) {
				break
			}
			row := rows[i]
			label := searchRowLabel(a.searchResults, a.searchCollapsed, a.files, row)
			fileErrored := !row.isHit && a.searchErrorPath != "" && a.searchResults[row.file].AbsPath == a.searchErrorPath
			if fileErrored {
				label += " [" + a.searchErrorMessage + "]"
			}
			style := styleNormal
			switch {
			case fileErrored && time.Since(a.searchErrorFlashStart) < flashDuration:
				style = styleFlashError
			case i == a.searchSelected:
				style = styleSelected
			case !row.isHit && a.searchResults[row.file].AbsPath == a.searchFlashPath && time.Since(a.searchFlashStart) < flashDuration:
				style = styleFlash
			}
			a.drawText(0, listTop+line, w, label, style)
		}
	}
}

// searchRowLabel renders one flattened search-result row (SPEC.md
// §9.2): a file row shows a disclosure indicator, an "already open"
// indicator (●) when the open-files list already has that file open,
// then its root-relative path and hit count; a hit row is indented
// under its file and shows its 1-based line number and (trimmed) text.
// The open indicator is deliberately file-row-only, not repeated on
// each hit row underneath, since "open" is a per-file fact.
func searchRowLabel(results []search.FileResult, collapsed map[string]bool, files *openfiles.List, row searchRow) string {
	r := results[row.file]
	if row.isHit {
		h := r.Hits[row.hit]
		return fmt.Sprintf("    %d: %s", h.LineNum, strings.TrimSpace(h.LineText))
	}
	marker := "▾"
	if collapsed[r.AbsPath] {
		marker = "▸"
	}
	open := " "
	if files.IsOpen(r.AbsPath) {
		open = "●"
	}
	plural := "es"
	if len(r.Hits) == 1 {
		plural = ""
	}
	return fmt.Sprintf("%s %s %s (%d match%s)", marker, open, r.RelPath, len(r.Hits), plural)
}

// rootLabel renders the tree root path for display, abbreviated with
// shell-style shortcuts (e.g. "~/dirtree" instead of "/Users/x/dirtree")
// to keep it short in the menu bar and popup title.
func (a *App) rootLabel() string {
	return shellAbbreviate(a.rootPath)
}

// shellAbbreviate rewrites path's home-directory prefix, if any, to
// "~", the common shell shortcut, to save space when displaying it.
func shellAbbreviate(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+string(os.PathSeparator)); ok {
		return "~" + string(os.PathSeparator) + rest
	}
	return path
}

// menuBarText composes the top menu bar's content (SPEC.md §5.2): the
// tree root path left-aligned and a short keybinding legend
// right-aligned, with at least one space of separation between them.
// The root path is dropped when the terminal is too narrow to fit both
// alongside each other, re-evaluated every frame so a live resize can
// bring it back; the legend always takes priority since it's what
// makes the app usable.
func (a *App) menuBarText(w int, entries []legendEntry) string {
	return legendText(w, a.rootLabel(), entries)
}

// legendString joins entries' text left-to-right, in declaration order,
// with the app's standard two-space separator between legend segments.
func legendString(entries []legendEntry) string {
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = e.text
	}
	return strings.Join(parts, "  ")
}

// keepUpToPriority returns the subset of entries whose priority is
// maxPriority or lower, preserving their original order — the building
// block legendFit uses to progressively drop the lowest-priority tier
// first (SPEC.md §5.2's narrow-terminal drop order).
func keepUpToPriority(entries []legendEntry, maxPriority int) []legendEntry {
	out := make([]legendEntry, 0, len(entries))
	for _, e := range entries {
		if e.priority <= maxPriority {
			out = append(out, e)
		}
	}
	return out
}

// fitPair left-aligns left and right-aligns legend within w columns,
// with at least one space of separation between them, reporting whether
// they both fit at all.
func fitPair(w int, left, legend string) (text string, ok bool) {
	leftLen, legendLen := len([]rune(left)), len([]rune(legend))
	if gap := w - leftLen - legendLen; gap >= 1 {
		return left + strings.Repeat(" ", gap) + legend, true
	}
	return "", false
}

// legendText composes left and entries the same way legendFit does,
// discarding the leftIncluded flag for callers that don't need it (most
// don't — it exists only for drawHeaderMode's bold-label styling).
func legendText(w int, left string, entries []legendEntry) string {
	text, _ := legendFit(w, left, entries)
	return text
}

// legendFit is legendText's underlying fit computation (SPEC.md §5.2's
// header/title bar convention, applied everywhere a header combines
// some left-hand content with a keybinding legend), additionally
// reporting whether left was included (as opposed to dropped for width
// reasons), so a caller that needs to style left differently from the
// legend (drawHeaderMode, below) knows whether left actually appears in
// the returned text.
//
// The legend always takes priority over left: first the full legend is
// tried alongside left, then with left dropped entirely (the existing
// drop-left rule). If the legend still doesn't fit on its own, whole
// priority-3 entries are dropped, then priority-2 entries as well —
// entries are only ever omitted wholesale, never truncated mid-entry,
// and survivors keep their original left-to-right order. Priority-1
// entries are never dropped; by construction every legend's priority-1
// subset is short enough to fit any terminal wide enough to run the app
// at all, but as a last-resort fallback if it somehow still doesn't,
// that priority-1 text is returned anyway and left to drawText's own
// column clip.
func legendFit(w int, left string, entries []legendEntry) (text string, leftIncluded bool) {
	if fit, ok := fitPair(w, left, legendString(entries)); ok {
		return fit, true
	}
	for maxPriority := 3; maxPriority >= 1; maxPriority-- {
		legend := legendString(keepUpToPriority(entries, maxPriority))
		if len([]rune(legend)) <= w || maxPriority == 1 {
			return legend, false
		}
	}
	return "", false
}

// drawFileTitleBar renders the currently-displayed file's own title bar
// (its root-relative path) in the row above the preview content, when a
// file is displayed. Returns the number of rows it occupied (0 or 1) so
// callers can shrink the preview's rectangle accordingly.
// interactive is false while another overlay (e.g. open-files-list,
// SPEC.md §2.3) owns input and the preview pane underneath it is
// read-only, accepting neither scrolling nor goto-line — in which case
// file-specific action keys like goto-line don't apply and their legend
// is omitted rather than advertising a key that won't do anything right
// now.
func (a *App) drawFileTitleBar(x0, y0, w int, interactive bool) int {
	e := a.files.DisplayedEntry()
	if e == nil {
		return 0
	}
	rel := tree.RelativeDisplayPath(a.rootPath, e.Path)

	// copyModeTag prefixes rel whenever e is in copy mode, so that state
	// is always legible in this row regardless of which case below fires
	// (find status/prompt text otherwise has no room to also mention
	// it) — the row's own distinct style (below) reinforces this further.
	left := rel
	if e.CopyMode {
		left = "[copy mode] " + rel
	}

	var text string
	switch {
	case interactive && a.findPromptOpen:
		text = legendText(w, "/"+a.findInput, findPromptLegend)
	case !interactive:
		text = left
	case e.FindQuery != "" && len(e.FindMatches) > 0:
		text = legendText(w, left, withStatus(findStatusText(e), findLegend))
	case e.FindQuery != "":
		text = legendText(w, left, withStatus(findStatusText(e), findLegendNoMatches))
	case e.CopyMode:
		text = legendText(w, left, fileLegendCopyModeOn)
	default:
		text = legendText(w, rel, fileLegend)
	}

	style := styleFileTitle
	if e.CopyMode {
		style = styleCopyModeTitle
	}
	a.drawText(x0, y0, w, text, style)
	return 1
}

// withStatus prepends a synthetic, always-shown (priority 1) legend
// entry carrying the in-file find's live status text (query, match
// position/count, wrap note — findStatusText below) ahead of legend's
// own entries, so status and legend tier together through legendFit the
// same way the path and the legend used to be concatenated directly:
// status is never dropped for width, exactly like a priority-1 legend
// key, while legend's own lower-priority entries (e.g. findLegend's, all
// priority 1 today) still drop first if it ever came to that.
func withStatus(status string, legend []legendEntry) []legendEntry {
	return append([]legendEntry{{status, 1}}, legend...)
}

// findStatusText renders in-file find's live status (SPEC.md §2.4): the
// query, how many matches it found and which one is current, and a
// transient note when the most recent next/previous step wrapped
// around either end of the match list.
func findStatusText(e *openfiles.Entry) string {
	if len(e.FindMatches) == 0 {
		return "/" + e.FindQuery + "  no matches"
	}
	status := fmt.Sprintf("/%s  %d/%d", e.FindQuery, e.FindCurrent+1, len(e.FindMatches))
	if e.FindWrapNote != "" {
		status += " (" + e.FindWrapNote + ")"
	}
	return status
}

// drawPreview renders the primary preview view's content (SPEC.md
// §2.1) into the (x0, y0)-(x0+w, y0+h) rectangle: a line-number gutter
// plus wrapped, highlighted rows for the currently-displayed entry, or
// an explanatory empty-state message if none is displayed. The
// goto-line prompt, when open, occupies the bottom row — reachable only
// when this is the primary (non-overlaid) view, since no overlay leaves
// the goto-line key handled while this is showing.
func (a *App) drawPreview(x0, y0, w, h int) {
	e := a.files.DisplayedEntry()
	if e == nil {
		msg := "no files open — press b to browse, o to quick-open, s to search contents"
		row := y0 + max(h/2, 1)
		a.drawText(x0, row, w, centerPad(msg, w), styleNormal)
		return
	}

	gw := previewGutterWidth(e)
	contentWidth := max(w-gw, 1)
	a.ensurePreviewWrapped(e, contentWidth)

	viewportHeight := h
	if a.gotoPromptOpen {
		viewportHeight--
	}
	digits := gw - 2

	for row := range viewportHeight {
		y := y0 + row
		i := e.Scroll + row
		if i >= len(e.Rows) {
			break
		}
		dr := e.Rows[i]
		if gw > 0 {
			numField := strings.Repeat(" ", digits)
			if dr.HasNumber {
				numField = fmt.Sprintf("%*d", digits, dr.SourceLine+1)
			}
			a.drawText(x0, y, gw, numField+"  ", styleNormal)
		}
		a.drawSegments(x0+gw, y, contentWidth, dr.Segments, findHighlightsForRow(e, dr), e.CopyMode)
	}

	if a.gotoPromptOpen {
		a.drawText(x0, y0+h-1, w, legendText(w, "goto line: "+a.gotoInput, gotoLegend), styleNormal)
	}
}

// findHighlight is one in-file find match's column range within a
// single wrapped display row, in row-relative rune columns (SPEC.md
// §2.4) — Current picks styleFindCurrent over styleFindMatch so the
// active match stands out from the rest.
type findHighlight struct {
	Start, End int
	Current    bool
}

// findHighlightsForRow returns row's portion of every in-file find
// match that overlaps it, converting each match's source-line-relative
// column range (found against e.Lines, independent of wrapping) into
// row-relative columns via the row's ColStart — a match split across
// two wrapped rows by a mid-token wrap naturally yields one highlight
// per row it touches.
func findHighlightsForRow(e *openfiles.Entry, row preview.DisplayRow) []findHighlight {
	if len(e.FindMatches) == 0 {
		return nil
	}
	rowLen := preview.SegmentsRuneLen(row.Segments)
	var out []findHighlight
	for i, m := range e.FindMatches {
		if m.Line != row.SourceLine {
			continue
		}
		start := m.Col - row.ColStart
		end := start + m.Len
		if end <= 0 || start >= rowLen {
			continue
		}
		start = max(start, 0)
		end = min(end, rowLen)
		out = append(out, findHighlight{Start: start, End: end, Current: i == e.FindCurrent})
	}
	return out
}

// drawSegments draws seg fragments left to right starting at (x, y),
// each in its category's style (SPEC.md §2.1), clipped and padded with
// spaces to exactly w columns — unless copy mode is active, in which
// case every fragment uses the plain style instead, since copy mode's
// whole point is a screen with nothing on it but the file's own
// characters. Any column covered by a highlights entry (SPEC.md §2.4's
// in-file find) still overrides that with the find-match style, copy
// mode or not — it's not literal selectable text either way, so it
// doesn't undermine copy mode's purpose, and staying visible there is
// more useful than not.
func (a *App) drawSegments(x, y, w int, segs []preview.Segment, highlights []findHighlight, plain bool) {
	col := 0
	for _, seg := range segs {
		style := styleNormal
		if !plain {
			style = styleFor(seg.Category)
		}
		for _, r := range seg.Text {
			if col >= w {
				return
			}
			a.screen.SetContent(x+col, y, r, nil, highlightStyleAt(col, highlights, style))
			col++
		}
	}
	for ; col < w; col++ {
		a.screen.SetContent(x+col, y, ' ', nil, styleNormal)
	}
}

// highlightStyleAt returns the find-match style covering col, if any,
// else base.
func highlightStyleAt(col int, highlights []findHighlight, base tcell.Style) tcell.Style {
	for _, h := range highlights {
		if col >= h.Start && col < h.End {
			if h.Current {
				return styleFindCurrent
			}
			return styleFindMatch
		}
	}
	return base
}

// gutterWidth returns the gutter column width for a file with numLines
// source lines: wide enough for the largest line number, plus a
// two-column separator (SPEC.md §2.1).
func gutterWidth(numLines int) int {
	digits := max(len(fmt.Sprintf("%d", numLines)), 1)
	return digits + 2
}

func centerPad(s string, w int) string {
	if len(s) >= w {
		return s
	}
	pad := (w - len(s)) / 2
	return strings.Repeat(" ", pad) + s
}

func (a *App) drawHeader(w int, text string) {
	a.drawText(0, 0, w, text, styleHeader)
}

// drawHeaderMode renders the header/title bar with label (a bold,
// all-caps mode name, e.g. "BROWSE" or "SEARCH") standing in for the
// tree root path on the left, and legend right-aligned, using the same
// fit/drop rule legendText uses elsewhere — label is dropped first (in
// favor of legend alone) if the terminal is too narrow, exactly like
// the root path is. Only label itself is bold; legend keeps the normal
// header weight.
func (a *App) drawHeaderMode(w int, label string, entries []legendEntry) {
	text, included := legendFit(w, label, entries)
	a.drawText(0, 0, w, text, styleHeader)
	if included {
		a.drawText(0, 0, len([]rune(label)), label, styleHeaderMode)
	}
}

// drawBrowser renders the browser's currently-visible flattened list
// (SPEC.md §3.1, §5.2), keeping the selected row scrolled into view.
func (a *App) drawBrowser(x0, y0, w, h int) {
	if h < 1 {
		return
	}
	flat := a.root.Flatten()
	selIdx := indexOf(flat, a.browserSelected)

	if selIdx < a.browserScroll {
		a.browserScroll = selIdx
	}
	if selIdx >= a.browserScroll+h {
		a.browserScroll = selIdx - h + 1
	}
	if a.browserScroll < 0 {
		a.browserScroll = 0
	}

	var isMatch map[*tree.Node]bool
	if a.jumpActive && len(a.jumpMatches) > 0 {
		isMatch = make(map[*tree.Node]bool, len(a.jumpMatches))
		for _, m := range a.jumpMatches {
			isMatch[m] = true
		}
	}

	for row := range h {
		i := a.browserScroll + row
		if i >= len(flat) {
			break
		}
		n := flat[i]
		style := styleNormal
		switch {
		// SPEC.md §6.1: same reasoning as the open-flash case below —
		// the errored directory could well be the currently-selected
		// row, and reverse-video would otherwise mask the flash
		// entirely, so it takes precedence even over selection.
		case isErrorFlashing(a.browserErrorFlashes, n.Path):
			style = styleFlashError
		// SPEC.md §5.2: the flash takes precedence here, unlike content
		// search's own flash/selected precedence — Return never moves
		// the browser's selection (§3.4), so the just-opened row is
		// always the already-selected row; if styleSelected won here the
		// same way it does in content search, the flash would be
		// permanently masked by reverse-video and never actually visible.
		case n.Path == a.browserFlashPath && time.Since(a.browserFlashStart) < flashDuration:
			style = styleFlash
		case n == a.browserSelected:
			style = styleSelected
		case isMatch[n]:
			style = styleFindMatch
		}
		a.drawText(x0, y0+row, w, browserLabel(n, a.files.IsOpen(n.Path)), style)
	}
}

// isErrorFlashing reports whether path's brief red error flash (SPEC.md
// §6.1) is still within its display window.
func isErrorFlashing(flashes map[string]time.Time, path string) bool {
	start, ok := flashes[path]
	return ok && time.Since(start) < flashDuration
}

// browserLabel renders one browser row's indentation, expand/collapse
// marker, lasting "●" open indicator (files already in the open-files
// list, SPEC.md §2.2/§5.2 — the same indicator content search's own
// file rows use, §9.2), name, and any per-node error indicator.
func browserLabel(n *tree.Node, open bool) string {
	indent := strings.Repeat("  ", n.Depth)
	marker := "  "
	if n.IsDir {
		if n.Expanded {
			marker = "v "
		} else {
			marker = "> "
		}
	}
	openMarker := " "
	if open {
		openMarker = "●"
	}
	label := indent + marker + openMarker + " " + n.Name
	if n.Err != "" {
		label += " [" + n.Err + "]"
	}
	return label
}

// drawBadge renders the bottom-right delayed-loading indicator badge
// (SPEC.md §5.2) if the background index warrants showing one.
func (a *App) drawBadge(w, h int) {
	elapsed := a.idx.Elapsed()
	sinceDone, done := a.idx.SinceDone()
	text, hiddenPrefix, ok := spinner.BadgeDecision(
		elapsed, sinceDone, done, spinner.DebugAlwaysShow, a.badgeSkip,
		spinnerThreshold, spinnerMinDisplayDuration, toastDisplayDuration, toastFadeDuration,
		spinnerFPS, completionMessage,
	)
	if !ok {
		return
	}
	visible := []rune(text)
	if hiddenPrefix < len(visible) {
		visible = visible[hiddenPrefix:]
	} else {
		visible = nil
	}
	if len(visible) == 0 {
		return
	}
	a.drawCornerBadge(w, h, string(visible), styleBadge)
}

// drawToast renders the generic bottom-right transient notification
// (SPEC.md §5.3, e.g. the open-file live-reload notice, §6.1a) if one
// is currently active, sharing drawCornerBadge's anchor/style with the
// indexing badge; drawn after it in draw() so an active toast wins the
// corner over the badge on the rare frame both would otherwise want it.
// Clears toastMessage once its fade has fully completed, so a finished
// toast doesn't keep being redecided on every subsequent frame.
func (a *App) drawToast(w, h int) {
	if a.toastMessage == "" {
		return
	}
	phase, hiddenPrefix := toast.Decide(
		time.Since(a.toastStart), toastDisplayDuration, toastFadeDuration, len([]rune(a.toastMessage)),
	)
	if phase == toast.Hidden {
		a.toastMessage = ""
		return
	}
	visible := []rune(a.toastMessage)
	if hiddenPrefix < len(visible) {
		visible = visible[hiddenPrefix:]
	} else {
		visible = nil
	}
	if len(visible) == 0 {
		return
	}
	x := max(w-len(visible), 0)
	y := h - 1
	if y < 0 {
		return
	}
	for i, r := range visible {
		style := styleBadge
		if inBoldRange(a.toastBoldRanges, hiddenPrefix+i) {
			style = style.Bold(true)
		}
		a.screen.SetContent(x+i, y, r, nil, style)
	}
}

// inBoldRange reports whether rune index idx falls within any of the
// [start, end) ranges toastBoldRanges marks for bold rendering (e.g.
// reloaded file names, SPEC.md §6.1a).
func inBoldRange(ranges [][2]int, idx int) bool {
	for _, r := range ranges {
		if idx >= r[0] && idx < r[1] {
			return true
		}
	}
	return false
}

// drawCornerBadge draws text right-anchored on the bottom row, the
// shared anchor point both the indexing badge and the error toast fade
// out from (SPEC.md §5.3's "anchored, directional motion").
func (a *App) drawCornerBadge(w, h int, text string, style tcell.Style) {
	visible := []rune(text)
	x := max(w-len(visible), 0)
	y := h - 1
	if y < 0 {
		return
	}
	a.drawText(x, y, len(visible), text, style)
}

// drawText draws text starting at (x, y), clipped and padded with
// spaces to exactly w columns so style (e.g. selection reverse-video,
// the header/badge backgrounds) fills the full row width regardless of
// text length.
func (a *App) drawText(x, y, w int, text string, style tcell.Style) {
	runes := []rune(text)
	for i := range w {
		r := ' '
		if i < len(runes) {
			r = runes[i]
		}
		a.screen.SetContent(x+i, y, r, nil, style)
	}
}
