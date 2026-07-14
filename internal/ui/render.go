package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"

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
	default:
		a.drawHeader(w, a.previewHeaderText()+"   [e] browse  [/] jump  [q] quit")
		a.drawPreviewPlaceholder(w, h)
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

func (a *App) previewHeaderText() string {
	if e := a.files.DisplayedEntry(); e != nil {
		return e.Path
	}
	return "(no file open)"
}

// drawPreviewPlaceholder stands in for the primary preview view's real
// content rendering (SPEC.md §2.1), which lands in stage 5.
func (a *App) drawPreviewPlaceholder(w, h int) {
	var msg string
	if e := a.files.DisplayedEntry(); e != nil {
		msg = fmt.Sprintf("%s (%d lines) — preview rendering not yet implemented", e.Path, len(e.Lines))
	} else {
		msg = "no files open — press e to browse, / to search"
	}
	row := max(h/2, 1)
	a.drawText(0, row, w, centerPad(msg, w), styleNormal)
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
