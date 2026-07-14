package ui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/preview"
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

func (a *App) draw() {
	a.screen.Clear()
	w, h := a.screen.Size()

	switch a.mode {
	case modeTree:
		a.drawHeader(w, fmt.Sprintf("%s   [/] jump  [space] preview  [q] quit", a.rootPath))
		a.drawTree(0, 1, w, h-1, true)
		a.drawSpinnerBadge(w, h)
	case modeJump:
		a.drawHeader(w, fmt.Sprintf("/%s   [tab] next  [enter] jump  [esc] cancel", a.jumpQuery))
		a.drawJumpMatches(0, 1, w, h-1)
		a.drawSpinnerBadge(w, h)
	case modePreview:
		a.ensurePreviewWrapped()
		treeWidth, previewPaneWidth, split := a.computeSplitLayout(w)
		if split {
			a.drawHeader(w, fmt.Sprintf("%s   [g] goto  [q] close", a.previewNode.Name))
			a.drawTree(0, 1, treeWidth, h-1, false)
			a.drawVerticalRule(treeWidth, 1, h-1)
			a.drawPreview(treeWidth+1, 1, previewPaneWidth, h-1)
		} else {
			a.drawHeader(w, a.rootPath)
			a.drawTree(0, 1, w, h-1, false)
			a.drawPreviewPopup(w, h)
		}
		if a.gotoPromptOpen {
			a.drawGotoPrompt(w, h)
		}
	}

	a.screen.Show()
}

func (a *App) drawHeader(w int, text string) {
	for x := 0; x < w; x++ {
		a.screen.SetContent(x, 0, ' ', nil, styleHeader)
	}
	drawText(a.screen, 0, 0, text, styleHeader)
}

func (a *App) drawSpinnerBadge(w, h int) {
	text, hiddenPrefix, ok := a.badgeText()
	if !ok {
		return
	}
	runes := []rune(text)
	// Anchor the badge's right edge at w-2, same corner the
	// single-character spinner used, so the animated frame character
	// (the message's last rune while running) lands in the same spot
	// it always has; a fading completion message shrinks from the left
	// without that anchor moving.
	startX := w - 2 - len(runes)
	if startX < 0 {
		startX = 0
	}
	for i, r := range runes {
		if i < hiddenPrefix {
			continue
		}
		a.screen.SetContent(startX+i, h-1, r, nil, styleBadge)
	}
}

// drawTree renders the flattened tree within the given rectangle.
// interactive controls whether the selection highlight reflects
// keyboard focus (false in split-view preview mode, where the tree
// pane is read-only per SPEC.md §9).
func (a *App) drawTree(x, y, width, height int, interactive bool) {
	flat := a.root.Flatten()
	selIdx := indexOf(flat, a.selected)

	a.scroll = clampScroll(a.scroll, selIdx, height)

	for row := 0; row < height && a.scroll+row < len(flat); row++ {
		n := flat[a.scroll+row]
		label := treeLabel(n)
		style := styleNormal
		if n == a.selected {
			style = styleSelected
		}
		drawTextClipped(a.screen, x, y+row, width, label, style)
		if n.Err != "" {
			drawTextClipped(a.screen, x+len(label)+1, y+row, width, fmt.Sprintf("[%s]", n.Err), styleError)
		}
	}
	_ = interactive
}

func treeLabel(n *tree.Node) string {
	indent := ""
	for i := 0; i < n.Depth; i++ {
		indent += "  "
	}
	marker := " "
	if n.IsDir {
		if n.Expanded {
			marker = "v"
		} else {
			marker = ">"
		}
	}
	return indent + marker + " " + n.Name
}

func clampScroll(scroll, selIdx, height int) int {
	if selIdx < scroll {
		return selIdx
	}
	if selIdx >= scroll+height {
		return selIdx - height + 1
	}
	return scroll
}

func (a *App) drawJumpMatches(x, y, width, height int) {
	entries, done := a.idx.Snapshot()
	_ = entries
	if !done {
		frame, ok := a.spinnerVisible()
		if ok {
			drawText(a.screen, x, y, "indexing "+string(frame), styleNormal)
		}
		return
	}
	for row := 0; row < height && row < len(a.jumpMatches); row++ {
		e := a.jumpMatches[row]
		style := styleNormal
		if row == a.jumpSelected {
			style = styleSelected
		}
		drawTextClipped(a.screen, x, y+row, width, e.RelPath, style)
	}
}

func (a *App) drawVerticalRule(x, y, height int) {
	for row := 0; row < height; row++ {
		a.screen.SetContent(x, y+row, tcell.RuneVLine, nil, styleNormal)
	}
}

func (a *App) drawPreview(x, y, width, height int) {
	numLines := len(a.previewLines)
	gutter := gutterWidth(numLines)

	for row := 0; row < height; row++ {
		rowIdx := a.previewScroll + row
		if rowIdx >= len(a.previewRows) {
			break
		}
		dr := a.previewRows[rowIdx]
		if dr.HasNumber {
			numStr := itoa(dr.SourceLine + 1)
			pad := gutter - len(numStr) - 1
			drawText(a.screen, x+pad, y+row, numStr+" ", styleNormal)
		}
		col := x + gutter
		for _, seg := range dr.Segments {
			style, ok := categoryStyles[seg.Category]
			if !ok {
				style = styleNormal
			}
			col = drawTextClipped(a.screen, col, y+row, width-gutter, seg.Text, style)
		}
	}
}

func (a *App) drawPreviewPopup(w, h int) {
	margin := 4
	px, py := margin, margin/2
	pw, ph := w-2*margin, h-margin

	for row := 0; row < ph; row++ {
		for col := 0; col < pw; col++ {
			a.screen.SetContent(px+col, py+row, ' ', nil, styleNormal)
		}
	}
	drawTextClipped(a.screen, px, py, pw, a.previewNode.Name, styleHeader)
	a.drawPreview(px, py+1, pw, ph-2)
	drawTextClipped(a.screen, px, py+ph-1, pw, "[g] goto  [q]/[esc] close", styleHeader)
}

func (a *App) drawGotoPrompt(_, h int) {
	prompt := "goto line: " + a.gotoInput
	drawText(a.screen, 0, h-1, prompt, styleHeader)
}

func drawText(s tcell.Screen, x, y int, text string, style tcell.Style) {
	for _, r := range text {
		s.SetContent(x, y, r, nil, style)
		x++
	}
}

// drawTextClipped draws text starting at x,y, clipped to maxWidth
// columns, and returns the column immediately after the last rune
// drawn (useful for laying out successive segments on one row).
func drawTextClipped(s tcell.Screen, x, y, maxWidth int, text string, style tcell.Style) int {
	col := x
	limit := x + maxWidth
	for _, r := range text {
		if col >= limit {
			break
		}
		s.SetContent(col, y, r, nil, style)
		col++
	}
	return col
}
