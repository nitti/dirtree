// Package ui implements the terminal-rendering layer: the tcell draw
// loop, input handling, resize polling, and escape-timeout
// configuration (SPEC.md §6.2, §6.3). Unlike internal/tree,
// internal/openfiles, internal/match, internal/preview,
// internal/layout, and internal/spinner, this package is not expected
// to be unit-tested — verification is manual, in a real terminal.
//
// The interactive UI is being rebuilt in stages against SPEC.md's
// open-files-primary-view redesign (see README.md's Status section for
// the full plan). This stage adds the primary preview view's own
// content rendering, scrolling, and goto-line (§2.1); the tree
// explorer (§3.4), jump/fuzzy-picker (§4), and open-files-list (§2.3)
// overlays already worked from earlier stages. The tree explorer's
// dual split/popup layout (§5.1) is not wired yet and lands in stage 6.
package ui

import (
	"os"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/ignore"
	"github.com/nitti/dirtree/internal/index"
	"github.com/nitti/dirtree/internal/layout"
	"github.com/nitti/dirtree/internal/match"
	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/preview"
	"github.com/nitti/dirtree/internal/spinner"
	"github.com/nitti/dirtree/internal/tree"
	"github.com/nitti/dirtree/internal/watch"
)

// overlay identifies which overlay, if any, is currently active over
// the primary preview view (SPEC.md §5.1). overlayOpenFiles is not
// reachable yet — it lands in stage 4 — but is declared now so this
// stage's overlay-dispatch code doesn't need reshaping later.
type overlay int

const (
	overlayNone overlay = iota
	overlayTree
	overlayJump
	overlayOpenFiles
)

// jumpEntryPoint records which screen the jump/fuzzy-picker overlay
// was opened from, since that determines which action (reveal-in-tree
// vs. open-into-list) Enter and Space each perform (SPEC.md §4.2).
type jumpEntryPoint int

const (
	jumpFromTree jumpEntryPoint = iota
	jumpFromPreview
)

const (
	resizePollInterval        = 100 * time.Millisecond
	spinnerThreshold          = 250 * time.Millisecond
	spinnerMinDisplayDuration = 1 * time.Second
	spinnerFPS                = 10.0
	completionDisplayDuration = 2 * time.Second
	completionFadeDuration    = 400 * time.Millisecond
	completionMessage         = "indexing complete"
	watchDebounce             = 300 * time.Millisecond
	previewByteCap            = preview.DefaultByteCap

	// Tree explorer split/popup layout (SPEC.md §5.1).
	previewMaxWidth  = 120 // preview pane's own width cap in split view
	minPreviewWidth  = 40  // minimum usable preview width for the split-vs-popup threshold
	minTreePaneWidth = 20
	maxTreePaneWidth = 60
	popupMarginX     = 4
	popupMarginY     = 2
)

// App holds all interactive state for a running session.
type App struct {
	screen   tcell.Screen
	rootPath string
	root     *tree.Node
	ignorer  *ignore.Multi
	idx      *index.Index
	watcher  *watch.Watcher

	overlay overlay

	// tree explorer overlay state (SPEC.md §3.4)
	treeSelected *tree.Node
	treeScroll   int
	treeMessage  string // transient inline status/failure message (§2.2)

	// jump/fuzzy-picker overlay state (SPEC.md §4.2)
	jumpEntry    jumpEntryPoint
	jumpQuery    string
	jumpMatches  []index.Entry // nil while the background index isn't done yet, distinct from "genuinely zero matches"
	jumpSelected int
	jumpScroll   int
	jumpMessage  string // transient inline failure message for open-into-list (§2.2)

	// files is the open-files list (SPEC.md §2.2, §2.3) the primary
	// preview view and both overlays' open actions operate on.
	files *openfiles.List

	// open-files-list overlay state (SPEC.md §2.3)
	openFilesSelected int
	openFilesScroll   int

	// primary preview view's goto-line prompt state (SPEC.md §2.1);
	// scroll and the wrapped-row cache live per-entry on
	// openfiles.Entry instead, since they're tracked per open file.
	gotoPromptOpen bool
	gotoInput      string

	badgeSkip spinner.MinDurationSkip

	quit bool
}

// New builds a new App rooted at rootPath. It does not touch the
// terminal yet; call Run to start the interactive session.
func New(rootPath string) *App {
	ignorer := ignore.LoadAll(rootPath)
	root := tree.NewRoot(rootPath, ignorer)
	idx := index.Start(rootPath, ignorer)

	// Live refresh (SPEC.md §6.1) is best-effort: if the OS notification
	// facility can't be initialized (e.g. inotify watch-limit exhausted),
	// the app still runs, it just won't auto-refresh.
	watcher, _ := watch.New(watchDebounce)

	a := &App{
		rootPath:     rootPath,
		root:         root,
		ignorer:      ignorer,
		idx:          idx,
		watcher:      watcher,
		overlay:      overlayTree, // SPEC.md §1: explorer auto-opens on top of the (empty) primary view at startup
		treeSelected: root,
		files:        openfiles.New(),
	}
	a.syncWatches()
	return a
}

// syncWatches adds an fs watch for every directory in the tree that has
// been loaded at least once, so RefreshTree's re-listing target set
// matches what's actually being watched. Idempotent and cheap to call
// after any action that might have loaded a new directory (expand,
// jump-to reveal).
func (a *App) syncWatches() {
	if a.watcher == nil {
		return
	}
	var walk func(n *tree.Node)
	walk = func(n *tree.Node) {
		if !n.IsDir || !n.Loaded() {
			return
		}
		a.watcher.Add(n.Path)
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(a.root)
}

// handleFSChange reacts to a debounced filesystem-change signal from
// the watcher (SPEC.md §6.1): re-list every already-loaded directory,
// merging by path so unaffected nodes keep their identity/expand
// state, re-anchor tree selection if the previously-selected node was
// deleted, kick off a background index rebuild so jump mode eventually
// reflects the change too, and pick up watches on any newly-loaded
// directories.
func (a *App) handleFSChange() {
	tree.RefreshTree(a.root, a.rootPath, a.ignorer)
	a.treeSelected = tree.NearestSurviving(a.treeSelected)
	a.idx.Rebuild(a.rootPath, a.ignorer)
	a.badgeSkip.Reset()
	a.syncWatches()
}

// Run configures the terminal and drives the main loop until the user
// quits. Per SPEC.md §6.3, the escape-sequence timeout is configured
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

	if a.watcher != nil {
		defer func() { _ = a.watcher.Close() }()
	}

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

	// SPEC.md §6.2: the main input-wait must not block indefinitely, so
	// layout can be recomputed and redrawn on a short periodic cadence
	// even without an explicit resize signal (the primary target,
	// terminal multiplexers, don't reliably deliver one).
	ticker := time.NewTicker(resizePollInterval)
	defer ticker.Stop()

	var watchEvents <-chan struct{}
	if a.watcher != nil {
		watchEvents = a.watcher.Events
	}

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
		case <-watchEvents:
			a.handleFSChange()
			if a.overlay == overlayJump {
				a.recomputeJumpMatches()
			}
			a.draw()
		case <-ticker.C:
			// Also catches the background index (re)build finishing
			// while the jump overlay is open with an unchanged query,
			// so matches don't go stale until the next keystroke.
			if a.overlay == overlayJump {
				a.recomputeJumpMatches()
			}
			a.draw()
		}
	}
	return nil
}

func (a *App) handleKey(ev *tcell.EventKey) {
	switch a.overlay {
	case overlayTree:
		a.handleTreeKey(ev)
	case overlayJump:
		a.handleJumpKey(ev)
	case overlayOpenFiles:
		a.handleOpenFilesKey(ev)
	case overlayNone:
		a.handlePreviewKey(ev)
	}
}

// handlePreviewKey handles input at the primary preview view when no
// overlay is active. Full preview interaction (scrolling, goto-line,
// per-entry content — SPEC.md §2.1) lands in stage 5; for now this only
// wires the keys needed to reach/leave the tree explorer and jump
// overlays and to quit (SPEC.md §7).
func (a *App) handlePreviewKey(ev *tcell.EventKey) {
	if a.gotoPromptOpen {
		a.handleGotoPromptKey(ev)
		return
	}

	switch {
	case ev.Rune() == 'e':
		a.overlay = overlayTree
	case ev.Key() == tcell.KeyTab:
		a.overlay = overlayOpenFiles
		a.openFilesSelected = max(a.files.Displayed, 0)
	case ev.Rune() == '/':
		a.openJump(jumpFromPreview)
	case ev.Rune() == 'q', ev.Key() == tcell.KeyEscape:
		a.quit = true
	case ev.Key() == tcell.KeyUp:
		a.scrollPreview(-1)
	case ev.Key() == tcell.KeyDown:
		a.scrollPreview(1)
	case ev.Key() == tcell.KeyPgUp:
		a.scrollPreview(-a.previewViewportHeight())
	case ev.Key() == tcell.KeyPgDn:
		a.scrollPreview(a.previewViewportHeight())
	case ev.Rune() == 'g':
		if a.files.DisplayedEntry() != nil {
			a.gotoPromptOpen = true
			a.gotoInput = ""
		}
	}
}

// handleGotoPromptKey handles input while the goto-line prompt is open
// (SPEC.md §2.1): only digits and backspace are accepted, Enter jumps
// to the entered line, Escape cancels without changing scroll.
func (a *App) handleGotoPromptKey(ev *tcell.EventKey) {
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
}

// scrollPreview scrolls the currently-displayed entry by delta display
// rows (SPEC.md §2.1), clamped so it never goes negative or past the
// point where the last display row would leave the viewport. A no-op
// at the empty state (no displayed entry).
func (a *App) scrollPreview(delta int) {
	e := a.files.DisplayedEntry()
	if e == nil {
		return
	}
	a.ensurePreviewWrapped(e, a.computedPreviewWidth())
	e.Scroll = clamp(e.Scroll+delta, 0, a.maxPreviewScroll(e, a.previewViewportHeight()))
}

// gotoLine jumps the currently-displayed entry's scroll to the source
// line's first display row (SPEC.md §2.1), clamped to [1, total source
// lines]. A no-op if input is empty or there's no displayed entry.
func (a *App) gotoLine(input string) {
	e := a.files.DisplayedEntry()
	if input == "" || e == nil {
		return
	}
	a.ensurePreviewWrapped(e, a.computedPreviewWidth())
	n := 0
	for _, r := range input {
		n = n*10 + int(r-'0')
	}
	n = clamp(n, 1, len(e.Lines))
	if row, ok := e.FirstRow[n-1]; ok {
		e.Scroll = clamp(row, 0, a.maxPreviewScroll(e, a.previewViewportHeight()))
	}
}

func (a *App) maxPreviewScroll(e *openfiles.Entry, viewportHeight int) int {
	return max(len(e.Rows)-viewportHeight, 0)
}

func (a *App) previewViewportHeight() int {
	_, h := a.screen.Size()
	height := h - 1 // header row
	if a.gotoPromptOpen {
		height--
	}
	return height
}

// computedPreviewWidth returns the content width (in columns) available
// to the preview's wrapped text at the primary preview view when no
// overlay is active (full terminal width). This is only used by the
// scroll/goto-line key handlers, which are only reachable in that
// context (SPEC.md §5.1: the preview pane is read-only, with a
// narrower width, while the tree explorer's split-view overlay is
// active — drawPreview computes that width itself from the layout it's
// given, independently of this helper).
func (a *App) computedPreviewWidth() int {
	w, _ := a.screen.Size()
	e := a.files.DisplayedEntry()
	if e == nil {
		return w
	}
	return max(w-gutterWidth(len(e.Lines)), 1)
}

// ensurePreviewWrapped recomputes e's wrapped display rows if width has
// changed since they were last computed (SPEC.md §2.1: "wrapping must
// be recomputed whenever the available width changes"), caching the
// result on the entry so it's not redone every frame.
func (a *App) ensurePreviewWrapped(e *openfiles.Entry, width int) {
	if e.RowsWidth == width && e.Rows != nil {
		return
	}
	e.Rows, e.FirstRow = preview.BuildDisplayRows(e.Segs, width)
	e.RowsWidth = width
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// handleTreeKey implements the tree explorer overlay's navigation and
// open actions (SPEC.md §3.4).
func (a *App) handleTreeKey(ev *tcell.EventKey) {
	flat := a.root.Flatten()
	idx := indexOf(flat, a.treeSelected)

	switch {
	case ev.Key() == tcell.KeyUp:
		a.treeMessage = ""
		a.treeSelected = flat[tree.MoveSelection(idx, -1, len(flat))]
	case ev.Key() == tcell.KeyDown:
		a.treeMessage = ""
		a.treeSelected = flat[tree.MoveSelection(idx, 1, len(flat))]
	case ev.Key() == tcell.KeyRight:
		a.treeMessage = ""
		a.treeSelected = a.treeSelected.MoveRight(a.rootPath, a.ignorer)
		a.syncWatches()
	case ev.Key() == tcell.KeyLeft:
		a.treeMessage = ""
		a.treeSelected = a.treeSelected.MoveLeft()
	case ev.Rune() == ' ':
		a.treeOpen(false)
	case ev.Rune() == 'a':
		a.treeOpen(true)
	case ev.Rune() == '/':
		a.openJump(jumpFromTree)
	case ev.Key() == tcell.KeyEscape:
		a.treeMessage = ""
		a.overlay = overlayNone
	}
}

// treeOpen implements Space (keepOpen=false) and `a` (keepOpen=true)
// from the tree explorer, per SPEC.md §3.4 and §2.2's open-failure
// signaling: a no-op on a directory; on a file, an "opened" result
// displays it in the primary preview view, closing the explorer unless
// keepOpen; a "failed" result always leaves the explorer open with the
// message shown inline, regardless of keepOpen.
func (a *App) treeOpen(keepOpen bool) {
	if a.treeSelected.IsDir {
		return
	}
	res := a.files.Open(a.treeSelected.Path, previewByteCap)
	if res.Outcome != openfiles.Opened {
		a.treeMessage = res.Message
		return
	}
	a.treeMessage = ""
	if !keepOpen {
		a.overlay = overlayNone
	}
}

// handleOpenFilesKey implements the open-files-list overlay's input
// handling (SPEC.md §2.3). Shift-Up/Shift-Down (reorder) are checked
// ahead of plain Up/Down (navigate) since tcell reports them as the
// same Key with ModShift set, not a distinct key.
func (a *App) handleOpenFilesKey(ev *tcell.EventKey) {
	n := len(a.files.Entries)
	shift := ev.Modifiers()&tcell.ModShift != 0

	switch {
	case ev.Key() == tcell.KeyEscape:
		a.overlay = overlayNone
	case ev.Key() == tcell.KeyUp && shift:
		if n > 0 {
			a.openFilesSelected = a.files.MoveUp(a.openFilesSelected)
		}
	case ev.Key() == tcell.KeyDown && shift:
		if n > 0 {
			a.openFilesSelected = a.files.MoveDown(a.openFilesSelected)
		}
	case ev.Key() == tcell.KeyUp:
		if n > 0 {
			a.openFilesSelected = tree.MoveSelection(a.openFilesSelected, -1, n)
		}
	case ev.Key() == tcell.KeyDown:
		if n > 0 {
			a.openFilesSelected = tree.MoveSelection(a.openFilesSelected, 1, n)
		}
	case ev.Key() == tcell.KeyEnter:
		if n > 0 {
			a.files.Display(a.openFilesSelected)
			a.overlay = overlayNone
		}
	case ev.Rune() == 'x':
		if n > 0 {
			a.openFilesSelected = a.files.Remove(a.openFilesSelected)
			if len(a.files.Entries) == 0 {
				// SPEC.md §2.3: emptying the list auto-closes the
				// overlay to the primary preview view's empty state,
				// which in turn auto-opens the tree explorer exactly
				// as it does on startup (§1).
				a.overlay = overlayTree
			}
		}
	}
}

// openJump opens the jump/fuzzy-picker overlay from the given entry
// point (SPEC.md §4.2): tree explorer via `/` (default action
// reveal-in-tree) or primary preview view via `/` (default action
// open-into-list). Per SPEC.md §5.2, opening the overlay while
// indexing is already done means the user has directly seen indexing
// is ready, so it short-circuits the badge's minimum-display-duration
// floor.
func (a *App) openJump(entry jumpEntryPoint) {
	a.overlay = overlayJump
	a.jumpEntry = entry
	a.jumpQuery = ""
	a.jumpSelected = 0
	a.jumpMessage = ""
	a.recomputeJumpMatches()
	if _, done := a.idx.Snapshot(); done {
		a.badgeSkip.NoteIndexAlreadyDone(true, a.idx.Elapsed())
	}
}

// recomputeJumpMatches rebuilds jumpMatches from the current query
// against the background index (SPEC.md §4.2). While the index hasn't
// finished building, matches are nil/unavailable rather than an empty
// "no matches" result (SPEC.md §5.2).
func (a *App) recomputeJumpMatches() {
	a.jumpScroll = 0
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

// handleJumpKey implements the jump/fuzzy-picker overlay's input
// handling (SPEC.md §4.2).
func (a *App) handleJumpKey(ev *tcell.EventKey) {
	switch {
	case ev.Key() == tcell.KeyEscape:
		a.overlay = a.jumpReturnOverlay()
	case ev.Key() == tcell.KeyEnter:
		a.performJumpAction(a.defaultAction())
	case ev.Rune() == ' ':
		a.performJumpAction(otherAction(a.defaultAction()))
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

// jumpAction identifies one of SPEC.md §4.2's two actions.
type jumpAction int

const (
	actionRevealInTree jumpAction = iota
	actionOpenIntoList
)

// defaultAction returns which action Enter performs for the overlay's
// current entry point (SPEC.md §4.2): reveal-in-tree from the tree
// explorer, open-into-list from the primary preview view. Space always
// performs the other one.
func (a *App) defaultAction() jumpAction {
	if a.jumpEntry == jumpFromTree {
		return actionRevealInTree
	}
	return actionOpenIntoList
}

func otherAction(a jumpAction) jumpAction {
	if a == actionRevealInTree {
		return actionOpenIntoList
	}
	return actionRevealInTree
}

// jumpReturnOverlay is which overlay Escape (or a successful
// exit-performing action) lands on: the tree explorer if the picker
// was entered from it, otherwise back to the primary preview view.
func (a *App) jumpReturnOverlay() overlay {
	if a.jumpEntry == jumpFromTree {
		return overlayTree
	}
	return overlayNone
}

// performJumpAction runs the given action on the currently-selected
// match, if any, per SPEC.md §4.2.
func (a *App) performJumpAction(action jumpAction) {
	if len(a.jumpMatches) == 0 {
		return
	}
	target := a.jumpMatches[a.jumpSelected]
	switch action {
	case actionRevealInTree:
		a.revealInTree(target)
	case actionOpenIntoList:
		a.openIntoList(target)
	}
}

// revealInTree implements SPEC.md §4.2's reveal-in-tree action:
// expanding every ancestor down to the match and selecting it in the
// tree explorer, which is left open (opened first if the picker was
// entered from the preview). Resolution failure (the path no longer
// exists) exits the overlay without changing tree selection.
func (a *App) revealInTree(target index.Entry) {
	if n := tree.RevealPath(a.root, a.rootPath, target.AbsPath, a.ignorer); n != nil {
		a.treeSelected = n
		a.syncWatches()
	}
	a.overlay = overlayTree
}

// openIntoList implements SPEC.md §4.2's open-into-list action: open
// the match per §2.2's open semantics. An opened result closes the
// tree explorer overlay if it was open, landing on the primary preview
// view; a failed result leaves the picker open with the message shown
// inline instead of exiting (§2.2's open-failure signaling).
func (a *App) openIntoList(target index.Entry) {
	res := a.files.Open(target.AbsPath, previewByteCap)
	if res.Outcome != openfiles.Opened {
		a.jumpMessage = res.Message
		return
	}
	a.overlay = overlayNone
}

func indexOf(list []*tree.Node, n *tree.Node) int {
	for i, c := range list {
		if c == n {
			return i
		}
	}
	return 0
}

// treePaneWidth returns the tree explorer pane's width for split view
// (SPEC.md §5.1): wide enough to fit the longest currently-visible
// row's rendered label (indentation + expand marker + name), clamped
// to [minTreePaneWidth, maxTreePaneWidth].
func (a *App) treePaneWidth() int {
	flat := a.root.Flatten()
	lengths := make([]int, len(flat))
	for i, n := range flat {
		lengths[i] = n.Depth*2 + 2 + len(n.Name) // indent + marker + name, matching treeLabel
	}
	return layout.ComputeTreePaneWidth(lengths, minTreePaneWidth, maxTreePaneWidth)
}

// computeSplitLayout decides split-vs-popup and, for split view, the
// tree-pane and preview-pane widths to render, per SPEC.md §5.1: the
// preview pane's own width is capped at previewMaxWidth; once the
// terminal is wide enough that the preview would exceed that cap, the
// extra width grows the tree pane (up to its own max) instead of
// stretching the preview further.
func (a *App) computeSplitLayout(termWidth int) (treeWidth, previewPaneWidth int, split bool) {
	baseTreeWidth := a.treePaneWidth()
	if !layout.ShouldSplitView(termWidth, baseTreeWidth, minPreviewWidth) {
		return baseTreeWidth, 0, false
	}

	natural := termWidth - baseTreeWidth - 1
	if natural <= previewMaxWidth {
		return baseTreeWidth, natural, true
	}

	leftover := natural - previewMaxWidth
	treeWidth = min(baseTreeWidth+leftover, maxTreePaneWidth)
	return treeWidth, previewMaxWidth, true
}
