package ui

import (
	"github.com/nitti/dirtree/internal/ui/canvas"
	"github.com/nitti/dirtree/internal/ui/views"
)

// helpBoxTitle is the border title of the help overlay's own popup
// (SPEC.md §5.4), matching the open-files popup's title-in-border
// convention (§2.3).
const helpBoxTitle = "keys"

// Help overlay sizing (SPEC.md §5.4): wide enough for its longest
// currently-shown entry, clamped to this range so it still reads as a
// compact reference popup rather than growing to fill the terminal —
// the same sizing convention the open-files popup uses.
const (
	helpBoxMinWidth = 24
	helpBoxMaxWidth = 60
)

// helpTitleRows reports how many rows of screen-top "title bar" real
// estate the current context occupies, so the help overlay — anchored
// to the upper-right corner, title bars excluded (§5.4) — starts
// immediately below them rather than overlapping. This is distinct
// from helpLegendGroups below: a query-input row (quick open, content
// search, jump to file) occupies a screen row without itself carrying
// a keybinding legend, so it must still be accounted for here even
// though it contributes no group there.
func (a *App) helpTitleRows() int {
	switch a.overlay {
	case views.OverlayBrowser:
		if a.Browser.JumpActive {
			return 2
		}
		return 1
	case views.OverlayQuickOpen, views.OverlaySearch:
		return 2
	case views.OverlayOpenFiles:
		return 1
	default: // OverlayNone
		if a.shared.Files.DisplayedEntry() != nil {
			return 2
		}
		return 1
	}
}

// helpLegendGroups returns every keybinding legend currently live in
// the app, in the same top-to-bottom order their title bars occupy on
// screen (SPEC.md §5.4): the main title bar first, then whatever
// secondary title bar the current context has (the file title bar
// and/or the goto-line prompt row in the primary preview view, the
// open-files popup's own header in that overlay). Each view's
// CurrentLegend (or equivalent accessor) mirrors the exact same state
// precedence its own Draw method uses to pick a legend, so this can
// never drift out of sync with what's actually bound right now.
func (a *App) helpLegendGroups() [][]canvas.LegendEntry {
	switch a.overlay {
	case views.OverlayBrowser:
		return [][]canvas.LegendEntry{a.Browser.CurrentLegend()}
	case views.OverlayQuickOpen:
		return [][]canvas.LegendEntry{a.QuickOpen.CurrentLegend()}
	case views.OverlaySearch:
		return [][]canvas.LegendEntry{a.Search.CurrentLegend()}
	case views.OverlayOpenFiles:
		return [][]canvas.LegendEntry{previewLegend, a.OpenFiles.CurrentLegend()}
	default: // OverlayNone
		groups := [][]canvas.LegendEntry{previewLegend}
		if entries, ok := a.Preview.CurrentFileLegend(); ok {
			groups = append(groups, entries)
		}
		if entries, ok := a.Preview.GotoPromptLegend(); ok {
			groups = append(groups, entries)
		}
		return groups
	}
}

// drawHelp draws the help overlay (SPEC.md §5.4): a small bordered
// popup, anchored to the screen's upper-right corner just below
// whatever title bar row(s) the current context occupies, listing
// every keybinding entry currently live — one per line, left-aligned,
// formatted exactly as it reads in its own title bar (e.g. `[tab]
// switch files`) — grouped and ordered exactly as the title bars
// themselves show them, with a blank line separating each group.
// Recomputed fresh every frame from helpLegendGroups/helpTitleRows, so
// it always reflects whatever's actually on screen right now rather
// than a snapshot of the context it was opened from.
func (a *App) drawHelp(w, h int) {
	groups := a.helpLegendGroups()

	longest := len([]rune(helpBoxTitle)) + 2
	lines := 0
	for i, g := range groups {
		if i > 0 {
			lines++ // blank separator between groups
		}
		for _, e := range g {
			lines++
			if n := len([]rune(e.Text)); n > longest {
				longest = n
			}
		}
	}
	if lines == 0 {
		return
	}

	titleRows := a.helpTitleRows()
	boxW := min(max(longest+4, helpBoxMinWidth), min(helpBoxMaxWidth, w))
	boxH := min(lines+2, h-titleRows)
	if boxW < 4 || boxH < 3 {
		return
	}
	x0 := w - boxW
	y0 := titleRows
	innerW := boxW - 2

	a.shared.Canvas.DrawBox(x0, y0, boxW, boxH, helpBoxTitle)

	row := 0
	for i, g := range groups {
		if i > 0 {
			row++
		}
		for _, e := range g {
			if row > boxH-3 {
				return
			}
			a.shared.Canvas.DrawText(x0+1, y0+1+row, innerW, e.Text, canvas.StyleNormal)
			a.boldQuitHoldWord(x0+1, y0+1+row, e.Text, canvas.StyleNormal.Bold(true))
			row++
		}
	}
}
