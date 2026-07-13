// Package ui implements the terminal-rendering layer: the tcell draw
// loop, input handling, resize polling, and escape-timeout
// configuration (SPEC.md §9-§13). Unlike internal/tree, internal/match,
// internal/preview, and internal/layout, this package is not expected
// to be unit-tested — verification is manual, in a real terminal.
package ui

import (
	"os"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/ignore"
	"github.com/nitti/dirtree/internal/index"
	"github.com/nitti/dirtree/internal/layout"
	"github.com/nitti/dirtree/internal/match"
	"github.com/nitti/dirtree/internal/preview"
	"github.com/nitti/dirtree/internal/spinner"
	"github.com/nitti/dirtree/internal/tree"
)

type mode int

const (
	modeTree mode = iota
	modeJump
	modePreview
)

const (
	resizePollInterval = 100 * time.Millisecond
	spinnerThreshold   = 250 * time.Millisecond
	spinnerFPS         = 10.0
	previewByteCap     = preview.DefaultByteCap
	previewMaxWidth    = 120
	minPreviewWidth    = 40
	minTreePaneWidth   = 20
	maxTreePaneWidth   = 60
)

// App holds all interactive state for a running session.
type App struct {
	screen   tcell.Screen
	rootPath string
	root     *tree.Node
	ignorer  *ignore.Multi
	idx      *index.Index

	mode     mode
	selected *tree.Node
	scroll   int

	// jump mode
	jumpQuery    string
	jumpMatches  []index.Entry
	jumpSelected int

	// preview mode
	previewNode     *tree.Node
	previewLines    []string
	previewSegs     [][]preview.Segment
	previewRows     []preview.DisplayRow
	previewFirstRow map[int]int
	previewScroll   int
	previewWidth    int // width the current wrap was computed for
	gotoPromptOpen  bool
	gotoInput       string

	quit bool
}

// New builds a new App rooted at rootPath. It does not touch the
// terminal yet; call Run to start the interactive session.
func New(rootPath string) *App {
	ignorer := ignore.LoadAll(rootPath)
	root := tree.NewRoot(rootPath, ignorer)
	idx := index.Start(rootPath, ignorer)

	return &App{
		rootPath: rootPath,
		root:     root,
		ignorer:  ignorer,
		idx:      idx,
		selected: root,
	}
}

// Run configures the terminal and drives the main loop until the user
// quits. Per SPEC.md §12, the escape-sequence timeout is configured
// redundantly (env var, set as early as possible) before the screen is
// initialized.
func (a *App) Run() error {
	os.Setenv("ESCDELAY", "25")
	os.Setenv("TCELL_ESCDELAY", "25")

	screen, err := tcell.NewScreen()
	if err != nil {
		return err
	}
	if err := screen.Init(); err != nil {
		return err
	}
	defer screen.Fini()
	screen.SetStyle(tcell.StyleDefault)
	screen.EnablePaste()

	a.screen = screen

	events := make(chan tcell.Event, 16)
	go func() {
		for {
			ev := screen.PollEvent()
			if ev == nil {
				return
			}
			events <- ev
		}
	}()

	ticker := time.NewTicker(resizePollInterval)
	defer ticker.Stop()

	a.draw()
	for !a.quit {
		select {
		case ev := <-events:
			switch e := ev.(type) {
			case *tcell.EventResize:
				screen.Sync()
			case *tcell.EventKey:
				a.handleKey(e)
			}
			a.draw()
		case <-ticker.C:
			a.draw()
		}
	}
	return nil
}

func (a *App) handleKey(ev *tcell.EventKey) {
	switch a.mode {
	case modeTree:
		a.handleTreeKey(ev)
	case modeJump:
		a.handleJumpKey(ev)
	case modePreview:
		a.handlePreviewKey(ev)
	}
}

func (a *App) handleTreeKey(ev *tcell.EventKey) {
	flat := a.root.Flatten()
	idx := indexOf(flat, a.selected)

	switch {
	case ev.Key() == tcell.KeyUp:
		a.selected = flat[tree.MoveSelection(idx, -1, len(flat))]
	case ev.Key() == tcell.KeyDown:
		a.selected = flat[tree.MoveSelection(idx, 1, len(flat))]
	case ev.Key() == tcell.KeyRight:
		a.selected = a.selected.MoveRight(a.rootPath, a.ignorer)
	case ev.Key() == tcell.KeyLeft:
		a.selected = a.selected.MoveLeft()
	case ev.Rune() == ' ':
		if !a.selected.IsDir {
			a.openPreview(a.selected)
		}
	case ev.Rune() == '/':
		a.mode = modeJump
		a.jumpQuery = ""
		a.jumpSelected = 0
		a.recomputeJumpMatches()
	case ev.Rune() == 'q', ev.Key() == tcell.KeyEscape:
		a.quit = true
	}
}

func indexOf(list []*tree.Node, n *tree.Node) int {
	for i, c := range list {
		if c == n {
			return i
		}
	}
	return 0
}

func (a *App) recomputeJumpMatches() {
	entries, done := a.idx.Snapshot()
	if !done {
		a.jumpMatches = nil
		return
	}
	matches := make([]index.Entry, 0, len(entries))
	for _, e := range entries {
		if match.Matches(a.jumpQuery, e.RelPath) {
			matches = append(matches, e)
		}
	}
	a.jumpMatches = matches
}

func (a *App) handleJumpKey(ev *tcell.EventKey) {
	switch {
	case ev.Key() == tcell.KeyEscape:
		a.mode = modeTree
	case ev.Key() == tcell.KeyEnter:
		if len(a.jumpMatches) > 0 {
			target := a.jumpMatches[a.jumpSelected]
			if n := tree.RevealPath(a.root, a.rootPath, target.AbsPath, a.ignorer); n != nil {
				a.selected = n
			}
		}
		a.mode = modeTree
	case ev.Key() == tcell.KeyTab, ev.Key() == tcell.KeyDown:
		if len(a.jumpMatches) > 0 {
			a.jumpSelected = tree.MoveSelection(a.jumpSelected, 1, len(a.jumpMatches))
		}
	case ev.Key() == tcell.KeyBacktab, ev.Key() == tcell.KeyUp:
		if len(a.jumpMatches) > 0 {
			a.jumpSelected = tree.MoveSelection(a.jumpSelected, -1, len(a.jumpMatches))
		}
	case ev.Key() == tcell.KeyBackspace, ev.Key() == tcell.KeyBackspace2:
		if len(a.jumpQuery) > 0 {
			r := []rune(a.jumpQuery)
			a.jumpQuery = string(r[:len(r)-1])
		}
		a.jumpSelected = 0
		a.recomputeJumpMatches()
	case ev.Rune() != 0 && ev.Key() == tcell.KeyRune:
		a.jumpQuery += string(ev.Rune())
		a.jumpSelected = 0
		a.recomputeJumpMatches()
	}
}

func (a *App) openPreview(n *tree.Node) {
	a.previewNode = n
	lines := preview.ReadLines(n.Path, previewByteCap)
	segs := preview.Highlight(n.Path, lines)
	if segs != nil {
		segs = preview.AlignSegmentsToLines(segs, len(lines))
	}
	a.previewLines = lines
	a.previewSegs = segs
	a.previewScroll = 0
	a.previewWidth = 0
	a.mode = modePreview
	a.gotoPromptOpen = false
}

func (a *App) handlePreviewKey(ev *tcell.EventKey) {
	if a.gotoPromptOpen {
		switch {
		case ev.Key() == tcell.KeyEscape:
			a.gotoPromptOpen = false
		case ev.Key() == tcell.KeyEnter:
			a.gotoLine(a.gotoInput)
			a.gotoPromptOpen = false
		case ev.Key() == tcell.KeyBackspace, ev.Key() == tcell.KeyBackspace2:
			if len(a.gotoInput) > 0 {
				a.gotoInput = a.gotoInput[:len(a.gotoInput)-1]
			}
		case ev.Rune() >= '0' && ev.Rune() <= '9':
			a.gotoInput += string(ev.Rune())
		}
		return
	}

	viewportHeight := a.previewViewportHeight()
	maxScroll := a.maxPreviewScroll(viewportHeight)

	switch {
	case ev.Key() == tcell.KeyUp:
		a.previewScroll = clamp(a.previewScroll-1, 0, maxScroll)
	case ev.Key() == tcell.KeyDown:
		a.previewScroll = clamp(a.previewScroll+1, 0, maxScroll)
	case ev.Key() == tcell.KeyPgUp:
		a.previewScroll = clamp(a.previewScroll-viewportHeight, 0, maxScroll)
	case ev.Key() == tcell.KeyPgDn:
		a.previewScroll = clamp(a.previewScroll+viewportHeight, 0, maxScroll)
	case ev.Rune() == 'g':
		a.gotoPromptOpen = true
		a.gotoInput = ""
	case ev.Rune() == 'q', ev.Key() == tcell.KeyEscape:
		a.mode = modeTree
	}
}

func (a *App) gotoLine(input string) {
	if input == "" || a.previewFirstRow == nil {
		return
	}
	n := 0
	for _, r := range input {
		n = n*10 + int(r-'0')
	}
	if n < 1 {
		n = 1
	}
	if n > len(a.previewLines) {
		n = len(a.previewLines)
	}
	if row, ok := a.previewFirstRow[n-1]; ok {
		vh := a.previewViewportHeight()
		a.previewScroll = clamp(row, 0, a.maxPreviewScroll(vh))
	}
}

func (a *App) maxPreviewScroll(viewportHeight int) int {
	total := len(a.previewRows)
	max := total - viewportHeight
	if max < 0 {
		max = 0
	}
	return max
}

func (a *App) previewViewportHeight() int {
	_, h := a.screen.Size()
	return h - 1 // header row
}

func clamp(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// computeSplitLayout decides split-vs-popup and, for split view, the
// actual tree-pane and preview-pane widths to render. Per SPEC.md §9,
// the preview pane's own width (not its wrapped content width) is
// capped at previewMaxWidth; once the terminal is wide enough that the
// preview would exceed that cap, the extra width goes to growing the
// tree pane (up to its own max) instead of stretching the preview.
func (a *App) computeSplitLayout(termWidth int) (treeWidth, previewPaneWidth int, split bool) {
	baseTreeWidth := a.treePaneWidth()
	if !layout.ShouldSplitView(termWidth, baseTreeWidth, minPreviewWidth) {
		return baseTreeWidth, 0, false
	}

	natural := termWidth - baseTreeWidth - 1
	if natural <= previewMaxWidth {
		return baseTreeWidth, natural, true
	}

	// The preview pane stays capped at previewMaxWidth regardless of
	// how much width is left over; the leftover goes to growing the
	// tree pane, up to its own max. Any width beyond that simply goes
	// unused (SPEC.md §9) rather than stretching the preview further.
	leftover := natural - previewMaxWidth
	treeWidth = baseTreeWidth + leftover
	if treeWidth > maxTreePaneWidth {
		treeWidth = maxTreePaneWidth
	}
	return treeWidth, previewMaxWidth, true
}

// computedPreviewWidth returns the content width (in columns) available
// to the preview's wrapped text given the current layout decision.
func (a *App) computedPreviewWidth() int {
	w, _ := a.screen.Size()
	_, previewPaneWidth, split := a.computeSplitLayout(w)
	if split {
		return previewPaneWidth - gutterWidth(len(a.previewLines))
	}
	// Popup: terminal width minus fixed margin.
	popupW := w - 4
	if popupW > previewMaxWidth {
		popupW = previewMaxWidth
	}
	return popupW - gutterWidth(len(a.previewLines))
}

func gutterWidth(numLines int) int {
	digits := len(itoa(numLines))
	if digits < 1 {
		digits = 1
	}
	return digits + 2
}

func itoa(n int) string {
	if n <= 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func (a *App) treePaneWidth() int {
	flat := a.root.Flatten()
	lengths := make([]int, len(flat))
	for i, n := range flat {
		lengths[i] = n.Depth*2 + len(n.Name) + 2
	}
	return layout.ComputeTreePaneWidth(lengths, minTreePaneWidth, maxTreePaneWidth)
}

func (a *App) ensurePreviewWrapped() {
	width := a.computedPreviewWidth()
	if width < 1 {
		width = 1
	}
	if width == a.previewWidth && a.previewRows != nil {
		return
	}
	segs := a.previewSegs
	if segs == nil {
		segs = make([][]preview.Segment, len(a.previewLines))
		for i, l := range a.previewLines {
			segs[i] = []preview.Segment{{Text: l, Category: preview.CategoryText}}
		}
	}
	a.previewRows, a.previewFirstRow = preview.BuildDisplayRows(segs, width)
	a.previewWidth = width
}

func (a *App) spinnerVisible() (rune, bool) {
	_, done := a.idx.Snapshot()
	elapsed := a.idx.Elapsed()
	if !spinner.ShouldShow(done, elapsed, spinnerThreshold) {
		return ' ', false
	}
	return spinner.Frame(elapsed, spinnerFPS, spinner.DefaultFrames), true
}
