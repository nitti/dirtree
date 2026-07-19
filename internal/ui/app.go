// Package ui implements the terminal-rendering layer: the tcell draw
// loop, input handling, resize polling, and escape-timeout
// configuration (SPEC.md §6.2, §6.3). Unlike internal/tree,
// internal/openfiles, internal/match, internal/preview,
// and internal/spinner, this package is not expected to be
// unit-tested — verification is manual, in a real terminal.
//
// The primary preview view's own content rendering, scrolling, and
// goto-line (§2.1) is wired alongside the browser (§3.4, including its
// jump-to-file typing mode, §4.3), quick open (§4.2), and
// open-files-list (§2.3) overlays, all rendered full-screen (§5.1).
package ui

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/find"
	"github.com/nitti/dirtree/internal/ignore"
	"github.com/nitti/dirtree/internal/index"
	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/preview"
	"github.com/nitti/dirtree/internal/search"
	"github.com/nitti/dirtree/internal/spinner"
	"github.com/nitti/dirtree/internal/tree"
	"github.com/nitti/dirtree/internal/ui/canvas"
	"github.com/nitti/dirtree/internal/ui/views"
	"github.com/nitti/dirtree/internal/watch"
)

// searchOutcome is what a background content-search scan (SPEC.md §9.1)
// sends back once it finishes (or is canceled), tagged with the
// generation it was started for so a stale result from a superseded
// query can be discarded rather than clobbering a newer one.
type searchOutcome struct {
	gen     int
	results []search.FileResult
	err     error // non-nil only for a ModeRegex compile failure (defensive: recomputeSearch already validates synchronously before dispatching)
}

// searchRow is one flattened, displayable row of the content search
// overlay's results (SPEC.md §9.2): either a file row (one per
// FileResult, always present) or a hit row (one per matching line
// within that file, present only while the file is expanded). Hit rows
// are addressed by (file, hit) rather than a flat index into a
// concatenated slice so toggling one file's disclosure state doesn't
// require renumbering anything.
type searchRow struct {
	file  int // index into App.searchResults
	hit   int // index into searchResults[file].Hits; only meaningful when isHit
	isHit bool
}

const (
	resizePollInterval        = 100 * time.Millisecond
	spinnerThreshold          = 250 * time.Millisecond
	spinnerMinDisplayDuration = 1 * time.Second
	spinnerFPS                = 10.0
	// toastDisplayDuration/toastFadeDuration are the "show in full, then
	// fade left-to-right" timing (internal/toast, SPEC.md §5.3) backing
	// the indexing-complete badge message.
	toastDisplayDuration = 2 * time.Second
	toastFadeDuration    = 400 * time.Millisecond
	completionMessage    = "indexing complete"
	// flashDuration is how long the post-open row flash (§2.2/§3.4/§9.2's
	// "on-open confirmation") stays visible before fading back to normal,
	// shared by the browser and content search overlays.
	flashDuration  = 400 * time.Millisecond
	watchDebounce  = 300 * time.Millisecond
	previewByteCap = preview.DefaultByteCap

	// Open-files-list overlay dropdown sizing (SPEC.md §2.3): wide enough
	// to fit its own header row (page counter + keybinding legend) or
	// its longest visible path, whichever is larger, clamped to this
	// range so it still reads as a compact dropdown rather than growing
	// to fill the terminal.
	openFilesMinWidth = 40
	openFilesMaxWidth = 84
)

// App holds all interactive state for a running session.
type App struct {
	rootPath string
	root     *tree.Node
	ignorer  *ignore.Multi
	idx      *index.Index
	watcher  *watch.Watcher

	// shared bundles the state views need to read (and occasionally
	// write) that isn't specific to any one view — see views.Shared's
	// own doc comment for why this is a plain struct, not an interface.
	// Its Canvas field is nil until Run() initializes the real terminal
	// screen.
	shared *views.Shared

	overlay views.Overlay

	// browser overlay state (SPEC.md §3.4)
	browserSelected   *tree.Node
	browserScroll     int
	browserFlashPath  string    // absolute path of the file row most recently opened via browserOpen, for a brief post-open flash (mirrors searchFlashPath below)
	browserFlashStart time.Time // when the flash started; drawn only while time.Since(browserFlashStart) < flashDuration
	// browserErrorFlashes tracks a brief red flash (styleFlashError) for
	// any node whose most recent operation failed — a directory that
	// newly started failing to list during a live-refresh (SPEC.md §6.1),
	// or a file that failed to open (§2.2, e.g. permission denied or
	// binary). Unlike browserFlashPath above, the flash itself is only
	// the attention-grabbing part and always decays; the error text it's
	// drawing attention to (tree.Node.Err, rendered inline by
	// browserLabel for both cases) does not fade with it and stays until
	// overwritten by a later successful operation on that same node.
	// Keyed by path since more than one directory could newly error in
	// the same debounced live-refresh batch.
	browserErrorFlashes map[string]time.Time

	// toastMessage/toastStart drive the generic bottom-right transient
	// notification (internal/toast, SPEC.md §5.3) — currently only used
	// for the open-file live-reload notice (§6.1a), sharing
	// drawCornerBadge's anchor and fade timing with the indexing badge.
	// toastMessage == "" means no toast is active.
	toastMessage string
	toastStart   time.Time
	// toastBoldRanges marks [start, end) rune ranges within toastMessage
	// (e.g. reloaded file names, SPEC.md §6.1a) to render bold, so the
	// scannable part of a toast stands out from its surrounding prose.
	toastBoldRanges [][2]int

	// QuickOpen is the quick open overlay's own state (SPEC.md §4.2).
	QuickOpen views.QuickOpenView

	// jump-to-file typing mode (SPEC.md §4.3): not a separate overlay —
	// App.overlay stays overlayBrowser throughout, and the browser's own
	// row list keeps rendering. jumpPrevSelected/jumpPrevScroll capture
	// the browser's selection/scroll at the moment `/` was pressed, so
	// Escape can restore them exactly.
	jumpActive       bool
	jumpQuery        string
	jumpDisclosed    string       // slash-to-expand path segments committed so far (e.g. "internal/ui/"), shown ahead of jumpQuery so disclosing a directory doesn't read as losing what was typed (SPEC.md §4.3)
	jumpMatches      []*tree.Node // recomputed on every query change from a.jumpScope.Flatten(); rows both files and directories
	jumpSelected     int          // index into jumpMatches
	jumpPrevSelected *tree.Node
	jumpPrevScroll   int
	jumpScope        *tree.Node // matching root for jumpMatches: a.root normally, or a directory drilled into via slash-to-expand (SPEC.md §4.3)

	// content search overlay state (SPEC.md §9)
	searchQuery     string
	searchRegex     bool                // ModeRegex vs. ModeSubstring toggle (ctrl+r)
	searchResults   []search.FileResult // nil while not yet searched (empty query, waiting on index, or a scan in flight), distinct from "searched, zero matches"
	searchError     string              // non-empty only for an invalid regex query (ModeRegex); takes precedence over searchResults' nil/empty distinction
	searchCollapsed map[string]bool     // AbsPath -> collapsed; absent (or false) means expanded, so results start expanded by default
	searchSelected  int                 // index into a.searchRows(), not directly into searchResults
	searchScroll    int
	// searchErrorPath/searchErrorMessage/searchErrorFlashStart mirror
	// finderErrorPath et al. above for content search's open-into-list
	// action (§2.2, §9.2, §5.3): AbsPath of the file row whose open
	// attempt most recently failed (a hit row's failure attributes to
	// its parent file row, the same way the on-open flash already does),
	// the failure message, and when to stop flashing it red. Cleared
	// whenever the query changes.
	searchErrorPath       string
	searchErrorMessage    string
	searchErrorFlashStart time.Time
	searchGen             int
	searchCancel          context.CancelFunc
	searchScanStart       time.Time // when the in-flight scan (searchCancel != nil) started, for the spinner shown once it runs long enough to be noticeable
	searchFlashPath       string    // AbsPath of the file row most recently opened via performSearchOpen, for a brief post-open flash
	searchFlashStart      time.Time // when the flash started; the flash is drawn only while time.Since(searchFlashStart) < flashDuration
	searchDone            chan searchOutcome

	// files is the open-files list (SPEC.md §2.2, §2.3) the primary
	// preview view and both overlays' open actions operate on.
	files *openfiles.List

	// open-files-list overlay state (SPEC.md §2.3): openFilesSelected is
	// an index into files.Entries. Which page it falls on is derived
	// (openfiles.Page), not stored, per that package's doc comment.
	openFilesSelected int

	// primary preview view's goto-line prompt state (SPEC.md §2.1);
	// scroll and the wrapped-row cache live per-entry on
	// openfiles.Entry instead, since they're tracked per open file.
	gotoPromptOpen bool
	gotoInput      string

	// primary preview view's in-file find prompt state (SPEC.md §2.4);
	// the query, matches, current index, and wrap note live per-entry on
	// openfiles.Entry instead (same reasoning as goto-line above), since
	// only this transient "still typing the query" state needs to be
	// app-level.
	findPromptOpen bool
	findInput      string

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
		rootPath:            rootPath,
		root:                root,
		ignorer:             ignorer,
		idx:                 idx,
		watcher:             watcher,
		overlay:             views.OverlayNone, // SPEC.md §1: startup lands on the (empty) primary preview view
		browserSelected:     root,
		files:               openfiles.New(),
		searchDone:          make(chan searchOutcome, 8),
		browserErrorFlashes: map[string]time.Time{},
	}

	a.shared = &views.Shared{
		Files:     a.files,
		Idx:       a.idx,
		Root:      a.root,
		RootPath:  a.rootPath,
		Ignorer:   a.ignorer,
		BadgeSkip: &a.badgeSkip,
		Overlay:   &a.overlay,
	}
	a.QuickOpen.Shared = a.shared

	a.syncWatches()
	return a
}

// syncWatches adds an fs watch for every directory in the tree that has
// been loaded at least once, so RefreshTree's re-listing target set
// matches what's actually being watched. Idempotent and cheap to call
// after any action that might have loaded a new directory (expand —
// jump to file, §4.3, never itself expands anything, so it never needs
// this call).
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
// was deleted, kick off a background index rebuild so quick open
// eventually reflects the change too, reload the content of any open
// file that changed on disk (§6.1a), and pick up watches on any
// newly-loaded directories.
func (a *App) handleFSChange() {
	newlyErrored := tree.RefreshTree(a.root, a.rootPath, a.ignorer)
	a.browserSelected = tree.NearestSurviving(a.browserSelected)
	a.idx.Rebuild(a.rootPath, a.ignorer)
	a.badgeSkip.Reset()
	a.syncWatches()
	if len(newlyErrored) > 0 {
		a.flagErrorFlashes(newlyErrored)
	}
	if reloaded := a.files.Reload(previewByteCap); len(reloaded) > 0 {
		msg, boldRanges := reloadToastMessage(reloaded)
		a.showToast(msg, boldRanges...)
	}
}

// reloadToastMessage formats the names of one or more just-reloaded
// open files (SPEC.md §6.1a) into the transient notification text,
// along with the rune ranges each name occupies so the caller can
// render them bold for easier visual scanning.
func reloadToastMessage(names []string) (string, [][2]int) {
	var b strings.Builder
	ranges := make([][2]int, 0, len(names))
	for i, name := range names {
		if i > 0 {
			b.WriteString(", ")
		}
		start := len([]rune(b.String()))
		b.WriteString(name)
		ranges = append(ranges, [2]int{start, len([]rune(b.String()))})
	}
	b.WriteString(" reloaded (changed on disk)")
	return b.String(), ranges
}

// showToast starts the generic bottom-right transient notification
// (SPEC.md §5.3) with msg, replacing any toast already in progress.
// boldRanges optionally marks [start, end) rune ranges of msg to
// render bold (e.g. the reloaded file names).
func (a *App) showToast(msg string, boldRanges ...[2]int) {
	a.toastMessage = msg
	a.toastStart = time.Now()
	a.toastBoldRanges = boldRanges
}

// flagErrorFlashes starts a brief red flash (SPEC.md §2.2, §6.1, §5.3)
// for each path in paths, drawing attention to the inline error text
// browserLabel already renders for it (tree.Node.Err) without the error
// text itself fading — only the flash decays. Used both for a
// live-refresh's newly-erroring directories (handleFSChange, several
// paths at once) and a single failed file-open (browserOpen). Also
// prunes any previously-flashed path whose flash has already finished,
// so the map doesn't grow unbounded over a long-running session.
func (a *App) flagErrorFlashes(paths []string) {
	now := time.Now()
	for p, start := range a.browserErrorFlashes {
		if now.Sub(start) >= flashDuration {
			delete(a.browserErrorFlashes, p)
		}
	}
	for _, p := range paths {
		a.browserErrorFlashes[p] = now
	}
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

	a.shared.Canvas = canvas.New(screen)

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
				// SPEC.md §6.4: while the terminal is below the minimum
				// size, the too-small screen owns the frame and no key
				// is processed against whatever view/overlay is
				// underneath it — resizing back above the minimum
				// resumes that view exactly as it was, untouched by
				// anything typed while it was too small to show.
				if w, h := screen.Size(); w >= canvas.MinTerminalWidth && h >= canvas.MinTerminalHeight {
					a.handleKey(e)
				}
			}
			a.draw()
		case <-watchEvents:
			a.handleFSChange()
			if a.overlay == views.OverlayQuickOpen {
				a.QuickOpen.RefreshMatches()
			}
			a.draw()
		case out := <-a.searchDone:
			if out.gen == a.searchGen {
				a.searchCancel = nil
				if out.err != nil {
					a.searchError = out.err.Error()
					a.searchResults = nil
				} else {
					a.searchResults = out.results
				}
				if rows := a.searchRows(); a.searchSelected >= len(rows) {
					a.searchSelected = max(len(rows)-1, 0)
				}
			}
			a.draw()
		case <-ticker.C:
			// Also catches the background index (re)build finishing
			// while quick open is open with an unchanged query, so
			// matches don't go stale until the next keystroke.
			if a.overlay == views.OverlayQuickOpen {
				a.QuickOpen.RefreshMatches()
			}
			// Same idea for content search (SPEC.md §9.1): a query typed
			// before the index finished is held pending (searchResults
			// stays nil) until it's done, then run once here rather than
			// on every tick.
			if a.overlay == views.OverlaySearch && a.searchQuery != "" && a.searchResults == nil && a.searchCancel == nil {
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
	case views.OverlayBrowser:
		a.handleBrowserKey(ev)
	case views.OverlayQuickOpen:
		a.QuickOpen.HandleKey(ev)
		// LastOpenedPath is quick open's outbox for a coordinator-level
		// concern (keeping the browser's disclosure/selection in sync
		// with a file opened from elsewhere, SPEC.md §4.2) that quick
		// open itself has no business knowing how to do — see its doc
		// comment.
		if p := a.QuickOpen.LastOpenedPath; p != "" {
			a.revealInBrowser(p)
			a.QuickOpen.LastOpenedPath = ""
		}
	case views.OverlayOpenFiles:
		a.handleOpenFilesKey(ev)
	case views.OverlaySearch:
		a.handleSearchKey(ev)
	case views.OverlayNone:
		a.handlePreviewKey(ev)
	}
}

// handlePreviewKey handles input at the primary preview view when no
// overlay is active: reaching the browser, quick open, and open-files
// overlays, preview scrolling/goto-line/in-file-find, and quitting
// (SPEC.md §7). `b` only opens the browser overlay (SPEC.md §5.1);
// closing it back to this view is Escape's job alone, handled in
// handleBrowserKey (quick open, opened by `o`, is likewise not a
// toggle — see handleQuickOpenKey). Escape does not quit and
// there is no overlay to back out of here (`q` is the only way to
// quit, so an accidental Escape press can't lose the session's
// open-files state) — its only effect at this view is clearFind,
// clearing an active in-file find if there is one, and otherwise
// remaining a no-op.
func (a *App) handlePreviewKey(ev *tcell.EventKey) {
	if a.gotoPromptOpen {
		a.handleGotoPromptKey(ev)
		return
	}
	if a.findPromptOpen {
		a.handleFindPromptKey(ev)
		return
	}

	switch {
	case ev.Rune() == 'b':
		a.overlay = views.OverlayBrowser
	case ev.Key() == tcell.KeyTab:
		a.overlay = views.OverlayOpenFiles
		a.openFilesSelected = max(a.files.Displayed, 0)
	case ev.Rune() == 'o':
		a.openQuickOpen()
	case ev.Rune() == 's':
		a.openSearch()
	case ev.Rune() == 'q':
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
	case ev.Rune() == '/':
		if a.files.DisplayedEntry() != nil {
			a.findPromptOpen = true
			a.findInput = ""
		}
	case ev.Rune() == 'c':
		if e := a.files.DisplayedEntry(); e != nil {
			e.CopyMode = !e.CopyMode
		}
	case ev.Rune() == 'n':
		a.findStep(1)
	case ev.Rune() == 'N':
		a.findStep(-1)
	case ev.Key() == tcell.KeyEscape:
		a.clearFind()
	}
}

// clearFind clears the displayed entry's in-file find state (SPEC.md
// §2.4), if any — its query, matches, current index, and wrap note —
// so its highlighting and file-title-bar status disappear, leaving the
// idle file title bar in their place. Bound to Escape at the primary
// preview view: this does not conflict with Escape's deliberate
// no-op-when-nothing-to-back-out-of behavior there (it still never
// quits — only `q` does), it just gives find an explicit way out,
// since otherwise it would persist until superseded by a new search on
// the same entry. A no-op if there's no displayed entry or no active
// find, so Escape stays inert exactly when there was nothing to clear.
func (a *App) clearFind() {
	e := a.files.DisplayedEntry()
	if e == nil || e.FindQuery == "" {
		return
	}
	e.FindQuery = ""
	e.FindMatches = nil
	e.FindCurrent = -1
	e.FindWrapNote = ""
}

// handleGotoPromptKey handles input while the goto-line prompt is open
// (SPEC.md §2.1): only digits, backspace, and Ctrl+U (clear) are accepted, Enter jumps
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
	case ev.Key() == tcell.KeyCtrlU:
		a.gotoInput = ""
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
	n := 0
	for _, r := range input {
		n = n*10 + int(r-'0')
	}
	a.scrollToLine(e, n)
}

// scrollToLine jumps e's scroll to source line n's first display row
// (SPEC.md §2.1), clamped to [1, total source lines]. Used by both the
// goto-line prompt and content search's jump-to-hit (§9.2).
func (a *App) scrollToLine(e *openfiles.Entry, n int) {
	a.ensurePreviewWrapped(e, a.computedPreviewWidth())
	n = clamp(n, 1, len(e.Lines))
	if row, ok := e.FirstRow[n-1]; ok {
		e.Scroll = clamp(row, 0, a.maxPreviewScroll(e, a.previewViewportHeight()))
	}
}

func (a *App) maxPreviewScroll(e *openfiles.Entry, viewportHeight int) int {
	return max(len(e.Rows)-viewportHeight, 0)
}

// handleFindPromptKey handles input while the in-file find prompt is
// open (SPEC.md §2.4): any printable character is accepted (unlike
// goto-line's digits-only prompt, since a search query is free text),
// Enter executes the search, Escape cancels the prompt without
// changing the entry's existing find state (if any).
func (a *App) handleFindPromptKey(ev *tcell.EventKey) {
	switch {
	case ev.Key() == tcell.KeyEscape:
		a.findPromptOpen = false
	case ev.Key() == tcell.KeyEnter:
		a.performFind(a.findInput)
		a.findPromptOpen = false
	case ev.Key() == tcell.KeyBackspace, ev.Key() == tcell.KeyBackspace2:
		if len(a.findInput) > 0 {
			r := []rune(a.findInput)
			a.findInput = string(r[:len(r)-1])
		}
	case ev.Key() == tcell.KeyCtrlU:
		a.findInput = ""
	case ev.Rune() != 0 && ev.Key() == tcell.KeyRune:
		a.findInput += string(ev.Rune())
	}
}

// performFind executes an in-file find (SPEC.md §2.4): locates every
// case-insensitive match of query in the displayed entry's lines, then
// jumps to the first one at or after the source line currently at the
// top of the viewport — the same "search forward from here" behavior
// as `less` — wrapping to the very first match (and noting the wrap)
// if none exists at or after that point. A no-op if there's no
// displayed entry; an empty query clears any existing find state
// instead of searching (mirroring a bare "/" + Enter in `less`).
func (a *App) performFind(query string) {
	e := a.files.DisplayedEntry()
	if e == nil {
		return
	}
	a.ensurePreviewWrapped(e, a.computedPreviewWidth())

	e.FindQuery = query
	e.FindMatches = find.InLines(e.Lines, query)
	e.FindWrapNote = ""
	if len(e.FindMatches) == 0 {
		e.FindCurrent = -1
		return
	}

	startLine := 0
	if e.Scroll >= 0 && e.Scroll < len(e.Rows) {
		startLine = e.Rows[e.Scroll].SourceLine
	}
	idx := 0
	for i, m := range e.FindMatches {
		if m.Line >= startLine {
			idx = i
			break
		}
	}
	if e.FindMatches[idx].Line < startLine {
		e.FindWrapNote = "wrapped to top"
	}
	e.FindCurrent = idx
	a.scrollToFindMatch(e)
}

// findStep moves the current match by delta (+1 for `n`/next, -1 for
// `N`/previous), wrapping around at either end and noting the wrap
// (SPEC.md §2.4) — the same wraparound stepper the finder overlays and
// browser navigation already use (internal/tree.MoveSelection). A
// no-op if there's no displayed entry or it has no matches.
func (a *App) findStep(delta int) {
	e := a.files.DisplayedEntry()
	if e == nil || len(e.FindMatches) == 0 {
		return
	}
	next := tree.MoveSelection(e.FindCurrent, delta, len(e.FindMatches))
	switch {
	case delta > 0 && next < e.FindCurrent:
		e.FindWrapNote = "wrapped to top"
	case delta < 0 && next > e.FindCurrent:
		e.FindWrapNote = "wrapped to bottom"
	default:
		e.FindWrapNote = ""
	}
	e.FindCurrent = next
	a.scrollToFindMatch(e)
}

// scrollToFindMatch scrolls e just enough to bring its current find
// match into view (the same "only scroll if it's not already visible"
// rule the browser uses for its own selection, SPEC.md §5.2), a no-op
// if there is no current match.
func (a *App) scrollToFindMatch(e *openfiles.Entry) {
	if e.FindCurrent < 0 || e.FindCurrent >= len(e.FindMatches) {
		return
	}
	row := findMatchRow(e, e.FindMatches[e.FindCurrent])
	if row < 0 {
		return
	}
	h := a.previewViewportHeight()
	if row < e.Scroll {
		e.Scroll = row
	}
	if row >= e.Scroll+h {
		e.Scroll = row - h + 1
	}
	e.Scroll = clamp(e.Scroll, 0, a.maxPreviewScroll(e, h))
}

// findMatchRow returns the index into e.Rows of the specific wrapped
// row m falls in (not just its source line's first row, since a long
// line's match may land in a continuation row) — a.ensurePreviewWrapped
// must already have been called. Falls back to the source line's first
// row if m's column can't be located in any of its rows (shouldn't
// happen for a match found in e.Lines, but degrades safely rather than
// losing the jump entirely).
func findMatchRow(e *openfiles.Entry, m find.Match) int {
	start, ok := e.FirstRow[m.Line]
	if !ok {
		return -1
	}
	for i := start; i < len(e.Rows); i++ {
		r := e.Rows[i]
		if r.SourceLine != m.Line {
			break
		}
		rowLen := preview.SegmentsRuneLen(r.Segments)
		if m.Col >= r.ColStart && m.Col < r.ColStart+max(rowLen, 1) {
			return i
		}
	}
	return start
}

func (a *App) previewViewportHeight() int {
	_, h := a.shared.Canvas.Size()
	height := h - 1 // header row
	if a.files.DisplayedEntry() != nil {
		height-- // file title bar row, shown whenever a file is displayed
	}
	if a.gotoPromptOpen {
		height--
	}
	return height
}

// computedPreviewWidth returns the content width (in columns) available
// to the preview's wrapped text at the primary preview view when no
// overlay is active (full terminal width). This is only used by the
// scroll/goto-line key handlers, which are only reachable in that
// context.
func (a *App) computedPreviewWidth() int {
	w, _ := a.shared.Canvas.Size()
	e := a.files.DisplayedEntry()
	if e == nil {
		return w
	}
	return max(w-previewGutterWidth(e), 1)
}

// previewGutterWidth returns the line-number gutter's width for e:
// gutterWidth's normal computation, or 0 while e is in copy mode
// (SPEC.md §2.1) and stripping the gutter out of its preview entirely.
// Shared by computedPreviewWidth and drawPreview so both agree on the
// same content width — otherwise they'd race to invalidate each
// other's wrap cache every frame.
func previewGutterWidth(e *openfiles.Entry) int {
	if e.CopyMode {
		return 0
	}
	return canvas.GutterWidth(len(e.Lines))
}

// ensurePreviewWrapped recomputes e's wrapped display rows if width has
// changed since they were last computed (SPEC.md §2.1: "wrapping must
// be recomputed whenever the available width changes"), caching the
// result on the entry so it's not redone every frame. Copy mode wraps
// the same way normal display does — an earlier version of copy mode
// disabled wrapping entirely and clipped long lines instead, but that
// meant anything past the pane's right edge was never drawn at all,
// making it impossible to select the rest of the line by any means;
// word-wrapping (SPEC.md §2.1) keeps every character of the line
// visible and selectable somewhere on screen, which matters more than
// avoiding the extra line break a multi-row selection can pick up at a
// wrap point — an inherent limitation of any fixed-width terminal grid,
// not something copy mode can fully solve either way.
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
// actions (SPEC.md §3.4). Escape is the sole key that closes the
// browser back to the primary preview view (SPEC.md §5.1); `b` only
// opens it from there and has no effect once inside. While
// jump-to-file typing mode (§4.3) is active, every key below is
// bypassed in favor of handleJumpKey, since jump mode repurposes all
// of them as query input.
// `o` and `s` have no effect here — browse, quick open, and content
// search are mutually exclusive (SPEC.md §5.1): reaching another one
// requires closing the browser back to the primary preview view first.
func (a *App) handleBrowserKey(ev *tcell.EventKey) {
	if a.jumpActive {
		a.handleJumpKey(ev)
		return
	}

	flat := a.root.Flatten()
	idx := indexOf(flat, a.browserSelected)

	switch {
	case ev.Key() == tcell.KeyUp:
		a.browserSelected = flat[tree.MoveSelection(idx, -1, len(flat))]
	case ev.Key() == tcell.KeyDown:
		a.browserSelected = flat[tree.MoveSelection(idx, 1, len(flat))]
	case ev.Key() == tcell.KeyRight:
		target := a.browserSelected
		a.browserSelected = target.MoveRight(a.rootPath, a.ignorer)
		// Flash on every attempt that ends in an error, not just the
		// first: MoveRight/Expand retries the listing each time (a
		// still-failing directory never gets marked Expanded, SPEC.md
		// §3.1), so a still-broken directory the user keeps trying to
		// disclose should keep giving feedback each time, not just once.
		if target.Err != "" {
			a.flagErrorFlashes([]string{target.Path})
		}
		a.syncWatches()
	case ev.Key() == tcell.KeyLeft:
		a.browserSelected = a.browserSelected.MoveLeft()
	case ev.Key() == tcell.KeyEnter:
		a.browserOpen()
	case ev.Rune() == '/':
		a.openJumpToFile()
	case ev.Key() == tcell.KeyEscape:
		a.overlay = views.OverlayNone
	}
}

// openJumpToFile enters jump-to-file typing mode (SPEC.md §4.3) from
// within the browser: not an overlay change (a.overlay stays
// overlayBrowser), just a mode layered on top of it that remembers the
// current selection/scroll so Escape can restore them.
func (a *App) openJumpToFile() {
	a.jumpActive = true
	a.jumpQuery = ""
	a.jumpDisclosed = ""
	a.jumpMatches = nil
	a.jumpSelected = 0
	a.jumpPrevSelected = a.browserSelected
	a.jumpPrevScroll = a.browserScroll
	a.jumpScope = a.root
}

// recomputeJumpMatches rebuilds jumpMatches from the current query
// against a.jumpScope's live flattened row list (SPEC.md §4.3's
// candidate set: whatever the tree's current expand/collapse state
// already exposes, files and directories alike), matching each row's
// leaf name by case-insensitive prefix. jumpScope is normally the tree
// root, but narrows after a slash-to-expand drill-down (see
// handleJumpKey).
func (a *App) recomputeJumpMatches() {
	a.jumpSelected = 0
	a.jumpMatches = tree.JumpMatches(a.jumpScope, a.jumpQuery)
}

// handleJumpKey implements jump-to-file typing mode's input handling
// (SPEC.md §4.3): every printable rune is query input, Tab/Shift-Tab
// (or Down/Up) cycle among current matches, and Return/Escape are the
// only ways out.
func (a *App) handleJumpKey(ev *tcell.EventKey) {
	switch {
	case ev.Key() == tcell.KeyEscape:
		a.browserSelected = a.jumpPrevSelected
		a.browserScroll = a.jumpPrevScroll
		a.jumpActive = false
	case ev.Key() == tcell.KeyEnter:
		a.jumpActive = false
	case ev.Key() == tcell.KeyTab, ev.Key() == tcell.KeyDown:
		if len(a.jumpMatches) > 0 {
			a.jumpSelected = tree.MoveSelection(a.jumpSelected, 1, len(a.jumpMatches))
			a.browserSelected = a.jumpMatches[a.jumpSelected]
		}
	case ev.Key() == tcell.KeyBacktab, ev.Key() == tcell.KeyUp:
		if len(a.jumpMatches) > 0 {
			a.jumpSelected = tree.MoveSelection(a.jumpSelected, -1, len(a.jumpMatches))
			a.browserSelected = a.jumpMatches[a.jumpSelected]
		}
	case ev.Key() == tcell.KeyBackspace, ev.Key() == tcell.KeyBackspace2:
		if len(a.jumpQuery) > 0 {
			r := []rune(a.jumpQuery)
			a.jumpQuery = string(r[:len(r)-1])
		}
		a.recomputeJumpMatches()
		if len(a.jumpMatches) > 0 {
			a.browserSelected = a.jumpMatches[0]
		}
	case ev.Key() == tcell.KeyCtrlU:
		a.jumpQuery = ""
		a.recomputeJumpMatches()
		if len(a.jumpMatches) > 0 {
			a.browserSelected = a.jumpMatches[0]
		}
	case ev.Rune() == '/' && ev.Key() == tcell.KeyRune && len(a.jumpMatches) == 1 && a.jumpMatches[0].IsDir:
		// Slash-to-expand (SPEC.md §4.3): when the query already
		// uniquely identifies a directory, `/` disclosed it and
		// re-scopes matching to its descendants instead of being
		// consumed as a literal query character (which could never
		// match anyway, since no filename contains `/`). This is
		// jump to file's one deliberate exception to "never expands
		// or collapses anything" — it only ever expands, never
		// collapses, and only on this explicit unique-match+`/` signal.
		// jumpDisclosed accumulates the committed segment (jumpQuery,
		// which named this directory, plus the slash) so it keeps
		// showing ahead of the next segment's query rather than
		// vanishing — clearing jumpQuery to start the next segment
		// must not read as losing what was already typed.
		target := a.jumpMatches[0]
		target.Expand(a.rootPath, a.ignorer)
		if target.Err != "" {
			a.flagErrorFlashes([]string{target.Path})
		} else {
			a.jumpScope = target
			a.jumpDisclosed += a.jumpQuery + "/"
			a.jumpQuery = ""
			a.browserSelected = target
			a.syncWatches()
			a.recomputeJumpMatches()
		}
	case ev.Rune() != 0 && ev.Key() == tcell.KeyRune:
		a.jumpQuery += string(ev.Rune())
		a.recomputeJumpMatches()
		if len(a.jumpMatches) > 0 {
			a.browserSelected = a.jumpMatches[0]
		}
	}
}

// browserOpen implements Enter from the browser, per SPEC.md §3.4 and
// §2.2's open-failure signaling: a no-op on a directory; on a file, an
// "opened" result displays it in the primary preview view without
// closing the browser, so several files can be queued up in a row
// before returning to the preview; a "failed" result leaves the browser
// open with the failure recorded on the node itself (tree.Node.Err,
// browserLabel's existing inline `[error]` rendering) and a brief red
// flash (browserErrorFlashes/styleFlashError, §5.3) — the same treatment
// a directory that newly fails to list gets from a live-refresh (§6.1),
// unified since both are "the last operation on this node failed." On
// success, the opened row also gets a brief flash (browserFlashPath/
// browserFlashStart, drawn in drawBrowser) as an on-open confirmation,
// the same treatment content search gives its own rows
// (performSearchOpen) — distinct from the lasting "●" open indicator
// every open file's row shows regardless of when it was opened.
func (a *App) browserOpen() {
	if a.browserSelected.IsDir {
		return
	}
	res := a.files.Open(a.browserSelected.Path, previewByteCap)
	if res.Outcome != openfiles.Opened {
		a.browserSelected.Err = res.Message
		a.flagErrorFlashes([]string{a.browserSelected.Path})
		return
	}
	a.browserSelected.Err = ""
	a.browserFlashPath = a.browserSelected.Path
	a.browserFlashStart = time.Now()
}

// handleOpenFilesKey implements the open-files-list overlay's input
// handling (SPEC.md §2.3): a dropdown-style popup showing at most
// openfiles.PageSize entries at a time ("a page"), each row labeled
// with its 0-9 position on the current page. Shift-Page-Up/Shift-Page-
// Down and Shift-Up/Shift-Down (reorder) are checked ahead of their
// plain Page-Up/Page-Down and Up/Down counterparts (navigate) since
// tcell reports the shifted and unshifted forms as the same Key with
// ModShift set, not a distinct key.
func (a *App) handleOpenFilesKey(ev *tcell.EventKey) {
	n := len(a.files.Entries)
	shift := ev.Modifiers()&tcell.ModShift != 0

	switch {
	case ev.Key() == tcell.KeyEscape:
		a.overlay = views.OverlayNone
	case ev.Key() == tcell.KeyPgUp && shift:
		if n > 0 {
			a.openFilesSelected = a.files.MoveUpPage(a.openFilesSelected, openfiles.PageSize)
		}
	case ev.Key() == tcell.KeyPgDn && shift:
		if n > 0 {
			a.openFilesSelected = a.files.MoveDownPage(a.openFilesSelected, openfiles.PageSize)
		}
	case ev.Key() == tcell.KeyUp && shift:
		if n > 0 {
			a.openFilesSelected = a.files.MoveUp(a.openFilesSelected)
		}
	case ev.Key() == tcell.KeyDown && shift:
		if n > 0 {
			a.openFilesSelected = a.files.MoveDown(a.openFilesSelected)
		}
	case ev.Key() == tcell.KeyPgUp:
		a.openFilesSelected = openfiles.SelectPage(a.openFilesSelected, -1, openfiles.PageSize, n)
	case ev.Key() == tcell.KeyPgDn:
		a.openFilesSelected = openfiles.SelectPage(a.openFilesSelected, 1, openfiles.PageSize, n)
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
			a.overlay = views.OverlayNone
		}
	case ev.Rune() >= '0' && ev.Rune() <= '9':
		if idx, ok := openfiles.SelectDigit(a.openFilesSelected, int(ev.Rune()-'0'), openfiles.PageSize, n); ok {
			a.openFilesSelected = idx
			a.files.Display(idx)
			a.overlay = views.OverlayNone
		}
	case ev.Rune() == 'x':
		if n > 0 {
			a.openFilesSelected = a.files.Remove(a.openFilesSelected)
			if len(a.files.Entries) == 0 {
				// SPEC.md §2.3: emptying the list auto-closes the
				// overlay to the primary preview view's empty state,
				// which in turn auto-opens the browser exactly as it
				// does on startup (§1).
				a.overlay = views.OverlayBrowser
			}
		}
	}
}

// openQuickOpen opens the quick open overlay (SPEC.md §4.2), reachable
// only from the primary preview view: browse, quick open, and content
// search are mutually exclusive (SPEC.md §5.1), so Escape always
// returns here rather than to some other mode. Per SPEC.md §5.2,
// opening it while indexing is already done means the user has
// directly seen indexing is ready, so it short-circuits the badge's
// minimum-display-duration floor.
func (a *App) openQuickOpen() {
	a.overlay = views.OverlayQuickOpen
	a.QuickOpen.Open()
}

// revealInBrowser expands the browser tree's disclosure state down to
// absPath and selects it, so opening a file from quick open or content
// search (which search the whole tree regardless of what's currently
// expanded) leaves the browser showing where that file lives the next
// time it's opened (SPEC.md §4.2, §9.2) — not gated on the browser
// overlay actually being open right now, purely updating its state for
// later.
func (a *App) revealInBrowser(absPath string) {
	if n := tree.RevealPath(a.root, a.rootPath, absPath, a.ignorer); n != nil {
		a.browserSelected = n
	}
}

// openSearch opens the content search overlay (SPEC.md §9.1), reachable
// only from the primary preview view: browse, quick open, and content
// search are mutually exclusive (SPEC.md §5.1), so Escape always
// returns here. The query, results, selection, and per-file disclosure
// state are deliberately left untouched here: content search persists
// across close/reopen (Escape closes the overlay without discarding
// it) and is only reset by the user explicitly clearing the query
// themselves (backspacing it to empty, or typing a new one).
func (a *App) openSearch() {
	a.overlay = views.OverlaySearch
	a.searchErrorPath = ""
}

// searchRows flattens the current search results into displayable rows
// (SPEC.md §9.2): one file row per result, followed by that file's hit
// rows in source order when it's expanded (i.e. not present in
// searchCollapsed, so results are expanded by default). This is
// recomputed on demand rather than cached, since it's cheap and only
// ever needed while rendering or handling a keypress.
func (a *App) searchRows() []searchRow {
	var rows []searchRow
	for fi, r := range a.searchResults {
		rows = append(rows, searchRow{file: fi})
		if a.searchCollapsed[r.AbsPath] {
			continue
		}
		for hi := range r.Hits {
			rows = append(rows, searchRow{file: fi, hit: hi, isHit: true})
		}
	}
	return rows
}

// toggleSearchDisclosure flips the expanded/collapsed state of the file
// the row at index i belongs to (SPEC.md §9.2), keeping the selection on
// that same file's row so collapsing a file you're inside of doesn't
// strand the selection.
func (a *App) toggleSearchDisclosure(i int) {
	rows := a.searchRows()
	if i < 0 || i >= len(rows) {
		return
	}
	fi := rows[i].file
	path := a.searchResults[fi].AbsPath
	if a.searchCollapsed == nil {
		a.searchCollapsed = make(map[string]bool)
	}
	a.searchCollapsed[path] = !a.searchCollapsed[path]
	a.searchSelected = a.searchFileRowIndex(fi)
}

// searchFileRowIndex returns the row index of the file row for
// searchResults[fi] in the current flattened row list.
func (a *App) searchFileRowIndex(fi int) int {
	rows := a.searchRows()
	for i, row := range rows {
		if !row.isHit && row.file == fi {
			return i
		}
	}
	return 0
}

// recomputeSearch (re)starts the background content scan for the
// current query (SPEC.md §9.1): any scan still running for the previous
// query is canceled first, so only the most recently typed query's
// result is ever applied. An empty query performs no scan at all. If
// the background index hasn't finished building yet, the scan is
// deferred — Run's ticker case retries once it has — rather than
// scanning a partial candidate set. In regex mode, the query is
// compiled synchronously first — cheap, and lets an invalid pattern be
// reported immediately as searchError without spawning a scan at all
// (search.Run would otherwise reject it the same way, just one
// goroutine-hop later). The scan itself always runs in a background
// goroutine regardless of mode — even a slow regex over a large tree
// never blocks the UI thread, since results only arrive back via
// searchDone in the main select loop (Run.go).
func (a *App) recomputeSearch() {
	a.cancelSearch()
	a.searchSelected = 0
	a.searchScroll = 0
	a.searchErrorPath = ""
	a.searchError = ""
	a.searchCollapsed = nil

	if a.searchQuery == "" {
		a.searchResults = nil
		return
	}

	mode := search.ModeSubstring
	if a.searchRegex {
		mode = search.ModeRegex
		if _, err := search.CompileRegex(a.searchQuery); err != nil {
			a.searchError = err.Error()
			a.searchResults = nil
			return
		}
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
	a.searchScanStart = time.Now()
	gen, query := a.searchGen, a.searchQuery
	go func() {
		results, err := search.Run(ctx, query, mode, candidates, previewByteCap)
		a.searchDone <- searchOutcome{gen: gen, results: results, err: err}
	}()
}

// clearSearch resets the query and results back to empty (SPEC.md
// §9.2's Ctrl+U), the explicit "clear it yourself" action — since
// Escape deliberately no longer discards search state (openSearch),
// this is the only way to reset a query short of backspacing it out
// one character at a time. The regex-mode toggle is left as-is; it's a
// standing preference, not part of the query being cleared.
func (a *App) clearSearch() {
	a.searchQuery = ""
	a.recomputeSearch()
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
		// The query, results, and any still-running scan are deliberately
		// left alone: content search persists across close/reopen (see
		// openSearch), so Escape only leaves the overlay rather than
		// discarding its state.
		a.overlay = views.OverlayNone
	case ev.Key() == tcell.KeyEnter:
		// Opening always leaves the overlay open (SPEC.md §9.2) — Escape
		// is what closes it (and preserves state when it does) — so
		// several hits can be opened in a row without re-entering the
		// search each time.
		a.performSearchOpen()
	case ev.Key() == tcell.KeyCtrlR:
		a.searchRegex = !a.searchRegex
		a.recomputeSearch()
	case ev.Key() == tcell.KeyCtrlU:
		a.clearSearch()
	case ev.Key() == tcell.KeyDown:
		if rows := a.searchRows(); len(rows) > 0 {
			a.searchSelected = tree.MoveSelection(a.searchSelected, 1, len(rows))
		}
	case ev.Key() == tcell.KeyUp:
		if rows := a.searchRows(); len(rows) > 0 {
			a.searchSelected = tree.MoveSelection(a.searchSelected, -1, len(rows))
		}
	case ev.Key() == tcell.KeyLeft:
		rows := a.searchRows()
		if a.searchSelected >= 0 && a.searchSelected < len(rows) {
			row := rows[a.searchSelected]
			if row.isHit || !a.searchCollapsed[a.searchResults[row.file].AbsPath] {
				a.toggleSearchDisclosure(a.searchFileRowIndex(row.file))
			}
		}
	case ev.Key() == tcell.KeyRight:
		rows := a.searchRows()
		if a.searchSelected >= 0 && a.searchSelected < len(rows) {
			row := rows[a.searchSelected]
			if !row.isHit && a.searchCollapsed[a.searchResults[row.file].AbsPath] {
				a.toggleSearchDisclosure(a.searchSelected)
			}
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

// performSearchOpen implements Return (SPEC.md §9.2): open the selected
// row's file into the open-files list per §2.2's open semantics and
// jump to its line, leaving the overlay open so several hits can be
// opened in a row without re-entering the search — Escape is what
// closes the overlay (and, per openSearch/handleSearchKey, preserves
// its state when it does). A "failed" result (e.g. the file changed or
// was removed between scanning and opening) leaves the overlay open
// with the failure recorded against that file's path
// (searchErrorPath/searchErrorMessage) and rendered inline on its file
// row by searchRowLabel, plus a brief red flash (§2.2, §5.3) — attributed
// to the parent file row even when opened from one of its hit rows, the
// same as the on-open flash below. On success, the opened file's row
// also gets a brief flash (searchFlashPath/searchFlashStart, drawn in
// drawSearch) as an on-open confirmation distinct from the lasting
// "already open" indicator every open file's row shows regardless of
// whether it was just opened this way.
func (a *App) performSearchOpen() {
	rows := a.searchRows()
	if a.searchSelected < 0 || a.searchSelected >= len(rows) {
		return
	}
	row := rows[a.searchSelected]
	result := a.searchResults[row.file]

	res := a.files.Open(result.AbsPath, previewByteCap)
	if res.Outcome != openfiles.Opened {
		a.searchErrorPath = result.AbsPath
		a.searchErrorMessage = res.Message
		a.searchErrorFlashStart = time.Now()
		return
	}
	a.searchErrorPath = ""
	a.searchFlashPath = result.AbsPath
	a.searchFlashStart = time.Now()
	a.revealInBrowser(result.AbsPath)

	// A file row jumps to its first hit (the file's earliest match); a
	// hit row jumps to that specific line (SPEC.md §9.2).
	line := result.Hits[0].LineNum
	if row.isHit {
		line = result.Hits[row.hit].LineNum
	}
	a.scrollToLine(res.Entry, line)
}

func indexOf(list []*tree.Node, n *tree.Node) int {
	for i, c := range list {
		if c == n {
			return i
		}
	}
	return 0
}
