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
	"github.com/nitti/dirtree/internal/watch"
)

type mode int

const (
	modeTree mode = iota
	modeJump
	modePreview
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
	previewMaxWidth           = 120
	minPreviewWidth           = 40
	minTreePaneWidth          = 20
	maxTreePaneWidth          = 60
)

// App holds all interactive state for a running session.
type App struct {
	screen   tcell.Screen
	rootPath string
	root     *tree.Node
	ignorer  *ignore.Multi
	idx      *index.Index
	watcher  *watch.Watcher

	mode     mode
	selected *tree.Node
	scroll   int

	// badgeSkipMinDuration is set once the user opens jump mode while
	// indexing is already done, so they've directly seen indexing is
	// ready (jump mode shows real matches immediately once done). It
	// tells the bottom-right badge to skip its minimum-display-duration
	// floor and jump straight to the completion step, rather than keep
	// pretending indexing is still running for a UI element the user has
	// already seen is finished. badgeSkipElapsedAt records a.idx.Elapsed()
	// at the moment the skip happened, and the badge treats that instant
	// as indexing's completion time — not the index's real (possibly
	// long-past) completion time — so the completion message/fade
	// sequence starts fresh from when the user opened jump mode instead
	// of potentially already being past its display+fade window. Both
	// reset whenever a new indexing cycle starts (handleFSChange's
	// Rebuild), since the flash-prevention floor is meaningful again for
	// that fresh run.
	badgeSkipMinDuration bool
	badgeSkipElapsedAt   time.Duration

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

	// Live refresh (SPEC.md §6a) is best-effort: if the OS notification
	// facility can't be initialized (e.g. inotify watch-limit exhausted),
	// the app still runs, it just won't auto-refresh.
	watcher, _ := watch.New(watchDebounce)

	a := &App{
		rootPath: rootPath,
		root:     root,
		ignorer:  ignorer,
		idx:      idx,
		watcher:  watcher,
		selected: root,
	}
	a.syncWatches()
	return a
}

// syncWatches adds an fs watch for every directory in the tree that has
// been loaded at least once (i.e. every directory whose current contents
// the UI could plausibly display), so RefreshTree's re-listing target
// set matches what's actually being watched. Idempotent and cheap to
// call after any action that might have loaded a new directory (expand,
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

// handleFSChange reacts to a debounced filesystem-change signal from the
// watcher (SPEC.md §6a): re-list every already-loaded directory, merging
// by path so unaffected nodes keep their identity/expand state, re-anchor
// the selection if the previously-selected node was deleted, kick off a
// background index rebuild so jump mode eventually reflects the change
// too, and pick up watches on any newly-loaded directories.
func (a *App) handleFSChange() {
	tree.RefreshTree(a.root, a.rootPath, a.ignorer)
	a.selected = tree.NearestSurviving(a.selected)
	a.idx.Rebuild(a.rootPath, a.ignorer)
	a.badgeSkipMinDuration = false
	a.badgeSkipElapsedAt = 0
	a.syncWatches()
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

	if a.watcher != nil {
		defer a.watcher.Close()
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
			if a.mode == modeJump {
				a.recomputeJumpMatches()
			}
			a.draw()
		case <-ticker.C:
			// Also catches the background index rebuild (kicked off by
			// handleFSChange) finishing while jump mode is still open
			// with an unchanged query, so matches don't go stale.
			if a.mode == modeJump {
				a.recomputeJumpMatches()
			}
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
		a.syncWatches()
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
		if _, done := a.idx.Snapshot(); done && !a.badgeSkipMinDuration {
			a.badgeSkipMinDuration = true
			a.badgeSkipElapsedAt = a.idx.Elapsed()
		}
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
				a.syncWatches()
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

// badgeText returns what the bottom-right status badge should show
// this frame: the running spinner, the transient "indexing complete"
// message (full or mid fade-out), or nothing.
func (a *App) badgeText() (text string, hiddenPrefix int, ok bool) {
	elapsed := a.idx.Elapsed()
	sinceDone, done := a.idx.SinceDone()
	return badgeDecision(elapsed, sinceDone, done, spinner.DebugAlwaysShow, a.badgeSkipMinDuration, a.badgeSkipElapsedAt)
}

// badgeDecision is badgeText's terminal-free decision logic, kept as a
// pure function of elapsed/sinceDone durations so it can be unit tested
// without waiting on real indexing timing. hiddenPrefix is how many of
// text's leading runes should be skipped when drawing, to animate the
// completion message fading away left-to-right while its right edge
// stays anchored in place.
//
// The full sequence: nothing is shown until indexing has been running
// long enough to be perceptible (spinnerThreshold); then the spinner
// shows, guaranteed visible for at least spinnerMinDisplayDuration even
// if indexing genuinely finishes sooner (real directories often do, in
// microseconds — without this floor the spinner could cross the
// threshold and complete in the same frame, an unreadable flash); then
// it hands off to the "indexing complete" message for
// completionDisplayDuration; then that message fades out over
// completionFadeDuration. If indexing finishes before ever crossing
// spinnerThreshold, none of this is shown at all — the spinner was
// never perceptible, so announcing its completion would be exactly the
// flashing-chrome-for-instant-work distraction the threshold exists to
// avoid.
//
// debugAlwaysShow (spinner.DebugAlwaysShow: a build-time-only escape
// hatch, never set in a shipped build) bypasses only spinnerThreshold —
// the spinner appears the instant indexing starts — while every other
// timing above (the minimum display duration, the completion message,
// the fade-out) behaves exactly as it would otherwise, so the full
// sequence can be watched on demand without needing a genuinely slow
// index.
//
// skipMinDuration (App.badgeSkipMinDuration) drops the minimum-display-
// duration floor entirely, jumping straight to the completion step as
// of skipElapsedAt (App.badgeSkipElapsedAt: a.idx.Elapsed() at the
// moment the skip happened) rather than the index's real completion
// time — which may already be well past the completion message's
// display+fade window (e.g. debug mode had been artificially holding
// the spinner via the minimum-display-duration floor for a while
// before the skip), in which case using the real completion time would
// make the badge vanish immediately instead of visibly transitioning.
// Treating the skip's own moment as the completion instant instead
// means the display+fade sequence always restarts fresh right when the
// skip happens. This is set once the user opens jump mode while
// indexing is already done — jump mode shows real matches immediately
// in that case, so the user has already directly seen indexing is
// ready, and continuing to hold the badge on a "still indexing"
// spinner they've just seen is finished would be actively misleading
// rather than a perceptibility safeguard.
func badgeDecision(elapsed, sinceDone time.Duration, done, debugAlwaysShow, skipMinDuration bool, skipElapsedAt time.Duration) (text string, hiddenPrefix int, ok bool) {
	// doneAt is the wall-clock time since indexing started at which it
	// actually finished; meaningless (and unused) while still running.
	doneAt := elapsed - sinceDone

	var crossedThreshold bool
	switch {
	case debugAlwaysShow:
		crossedThreshold = true
	case done:
		crossedThreshold = doneAt >= spinnerThreshold
	default:
		crossedThreshold = elapsed >= spinnerThreshold
	}
	if !crossedThreshold {
		return "", 0, false
	}

	if done {
		if skipMinDuration {
			doneAt = skipElapsedAt
		} else {
			doneAt = max(doneAt, spinnerMinDisplayDuration)
		}
		done = elapsed >= doneAt
		sinceDone = elapsed - doneAt
	}

	if !done {
		frame := spinner.Frame(elapsed, spinnerFPS, spinner.DefaultFrames)
		return "indexing " + string(frame), 0, true
	}

	phase, faded := spinner.Completion(sinceDone, completionDisplayDuration, completionFadeDuration, len(completionMessage))
	if phase == spinner.CompletionHidden {
		return "", 0, false
	}
	return completionMessage, faded, true
}
