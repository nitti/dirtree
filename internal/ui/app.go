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
	// resizePollInterval and animationFrameInterval are the main loop's
	// two tick cadences (SPEC.md §6.2). Key events always redraw
	// immediately, so neither affects input latency; the tick only
	// covers what changes with no input. Idle, that's terminal
	// resizes the multiplexer didn't announce, for which 100ms reads as
	// prompt. While anything on screen is animating (App.animating),
	// the loop ticks at ~30fps instead, so fades and spinners advance
	// smoothly and expiring flashes clear on time. That rate is plenty
	// for a cell grid whose fades step one character at a time, and
	// terminal emulators and multiplexers batch output well below a
	// display's refresh rate anyway. Animations are elapsed-time-driven
	// (§5.3), so this changes their smoothness, never their speed.
	resizePollInterval        = 100 * time.Millisecond
	animationFrameInterval    = 33 * time.Millisecond
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
	// quitHoldReleaseGap, quitHoldReleaseFactor, and quitHoldMinReleaseGap
	// decide when a hold-to-quit gesture (SPEC.md §5.2) counts as
	// released. Terminals send no key-up event, so a held `q` can only be
	// inferred from its stream of key-repeat events, and a gap in that
	// stream is the only release signal. The trouble is that a real
	// terminal's repeat timing has two very different gaps: the initial
	// delay before a held key starts repeating at all (a few hundred ms,
	// OS-configurable), then a much shorter steady interval between
	// repeats (tens of ms, also configurable). No single fixed threshold
	// is both safe and fast. One generous enough for the initial delay
	// makes every release lag by that much. One tight enough to feel
	// responsive mistakes the initial delay, or a slow terminal's steady
	// interval, for a release. That flickers the header and silently
	// restarts the hold, which a 300ms fixed threshold measurably did on
	// real hardware.
	//
	// So the threshold adapts to the repeat rate actually measured
	// (quitHoldReleaseGapFor). Before the first repeat arrives it is
	// quitHoldReleaseGap, the 600ms value empirically confirmed safe
	// across the initial delay: until then a held key and a released one
	// look identical, so nothing shorter is safe. Once a steady
	// repeat-to-repeat interval is known, it becomes quitHoldReleaseFactor
	// times that interval, floored at quitHoldMinReleaseGap and capped at
	// quitHoldReleaseGap. The interval is this hold's largest sample once
	// it has one (from its second repeat on); between the first and
	// second repeat it falls back to the previous hold's measurement,
	// since the repeat rate is an OS setting that doesn't change between
	// holds. That bridge matters because releasing mid-fade lands right
	// around the first repeat (measured on the reference setup, Ghostty
	// in Zellij: 499ms initial delay, then 83ms ±3ms repeats), and
	// without it that release waited the full 600ms after the first repeat.
	//
	// Measuring rather than guessing is what makes the tight threshold
	// safe on a slow-repeating terminal, and using the largest interval
	// means jitter can only lengthen it. Intervals under
	// quitHoldMinRepeatSample are events that arrived bunched together
	// (e.g. flushed at once through a multiplexer), not a real repeat
	// rate, so they aren't sampled. The floor keeps an unusually fast
	// repeat rate from shrinking the threshold to something a single
	// event-loop hiccup could exceed.
	//
	// This one threshold is the single source of truth for "is `q` still
	// down": checkQuitHoldRelease resets the hold (or, once confirmed,
	// quits) on it, and render.go shows the quitting header purely from
	// quitHoldStart being non-zero, so the two can never disagree. An
	// earlier attempt at a separate, shorter visual-only threshold
	// flickered the header throughout ordinary holds. Checked on the main
	// loop's ticker, which runs at animationFrameInterval for the whole
	// hold (App.animating), so detection lags the threshold by at most
	// one ~33ms tick.
	quitHoldReleaseGap      = 600 * time.Millisecond
	quitHoldReleaseFactor   = 2
	quitHoldMinReleaseGap   = 150 * time.Millisecond
	quitHoldMinRepeatSample = 20 * time.Millisecond
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

	// idxWasDone is whether the background index was done as of the
	// previous tick, so Run refreshes quick open's matches once on the
	// index finishing rather than re-matching the whole index every tick.
	idxWasDone bool

	// quitHoldStart is when the currently-in-progress hold-to-quit
	// gesture (SPEC.md §5.2) began holding `q`, zero when no hold is in
	// progress — render.go reads this directly (zero vs. non-zero) to
	// decide whether to draw the header/title bar's attention-grabbing
	// quitting variant, the same state checkQuitHoldRelease itself resets
	// via its release threshold, so the header is visible for exactly as
	// long as the app believes `q` is down.
	// quitHoldLastKey is the most recent `q` key-repeat event's time.
	// quitHoldEvents counts this hold's `q` events, and
	// quitHoldMaxInterval is the largest steady repeat-to-repeat interval
	// among them (excluding the press-to-first-repeat initial delay),
	// which quitHoldReleaseGapFor turns into the release threshold.
	// quitRepeatInterval carries the latest hold's measurement over to
	// the next one; unlike the other quitHold* fields, resetQuitHold
	// leaves it alone.
	// quitConfirmed is set once the hold has been held for the full
	// quitHoldDuration: the app doesn't quit the instant that happens,
	// only once a release is subsequently detected (or, resetQuitHold is
	// never called while this is set) — this drains any `q` key-repeat
	// events still in flight from the terminal through the app's own
	// event loop instead of letting them leak into the shell once the
	// process actually exits and the terminal returns to normal mode.
	quitHoldStart       time.Time
	quitHoldLastKey     time.Time
	quitHoldEvents      int
	quitHoldMaxInterval time.Duration
	quitRepeatInterval  time.Duration
	quitConfirmed       bool

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
	// Rebuild has just reset the index to not-done. Record that here
	// rather than waiting for the next tick to observe it: a fast
	// rebuild could finish before then, and Run would miss the edge.
	a.idxWasDone = false
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
	tickInterval := resizePollInterval
	ticker := time.NewTicker(tickInterval)
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
			// re-triggering progressQuitHold well within its release
			// threshold (quitHoldReleaseGapFor), so this only ever
			// fires the reset (or, once confirmed, the actual quit)
			// once the stream of repeats actually stops. This is also
			// what redraws the header/title bar (which tracks
			// quitHoldStart directly) promptly after a release, even
			// with no new key event to trigger a draw otherwise.
			a.checkQuitHoldRelease()
			// Also catches the background index (re)build finishing
			// while quick open is open with an unchanged query, so
			// matches don't go stale until the next keystroke. Only on
			// the not-done-to-done edge: re-matching an unchanged
			// query against an unchanged index finds nothing new.
			_, idxDone := a.idx.Snapshot()
			if idxDone && !a.idxWasDone && a.overlay == views.OverlayQuickOpen {
				a.QuickOpen.RefreshMatches()
			}
			a.idxWasDone = idxDone
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
		if want := a.tickInterval(time.Now()); want != tickInterval {
			tickInterval = want
			ticker.Reset(tickInterval)
		}
	}
	return nil
}

// tickInterval picks the main loop's tick cadence for the frame just
// drawn (SPEC.md §6.2): animationFrameInterval while anything on screen
// is animating, resizePollInterval otherwise. Evaluated after each
// draw, so the frame that clears an expired animation is still drawn at
// the fast cadence and only the ticks after it slow down.
func (a *App) tickInterval(now time.Time) time.Duration {
	if a.animating(now) {
		return animationFrameInterval
	}
	return resizePollInterval
}

// animating reports whether anything currently on screen is
// time-driven (SPEC.md §5.3): the hold-to-quit fade (whose release
// detection also gets more responsive at the faster tick), a toast, the
// corner badge, or the active view's own flashes, spinners, and pending
// background results (each view's Animating method). The badge counts
// from the moment the index starts building, not just once its spinner
// is visible, so the loop is already ticking fast when the spinner's
// perceptibility threshold passes.
func (a *App) animating(now time.Time) bool {
	if !a.quitHoldStart.IsZero() || a.toastMessage != "" {
		return true
	}
	if a.badgeAnimating() {
		return true
	}
	switch a.overlay {
	case views.OverlayBrowser:
		return a.Browser.Animating(now)
	case views.OverlayQuickOpen:
		return a.QuickOpen.Animating(now)
	case views.OverlaySearch:
		return a.Search.Animating(now)
	default:
		// OverlayNone and OverlayOpenFiles both draw the preview.
		return a.Preview.Animating(now)
	}
}

// badgeAnimating reports whether the corner badge (SPEC.md §5.2) is
// either waiting on the index or still showing, using the same
// decision drawBadge does.
func (a *App) badgeAnimating() bool {
	sinceDone, done := a.idx.SinceDone()
	if !done {
		return true
	}
	_, _, ok := spinner.BadgeDecision(
		a.idx.Elapsed(), sinceDone, done, spinner.DebugAlwaysShow, a.badgeSkip,
		spinnerThreshold, spinnerMinDisplayDuration, toastDisplayDuration, toastFadeDuration,
		spinnerFPS, completionMessage,
	)
	return ok
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
		return a.Preview.FindPromptOpen || a.Preview.HexFindPromptOpen
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
				a.progressQuitHold(ev.When())
			}
			return
		}
		action := a.Preview.HandleKey(ev)
		if action == views.ActionQuitKey {
			a.progressQuitHold(ev.When())
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
//
// at is when the event was read from the terminal (tcell's
// EventKey.When), not when the main loop got to it, so time an event
// spent queued behind a slow draw doesn't distort the repeat intervals
// the release threshold is measured from. Events 1 and 2 are the press
// and the first repeat, whose gap is the terminal's initial repeat
// delay, so steady intervals are only sampled from event 3 on, and
// bunched intervals under quitHoldMinRepeatSample are skipped.
func (a *App) progressQuitHold(at time.Time) {
	if a.quitHoldStart.IsZero() {
		a.quitHoldStart = at
	} else if gap := at.Sub(a.quitHoldLastKey); a.quitHoldEvents >= 2 && gap >= quitHoldMinRepeatSample {
		a.quitHoldMaxInterval = max(a.quitHoldMaxInterval, gap)
		a.quitRepeatInterval = a.quitHoldMaxInterval
	}
	a.quitHoldEvents++
	a.quitHoldLastKey = at
	if !a.quitConfirmed && at.Sub(a.quitHoldStart) >= quitHoldDuration {
		a.quitConfirmed = true
	}
}

// quitHoldReleaseGapFor returns the release threshold for a hold that
// has seen events `q` events, the largest steady interval among them
// being holdInterval (0 if none sampled yet), with previousInterval the
// last hold's measurement (0 if none). Before any repeat (events < 2) it
// is the generous quitHoldReleaseGap. After that it is
// quitHoldReleaseFactor times holdInterval, or previousInterval while
// holdInterval is still unknown, clamped to [quitHoldMinReleaseGap,
// quitHoldReleaseGap]; with neither known it stays generous. See
// quitHoldReleaseGap's doc comment for why.
func quitHoldReleaseGapFor(events int, holdInterval, previousInterval time.Duration) time.Duration {
	interval := holdInterval
	if interval == 0 {
		interval = previousInterval
	}
	if events < 2 || interval == 0 {
		return quitHoldReleaseGap
	}
	return min(max(quitHoldReleaseFactor*interval, quitHoldMinReleaseGap), quitHoldReleaseGap)
}

// resetQuitHold cancels an in-progress, not-yet-confirmed hold-to-quit
// gesture (SPEC.md §5.2) — e.g. because a key other than `q` was
// pressed, or because checkQuitHoldRelease inferred `q` was released
// before reaching quitHoldDuration.
func (a *App) resetQuitHold() {
	a.quitHoldStart = time.Time{}
	a.quitHoldEvents = 0
	a.quitHoldMaxInterval = 0
	a.quitConfirmed = false
}

// checkQuitHoldRelease infers whether an in-progress hold-to-quit
// gesture (SPEC.md §5.2) has been released: terminals deliver no
// key-up event, so a held key is inferred from a steady stream of
// repeat events, and too long a gap since the last one
// (quitHoldReleaseGapFor, adapted to this hold's own repeat rate — see
// quitHoldReleaseGap's doc comment) means the key is no longer down.
// Once the gesture is confirmed (held the full quitHoldDuration), a
// detected release is what actually quits — see
// quitConfirmed's doc comment for why this is deferred rather than
// quitting the instant quitHoldDuration elapses. The header/title bar's
// own show/hide (render.go) reads quitHoldStart directly rather than
// duplicating this check, so it's always in sync with whatever this
// function decides.
func (a *App) checkQuitHoldRelease() {
	if a.quitHoldStart.IsZero() {
		return
	}
	if time.Since(a.quitHoldLastKey) >= quitHoldReleaseGapFor(a.quitHoldEvents, a.quitHoldMaxInterval, a.quitRepeatInterval) {
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
