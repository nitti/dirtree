// Package ui implements the terminal-rendering layer: the tcell draw
// loop, input handling, resize polling, and escape-timeout
// configuration (SPEC.md §6.2, §6.3). Unlike internal/tree,
// internal/openfiles, internal/match, internal/preview,
// internal/layout, and internal/spinner, this package is not expected
// to be unit-tested — verification is manual, in a real terminal.
//
// The interactive UI is being rebuilt in stages against SPEC.md's
// open-files-primary-view redesign (see README.md's Status section for
// the full plan). This stage wires the tree explorer overlay's own
// navigation and its Space/`a` open actions (§3.4), and the
// jump/fuzzy-picker overlay (§4) from both entry points, against the
// new internal/openfiles list (§2.2). The open-files-list overlay
// (§2.3), the primary preview view's own content rendering (§2.1), and
// the tree explorer's dual split/popup layout (§5.1) are not wired yet
// and land in later stages.
package ui

import (
	"os"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/ignore"
	"github.com/nitti/dirtree/internal/index"
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
	case overlayNone:
		a.handlePreviewKey(ev)
		// overlayOpenFiles is not reachable yet (stage 4); no key
		// handling exists for it.
	}
}

// handlePreviewKey handles input at the primary preview view when no
// overlay is active. Full preview interaction (scrolling, goto-line,
// per-entry content — SPEC.md §2.1) lands in stage 5; for now this only
// wires the keys needed to reach/leave the tree explorer and jump
// overlays and to quit (SPEC.md §7).
func (a *App) handlePreviewKey(ev *tcell.EventKey) {
	switch {
	case ev.Rune() == 'e':
		a.overlay = overlayTree
	case ev.Rune() == '/':
		a.openJump(jumpFromPreview)
	case ev.Rune() == 'q', ev.Key() == tcell.KeyEscape:
		a.quit = true
	}
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
