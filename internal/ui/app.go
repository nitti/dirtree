// Package ui implements the terminal-rendering layer: the tcell draw
// loop, input handling, resize polling, and escape-timeout
// configuration (SPEC.md §6.2, §6.3). Unlike internal/tree,
// internal/openfiles, internal/match, internal/preview,
// internal/layout, and internal/spinner, this package is not expected
// to be unit-tested — verification is manual, in a real terminal.
//
// The primary preview view's own content rendering, scrolling, and
// goto-line (§2.1) is wired alongside the browser (§3.4), quick open
// and jump to file (§4), and open-files-list (§2.3) overlays, plus the
// browser's dual split/popup layout (§5.1).
package ui

import (
	"context"
	"os"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/ignore"
	"github.com/nitti/dirtree/internal/index"
	"github.com/nitti/dirtree/internal/layout"
	"github.com/nitti/dirtree/internal/match"
	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/preview"
	"github.com/nitti/dirtree/internal/search"
	"github.com/nitti/dirtree/internal/spinner"
	"github.com/nitti/dirtree/internal/tree"
	"github.com/nitti/dirtree/internal/watch"
)

// overlay identifies which overlay, if any, is currently active over
// the primary preview view (SPEC.md §5.1).
type overlay int

const (
	overlayNone overlay = iota
	overlayBrowser
	overlayQuickOpen
	overlayJumpToFile
	overlayOpenFiles
	overlaySearch
)

// searchEntryPoint records which screen the content search overlay
// (SPEC.md §9) was opened from, so Escape returns to it. Both entry
// points behave identically once the overlay is open (§9.2) — there is
// no per-entry-point default-action split like quick open/jump to file.
type searchEntryPoint int

const (
	searchFromBrowser searchEntryPoint = iota
	searchFromPreview
)

// searchOutcome is what a background content-search scan (SPEC.md §9.1)
// sends back once it finishes (or is canceled), tagged with the
// generation it was started for so a stale result from a superseded
// query can be discarded rather than clobbering a newer one.
type searchOutcome struct {
	gen     int
	results []search.Match
}

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

	// Browser split/popup layout (SPEC.md §5.1).
	previewMaxWidth     = 120 // preview pane's own width cap in split view
	minPreviewWidth     = 40  // minimum usable preview width for the split-vs-popup threshold
	minBrowserPaneWidth = 20
	maxBrowserPaneWidth = 60
	popupMarginX        = 4
	popupMarginY        = 2
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

	// browser overlay state (SPEC.md §3.4)
	browserSelected *tree.Node
	browserScroll   int
	browserMessage  string // transient inline status/failure message (§2.2)

	// finder state, shared by the quick open and jump-to-file overlays
	// (SPEC.md §4.2, §4.3): which one is active is determined by
	// App.overlay, not stored here, since only one is ever active.
	finderQuery    string
	finderMatches  []index.Entry // nil while the background index isn't done yet, distinct from "genuinely zero matches"
	finderSelected int
	finderScroll   int
	finderMessage  string // transient inline open-failure message, quick open only (§2.2)

	// content search overlay state (SPEC.md §9)
	searchEntry    searchEntryPoint
	searchQuery    string
	searchResults  []search.Match // nil while not yet searched (empty query, waiting on index, or a scan in flight), distinct from "searched, zero matches"
	searchSelected int
	searchScroll   int
	searchMessage  string // transient inline failure message for open-into-list (§2.2)
	searchGen      int
	searchCancel   context.CancelFunc
	searchDone     chan searchOutcome

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
		rootPath:        rootPath,
		root:            root,
		ignorer:         ignorer,
		idx:             idx,
		watcher:         watcher,
		overlay:         overlayBrowser, // SPEC.md §1: browser auto-opens on top of the (empty) primary view at startup
		browserSelected: root,
		files:           openfiles.New(),
		searchDone:      make(chan searchOutcome, 8),
	}
	a.syncWatches()
	return a
}

// syncWatches adds an fs watch for every directory in the tree that has
// been loaded at least once, so RefreshTree's re-listing target set
// matches what's actually being watched. Idempotent and cheap to call
// after any action that might have loaded a new directory (expand,
// jump-to-file reveal).
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
// state, re-anchor browser selection if the previously-selected node
// was deleted, kick off a background index rebuild so quick open and
// jump to file eventually reflect the change too, and pick up watches
// on any newly-loaded directories.
func (a *App) handleFSChange() {
	tree.RefreshTree(a.root, a.rootPath, a.ignorer)
	a.browserSelected = tree.NearestSurviving(a.browserSelected)
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
			if a.isFinderOverlay() {
				a.recomputeFinderMatches()
			}
			a.draw()
		case out := <-a.searchDone:
			if out.gen == a.searchGen {
				a.searchResults = out.results
				a.searchCancel = nil
				if a.searchSelected >= len(a.searchResults) {
					a.searchSelected = max(len(a.searchResults)-1, 0)
				}
			}
			a.draw()
		case <-ticker.C:
			// Also catches the background index (re)build finishing
			// while quick open or jump to file is open with an
			// unchanged query, so matches don't go stale until the
			// next keystroke.
			if a.isFinderOverlay() {
				a.recomputeFinderMatches()
			}
			// Same idea for content search (SPEC.md §9.1): a query typed
			// before the index finished is held pending (searchResults
			// stays nil) until it's done, then run once here rather than
			// on every tick.
			if a.overlay == overlaySearch && a.searchQuery != "" && a.searchResults == nil && a.searchCancel == nil {
				if _, done := a.idx.Snapshot(); done {
					a.recomputeSearch()
				}
			}
			a.draw()
		}
	}
	return nil
}

func (a *App) handleKey(ev *tcell.EventKey) {
	switch a.overlay {
	case overlayBrowser:
		a.handleBrowserKey(ev)
	case overlayQuickOpen:
		a.handleQuickOpenKey(ev)
	case overlayJumpToFile:
		a.handleJumpToFileKey(ev)
	case overlayOpenFiles:
		a.handleOpenFilesKey(ev)
	case overlaySearch:
		a.handleSearchKey(ev)
	case overlayNone:
		a.handlePreviewKey(ev)
	}
}

// isFinderOverlay reports whether the currently-active overlay is one
// of the two that share the background index and matcher (SPEC.md
// §4.2, §4.3): quick open or jump to file.
func (a *App) isFinderOverlay() bool {
	return a.overlay == overlayQuickOpen || a.overlay == overlayJumpToFile
}

// handlePreviewKey handles input at the primary preview view when no
// overlay is active: reaching the browser, quick open, and open-files
// overlays, preview scrolling/goto-line, and quitting (SPEC.md §7).
// `B` and `O` are toggles on their own overlays (SPEC.md §5.1), but
// since this handler only runs when no overlay is active, pressing
// them here always opens rather than closes.
func (a *App) handlePreviewKey(ev *tcell.EventKey) {
	if a.gotoPromptOpen {
		a.handleGotoPromptKey(ev)
		return
	}

	switch {
	case ev.Rune() == 'B':
		a.overlay = overlayBrowser
	case ev.Key() == tcell.KeyTab:
		a.overlay = overlayOpenFiles
		a.openFilesSelected = max(a.files.Displayed, 0)
	case ev.Rune() == 'O':
		a.openQuickOpen()
	case ev.Rune() == 's':
		a.openSearch(searchFromPreview)
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
// narrower width, while the browser's split-view overlay is active —
// drawPreview computes that width itself from the layout it's given,
// independently of this helper).
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

// handleBrowserKey implements the browser overlay's navigation and open
// actions (SPEC.md §3.4). `B` is a toggle (SPEC.md §5.1): it closes the
// browser just like Escape does, since `B` is also the key that opens
// it from the primary preview view.
func (a *App) handleBrowserKey(ev *tcell.EventKey) {
	flat := a.root.Flatten()
	idx := indexOf(flat, a.browserSelected)

	switch {
	case ev.Key() == tcell.KeyUp:
		a.browserMessage = ""
		a.browserSelected = flat[tree.MoveSelection(idx, -1, len(flat))]
	case ev.Key() == tcell.KeyDown:
		a.browserMessage = ""
		a.browserSelected = flat[tree.MoveSelection(idx, 1, len(flat))]
	case ev.Key() == tcell.KeyRight:
		a.browserMessage = ""
		a.browserSelected = a.browserSelected.MoveRight(a.rootPath, a.ignorer)
		a.syncWatches()
	case ev.Key() == tcell.KeyLeft:
		a.browserMessage = ""
		a.browserSelected = a.browserSelected.MoveLeft()
	case ev.Rune() == ' ':
		a.browserOpen(false)
	case ev.Rune() == 'a':
		a.browserOpen(true)
	case ev.Rune() == '/':
		a.openJumpToFile()
	case ev.Rune() == 's':
		a.openSearch(searchFromBrowser)
	case ev.Rune() == 'B', ev.Key() == tcell.KeyEscape:
		a.browserMessage = ""
		a.overlay = overlayNone
	}
}

// browserOpen implements Space (keepOpen=false) and `a` (keepOpen=true)
// from the browser, per SPEC.md §3.4 and §2.2's open-failure signaling:
// a no-op on a directory; on a file, an "opened" result displays it in
// the primary preview view, closing the browser unless keepOpen; a
// "failed" result always leaves the browser open with the message
// shown inline, regardless of keepOpen.
func (a *App) browserOpen(keepOpen bool) {
	if a.browserSelected.IsDir {
		return
	}
	res := a.files.Open(a.browserSelected.Path, previewByteCap)
	if res.Outcome != openfiles.Opened {
		a.browserMessage = res.Message
		return
	}
	a.browserMessage = ""
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
				// which in turn auto-opens the browser exactly as it
				// does on startup (§1).
				a.overlay = overlayBrowser
			}
		}
	}
}

// openQuickOpen opens the quick open overlay from the primary preview
// view (SPEC.md §4.2). Per SPEC.md §5.2, opening it while indexing is
// already done means the user has directly seen indexing is ready, so
// it short-circuits the badge's minimum-display-duration floor.
func (a *App) openQuickOpen() {
	a.overlay = overlayQuickOpen
	a.openFinder()
}

// openJumpToFile opens the jump-to-file overlay from within the
// browser (SPEC.md §4.3), replacing the browser view. Same
// minimum-display-duration short-circuit as openQuickOpen.
func (a *App) openJumpToFile() {
	a.overlay = overlayJumpToFile
	a.openFinder()
}

// openFinder resets the shared quick-open/jump-to-file state and
// recomputes matches; the caller has already set a.overlay to whichever
// of the two is being opened.
func (a *App) openFinder() {
	a.finderQuery = ""
	a.finderSelected = 0
	a.finderMessage = ""
	a.recomputeFinderMatches()
	if _, done := a.idx.Snapshot(); done {
		a.badgeSkip.NoteIndexAlreadyDone(true, a.idx.Elapsed())
	}
}

// recomputeFinderMatches rebuilds finderMatches from the current query
// against the background index (SPEC.md §4.1), shared by quick open
// and jump to file. While the index hasn't finished building, matches
// are nil/unavailable rather than an empty "no matches" result (SPEC.md
// §5.2).
func (a *App) recomputeFinderMatches() {
	a.finderScroll = 0
	entries, done := a.idx.Snapshot()
	if !done {
		a.finderMatches = nil
		return
	}
	matches := make([]index.Entry, 0, len(entries))
	for _, e := range entries {
		if match.Matches(a.finderQuery, e.RelPath) {
			matches = append(matches, e)
		}
	}
	a.finderMatches = matches
}

// handleFinderTypingKey handles the navigation/query-editing keys
// shared by quick open and jump to file (SPEC.md §4.2, §4.3), reporting
// whether it consumed the event; the caller handles Escape and Enter
// itself since those differ per overlay.
func (a *App) handleFinderTypingKey(ev *tcell.EventKey) bool {
	switch {
	case ev.Key() == tcell.KeyTab, ev.Key() == tcell.KeyDown:
		if len(a.finderMatches) > 0 {
			a.finderSelected = tree.MoveSelection(a.finderSelected, 1, len(a.finderMatches))
		}
	case ev.Key() == tcell.KeyBacktab, ev.Key() == tcell.KeyUp:
		if len(a.finderMatches) > 0 {
			a.finderSelected = tree.MoveSelection(a.finderSelected, -1, len(a.finderMatches))
		}
	case ev.Key() == tcell.KeyBackspace, ev.Key() == tcell.KeyBackspace2:
		if len(a.finderQuery) > 0 {
			r := []rune(a.finderQuery)
			a.finderQuery = string(r[:len(r)-1])
		}
		a.finderSelected = 0
		a.recomputeFinderMatches()
	case ev.Rune() != 0 && ev.Key() == tcell.KeyRune:
		a.finderQuery += string(ev.Rune())
		a.finderSelected = 0
		a.recomputeFinderMatches()
	default:
		return false
	}
	return true
}

// handleQuickOpenKey implements quick open's input handling (SPEC.md
// §4.2): a single action, opening the selected match into the
// open-files list. `O` is a toggle, closing the overlay the same as
// Escape.
func (a *App) handleQuickOpenKey(ev *tcell.EventKey) {
	switch {
	case ev.Rune() == 'O', ev.Key() == tcell.KeyEscape:
		a.overlay = overlayNone
	case ev.Key() == tcell.KeyEnter:
		a.performOpenIntoList()
	default:
		a.handleFinderTypingKey(ev)
	}
}

// handleJumpToFileKey implements jump to file's input handling (SPEC.md
// §4.3): a single action, revealing the selected match in the browser.
// Escape always returns to the browser, since that's jump to file's
// only entry point.
func (a *App) handleJumpToFileKey(ev *tcell.EventKey) {
	switch {
	case ev.Key() == tcell.KeyEscape:
		a.overlay = overlayBrowser
	case ev.Key() == tcell.KeyEnter:
		a.performRevealInBrowser()
	default:
		a.handleFinderTypingKey(ev)
	}
}

// performRevealInBrowser implements jump to file's Enter action (SPEC.md
// §4.3): expanding every ancestor down to the selected match and
// selecting it in the browser, which is left open. Resolution failure
// (the path no longer exists) exits the overlay without changing
// browser selection.
func (a *App) performRevealInBrowser() {
	if len(a.finderMatches) == 0 {
		return
	}
	target := a.finderMatches[a.finderSelected]
	if n := tree.RevealPath(a.root, a.rootPath, target.AbsPath, a.ignorer); n != nil {
		a.browserSelected = n
		a.syncWatches()
	}
	a.overlay = overlayBrowser
}

// performOpenIntoList implements quick open's Enter action (SPEC.md
// §4.2): open the selected match per §2.2's open semantics. An opened
// result closes the overlay, landing on the primary preview view; a
// failed result leaves quick open open with the message shown inline
// instead of exiting (§2.2's open-failure signaling).
func (a *App) performOpenIntoList() {
	if len(a.finderMatches) == 0 {
		return
	}
	target := a.finderMatches[a.finderSelected]
	res := a.files.Open(target.AbsPath, previewByteCap)
	if res.Outcome != openfiles.Opened {
		a.finderMessage = res.Message
		return
	}
	a.overlay = overlayNone
}

// openSearch opens the content search overlay from the given entry
// point (SPEC.md §9.1). Both entry points behave identically once open
// (§9.2), so this only needs to remember where to return on Escape.
func (a *App) openSearch(entry searchEntryPoint) {
	a.overlay = overlaySearch
	a.searchEntry = entry
	a.searchQuery = ""
	a.searchSelected = 0
	a.searchScroll = 0
	a.searchMessage = ""
	a.recomputeSearch()
}

// recomputeSearch (re)starts the background content scan for the
// current query (SPEC.md §9.1): any scan still running for the previous
// query is canceled first, so only the most recently typed query's
// result is ever applied. An empty query performs no scan at all. If
// the background index hasn't finished building yet, the scan is
// deferred — Run's ticker case retries once it has — rather than
// scanning a partial candidate set.
func (a *App) recomputeSearch() {
	a.cancelSearch()
	a.searchSelected = 0
	a.searchScroll = 0
	a.searchMessage = ""

	if a.searchQuery == "" {
		a.searchResults = nil
		return
	}

	entries, done := a.idx.Snapshot()
	if !done {
		a.searchResults = nil
		return
	}

	a.searchResults = nil
	candidates := make([]search.Candidate, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir {
			candidates = append(candidates, search.Candidate{AbsPath: e.AbsPath, RelPath: e.RelPath})
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.searchCancel = cancel
	a.searchGen++
	gen, query := a.searchGen, a.searchQuery
	go func() {
		results := search.Run(ctx, query, candidates, previewByteCap)
		a.searchDone <- searchOutcome{gen: gen, results: results}
	}()
}

// cancelSearch stops any in-flight background scan without applying its
// (now-stale) result, so leaving the overlay or superseding the query
// doesn't leave wasted work running against a tree that could be large.
func (a *App) cancelSearch() {
	if a.searchCancel != nil {
		a.searchCancel()
		a.searchCancel = nil
	}
}

// handleSearchKey implements the content search overlay's input
// handling (SPEC.md §9.2). Unlike quick open/jump to file, space is
// never an action key — it always types a literal space into the query,
// since content search queries are plain text rather than path
// fragments.
func (a *App) handleSearchKey(ev *tcell.EventKey) {
	switch {
	case ev.Key() == tcell.KeyEscape:
		a.cancelSearch()
		a.overlay = a.searchReturnOverlay()
	case ev.Key() == tcell.KeyEnter:
		a.performSearchOpen()
	case ev.Key() == tcell.KeyTab, ev.Key() == tcell.KeyDown:
		if len(a.searchResults) > 0 {
			a.searchSelected = tree.MoveSelection(a.searchSelected, 1, len(a.searchResults))
		}
	case ev.Key() == tcell.KeyBacktab, ev.Key() == tcell.KeyUp:
		if len(a.searchResults) > 0 {
			a.searchSelected = tree.MoveSelection(a.searchSelected, -1, len(a.searchResults))
		}
	case ev.Key() == tcell.KeyBackspace, ev.Key() == tcell.KeyBackspace2:
		if len(a.searchQuery) > 0 {
			r := []rune(a.searchQuery)
			a.searchQuery = string(r[:len(r)-1])
		}
		a.recomputeSearch()
	case ev.Rune() != 0 && ev.Key() == tcell.KeyRune:
		a.searchQuery += string(ev.Rune())
		a.recomputeSearch()
	}
}

// searchReturnOverlay is which overlay Escape (or a successful open)
// lands on: the browser if the overlay was entered from it, otherwise
// back to the primary preview view.
func (a *App) searchReturnOverlay() overlay {
	if a.searchEntry == searchFromBrowser {
		return overlayBrowser
	}
	return overlayNone
}

// performSearchOpen implements Enter (SPEC.md §9.2): open the selected
// match into the open-files list per §2.2's open semantics. An "opened"
// result closes the overlay, landing on the primary preview view
// regardless of entry point; a "failed" result (e.g. the file changed
// or was removed between scanning and opening) leaves the overlay open
// with the message shown inline instead, per §2.2's open-failure
// signaling.
func (a *App) performSearchOpen() {
	if len(a.searchResults) == 0 {
		return
	}
	target := a.searchResults[a.searchSelected]
	res := a.files.Open(target.AbsPath, previewByteCap)
	if res.Outcome != openfiles.Opened {
		a.searchMessage = res.Message
		return
	}
	a.searchMessage = ""
	a.cancelSearch()
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

// browserPaneWidth returns the browser pane's width for split view
// (SPEC.md §5.1): wide enough to fit the longest currently-visible
// row's rendered label (indentation + expand marker + name), clamped
// to [minBrowserPaneWidth, maxBrowserPaneWidth].
func (a *App) browserPaneWidth() int {
	flat := a.root.Flatten()
	lengths := make([]int, len(flat))
	for i, n := range flat {
		lengths[i] = n.Depth*2 + 2 + len(n.Name) // indent + marker + name, matching browserLabel
	}
	return layout.ComputeBrowserPaneWidth(lengths, minBrowserPaneWidth, maxBrowserPaneWidth)
}

// computeSplitLayout decides split-vs-popup and, for split view, the
// browser-pane and preview-pane widths to render, per SPEC.md §5.1: the
// preview pane's own width is capped at previewMaxWidth; once the
// terminal is wide enough that the preview would exceed that cap, the
// extra width grows the browser pane (up to its own max) instead of
// stretching the preview further.
func (a *App) computeSplitLayout(termWidth int) (browserWidth, previewPaneWidth int, split bool) {
	baseBrowserWidth := a.browserPaneWidth()
	if !layout.ShouldSplitView(termWidth, baseBrowserWidth, minPreviewWidth) {
		return baseBrowserWidth, 0, false
	}

	natural := termWidth - baseBrowserWidth - 1
	if natural <= previewMaxWidth {
		return baseBrowserWidth, natural, true
	}

	leftover := natural - previewMaxWidth
	browserWidth = min(baseBrowserWidth+leftover, maxBrowserPaneWidth)
	return browserWidth, previewMaxWidth, true
}
