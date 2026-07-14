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

// draw renders one frame. The tree explorer and jump/fuzzy-picker
// overlays and a placeholder primary preview view exist this stage;
// the open-files-list overlay and the primary view's own content
// rendering (gutter, wrapping, scrolling — SPEC.md §2.1) land in later
// stages.
func (a *App) draw() {
	a.screen.Clear()
	w, h := a.screen.Size()

	switch a.overlay {
	case overlayTree:
		a.drawHeader(w, fmt.Sprintf("%s   [space] open+close  [a] open, keep open  [/] jump  [esc] close", a.rootPath))
		treeHeight := h - 1
		if a.treeMessage != "" {
			treeHeight--
		}
		a.drawTree(0, 1, w, treeHeight)
		if a.treeMessage != "" {
			a.drawText(0, h-1, w, a.treeMessage, styleError)
		}
		a.drawBadge(w, h)
	case overlayJump:
		a.drawJump(w, h)
		a.drawBadge(w, h)
	case overlayOpenFiles:
		a.drawOpenFiles(w, h)
	default:
		a.drawHeader(w, a.previewHeaderText()+"   [e] browse  [tab] open files  [/] jump  [g] goto  [q] quit")
		a.drawPreview(w, h)
	}

	a.screen.Show()
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

func (a *App) previewHeaderText() string {
	if e := a.files.DisplayedEntry(); e != nil {
		return e.Path
	}
	return "(no file open)"
}

// drawPreview renders the primary preview view's content (SPEC.md
// §2.1): a line-number gutter plus wrapped, highlighted rows for the
// currently-displayed entry, or an explanatory empty-state message if
// none is displayed. The goto-line prompt, when open, occupies the
// bottom row.
func (a *App) drawPreview(w, h int) {
	e := a.files.DisplayedEntry()
	if e == nil {
		msg := "no files open — press e to browse, / to search"
		row := max(h/2, 1)
		a.drawText(0, row, w, centerPad(msg, w), styleNormal)
		return
	}

	a.ensurePreviewWrapped(e)
	viewportHeight := a.previewViewportHeight()
	gw := gutterWidth(len(e.Lines))
	digits := gw - 2

	for row := range viewportHeight {
		y := 1 + row
		i := e.Scroll + row
		if i >= len(e.Rows) {
			break
		}
		dr := e.Rows[i]
		numField := strings.Repeat(" ", digits)
		if dr.HasNumber {
			numField = fmt.Sprintf("%*d", digits, dr.SourceLine+1)
		}
		a.drawText(0, y, gw, numField+"  ", styleNormal)
		a.drawSegments(gw, y, w-gw, dr.Segments)
	}

	if a.gotoPromptOpen {
		a.drawText(0, h-1, w, "goto line: "+a.gotoInput, styleNormal)
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
