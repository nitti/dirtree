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
	"github.com/nitti/dirtree/internal/tree"
)

var (
	styleNormal      = tcell.StyleDefault
	styleSelected    = tcell.StyleDefault.Reverse(true)
	styleHeader      = tcell.StyleDefault.Background(tcell.ColorDarkBlue).Foreground(tcell.ColorWhite)
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
	// styleSearchInput sets the content search query row (SPEC.md §9.2)
	// apart from the plain-background results list below it, so the
	// "this is where you're typing" row is visually unmistakable at a
	// glance rather than blending into the rest of the overlay.
	styleSearchInput = tcell.StyleDefault.Background(tcell.ColorDarkSlateGray).Foreground(tcell.ColorWhite)
	// styleSearchFlash briefly replaces a search-result file row's normal
	// style right after it's opened (performSearchOpen/searchFlashPath),
	// as an on-open confirmation distinct from styleSelected (cursor
	// position) and from the lasting "●" already-open indicator every
	// open file's row shows regardless of when it was opened.
	styleSearchFlash = tcell.StyleDefault.Background(tcell.ColorGreen).Foreground(tcell.ColorBlack).Bold(true)
)

const (
	previewLegend = "[b] browse  [tab] open files  [o] quick open  [s] search  [q] quit"
	browserLegend = "[return] open  [/] jump to file  [o] quick open  [s] search  [b/esc] close"
	jumpLegend    = "[tab] next match  [return] done  [esc] cancel"
	// searchLegend documents the content search overlay's actions (SPEC.md
	// §9.2): Return opens the selected row (jumping to its line if it's a
	// hit row) and leaves the overlay open, for opening several hits in a
	// row without re-triggering the search each time — Escape is what
	// closes the overlay; left/right collapse/expand a file's hit rows;
	// ctrl+r toggles regex mode; ctrl+u clears the query.
	searchLegend = "[return] open  [left/right] expand/collapse  [ctrl+r] regex  [ctrl+u] clear  [esc] close"
	// fileLegend lists actions specific to the currently-displayed file
	// (as opposed to app-wide navigation), shown in the file title bar
	// rather than the global menu bar (§5.2) — new file-specific actions
	// belong here going forward.
	fileLegend = "[g] goto line  [/] find  [c] copy mode"
	// fileLegendCopyModeOn replaces fileLegend once copy mode is active
	// (§2.1): goto-line and find are omitted since the point of copy
	// mode is a screen with nothing on it but the file's own text, and
	// scrolling/goto/find remain reachable via their own keys regardless
	// of whether they're listed here, the same way arrow-key scrolling
	// already is.
	fileLegendCopyModeOn = "[c] normal view"
	findLegend           = "[n] next  [N] prev  [esc] clear"
	// findLegendNoMatches is shown instead of findLegend when a find's
	// query matched nothing — there's no next/previous to step between,
	// but esc still clears it back to the idle file title bar.
	findLegendNoMatches = "[esc] clear"
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

	a.screen.Show()
}

// drawBrowserOverlay picks split-vs-popup layout (SPEC.md §5.1,
// recomputed every frame so a live resize can flip between them) and
// renders the browser overlay accordingly.
func (a *App) drawBrowserOverlay(w, h int) {
	browserWidth, previewWidth, split := a.computeSplitLayout(w)
	if split {
		a.drawBrowserSplitView(w, h, browserWidth, previewWidth)
	} else {
		a.drawBrowserPopup(w, h)
	}
	a.drawBadge(w, h)
}

// drawBrowserSplitView renders the wide-terminal layout (SPEC.md §5.1):
// the browser on the left, a vertical rule, and the primary preview
// view — still visible, showing whatever it had, but read-only (no
// goto-line prompt can be open while this overlay owns input) — on the
// right.
func (a *App) drawBrowserSplitView(w, h, browserWidth, previewWidth int) {
	a.drawHeader(w, a.browserHeaderText(w))

	browserHeight := h - 1
	if a.browserMessage != "" {
		browserHeight--
	}
	a.drawBrowser(0, 1, browserWidth, browserHeight)
	if a.browserMessage != "" {
		a.drawText(0, h-1, browserWidth, a.browserMessage, styleError)
	}

	for y := 1; y < h; y++ {
		a.screen.SetContent(browserWidth, y, '│', nil, styleNormal)
	}

	titleRows := a.drawFileTitleBar(browserWidth+1, 1, previewWidth, false)
	a.drawPreview(browserWidth+1, 1+titleRows, previewWidth, h-1-titleRows)
}

// drawBrowserPopup renders the narrow-terminal layout (SPEC.md §5.1):
// the primary preview view rendered exactly as it would with no overlay
// active ("unmodified, last-rendered"), with a centered, bordered
// floating window containing the browser on top.
func (a *App) drawBrowserPopup(w, h int) {
	a.drawHeader(w, a.menuBarText(w, previewLegend))
	titleRows := a.drawFileTitleBar(0, 1, w, false)
	a.drawPreview(0, 1+titleRows, w, h-1-titleRows)

	popupW := min(max(w-2*popupMarginX, 10), w)
	popupH := min(max(h-2*popupMarginY, 5), h)
	x0 := (w - popupW) / 2
	y0 := (h - popupH) / 2

	a.drawBox(x0, y0, popupW, popupH, a.rootLabel())
	a.fillRect(x0+1, y0+1, popupW-2, popupH-2, styleNormal)

	innerX, innerY := x0+1, y0+1
	innerW, innerH := popupW-2, popupH-2
	footerRow := innerH - 1
	browserHeight := footerRow
	if a.browserMessage != "" {
		browserHeight--
	}
	a.drawBrowser(innerX, innerY, innerW, browserHeight)
	if a.browserMessage != "" {
		a.drawText(innerX, innerY+browserHeight, innerW, a.browserMessage, styleError)
	}
	a.drawText(innerX, innerY+footerRow, innerW, a.browserFooterText(innerW), styleNormal)
}

// browserHeaderText composes the browser's own header/title-bar row
// (SPEC.md §5.2): the normal root-path-plus-legend content, or, while
// jump-to-file typing mode (§4.3) is active, the literal query
// (prefixed `/`) plus jump mode's own keybinding legend in its place —
// the root path is dropped in favor of the query the same way it would
// be dropped for width reasons elsewhere, since the query is the more
// relevant thing to show while actively typing it.
func (a *App) browserHeaderText(w int) string {
	if a.jumpActive {
		return headerText(w, "/"+a.jumpQuery, jumpLegend)
	}
	return a.menuBarText(w, browserLegend)
}

// browserFooterText is the popup layout's footer-line equivalent of
// browserHeaderText, since the popup shows the browser's legend as a
// dedicated footer row rather than sharing the top header row with the
// root path (SPEC.md §5.1).
func (a *App) browserFooterText(w int) string {
	if a.jumpActive {
		return headerText(w, "/"+a.jumpQuery, jumpLegend)
	}
	return browserLegend
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
// header showing the query and its single action (Return opens the
// selected match), and the flat match list.
func (a *App) drawQuickOpen(w, h int) {
	a.drawHeader(w, headerText(w, a.finderQuery, "[return] open  [esc] cancel"))
	a.drawFinderList(w, h)
}

// drawFinderList renders quick open's flat, root-relative match list
// (SPEC.md §4.1's index, §5.2's indexing/no-matches placeholder), plus
// any inline failure message from a failed open.
func (a *App) drawFinderList(w, h int) {
	listHeight := h - 1
	if a.finderMessage != "" {
		listHeight--
	}

	_, done := a.idx.Snapshot()
	switch {
	case !done:
		// SPEC.md §5.2: during the pre-threshold grace period this is
		// indistinguishable from "still indexing" at the pure-decision
		// level, so the match-list area stays blank either way rather
		// than claiming "no matches" before indexing has even looked.
		a.drawText(0, 1, w, centerPad("indexing…", w), styleNormal)
	case len(a.finderMatches) == 0:
		a.drawText(0, 1, w, centerPad("no matches", w), styleNormal)
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
			style := styleNormal
			if i == a.finderSelected {
				style = styleSelected
			}
			a.drawText(0, 1+row, w, a.finderMatches[i].RelPath, style)
		}
	}

	if a.finderMessage != "" {
		a.drawText(0, h-1, w, a.finderMessage, styleError)
	}
}

// drawOpenFiles renders the open-files-list overlay (SPEC.md §2.3):
// every entry in list order as its root-relative, slash-delimited
// path, the currently-displayed entry marked distinctly, or an
// explanatory message if the list is empty.
func (a *App) drawOpenFiles(w, h int) {
	a.drawHeader(w, headerText(w, "open files", "[return] display  [x] remove  [shift-up/down] reorder  [esc] close"))

	entries := a.files.Entries
	listHeight := h - 1
	if listHeight < 1 {
		return
	}

	if len(entries) == 0 {
		a.drawText(0, 1, w, centerPad("no open files", w), styleNormal)
		return
	}

	if a.openFilesSelected < a.openFilesScroll {
		a.openFilesScroll = a.openFilesSelected
	}
	if a.openFilesSelected >= a.openFilesScroll+listHeight {
		a.openFilesScroll = a.openFilesSelected - listHeight + 1
	}

	for row := range listHeight {
		i := a.openFilesScroll + row
		if i >= len(entries) {
			break
		}
		style := styleNormal
		if i == a.openFilesSelected {
			style = styleSelected
		}
		label := tree.RelativeDisplayPath(a.rootPath, entries[i].Path)
		if i == a.files.Displayed {
			label = "* " + label
		} else {
			label = "  " + label
		}
		a.drawText(0, 1+row, w, label, style)
	}
}

// drawSearch renders the content search overlay (SPEC.md §9.2): a
// header row (title plus keybinding legend), the query input on its own
// row directly below, and a two-level list — one row per matching file,
// disclosing (unless collapsed) its own matching-line rows below it —
// or a placeholder while there's nothing to show yet.
func (a *App) drawSearch(w, h int) {
	title := "search"
	if a.searchRegex {
		title = "search (regex)"
	}
	a.drawHeader(w, headerText(w, title, searchLegend))
	a.drawText(0, 1, w, "> "+a.searchQuery, styleSearchInput)

	const listTop = 2
	listHeight := h - listTop
	if a.searchMessage != "" {
		listHeight--
	}

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
			style := styleNormal
			if i == a.searchSelected {
				style = styleSelected
			}
			row := rows[i]
			if !row.isHit && a.searchResults[row.file].AbsPath == a.searchFlashPath && time.Since(a.searchFlashStart) < searchFlashDuration {
				style = styleSearchFlash
			}
			a.drawText(0, listTop+line, w, searchRowLabel(a.searchResults, a.searchCollapsed, a.files, row), style)
		}
	}

	if a.searchMessage != "" {
		a.drawText(0, h-1, w, a.searchMessage, styleError)
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
func (a *App) menuBarText(w int, legend string) string {
	return headerText(w, a.rootLabel(), legend)
}

// headerText left-aligns left and right-aligns legend within w columns,
// with at least one space of separation between them (SPEC.md §5.2's
// header/title bar convention, applied everywhere a header combines
// some left-hand content with a keybinding legend). If they don't both
// fit with that minimum separation, left is dropped entirely, since the
// legend is what makes the overlay usable and always takes priority.
func headerText(w int, left, legend string) string {
	leftLen, legendLen := len([]rune(left)), len([]rune(legend))
	if gap := w - leftLen - legendLen; gap >= 1 {
		return left + strings.Repeat(" ", gap) + legend
	}
	return legend
}

// drawFileTitleBar renders the currently-displayed file's own title bar
// (its root-relative path) in the row above the preview content, when a
// file is displayed. Returns the number of rows it occupied (0 or 1) so
// callers can shrink the preview's rectangle accordingly.
// interactive is false while the browser overlay owns input (SPEC.md
// §5.1: the preview pane is read-only in that case, accepting neither
// scrolling nor goto-line), in which case file-specific action keys
// like goto-line don't apply and their legend is omitted rather than
// advertising a key that won't do anything right now.
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
		text = headerText(w, "/"+a.findInput, "[return] search  [esc] cancel")
	case !interactive:
		text = left
	case e.FindQuery != "" && len(e.FindMatches) > 0:
		text = headerText(w, left, findStatusText(e)+"  "+findLegend)
	case e.FindQuery != "":
		text = headerText(w, left, findStatusText(e)+"  "+findLegendNoMatches)
	case e.CopyMode:
		text = headerText(w, left, fileLegendCopyModeOn)
	default:
		text = headerText(w, rel, fileLegend)
	}

	style := styleFileTitle
	if e.CopyMode {
		style = styleCopyModeTitle
	}
	a.drawText(x0, y0, w, text, style)
	return 1
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
// when this is the primary (non-overlaid) view, since the goto-line key
// isn't handled while the browser's split/popup overlay (§5.1) is
// showing this read-only.
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
		a.drawText(x0, y0+h-1, w, "goto line: "+a.gotoInput, styleNormal)
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
		case n == a.browserSelected:
			// SPEC.md §5.2: the cursor's own highlight always wins over
			// jump mode's match highlight, since the selected row is
			// itself one of the matches whenever jump mode is active.
			style = styleSelected
		case isMatch[n]:
			style = styleFindMatch
		}
		a.drawText(x0, y0+row, w, browserLabel(n), style)
	}
}

// browserLabel renders one browser row's indentation, expand/collapse
// marker, name, and any per-node error indicator (SPEC.md §5.2).
func browserLabel(n *tree.Node) string {
	indent := strings.Repeat("  ", n.Depth)
	marker := "  "
	if n.IsDir {
		if n.Expanded {
			marker = "v "
		} else {
			marker = "> "
		}
	}
	label := indent + marker + n.Name
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
		spinnerThreshold, spinnerMinDisplayDuration, completionDisplayDuration, completionFadeDuration,
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
	x := max(w-len(visible), 0)
	y := h - 1
	if y < 0 {
		return
	}
	a.drawText(x, y, len(visible), string(visible), styleBadge)
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
