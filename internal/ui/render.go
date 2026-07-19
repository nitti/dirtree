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
	"github.com/nitti/dirtree/internal/ui/canvas"
	"github.com/nitti/dirtree/internal/ui/views"
)

var (
	// previewLegend is the primary preview view's app-wide legend
	// (SPEC.md §5.2): the four ways to leave the preview for another
	// view/overlay, plus quit. Browse and open-files are the two most
	// direct routes to opening something and are kept at priority 1
	// alongside quit (the only way out of the app); quick open and
	// search are alternate entry points to the same goal and drop first
	// on a narrow terminal.
	previewLegend = []canvas.LegendEntry{
		{Text: "[b] browse", Priority: 1},
		{Text: "[tab] open files", Priority: 1},
		{Text: "[o] quick open", Priority: 2},
		{Text: "[s] search", Priority: 2},
		{Text: "[q] quit", Priority: 1},
	}
	// browserLegend documents the browser overlay's own actions (SPEC.md
	// §3.4): Return opens the selected file, left/right collapse/expand
	// a directory (or move to its parent/first child), `/` enters jump
	// to file (§4.3), Escape closes the overlay (the sole close key —
	// `b` is not a toggle here).
	browserLegend = []canvas.LegendEntry{
		{Text: "[return] open", Priority: 1},
		{Text: "[left/right] expand/collapse", Priority: 2},
		{Text: "[/] jump to file", Priority: 2},
		{Text: "[esc] close", Priority: 1},
	}
	// jumpLegend documents jump-to-file mode's own actions (SPEC.md
	// §4.3): Return leaves jump mode keeping the current selection,
	// Tab/Shift-Tab cycle the match set, `/` slash-to-expand narrows the
	// jump scope into a single-matching directory, Ctrl+U clears the
	// query, Escape cancels back to the selection/scroll jump mode was
	// entered with. Tab/Shift-Tab is priority 2, not 1, like quick
	// open's and content search's own match-cycling entries below —
	// keeping every legend's priority-1-only text under the terminal-size floor
	// (§6.4) means the "done"/"cancel" pair stays legible even at the
	// enforced minimum, where this entry's own long text (it names both
	// directions) would otherwise still overflow and clip.
	jumpLegend = []canvas.LegendEntry{
		{Text: "[return] done", Priority: 1},
		{Text: "[tab/shift-tab] next/prev match", Priority: 2},
		{Text: "[/] expand", Priority: 2},
		{Text: "[ctrl+u] clear", Priority: 2},
		{Text: "[esc] cancel", Priority: 1},
	}
	// searchLegend documents the content search overlay's actions (SPEC.md
	// §9.2): Return opens the selected row (jumping to its line if it's a
	// hit row) and leaves the overlay open, for opening several hits in a
	// row without re-triggering the search each time — Escape is what
	// closes the overlay; up/down move the flattened row list (arrow keys
	// already cover this, so it's not spelled out as its own legend entry
	// alongside left/right); left/right collapse/expand a file's hit
	// rows; ctrl+r toggles regex mode; ctrl+u clears the query.
	searchLegend = []canvas.LegendEntry{
		{Text: "[return] open", Priority: 1},
		{Text: "[left/right] expand/collapse", Priority: 2},
		{Text: "[ctrl+r] regex", Priority: 2},
		{Text: "[ctrl+u] clear", Priority: 3},
		{Text: "[esc] close", Priority: 1},
	}
	// fileLegend lists actions specific to the currently-displayed file
	// (as opposed to app-wide navigation), shown in the file title bar
	// rather than the global menu bar (§5.2) — new file-specific actions
	// belong here going forward.
	fileLegend = []canvas.LegendEntry{
		{Text: "[/] find", Priority: 1},
		{Text: "[g] goto line", Priority: 2},
		{Text: "[c] copy mode", Priority: 2},
	}
	// fileLegendCopyModeOn replaces fileLegend once copy mode is active
	// (§2.1): goto-line and find are omitted since the point of copy
	// mode is a screen with nothing on it but the file's own text, and
	// scrolling/goto/find remain reachable via their own keys regardless
	// of whether they're listed here, the same way arrow-key scrolling
	// already is.
	fileLegendCopyModeOn = []canvas.LegendEntry{
		{Text: "[c] normal view", Priority: 1},
	}
	// gotoLegend documents the goto-line prompt's own actions (SPEC.md
	// §5.2): Return jumps to the entered line and closes the prompt,
	// Ctrl+U clears the entered digits, Escape cancels without changing
	// scroll.
	gotoLegend = []canvas.LegendEntry{
		{Text: "[return] jump", Priority: 1},
		{Text: "[ctrl+u] clear", Priority: 2},
		{Text: "[esc] cancel", Priority: 1},
	}
	// findPromptLegend documents the in-file find prompt's own actions
	// (SPEC.md §2.4): Return executes the search and closes the prompt,
	// Ctrl+U clears the query, Escape cancels leaving any existing find
	// state unchanged.
	findPromptLegend = []canvas.LegendEntry{
		{Text: "[return] search", Priority: 1},
		{Text: "[ctrl+u] clear", Priority: 2},
		{Text: "[esc] cancel", Priority: 1},
	}
	findLegend = []canvas.LegendEntry{
		{Text: "[n] next", Priority: 1},
		{Text: "[N] prev", Priority: 1},
		{Text: "[esc] clear", Priority: 1},
	}
	// findLegendNoMatches is shown instead of findLegend when a find's
	// query matched nothing — there's no next/previous to step between,
	// but esc still clears it back to the idle file title bar.
	findLegendNoMatches = []canvas.LegendEntry{
		{Text: "[esc] clear", Priority: 1},
	}
)

// draw renders one frame.
func (a *App) draw() {
	a.shared.Canvas.Clear()
	w, h := a.shared.Canvas.Size()

	if w < canvas.MinTerminalWidth || h < canvas.MinTerminalHeight {
		a.drawTooSmall(w, h)
		a.shared.Canvas.Show()
		return
	}

	switch a.overlay {
	case views.OverlayBrowser:
		a.drawBrowserOverlay(w, h)
	case views.OverlayQuickOpen:
		a.QuickOpen.Draw(w, h)
		a.drawBadge(w, h)
	case views.OverlayOpenFiles:
		a.drawOpenFiles(w, h)
	case views.OverlaySearch:
		a.drawSearch(w, h)
		a.drawBadge(w, h)
	default:
		a.shared.Canvas.DrawHeader(w, a.menuBarText(w, previewLegend))
		titleRows := a.drawFileTitleBar(0, 1, w, true)
		a.drawPreview(0, 1+titleRows, w, h-1-titleRows)
	}
	a.drawToast(w, h)

	a.shared.Canvas.Show()
}

// drawTooSmall renders the too-small screen (SPEC.md §6.4), which
// supersedes every other view/overlay whenever the terminal is below
// the terminal-size floor in either dimension: none of
// the app's other layouts can be trusted to degrade gracefully below
// that floor, so this replaces the frame outright rather than drawing
// a truncated/overlapping version of whatever was active. It only
// assumes it can address individual cells directly (SetContent), not
// that a full row (DrawText's w-wide loop) or centered layout is safe
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
	widthShort := w < canvas.MinTerminalWidth
	heightShort := h < canvas.MinTerminalHeight

	showWidth := widthShort
	if widthShort && heightShort {
		widthRatio := float64(w) / float64(canvas.MinTerminalWidth)
		heightRatio := float64(h) / float64(canvas.MinTerminalHeight)
		showWidth = widthRatio <= heightRatio
	}

	if showWidth {
		a.drawTooSmallHorizontal(w, h, canvas.MinTerminalWidth)
	} else {
		a.drawTooSmallVertical(w, h, canvas.MinTerminalHeight)
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
	a.shared.Canvas.SetContent(x, y, r, canvas.StyleError)
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
		// left side keeps showing the "BROWSE" mode label — jump to file
		// is a typing mode layered on the browser, not a distinct mode of
		// its own, per §4.3 — and the query itself moves to its own input
		// row directly below the header, the same convention quick open
		// and content search already use for their own queries, prefixed
		// `> ` the same way theirs are. Any path segments already
		// disclosed via slash-to-expand (jumpDisclosed) render ahead of
		// the live query so committing a segment doesn't read as losing
		// what was typed.
		a.shared.Canvas.DrawHeaderMode(w, "BROWSE", jumpLegend)
		a.shared.Canvas.DrawText(0, 1, w, "> "+a.jumpDisclosed+a.jumpQuery, canvas.StyleSearchInput)
		browserTop = 2
	} else {
		a.shared.Canvas.DrawHeaderMode(w, "BROWSE", browserLegend)
	}

	a.drawBrowser(0, browserTop, w, h-browserTop)

	a.drawBadge(w, h)
}

// drawOpenFiles renders the open-files-list overlay (SPEC.md §2.3): a
// dropdown-style popup over the (unmodified, last-rendered) primary
// preview view, showing at most openfiles.PageSize entries of the
// current page, each row labeled with its 0-9 position, the
// currently-displayed entry marked distinctly, or an explanatory
// message if the list is empty.
func (a *App) drawOpenFiles(w, h int) {
	a.shared.Canvas.DrawHeader(w, a.menuBarText(w, previewLegend))
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
		a.shared.Canvas.DrawText(x0+1, y0+1, innerW, canvas.CenterPad("no open files", innerW), canvas.StyleNormal)
		a.shared.Canvas.DrawText(x0+1, y0+2, innerW, canvas.CenterPad("[esc] close", innerW), canvas.StyleNormal)
		return
	}

	pageSize := openfiles.PageSize
	page := openfiles.Page(a.openFilesSelected, pageSize)
	start, end := openfiles.PageBounds(page, pageSize, len(entries))
	pageCount := openfiles.PageCount(len(entries), pageSize)
	counter := fmt.Sprintf("%d–%d/%d", start+1, end, len(entries))
	legendEntries := openFilesLegend(pageCount > 1)

	// Content-driven width: the header row (counter + a 1-column gap +
	// legend) needs only the border columns around it, since LegendText
	// already accounts for its own internal spacing; row labels get 2
	// extra columns of breathing room around them. Sized off the full,
	// untiered legend text so the box is wide enough that its own
	// narrow-terminal tiering (LegendText below) rarely has to kick in.
	longest := len([]rune(counter)) + 1 + len([]rune(canvas.LegendString(legendEntries))) + 2
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
	a.shared.Canvas.DrawText(x0+1, y0+1, innerW, canvas.LegendText(innerW, counter, legendEntries), canvas.StyleNormal)

	for row := 0; row < itemRows && y0+2+row < y0+boxH-1; row++ {
		i := start + row
		style := canvas.StyleNormal
		if i == a.openFilesSelected {
			style = canvas.StyleSelected
		}
		label := openFilesRowLabel(row, i == a.files.Displayed, tree.RelativeDisplayPath(a.rootPath, entries[i].Path))
		a.shared.Canvas.DrawText(x0+1, y0+2+row, innerW, label, style)
	}
}

// drawOpenFilesBox draws the open-files dropdown's bordered box: the
// title embedded in the top border like canvas.Canvas.DrawBox, plus a
// "▲" in the top border's right end when a previous page exists and a
// "▼" in the bottom border's right end when a next page exists — so
// "more items available" is visible right at the edge it refers to
// (above/below) without spending a content row on it.
func (a *App) drawOpenFilesBox(x0, y0, w, h int, title string, hasPrev, hasNext bool) {
	a.shared.Canvas.DrawBox(x0, y0, w, h, title)
	if hasPrev && w > 2 {
		a.shared.Canvas.SetContent(x0+w-2, y0, '▲', canvas.StyleNormal)
	}
	if hasNext && w > 2 {
		a.shared.Canvas.SetContent(x0+w-2, y0+h-1, '▼', canvas.StyleNormal)
	}
	a.shared.Canvas.FillRect(x0+1, y0+1, w-2, h-2, canvas.StyleNormal)
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
func openFilesLegend(multiPage bool) []canvas.LegendEntry {
	entries := []canvas.LegendEntry{
		{Text: "[return/0-9] open", Priority: 1},
		{Text: "[x] remove", Priority: 2},
		{Text: "[shift+↑↓] move", Priority: 3},
	}
	if multiPage {
		entries = append(entries, canvas.LegendEntry{Text: "[pgup/pgdn] page", Priority: 2})
	}
	return append(entries, canvas.LegendEntry{Text: "[esc] close", Priority: 1})
}

// drawSearch renders the content search overlay (SPEC.md §9.2): a
// header row (title plus keybinding legend), the query input on its own
// row directly below, and a two-level list — one row per matching file,
// disclosing (unless collapsed) its own matching-line rows below it —
// or a placeholder while there's nothing to show yet.
func (a *App) drawSearch(w, h int) {
	a.shared.Canvas.DrawHeaderMode(w, "SEARCH", searchLegend)
	prompt := "> "
	if a.searchRegex {
		prompt = "[regex] > "
	}
	a.shared.Canvas.DrawText(0, 1, w, prompt+a.searchQuery, canvas.StyleSearchInput)

	const listTop = 2
	listHeight := h - listTop

	switch {
	case a.searchQuery == "":
		a.shared.Canvas.DrawText(0, listTop, w, canvas.CenterPad("type to search file contents", w), canvas.StyleNormal)
	case a.searchError != "":
		a.shared.Canvas.DrawText(0, listTop, w, canvas.CenterPad("invalid regex: "+a.searchError, w), canvas.StyleError)
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
		a.shared.Canvas.DrawText(0, listTop, w, canvas.CenterPad(msg, w), canvas.StyleNormal)
	case len(a.searchResults) == 0:
		a.shared.Canvas.DrawText(0, listTop, w, canvas.CenterPad("no matches", w), canvas.StyleNormal)
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
			style := canvas.StyleNormal
			switch {
			case fileErrored && time.Since(a.searchErrorFlashStart) < flashDuration:
				style = canvas.StyleFlashError
			case i == a.searchSelected:
				style = canvas.StyleSelected
			case !row.isHit && a.searchResults[row.file].AbsPath == a.searchFlashPath && time.Since(a.searchFlashStart) < flashDuration:
				style = canvas.StyleFlash
			}
			a.shared.Canvas.DrawText(0, listTop+line, w, label, style)
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
		// Indented past where the owning file row's own path text starts
		// (marker + open-indicator + two spaces = 4 columns), so a hit
		// row reads as visually nested under its file rather than lining
		// up with it.
		return fmt.Sprintf("      %d: %s", h.LineNum, strings.TrimSpace(h.LineText))
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
// The root path is dropped once even priority-3 legend entries can't
// buy back enough room (canvas.LegendFit's drop order), re-evaluated
// every frame so a live resize can bring it back.
func (a *App) menuBarText(w int, entries []canvas.LegendEntry) string {
	return canvas.LegendText(w, a.rootLabel(), entries)
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
		text = canvas.LegendText(w, "/"+a.findInput, findPromptLegend)
	case !interactive:
		text = left
	case e.FindQuery != "" && len(e.FindMatches) > 0:
		text = canvas.LegendText(w, left, withStatus(findStatusText(e), findLegend))
	case e.FindQuery != "":
		text = canvas.LegendText(w, left, withStatus(findStatusText(e), findLegendNoMatches))
	case e.CopyMode:
		text = canvas.LegendText(w, left, fileLegendCopyModeOn)
	default:
		text = canvas.LegendText(w, rel, fileLegend)
	}

	style := canvas.StyleFileTitle
	if e.CopyMode {
		style = canvas.StyleCopyModeTitle
	}
	a.shared.Canvas.DrawText(x0, y0, w, text, style)
	return 1
}

// withStatus prepends a synthetic, always-shown (priority 1) legend
// entry carrying the in-file find's live status text (query, match
// position/count, wrap note — findStatusText below) ahead of legend's
// own entries, so status and legend tier together through canvas.LegendFit
// the same way the path and the legend used to be concatenated directly:
// status is never dropped for width, exactly like a priority-1 legend
// key, while legend's own lower-priority entries (e.g. findLegend's, all
// priority 1 today) still drop first if it ever came to that.
func withStatus(status string, legend []canvas.LegendEntry) []canvas.LegendEntry {
	return append([]canvas.LegendEntry{{Text: status, Priority: 1}}, legend...)
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
		a.shared.Canvas.DrawText(x0, row, w, canvas.CenterPad(msg, w), canvas.StyleNormal)
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
			a.shared.Canvas.DrawText(x0, y, gw, numField+"  ", canvas.StyleNormal)
		}
		a.drawSegments(x0+gw, y, contentWidth, dr.Segments, findHighlightsForRow(e, dr), e.CopyMode)
	}

	if a.gotoPromptOpen {
		a.shared.Canvas.DrawText(x0, y0+h-1, w, canvas.LegendText(w, "goto line: "+a.gotoInput, gotoLegend), canvas.StyleNormal)
	}
}

// findHighlight is one in-file find match's column range within a
// single wrapped display row, in row-relative rune columns (SPEC.md
// §2.4) — Current picks canvas.StyleFindCurrent over canvas.StyleFindMatch
// so the active match stands out from the rest.
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
		style := canvas.StyleNormal
		if !plain {
			style = canvas.StyleFor(seg.Category)
		}
		for _, r := range seg.Text {
			if col >= w {
				return
			}
			a.shared.Canvas.SetContent(x+col, y, r, highlightStyleAt(col, highlights, style))
			col++
		}
	}
	for ; col < w; col++ {
		a.shared.Canvas.SetContent(x+col, y, ' ', canvas.StyleNormal)
	}
}

// highlightStyleAt returns the find-match style covering col, if any,
// else base.
func highlightStyleAt(col int, highlights []findHighlight, base tcell.Style) tcell.Style {
	for _, h := range highlights {
		if col >= h.Start && col < h.End {
			if h.Current {
				return canvas.StyleFindCurrent
			}
			return canvas.StyleFindMatch
		}
	}
	return base
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
		style := canvas.StyleNormal
		switch {
		// SPEC.md §6.1: same reasoning as the open-flash case below —
		// the errored directory could well be the currently-selected
		// row, and reverse-video would otherwise mask the flash
		// entirely, so it takes precedence even over selection.
		case isErrorFlashing(a.browserErrorFlashes, n.Path):
			style = canvas.StyleFlashError
		// SPEC.md §5.2: the flash takes precedence here, unlike content
		// search's own flash/selected precedence — Return never moves
		// the browser's selection (§3.4), so the just-opened row is
		// always the already-selected row; if styleSelected won here the
		// same way it does in content search, the flash would be
		// permanently masked by reverse-video and never actually visible.
		case n.Path == a.browserFlashPath && time.Since(a.browserFlashStart) < flashDuration:
			style = canvas.StyleFlash
		case n == a.browserSelected:
			style = canvas.StyleSelected
		case isMatch[n]:
			style = canvas.StyleFindMatch
		}
		a.shared.Canvas.DrawText(x0, y0+row, w, browserLabel(n, a.files.IsOpen(n.Path)), style)
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
	a.shared.Canvas.DrawCornerBadge(w, h, string(visible), canvas.StyleBadge)
}

// drawToast renders the generic bottom-right transient notification
// (SPEC.md §5.3, e.g. the open-file live-reload notice, §6.1a) if one
// is currently active, sharing DrawCornerBadge's anchor/style with the
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
		style := canvas.StyleBadge
		if inBoldRange(a.toastBoldRanges, hiddenPrefix+i) {
			style = style.Bold(true)
		}
		a.shared.Canvas.SetContent(x+i, y, r, style)
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
