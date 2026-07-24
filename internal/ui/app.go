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
	// quitHoldReleaseGapInitial is the release-gap threshold used until a
	// steady `q` key-repeat cadence is actually known (see
	// App.quitHoldReleaseThreshold): terminals deliver no key-up event,
	// so a held key can only be inferred from a steady stream of repeat
	// events, and early in a hold there's no way to tell "still holding,
	// first/second OS auto-repeat just hasn't fired yet" apart from
	// "already released" — so the threshold is set generously above a
	// typical OS's slow initial-repeat delay to avoid a false release.
	quitHoldReleaseGapInitial = 600 * time.Millisecond
	// quitHoldReleaseMultiplier, quitHoldReleaseGapFloor, and
	// quitHoldReleaseGapCeiling shape the *adaptive* release-gap
	// threshold used once a steady repeat cadence is known — see
	// App.quitHoldReleaseThreshold. The multiplier is applied to the most
	// recently observed inter-repeat interval rather than a fixed guess,
	// since real terminals/OSes vary widely in their auto-repeat rate:
	// a fixed threshold generous enough for a slow-repeating terminal
	// feels sluggish on a fast one, and a fixed threshold tight enough to
	// feel instant on a fast one falsely fires mid-hold on a slow one.
	// The floor guards against an anomalously tiny single interval
	// (e.g. two events that happened to land unusually close together)
	// swinging the threshold down to something that would false-trigger
	// on perfectly ordinary jitter; the ceiling guards symmetrically
	// against an anomalously large one.
	quitHoldReleaseMultiplier = 2.0
	quitHoldReleaseGapFloor   = 40 * time.Millisecond
	quitHoldReleaseGapCeiling = 250 * time.Millisecond
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
	// progress — render.go reads it directly to decide whether to draw
	// the header/title bar's attention-grabbing quitting variant.
	// quitHoldLastKey is the most recent `q` key-repeat event's time,
	// used by checkQuitHoldRelease to infer a release from a gap in the
	// repeat stream, since terminals deliver no key-up event.
	// quitHoldRepeats counts `q` events seen so far in the current hold,
	// so quitHoldReleaseThreshold can tell an established repeat cadence
	// (tight, adaptive release-gap threshold) from just the first couple
	// of events (generous fixed threshold, to survive a slow initial OS
	// repeat delay). quitHoldLastInterval is the gap between the two most
	// recent `q` events, the basis for that adaptive threshold once the
	// cadence is established.
	// quitConfirmed is set once the hold has been held for the full
	// quitHoldDuration: the app doesn't quit the instant that happens,
	// only once a release is subsequently detected (or, resetQuitHold is
	// never called while this is set) — this drains any `q` key-repeat
	// events still in flight from the terminal through the app's own
	// event loop instead of letting them leak into the shell once the
	// process actually exits and the terminal returns to normal mode.
	// quitHoldTimer fires exactly quitHoldReleaseThreshold() after the
	// most recent `q` event (reset on every subsequent one, by
	// rescheduleQuitHoldTimer) — Run's event loop selects on its channel
	// so a release registers the instant the threshold elapses, rather
	// than being discovered on whatever's left of the next coarse
	// resize-poll tick.
	quitHoldStart        time.Time
	quitHoldLastKey      time.Time
	quitHoldLastInterval time.Duration
	quitHoldRepeats      int
	quitConfirmed        bool
	quitHoldTimer        *time.Timer

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
		// quitHoldTimerC is nil (a permanently-blocking select case)
		// whenever no hold-to-quit gesture is in progress; once one
		// starts, quitHoldTimer fires exactly quitHoldReleaseThreshold()
		// after the most recent `q` event (reset by every subsequent
		// one), so a release registers the instant that threshold
		// elapses rather than being discovered on whatever's left of the
		// next coarse resize-poll tick (SPEC.md §5.2).
		var quitHoldTimerC <-chan time.Time
		if a.quitHoldTimer != nil {
			quitHoldTimerC = a.quitHoldTimer.C
		}
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
		case <-quitHoldTimerC:
			// The hold-to-quit gesture's release-gap threshold (SPEC.md
			// §5.2) has elapsed since the last `q` event with no
			// further one arriving to reschedule this timer — a
			// release, detected the instant it's inferable rather than
			// on the next resize-poll tick.
			a.checkQuitHoldRelease()
			a.draw()
		case <-ticker.C:
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
// confirmation too. Also (re)schedules quitHoldTimer, so a release is
// detected the instant the current release-gap threshold elapses rather
// than on the next resize-poll tick.
func (a *App) progressQuitHold() {
	now := time.Now()
	if a.quitHoldStart.IsZero() {
		a.quitHoldStart = now
		a.quitHoldRepeats = 0
		a.quitHoldLastInterval = 0
	} else {
		a.quitHoldLastInterval = now.Sub(a.quitHoldLastKey)
	}
	a.quitHoldLastKey = now
	a.quitHoldRepeats++
	if !a.quitConfirmed && now.Sub(a.quitHoldStart) >= quitHoldDuration {
		a.quitConfirmed = true
	}
	a.rescheduleQuitHoldTimer()
}

// resetQuitHold cancels an in-progress, not-yet-confirmed hold-to-quit
// gesture (SPEC.md §5.2) — e.g. because a key other than `q` was
// pressed, or because checkQuitHoldRelease inferred `q` was released
// before reaching quitHoldDuration.
func (a *App) resetQuitHold() {
	a.quitHoldStart = time.Time{}
	a.quitHoldLastInterval = 0
	a.quitHoldRepeats = 0
	a.quitConfirmed = false
	if a.quitHoldTimer != nil {
		a.quitHoldTimer.Stop()
	}
}

// rescheduleQuitHoldTimer (re)arms quitHoldTimer to fire exactly
// quitHoldReleaseThreshold() from now — called after every `q` event
// that keeps a hold going, so the timer always reflects the threshold
// as of the most recent event.
func (a *App) rescheduleQuitHoldTimer() {
	gap := a.quitHoldReleaseThreshold()
	if a.quitHoldTimer == nil {
		a.quitHoldTimer = time.NewTimer(gap)
		return
	}
	if !a.quitHoldTimer.Stop() {
		select {
		case <-a.quitHoldTimer.C:
		default:
		}
	}
	a.quitHoldTimer.Reset(gap)
}

// quitHoldReleaseThreshold returns the release-gap threshold currently
// in effect for the hold-to-quit gesture (SPEC.md §5.2): the fixed,
// generous quitHoldReleaseGapInitial until a steady repeat cadence is
// actually known — the interval between the *first* and *second* `q`
// event reflects the OS's initial-repeat delay, not its steady repeat
// rate, so it isn't a trustworthy baseline yet; only the interval
// between the second and third event onward is. From the third event
// on, the threshold adapts to quitHoldReleaseMultiplier times the most
// recently observed inter-repeat interval (quitHoldLastInterval),
// clamped to [quitHoldReleaseGapFloor, quitHoldReleaseGapCeiling] — this
// is what makes a release register within roughly one repeat interval
// on a fast-auto-repeating terminal, without falsely triggering mid-hold
// on a slower one, rather than living or dying by a single fixed guess.
func (a *App) quitHoldReleaseThreshold() time.Duration {
	if a.quitHoldRepeats < 3 {
		return quitHoldReleaseGapInitial
	}
	gap := time.Duration(float64(a.quitHoldLastInterval) * quitHoldReleaseMultiplier)
	if gap < quitHoldReleaseGapFloor {
		return quitHoldReleaseGapFloor
	}
	if gap > quitHoldReleaseGapCeiling {
		return quitHoldReleaseGapCeiling
	}
	return gap
}

// checkQuitHoldRelease infers whether an in-progress hold-to-quit
// gesture (SPEC.md §5.2) has been released: terminals deliver no
// key-up event, so a held key is inferred from a steady stream of
// repeat events, and too long a gap since the last one (per
// quitHoldReleaseThreshold) means the key is no longer down. Once the
// gesture is confirmed (held the full quitHoldDuration), a detected
// release is what actually quits — see quitConfirmed's doc comment for
// why this is deferred rather than quitting the instant quitHoldDuration
// elapses.
func (a *App) checkQuitHoldRelease() {
	if a.quitHoldStart.IsZero() {
		return
	}
	if time.Since(a.quitHoldLastKey) >= a.quitHoldReleaseThreshold() {
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
