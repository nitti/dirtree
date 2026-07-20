package views

import (
	"fmt"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/tree"
	"github.com/nitti/dirtree/internal/ui/canvas"
)

// Open-files-list overlay dropdown sizing (SPEC.md §2.3): wide enough
// to fit its own header row (page counter + keybinding legend) or its
// longest visible path, whichever is larger, clamped to this range so
// it still reads as a compact dropdown rather than growing to fill the
// terminal.
const (
	openFilesMinWidth = 40
	openFilesMaxWidth = 84
)

// OpenFiles holds the open-files-list overlay's own state (SPEC.md
// §2.3): Selected is an index into Files.Entries. Which page it falls
// on is derived (openfiles.Page), not stored, per that package's doc
// comment.
type OpenFiles struct {
	*Shared

	Selected int
}

// HandleKey implements the open-files-list overlay's input handling
// (SPEC.md §2.3): a dropdown-style popup showing at most
// openfiles.PageSize entries at a time ("a page"), each row labeled
// with its 0-9 position on the current page. Shift-Page-Up/Shift-Page-
// Down and Shift-Up/Shift-Down (reorder) are checked ahead of their
// plain Page-Up/Page-Down and Up/Down counterparts (navigate) since
// tcell reports the shifted and unshifted forms as the same Key with
// ModShift set, not a distinct key.
func (v *OpenFiles) HandleKey(ev *tcell.EventKey) {
	n := len(v.Files.Entries)
	shift := ev.Modifiers()&tcell.ModShift != 0

	switch {
	case ev.Key() == tcell.KeyEscape:
		*v.Overlay = OverlayNone
	case ev.Key() == tcell.KeyPgUp && shift:
		if n > 0 {
			v.Selected = v.Files.MoveUpPage(v.Selected, openfiles.PageSize)
		}
	case ev.Key() == tcell.KeyPgDn && shift:
		if n > 0 {
			v.Selected = v.Files.MoveDownPage(v.Selected, openfiles.PageSize)
		}
	case ev.Key() == tcell.KeyUp && shift:
		if n > 0 {
			v.Selected = v.Files.MoveUp(v.Selected)
		}
	case ev.Key() == tcell.KeyDown && shift:
		if n > 0 {
			v.Selected = v.Files.MoveDown(v.Selected)
		}
	case ev.Key() == tcell.KeyPgUp:
		v.Selected = openfiles.SelectPage(v.Selected, -1, openfiles.PageSize, n)
	case ev.Key() == tcell.KeyPgDn:
		v.Selected = openfiles.SelectPage(v.Selected, 1, openfiles.PageSize, n)
	case ev.Key() == tcell.KeyUp:
		if n > 0 {
			v.Selected = tree.MoveSelection(v.Selected, -1, n)
		}
	case ev.Key() == tcell.KeyDown:
		if n > 0 {
			v.Selected = tree.MoveSelection(v.Selected, 1, n)
		}
	case ev.Key() == tcell.KeyEnter:
		if n > 0 {
			v.Files.Display(v.Selected)
			*v.Overlay = OverlayNone
		}
	case ev.Rune() >= '0' && ev.Rune() <= '9':
		if idx, ok := openfiles.SelectDigit(v.Selected, int(ev.Rune()-'0'), openfiles.PageSize, n); ok {
			v.Selected = idx
			v.Files.Display(idx)
			*v.Overlay = OverlayNone
		}
	case ev.Rune() == 'x':
		if n > 0 {
			v.Selected = v.Files.Remove(v.Selected)
			if len(v.Files.Entries) == 0 {
				// SPEC.md §2.3: emptying the list auto-closes the
				// overlay to the primary preview view's empty state,
				// which in turn auto-opens the browser exactly as it
				// does on startup (§1).
				*v.Overlay = OverlayBrowser
			}
		}
	}
}

// Draw renders the open-files-list overlay (SPEC.md §2.3): a
// dropdown-style popup over the (unmodified, last-rendered) primary
// preview view — rendered here via preview.Draw in its non-interactive
// form, since this overlay owns input and the preview pane underneath
// it is read-only — showing at most openfiles.PageSize entries of the
// current page, each row labeled with its 0-9 position, the
// currently-displayed entry marked distinctly, or an explanatory
// message if the list is empty.
func (v *OpenFiles) Draw(x0, y0, w, h int, preview *Preview) {
	preview.Draw(x0, y0, w, h, false)

	entries := v.Files.Entries

	if len(entries) == 0 {
		boxW := min(max(openFilesMinWidth, 4), w)
		boxH := min(4, h)
		if boxW < 4 || boxH < 4 {
			return
		}
		bx0 := x0 + (w-boxW)/2
		v.drawBox(bx0, y0, boxW, boxH, "open files", false, false)
		innerW := boxW - 2
		v.Canvas.DrawText(bx0+1, y0+1, innerW, canvas.CenterPad("no open files", innerW), canvas.StyleNormal)
		v.Canvas.DrawText(bx0+1, y0+2, innerW, canvas.CenterPad("[esc] close", innerW), canvas.StyleNormal)
		return
	}

	pageSize := openfiles.PageSize
	page := openfiles.Page(v.Selected, pageSize)
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
		label := openFilesRowLabel(i-start, i == v.Files.Displayed, tree.RelativeDisplayPath(v.RootPath, entries[i].Path))
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
	bx0 := x0 + (w-boxW)/2
	innerW := boxW - 2

	v.drawBox(bx0, y0, boxW, boxH, "open files", page > 0, page < pageCount-1)
	v.Canvas.DrawText(bx0+1, y0+1, innerW, canvas.LegendText(innerW, counter, legendEntries), canvas.StyleNormal)

	for row := 0; row < itemRows && y0+2+row < y0+boxH-1; row++ {
		i := start + row
		style := canvas.StyleNormal
		if i == v.Selected {
			style = canvas.StyleSelected
		}
		label := openFilesRowLabel(row, i == v.Files.Displayed, tree.RelativeDisplayPath(v.RootPath, entries[i].Path))
		v.Canvas.DrawText(bx0+1, y0+2+row, innerW, label, style)
	}
}

// drawBox draws the open-files dropdown's bordered box: the title
// embedded in the top border like canvas.Canvas.DrawBox, plus a "▲" in
// the top border's right end when a previous page exists and a "▼" in
// the bottom border's right end when a next page exists — so "more
// items available" is visible right at the edge it refers to
// (above/below) without spending a content row on it.
func (v *OpenFiles) drawBox(x0, y0, w, h int, title string, hasPrev, hasNext bool) {
	v.Canvas.DrawBox(x0, y0, w, h, title)
	if hasPrev && w > 2 {
		v.Canvas.SetContent(x0+w-2, y0, '▲', canvas.StyleNormal)
	}
	if hasNext && w > 2 {
		v.Canvas.SetContent(x0+w-2, y0+h-1, '▼', canvas.StyleNormal)
	}
	v.Canvas.FillRect(x0+1, y0+1, w-2, h-2, canvas.StyleNormal)
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
