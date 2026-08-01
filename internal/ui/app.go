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
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/ignore"
	"github.com/nitti/dirtree/internal/index"
	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/preview"
	"github.com/nitti/dirtree/internal/spinner"
	"github.com/nitti/dirtree/internal/tree"
	"github.com/nitti/dirtree/internal/ui/canvas"
	"github.com/nitti/dirtree/internal/ui/views"
	"github.com/nitti/dirtree/internal/watch"
)

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
	// quitHoldDuration is how long `q` must be held at the primary
	// preview view before the app actually quits (SPEC.md §5.2's
	// hold-to-quit gesture) — long enough that a single accidental tap
	// can't discard the session's open-files state, short enough that a
	// deliberate press doesn't feel sluggish.
	quitHoldDuration = 1 * time.Second
	// quitHoldReleaseGap is the longest gap allowed between consecutive
	// `q` key-repeat events before the hold-to-quit gesture is considered
	// released: this is the single source of truth for "is `q` still
	// down" throughout the gesture, used both to decide whether to reset
	// quitHoldStart/quitConfirmed (or, once confirmed, whether to
	// actually quit — see checkQuitHoldRelease) and, in render.go, purely
	// via quitHoldStart's own zero-vs-non-zero state, whether the
	// header/title bar shows the quitting variant at all — the two are
	// deliberately tied to the exact same check rather than split into a
	// separate, shorter "just for the visual" threshold, after an earlier
	// attempt at that caused the header to flicker throughout an entire
	// genuine hold on any terminal whose real auto-repeat interval turned
	// out to be slower than the guessed visual-only threshold. Terminals
	// deliver no key-up event, so a held key can only be inferred from a
	// steady stream of repeat events, and a real terminal's own auto-repeat
	// timing — both the initial delay before a held key starts repeating
	// at all, and the steady interval between repeats after that — varies
	// enough across terminals/OSes that this threshold is kept generous
	// rather than tuned for snappiness: too short, and an ordinary hold's
	// own repeat cadence gets mistaken for a release, which either
	// flickers the header (if that mistake is visual-only) or silently
	// resets the gesture's elapsed-time progress (if it also touches
	// quitHoldStart) — both are worse than a release taking up to this
	// long to visibly register. Checked on the existing resize-poll ticker
	// (resizePollInterval) rather than a dedicated timer — imprecision on
	// the order of one poll tick is negligible against a threshold this
	// size. 600ms is empirically confirmed against real key-repeat
	// behavior, not just a conservative guess: an interim attempt at
	// 300ms measurably reintroduced the header-flicker failure mode this
	// threshold exists to avoid, on real hardware — so 600ms is the
	// tightest value actually verified safe so far. If lowering this is
	// revisited, measure the terminal's real auto-repeat gap first
	// rather than guessing again.
	quitHoldReleaseGap = 600 * time.Millisecond
)

// cursorStyleInherit is passed to Screen.SetCursorStyle in Run to opt
// out of tcell's per-frame DECSCUSR cursor-style assertion — see its
// call site for why. -1 is deliberately outside tcell's own
// CursorStyle enum (0-6); any value it doesn't recognize does the job.
const cursorStyleInherit = tcell.CursorStyle(-1)

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

	// Browser is the browser overlay's own state, including jump-to-file
	// typing mode (SPEC.md §3.4, §4.3).
	Browser views.Browser

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
	QuickOpen views.QuickOpen

	// Search is the content search overlay's own state (SPEC.md §9).
	Search views.Search

	// files is the open-files list (SPEC.md §2.2, §2.3) the primary
	// preview view and both overlays' open actions operate on.
	files *openfiles.List

	// OpenFiles is the open-files-list overlay's own state (SPEC.md
	// §2.3).
	OpenFiles views.OpenFiles

	// Preview is the primary preview view's own state: the goto-line
	// prompt (SPEC.md §2.1) and in-file find prompt (§2.4). The
	// displayed entry's scroll and wrapped-row cache live per-entry on
	// openfiles.Entry instead, since they're tracked per open file.
	Preview views.Preview

	badgeSkip spinner.MinDurationSkip

	// quitHoldStart is when the currently-in-progress hold-to-quit
	// gesture (SPEC.md §5.2) began holding `q`, zero when no hold is in
	// progress — render.go reads this directly (zero vs. non-zero) to
	// decide whether to draw the header/title bar's attention-grabbing
	// quitting variant, the same state checkQuitHoldRelease itself resets
	// via quitHoldReleaseGap, so the header is visible for exactly as
	// long as the app believes `q` is down.
	// quitHoldLastKey is the most recent `q` key-repeat event's time.
	// quitConfirmed is set once the hold has been held for the full
	// quitHoldDuration: the app doesn't quit the instant that happens,
	// only once a release is subsequently detected (or, resetQuitHold is
	// never called while this is set) — this drains any `q` key-repeat
	// events still in flight from the terminal through the app's own
	// event loop instead of letting them leak into the shell once the
	// process actually exits and the terminal returns to normal mode.
	quitHoldStart   time.Time
	quitHoldLastKey time.Time
	quitConfirmed   bool

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
		rootPath: rootPath,
		root:     root,
		ignorer:  ignorer,
		idx:      idx,
		watcher:  watcher,
		overlay:  views.OverlayNone, // SPEC.md §1: startup lands on the (empty) primary preview view
		files:    openfiles.New(),
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
	a.Search.Shared = a.shared
	a.Search.Done = make(chan views.SearchOutcome, 8)
	a.Browser.Shared = a.shared
	a.Browser.Selected = root
	a.Browser.ErrorFlashes = map[string]time.Time{}
	a.Preview.Shared = a.shared
	a.OpenFiles.Shared = a.shared

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
		a.watcher.Add(n.Path())
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
	a.Browser.Selected = tree.NearestSurviving(a.Browser.Selected)
	a.idx.Rebuild(a.rootPath, a.ignorer)
	a.badgeSkip.Reset()
	a.syncWatches()
	if len(newlyErrored) > 0 {
		a.Browser.FlagErrorFlashes(newlyErrored)
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
	// tcell asserts an explicit "blinking block" DECSCUSR cursor style
	// (tcell.CursorStyleDefault) on every single frame it draws — sent
	// this often (every redraw, including the 100ms resize-poll
	// ticker's idle ones), it resets the terminal's own blink timer
	// before a cycle ever completes, so the cursor visually never
	// blinks, and it silently overrides whatever cursor style/blink
	// policy the user's terminal or shell had already configured.
	// Passing a CursorStyle tcell doesn't recognize disables this
	// entirely: per Screen.SetCursorStyle's own doc comment, an
	// unsupported style "will have no effect," so the cursor's shape
	// and blink state are never touched by this app at all — whatever
	// the terminal/shell already had in place stays exactly as is.
	screen.SetCursorStyle(cursorStyleInherit)

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
		case out := <-a.Search.Done:
			a.Search.ApplyOutcome(out)
			a.draw()
		case <-ticker.C:
			// Catches a released `q` even when no further key event
			// arrives to notice it directly (SPEC.md §5.2's
			// hold-to-quit gesture): a genuinely held key keeps
			// re-triggering progressQuitHold well within
			// quitHoldReleaseGap of each other, so this only ever
			// fires the reset (or, once confirmed, the actual quit)
			// once the stream of repeats actually stops. This is also
			// what redraws the header/title bar (which tracks
			// quitHoldStart directly) promptly after a release, even
			// with no new key event to trigger a draw otherwise.
			a.checkQuitHoldRelease()
			// Also catches the background index (re)build finishing
			// while quick open is open with an unchanged query, so
			// matches don't go stale until the next keystroke.
			if a.overlay == views.OverlayQuickOpen {
				a.QuickOpen.RefreshMatches()
			}
			// Same idea for content search (SPEC.md §9.1): a query typed
			// before the index finished is held pending (Results stays
			// nil) until it's done, then run once here rather than on
			// every tick.
			if a.overlay == views.OverlaySearch && a.Search.Query != "" && a.Search.Results == nil && a.Search.Cancel == nil {
				if _, done := a.idx.Snapshot(); done {
					a.Search.RecomputeSearch()
				}
			}
			a.draw()
		}
	}
	return nil
}

// helpToggleKey is the sole key that opens/closes the help overlay
// (SPEC.md §5.4) — otherwise unused throughout the app, and the
// conventional "show help" binding in terminal UIs generally.
const helpToggleKey = '?'

// textInputActive reports whether the current context is one where
// every printable rune, including helpToggleKey itself, is live query
// input (quick open, content search, jump to file, in-file find) —
// `?` is also a quick open glob-match wildcard character (SPEC.md
// §4.2), so it must never be intercepted as the help toggle in any
// context where it could instead be typed.
func (a *App) textInputActive() bool {
	switch a.overlay {
	case views.OverlayQuickOpen, views.OverlaySearch:
		return true
	case views.OverlayBrowser:
		return a.Browser.JumpActive
	case views.OverlayNone:
		return a.Preview.FindPromptOpen
	}
	return false
}

func (a *App) handleKey(ev *tcell.EventKey) {
	if ev.Key() == tcell.KeyRune && ev.Rune() == helpToggleKey && !a.textInputActive() {
		// The help overlay (SPEC.md §5.4) is a passive HUD, not a modal
		// overlay: it never owns input, so toggling it is the only
		// effect this key has — whatever's currently active keeps
		// handling every other key exactly as it would otherwise.
		a.shared.HelpVisible = !a.shared.HelpVisible
		return
	}
	switch a.overlay {
	case views.OverlayBrowser:
		a.Browser.HandleKey(ev)
	case views.OverlayQuickOpen:
		a.QuickOpen.HandleKey(ev)
		// LastOpenedPath is quick open's outbox for a coordinator-level
		// concern (keeping the browser's disclosure/selection in sync
		// with a file opened from elsewhere, SPEC.md §4.2) that quick
		// open itself has no business knowing how to do — see its doc
		// comment.
		if p := a.QuickOpen.LastOpenedPath; p != "" {
			a.Browser.Reveal(p)
			a.QuickOpen.LastOpenedPath = ""
		}
	case views.OverlayOpenFiles:
		a.OpenFiles.HandleKey(ev)
	case views.OverlaySearch:
		a.Search.HandleKey(ev)
		// LastOpenedPath/Entry/Line are search's outbox for two
		// coordinator-level concerns it has no business performing
		// itself — see Search.LastOpenedPath's doc comment.
		if p := a.Search.LastOpenedPath; p != "" {
			a.Browser.Reveal(p)
			a.Preview.ScrollToLine(a.Search.LastOpenedEntry, a.Search.LastOpenedLine)
			a.Search.LastOpenedPath = ""
			a.Search.LastOpenedEntry = nil
		}
	case views.OverlayNone:
		if a.quitConfirmed {
			// The hold has already been held for the full duration —
			// the app is committed to quitting. Keep consuming `q`
			// key-repeat events (still arriving from the terminal for as
			// long as the physical key stays down) so checkQuitHoldRelease
			// can detect the eventual release and actually quit then,
			// rather than exiting immediately and letting those in-flight
			// repeats leak into the shell once the terminal returns to
			// normal mode. Every other key is ignored outright: nothing
			// should still be actionable mid-quit.
			if ev.Key() == tcell.KeyRune && ev.Rune() == 'q' {
				a.progressQuitHold()
			}
			return
		}
		action := a.Preview.HandleKey(ev)
		if action == views.ActionQuitKey {
			a.progressQuitHold()
		} else {
			a.resetQuitHold()
		}
		switch action {
		case views.ActionOpenBrowser:
			a.overlay = views.OverlayBrowser
		case views.ActionOpenFiles:
			a.overlay = views.OverlayOpenFiles
			a.OpenFiles.Selected = max(a.files.Displayed, 0)
		case views.ActionOpenQuickOpen:
			a.openQuickOpen()
		case views.ActionOpenSearch:
			a.openSearch()
		}
	}
}

// progressQuitHold records a `q` key-repeat event during the
// hold-to-quit gesture (SPEC.md §5.2), starting the hold on its first
// event and marking it confirmed once it's been held continuously for
// quitHoldDuration. Confirmation alone doesn't quit — see quitConfirmed's
// doc comment and checkQuitHoldRelease — so this keeps being called (and
// keeps bumping quitHoldLastKey) for `q` events that arrive after
// confirmation too.
func (a *App) progressQuitHold() {
	now := time.Now()
	if a.quitHoldStart.IsZero() {
		a.quitHoldStart = now
	}
	a.quitHoldLastKey = now
	if !a.quitConfirmed && now.Sub(a.quitHoldStart) >= quitHoldDuration {
		a.quitConfirmed = true
	}
}

// resetQuitHold cancels an in-progress, not-yet-confirmed hold-to-quit
// gesture (SPEC.md §5.2) — e.g. because a key other than `q` was
// pressed, or because checkQuitHoldRelease inferred `q` was released
// before reaching quitHoldDuration.
func (a *App) resetQuitHold() {
	a.quitHoldStart = time.Time{}
	a.quitConfirmed = false
}

// checkQuitHoldRelease infers whether an in-progress hold-to-quit
// gesture (SPEC.md §5.2) has been released: terminals deliver no
// key-up event, so a held key is inferred from a steady stream of
// repeat events, and too long a gap since the last one (quitHoldReleaseGap
// — deliberately generous, see its own doc comment) means the key is no
// longer down. Once the gesture is confirmed (held the full
// quitHoldDuration), a detected release is what actually quits — see
// quitConfirmed's doc comment for why this is deferred rather than
// quitting the instant quitHoldDuration elapses. The header/title bar's
// own show/hide (render.go) reads quitHoldStart directly rather than
// duplicating this check, so it's always in sync with whatever this
// function decides.
func (a *App) checkQuitHoldRelease() {
	if a.quitHoldStart.IsZero() {
		return
	}
	if time.Since(a.quitHoldLastKey) >= quitHoldReleaseGap {
		if a.quitConfirmed {
			a.quit = true
			return
		}
		a.resetQuitHold()
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
	a.Search.Open()
}
