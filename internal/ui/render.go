package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nitti/dirtree/internal/spinner"
	"github.com/nitti/dirtree/internal/toast"
	"github.com/nitti/dirtree/internal/ui/canvas"
	"github.com/nitti/dirtree/internal/ui/views"
)

// switchFilesLegendText is previewLegend's open-files-list entry,
// broken out as its own constant so drawPreviewHeader's dim-when-empty
// check (below) can find it in the fitted legend text without
// duplicating the literal.
const switchFilesLegendText = "[tab] switch files"

var (
	// previewLegend is the primary preview view's app-wide legend
	// (SPEC.md §5.2): the four ways to leave the preview for another
	// view/overlay, plus quit. Browse and open-files are the two most
	// direct routes to opening something and are kept at priority 1
	// alongside quit (the only way out of the app); quick open and
	// search are alternate entry points to the same goal and drop first
	// on a narrow terminal.
	previewLegend = []canvas.LegendEntry{
		{Text: switchFilesLegendText, Priority: 1},
		{Text: "[o] quick open", Priority: 2},
		{Text: "[b] browse", Priority: 1},
		{Text: "[s] search", Priority: 2},
		{Text: "[hold q] quit", Priority: 1},
	}
	// quitHoldMessage is drawn right-aligned on the header/title bar in
	// place of the usual root path/legend while the hold-to-quit
	// gesture (SPEC.md §5.2) is in progress.
	quitHoldMessage = "quitting..."
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
	case views.OverlayQuickOpen:
		a.QuickOpen.Draw(w, h)
	case views.OverlayOpenFiles:
		a.drawPreviewHeader(w)
		a.OpenFiles.Draw(0, 1, w, h-1, &a.Preview)
	case views.OverlaySearch:
		a.Search.Draw(w, h)
	default:
		a.drawPreviewHeader(w)
		a.Preview.Draw(0, 1, w, h-1, true)
	}
	a.drawBadge(w, h)
	a.drawToast(w, h)
	if a.shared.HelpVisible {
		a.drawHelp(w, h)
	}

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

// drawPreviewHeader draws the top menu bar (SPEC.md §5.2) for both
// places the primary preview view's own header/title bar row is shown:
// the primary preview view itself, and underneath the open-files-list
// overlay's dropdown (which draws its own separate header below this
// one, §2.3). previewLegend's "switch files" entry is dimmed whenever
// the open-files list is empty, since there is then nothing for Tab to
// switch to or from — the entry stays in the legend (so the keybinding
// hint doesn't disappear and reappear as files are opened/closed) but
// reads as visually inert rather than actionable. While the open-files
// overlay itself is active, every entry in this row is inert — the
// overlay owns all input, and none of switch/quick-open/browse/
// search/quit are reachable until it's closed — so the whole legend
// (not just "switch files") is dimmed instead, rather than leaving
// four keys visibly advertised that this key press won't do anything.
func (a *App) drawPreviewHeader(w int) {
	// The quitting variant's show/hide is a pure function of recency
	// (quitHoldVisualGap), entirely independent of quitHoldStart's own
	// reset/confirm state machine (SPEC.md §5.2) — this is what lets the
	// header appear and disappear almost instantly on press/release
	// without risking the functional bug a shorter quitHoldReleaseGap
	// would cause (an ordinary auto-repeat gap silently restarting the
	// whole gesture, see that constant's doc comment): a brief gap before
	// the second `q` event can, at worst, hide the header for a frame or
	// two, and it reappears already correctly mid-fade rather than
	// restarting, since the underlying timeline was never touched.
	if !a.quitHoldStart.IsZero() && time.Since(a.quitHoldLastKey) < quitHoldVisualGap {
		a.drawQuitHoldHeader(w)
		return
	}
	if a.shared.HelpVisible {
		a.shared.Canvas.DrawHeader(w, a.menuBarText(w, canvas.HideKeysLegend))
		return
	}
	if a.overlay == views.OverlayOpenFiles {
		a.shared.Canvas.DrawHeaderAllDimmed(w, a.rootLabel(), previewLegend)
		return
	}
	if len(a.shared.Files.Entries) == 0 {
		a.shared.Canvas.DrawHeaderDimmed(w, a.rootLabel(), previewLegend, switchFilesLegendText)
		return
	}
	a.shared.Canvas.DrawHeader(w, a.menuBarText(w, previewLegend))
}

// drawQuitHoldHeader replaces the header/title bar with the hold-to-quit
// gesture's attention-grabbing variant (SPEC.md §5.2) while `q` is held:
// the whole row in canvas.StyleHeaderQuit with quitHoldMessage
// right-aligned, fading away left-to-right (toast.Decide, reusing §5.3's
// fade convention with a zero display duration so it starts fading
// immediately) over quitHoldDuration as the hold progresses — releasing
// early, anywhere in the fade, aborts the gesture the same as releasing
// before it started, so the visible fade is a direct read of how much
// longer the key must stay down.
func (a *App) drawQuitHoldHeader(w int) {
	_, hidden := toast.Decide(time.Since(a.quitHoldStart), 0, quitHoldDuration, w)
	a.shared.Canvas.DrawHeaderQuitting(w, quitHoldMessage, hidden)
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
