package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/preview"
	"github.com/nitti/dirtree/internal/spinner"
	"github.com/nitti/dirtree/internal/tree"
)

var (
	styleNormal   = tcell.StyleDefault
	styleSelected = tcell.StyleDefault.Reverse(true)
	styleHeader   = tcell.StyleDefault.Background(tcell.ColorDarkBlue).Foreground(tcell.ColorWhite)
	styleError    = tcell.StyleDefault.Foreground(tcell.ColorRed)
	styleBadge    = tcell.StyleDefault.Background(tcell.ColorOrange).Foreground(tcell.ColorBlack)
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
	case overlayTree:
		a.drawTreeOverlay(w, h)
	case overlayJump:
		a.drawJump(w, h)
		a.drawBadge(w, h)
	case overlayOpenFiles:
		a.drawOpenFiles(w, h)
	case overlaySearch:
		a.drawSearch(w, h)
		a.drawBadge(w, h)
	default:
		a.drawHeader(w, a.previewHeaderText()+"   [e] browse  [tab] open files  [/] jump  [s] search  [g] goto  [q] quit")
		a.drawPreview(0, 1, w, h-1)
	}

	a.screen.Show()
}

// drawTreeOverlay picks split-vs-popup layout (SPEC.md §5.1, recomputed
// every frame so a live resize can flip between them) and renders the
// tree explorer overlay accordingly.
func (a *App) drawTreeOverlay(w, h int) {
	treeWidth, previewWidth, split := a.computeSplitLayout(w)
	if split {
		a.drawTreeSplitView(w, h, treeWidth, previewWidth)
	} else {
		a.drawTreePopup(w, h)
	}
	a.drawBadge(w, h)
}

// drawTreeSplitView renders the wide-terminal layout (SPEC.md §5.1):
// the tree explorer on the left, a vertical rule, and the primary
// preview view — still visible, showing whatever it had, but read-only
// (no goto-line prompt can be open while this overlay owns input) — on
// the right.
func (a *App) drawTreeSplitView(w, h, treeWidth, previewWidth int) {
	a.drawHeader(w, fmt.Sprintf("%s   [space] open+close  [a] open, keep open  [/] jump  [s] search  [esc] close", a.rootPath))

	treeHeight := h - 1
	if a.treeMessage != "" {
		treeHeight--
	}
	a.drawTree(0, 1, treeWidth, treeHeight)
	if a.treeMessage != "" {
		a.drawText(0, h-1, treeWidth, a.treeMessage, styleError)
	}

	for y := 1; y < h; y++ {
		a.screen.SetContent(treeWidth, y, '│', nil, styleNormal)
	}

	a.drawPreview(treeWidth+1, 1, previewWidth, h-1)
}

// drawTreePopup renders the narrow-terminal layout (SPEC.md §5.1): the
// primary preview view rendered exactly as it would with no overlay
// active ("unmodified, last-rendered"), with a centered, bordered
// floating window containing the tree explorer on top.
func (a *App) drawTreePopup(w, h int) {
	a.drawHeader(w, a.previewHeaderText()+"   [e] browse  [tab] open files  [/] jump  [s] search  [g] goto  [q] quit")
	a.drawPreview(0, 1, w, h-1)

	popupW := min(max(w-2*popupMarginX, 10), w)
	popupH := min(max(h-2*popupMarginY, 5), h)
	x0 := (w - popupW) / 2
	y0 := (h - popupH) / 2

	a.drawBox(x0, y0, popupW, popupH, a.rootPath)
	a.fillRect(x0+1, y0+1, popupW-2, popupH-2, styleNormal)

	innerX, innerY := x0+1, y0+1
	innerW, innerH := popupW-2, popupH-2
	footerRow := innerH - 1
	treeHeight := footerRow
	if a.treeMessage != "" {
		treeHeight--
	}
	a.drawTree(innerX, innerY, innerW, treeHeight)
	if a.treeMessage != "" {
		a.drawText(innerX, innerY+treeHeight, innerW, a.treeMessage, styleError)
	}
	a.drawText(innerX, innerY+footerRow, innerW, "[space] open+close  [a] open, keep open  [/] jump  [s] search  [esc] close", styleNormal)
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

// drawJump renders the jump/fuzzy-picker overlay (SPEC.md §4.2, §5.2):
// a header showing the query and which of Enter/Space maps to which
// action for the current entry point, and the flat, root-relative
// match list (or an indexing/no-matches placeholder).
func (a *App) drawJump(w, h int) {
	enterLabel, spaceLabel := "reveal in tree", "open"
	if a.jumpEntry == jumpFromPreview {
		enterLabel, spaceLabel = "open", "reveal in tree"
	}
	a.drawHeader(w, fmt.Sprintf("/%s   [enter] %s  [space] %s  [esc] cancel", a.jumpQuery, enterLabel, spaceLabel))

	listHeight := h - 1
	if a.jumpMessage != "" {
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
	case len(a.jumpMatches) == 0:
		a.drawText(0, 1, w, centerPad("no matches", w), styleNormal)
	default:
		if listHeight > 0 {
			if a.jumpSelected < a.jumpScroll {
				a.jumpScroll = a.jumpSelected
			}
			if a.jumpSelected >= a.jumpScroll+listHeight {
				a.jumpScroll = a.jumpSelected - listHeight + 1
			}
		}
		for row := range listHeight {
			i := a.jumpScroll + row
			if i >= len(a.jumpMatches) {
				break
			}
			style := styleNormal
			if i == a.jumpSelected {
				style = styleSelected
			}
			a.drawText(0, 1+row, w, a.jumpMatches[i].RelPath, style)
		}
	}

	if a.jumpMessage != "" {
		a.drawText(0, h-1, w, a.jumpMessage, styleError)
	}
}

// drawOpenFiles renders the open-files-list overlay (SPEC.md §2.3):
// every entry in list order as its root-relative, slash-delimited
// path, the currently-displayed entry marked distinctly, or an
// explanatory message if the list is empty.
func (a *App) drawOpenFiles(w, h int) {
	a.drawHeader(w, "open files   [enter] display  [x] remove  [shift-up/down] reorder  [esc] close")

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
// header showing the query, and the flat list of matching files (each
// as its root-relative path plus its first matching line's number and
// text), or a placeholder while there's nothing to show yet.
func (a *App) drawSearch(w, h int) {
	a.drawHeader(w, fmt.Sprintf("search: %s   [enter] open  [esc] cancel", a.searchQuery))

	listHeight := h - 1
	if a.searchMessage != "" {
		listHeight--
	}

	switch {
	case a.searchQuery == "":
		a.drawText(0, 1, w, centerPad("type to search file contents", w), styleNormal)
	case a.searchResults == nil:
		// SPEC.md §9.1: covers both "index not done yet" and "a scan for
		// this query is still running" — either way, nothing has been
		// found yet, which is a different state from genuinely zero
		// matches, so this must not render as "no matches."
		_, indexDone := a.idx.Snapshot()
		msg := "searching…"
		if !indexDone {
			msg = "indexing…"
		}
		a.drawText(0, 1, w, centerPad(msg, w), styleNormal)
	case len(a.searchResults) == 0:
		a.drawText(0, 1, w, centerPad("no matches", w), styleNormal)
	default:
		if listHeight > 0 {
			if a.searchSelected < a.searchScroll {
				a.searchScroll = a.searchSelected
			}
			if a.searchSelected >= a.searchScroll+listHeight {
				a.searchScroll = a.searchSelected - listHeight + 1
			}
		}
		for row := range listHeight {
			i := a.searchScroll + row
			if i >= len(a.searchResults) {
				break
			}
			style := styleNormal
			if i == a.searchSelected {
				style = styleSelected
			}
			m := a.searchResults[i]
			label := fmt.Sprintf("%s:%d: %s", m.RelPath, m.LineNum, strings.TrimSpace(m.LineText))
			a.drawText(0, 1+row, w, label, style)
		}
	}

	if a.searchMessage != "" {
		a.drawText(0, h-1, w, a.searchMessage, styleError)
	}
}

func (a *App) previewHeaderText() string {
	if e := a.files.DisplayedEntry(); e != nil {
		return e.Path
	}
	return "(no file open)"
}

// drawPreview renders the primary preview view's content (SPEC.md
// §2.1) into the (x0, y0)-(x0+w, y0+h) rectangle: a line-number gutter
// plus wrapped, highlighted rows for the currently-displayed entry, or
// an explanatory empty-state message if none is displayed. The
// goto-line prompt, when open, occupies the bottom row — reachable only
// when this is the primary (non-overlaid) view, since the goto-line key
// isn't handled while the tree explorer's split/popup overlay (§5.1) is
// showing this read-only.
func (a *App) drawPreview(x0, y0, w, h int) {
	e := a.files.DisplayedEntry()
	if e == nil {
		msg := "no files open — press e to browse, / to jump, s to search contents"
		row := y0 + max(h/2, 1)
		a.drawText(x0, row, w, centerPad(msg, w), styleNormal)
		return
	}

	gw := gutterWidth(len(e.Lines))
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
		numField := strings.Repeat(" ", digits)
		if dr.HasNumber {
			numField = fmt.Sprintf("%*d", digits, dr.SourceLine+1)
		}
		a.drawText(x0, y, gw, numField+"  ", styleNormal)
		a.drawSegments(x0+gw, y, contentWidth, dr.Segments)
	}

	if a.gotoPromptOpen {
		a.drawText(x0, y0+h-1, w, "goto line: "+a.gotoInput, styleNormal)
	}
}

// drawSegments draws seg fragments left to right starting at (x, y),
// each in its category's style (SPEC.md §2.1), clipped and padded with
// spaces to exactly w columns.
func (a *App) drawSegments(x, y, w int, segs []preview.Segment) {
	col := 0
	for _, seg := range segs {
		style := styleFor(seg.Category)
		for _, r := range seg.Text {
			if col >= w {
				return
			}
			a.screen.SetContent(x+col, y, r, nil, style)
			col++
		}
	}
	for ; col < w; col++ {
		a.screen.SetContent(x+col, y, ' ', nil, styleNormal)
	}
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

// drawTree renders the tree explorer's currently-visible flattened
// list (SPEC.md §3.1, §5.2), keeping the selected row scrolled into
// view. This is a simplified full-width rendering; the dual
// split/popup layout (SPEC.md §5.1) lands in stage 6.
func (a *App) drawTree(x0, y0, w, h int) {
	if h < 1 {
		return
	}
	flat := a.root.Flatten()
	selIdx := indexOf(flat, a.treeSelected)

	if selIdx < a.treeScroll {
		a.treeScroll = selIdx
	}
	if selIdx >= a.treeScroll+h {
		a.treeScroll = selIdx - h + 1
	}
	if a.treeScroll < 0 {
		a.treeScroll = 0
	}

	for row := range h {
		i := a.treeScroll + row
		if i >= len(flat) {
			break
		}
		n := flat[i]
		style := styleNormal
		if n == a.treeSelected {
			style = styleSelected
		}
		a.drawText(x0, y0+row, w, treeLabel(n), style)
	}
}

// treeLabel renders one tree row's indentation, expand/collapse
// marker, name, and any per-node error indicator (SPEC.md §5.2).
func treeLabel(n *tree.Node) string {
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
