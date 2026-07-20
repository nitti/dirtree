package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nitti/dirtree/internal/openfiles"
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
		a.Browser.Draw(w, h)
		a.drawBadge(w, h)
	case views.OverlayQuickOpen:
		a.QuickOpen.Draw(w, h)
		a.drawBadge(w, h)
	case views.OverlayOpenFiles:
		a.drawOpenFiles(w, h)
	case views.OverlaySearch:
		a.Search.Draw(w, h)
		a.drawBadge(w, h)
	default:
		a.shared.Canvas.DrawHeader(w, a.menuBarText(w, previewLegend))
		a.Preview.Draw(0, 1, w, h-1, true)
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

// drawOpenFiles renders the open-files-list overlay (SPEC.md §2.3): a
// dropdown-style popup over the (unmodified, last-rendered) primary
// preview view, showing at most openfiles.PageSize entries of the
// current page, each row labeled with its 0-9 position, the
// currently-displayed entry marked distinctly, or an explanatory
// message if the list is empty.
func (a *App) drawOpenFiles(w, h int) {
	a.shared.Canvas.DrawHeader(w, a.menuBarText(w, previewLegend))
	a.Preview.Draw(0, 1, w, h-1, false)

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
