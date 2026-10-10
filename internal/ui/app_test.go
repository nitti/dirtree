package ui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/asyncjob"
	"github.com/nitti/dirtree/internal/index"
	"github.com/nitti/dirtree/internal/ui/views"
)

// TestProgressQuitHoldStartsTimerWithoutQuitting guards the first half
// of the hold-to-quit gesture (SPEC.md §5.2): a single `q` event starts
// the hold timer but does not, on its own, quit — only continuously
// holding for the full quitHoldDuration does.
func TestProgressQuitHoldStartsTimerWithoutQuitting(t *testing.T) {
	a := &App{}
	a.progressQuitHold(time.Now())
	if a.quitHoldStart.IsZero() {
		t.Fatal("progressQuitHold did not start the hold timer")
	}
	if a.quit {
		t.Fatal("a single q event quit the app; want it to only start the hold")
	}
}

// TestProgressQuitHoldConfirmsOnceDurationElapsedWithoutQuitting
// backdates quitHoldStart to simulate quitHoldDuration having already
// elapsed since the hold began (SPEC.md §5.3's elapsed-time-driven
// discipline: this is verified with a synthetic duration, not a real
// sleep), then confirms the next `q` key-repeat event marks the gesture
// confirmed — but still doesn't quit outright. Actually quitting is
// deferred to a subsequently-detected release (or continued holding),
// so a `q` still physically in flight when the duration elapses doesn't
// leak into the shell the instant the app exits — see quitConfirmed's
// doc comment.
func TestProgressQuitHoldConfirmsOnceDurationElapsedWithoutQuitting(t *testing.T) {
	a := &App{}
	a.progressQuitHold(time.Now())
	a.quitHoldStart = time.Now().Add(-quitHoldDuration)
	a.progressQuitHold(time.Now())
	if !a.quitConfirmed {
		t.Fatal("progressQuitHold did not confirm the gesture once held for quitHoldDuration")
	}
	if a.quit {
		t.Fatal("progressQuitHold quit immediately on confirmation; want it deferred to a detected release")
	}
}

// TestCheckQuitHoldReleaseQuitsOnceConfirmedAndReleased guards the
// second half of the deferred-quit behavior: once a hold is confirmed,
// a subsequently-detected release (a gap in the `q` repeat stream
// exceeding quitHoldReleaseGap) is what actually quits.
func TestCheckQuitHoldReleaseQuitsOnceConfirmedAndReleased(t *testing.T) {
	a := &App{}
	a.progressQuitHold(time.Now())
	a.quitConfirmed = true
	a.quitHoldLastKey = time.Now().Add(-(quitHoldReleaseGap + time.Millisecond))
	a.checkQuitHoldRelease()
	if !a.quit {
		t.Fatal("checkQuitHoldRelease did not quit once a confirmed hold's repeat stream went quiet")
	}
}

// TestCheckQuitHoldReleaseDoesNotQuitConfirmedHoldStillRepeating is
// TestCheckQuitHoldReleaseQuitsOnceConfirmedAndReleased's counterpart:
// a confirmed hold with `q` still actively repeating (last event well
// within quitHoldReleaseGap) must not quit yet, however long it's
// already been confirmed for — only an actual release does.
func TestCheckQuitHoldReleaseDoesNotQuitConfirmedHoldStillRepeating(t *testing.T) {
	a := &App{}
	a.progressQuitHold(time.Now())
	a.quitConfirmed = true
	a.quitHoldLastKey = time.Now().Add(-(quitHoldReleaseGap - 5*time.Millisecond))
	a.checkQuitHoldRelease()
	if a.quit {
		t.Fatal("checkQuitHoldRelease quit a confirmed hold whose `q` repeat stream hasn't actually gone quiet")
	}
}

// TestResetQuitHoldCancelsGesture guards the "any other key aborts the
// hold" behavior App.handleKey relies on: resetQuitHold must clear the
// in-progress hold so drawPreviewHeader stops showing the quitting
// variant and a fresh `q` press starts the timer over from zero.
func TestResetQuitHoldCancelsGesture(t *testing.T) {
	a := &App{}
	a.progressQuitHold(time.Now())
	a.resetQuitHold()
	if !a.quitHoldStart.IsZero() {
		t.Fatal("resetQuitHold left the hold timer running")
	}
	if a.quit {
		t.Fatal("resetQuitHold should never itself quit")
	}
}

// TestCheckQuitHoldReleaseResetsAfterGap guards the release-inference
// half of the gesture: terminals deliver no key-up event, so a released
// `q` is only detectable as a gap in its key-repeat stream exceeding
// quitHoldReleaseGap.
func TestCheckQuitHoldReleaseResetsAfterGap(t *testing.T) {
	a := &App{}
	a.progressQuitHold(time.Now())
	a.quitHoldLastKey = time.Now().Add(-(quitHoldReleaseGap + time.Millisecond))
	a.checkQuitHoldRelease()
	if !a.quitHoldStart.IsZero() {
		t.Fatal("checkQuitHoldRelease did not reset a hold whose repeat stream went quiet")
	}
}

// TestCheckQuitHoldReleaseKeepsHoldWithinGap is
// TestCheckQuitHoldReleaseResetsAfterGap's counterpart: a gap still
// under quitHoldReleaseGap (e.g. the ordinary spacing between OS
// key-repeat events) must not be mistaken for a release.
func TestCheckQuitHoldReleaseKeepsHoldWithinGap(t *testing.T) {
	a := &App{}
	a.progressQuitHold(time.Now())
	a.quitHoldLastKey = time.Now().Add(-(quitHoldReleaseGap - 5*time.Millisecond))
	a.checkQuitHoldRelease()
	if a.quitHoldStart.IsZero() {
		t.Fatal("checkQuitHoldRelease reset a hold still within quitHoldReleaseGap")
	}
}

// TestCheckQuitHoldReleaseNoopWhenNotHolding guards against
// checkQuitHoldRelease doing anything (in particular, touching
// quitHoldLastKey's stale value from a previous gesture) when no hold
// is currently in progress — it runs unconditionally on every
// resize-poll tick, most of which land while `q` isn't being held at
// all.
func TestCheckQuitHoldReleaseNoopWhenNotHolding(t *testing.T) {
	a := &App{}
	a.checkQuitHoldRelease()
	if !a.quitHoldStart.IsZero() || a.quit {
		t.Fatal("checkQuitHoldRelease acted while no hold was in progress")
	}
}

// TestHandleKeyIgnoresOtherKeysOnceConfirmed guards the confirmed-quit
// grace window (SPEC.md §5.2): once a hold is confirmed, the app must
// not act on any other key — e.g. `b` must not open the browser overlay
// mid-quit — while still tracking further `q` events so
// checkQuitHoldRelease can detect the eventual release.
func TestHandleKeyIgnoresOtherKeysOnceConfirmed(t *testing.T) {
	a := newTestApp(t, 60, 15)
	a.quitHoldStart = time.Now()
	a.quitHoldLastKey = time.Now()
	a.quitConfirmed = true

	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'b', tcell.ModNone))
	if a.overlay != views.OverlayNone {
		t.Fatal("handleKey acted on `b` while a hold-to-quit gesture was confirmed; want it ignored")
	}
	if !a.quitConfirmed {
		t.Fatal("handleKey cleared quitConfirmed on a non-`q` key; want the confirmed gesture left untouched")
	}

	before := a.quitHoldLastKey
	time.Sleep(time.Millisecond)
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone))
	if !a.quitHoldLastKey.After(before) {
		t.Fatal("handleKey did not bump quitHoldLastKey for a `q` event received while confirmed")
	}
	if a.quit {
		t.Fatal("a single further `q` event quit immediately; want release-gap detection to decide that")
	}
}

// TestTickInterval guards the main loop's two-cadence tick (SPEC.md
// §6.2): the fast animation cadence while anything on screen is
// time-driven, the idle resize-poll cadence otherwise. The index is
// built by hand rather than via index.Start so its done state is
// deterministic, and it finishes "instantly", under the badge's
// perceptibility threshold, so the badge itself stays hidden once done.
func TestTickInterval(t *testing.T) {
	tests := []struct {
		name      string
		indexDone bool
		setup     func(a *App)
		want      time.Duration
	}{
		{name: "idle", indexDone: true, setup: func(*App) {}, want: resizePollInterval},
		{name: "index still building", indexDone: false, setup: func(*App) {}, want: animationFrameInterval},
		{name: "toast showing", indexDone: true, setup: func(a *App) { a.showToast("x reloaded") }, want: animationFrameInterval},
		{name: "quit hold in progress", indexDone: true, setup: func(a *App) { a.progressQuitHold(time.Now()) }, want: animationFrameInterval},
		{name: "preview flash", indexDone: true, setup: func(a *App) { a.Preview.TopBumpFlashStart = time.Now() }, want: animationFrameInterval},
		{
			name:      "flash on a view that isn't showing",
			indexDone: true,
			setup:     func(a *App) { a.Browser.FlashStart = time.Now() },
			want:      resizePollInterval,
		},
		{
			name:      "flash on the active overlay",
			indexDone: true,
			setup: func(a *App) {
				a.overlay = views.OverlayBrowser
				a.Browser.FlashStart = time.Now()
			},
			want: animationFrameInterval,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestApp(t, 60, 15)
			a.idx = &index.Index{State: asyncjob.New()}
			if tt.indexDone {
				a.idx.MarkDone()
			}
			tt.setup(a)
			if got := a.tickInterval(time.Now()); got != tt.want {
				t.Errorf("tickInterval() = %v, want %v", got, tt.want)
			}
		})
	}
}

// simulateQHold feeds progressQuitHold a synthetic held-`q` event stream
// ending at end: the press, the first repeat initialDelay later, then
// steadyRepeats more repeats interval apart. Timestamps are synthetic,
// per SPEC.md §5.3's testing discipline, so no real sleeps are needed.
func simulateQHold(a *App, end time.Time, initialDelay, interval time.Duration, steadyRepeats int) {
	at := end.Add(-initialDelay - time.Duration(steadyRepeats)*interval)
	a.progressQuitHold(at)
	at = at.Add(initialDelay)
	a.progressQuitHold(at)
	for range steadyRepeats {
		at = at.Add(interval)
		a.progressQuitHold(at)
	}
}

// Repeat timing measured on the reference setup (Ghostty inside Zellij,
// macOS key-repeat settings): a 499ms initial delay, then repeats every
// ~83ms with ~3ms of jitter.
const (
	refInitialDelay   = 499 * time.Millisecond
	refRepeatInterval = 83 * time.Millisecond
)

// TestQuitHoldReleaseGapFor guards the adaptive release threshold
// (SPEC.md §5.2): generous before the first repeat, then a multiple of
// the measured repeat interval (this hold's, else the previous hold's),
// clamped between the floor and the generous gap.
func TestQuitHoldReleaseGapFor(t *testing.T) {
	tests := []struct {
		name             string
		events           int
		holdInterval     time.Duration
		previousInterval time.Duration
		want             time.Duration
	}{
		{name: "press only", events: 1, want: quitHoldReleaseGap},
		{name: "press only ignores previous hold", events: 1, previousInterval: refRepeatInterval, want: quitHoldReleaseGap},
		{name: "first repeat, nothing measured yet", events: 2, want: quitHoldReleaseGap},
		{name: "first repeat bridges with previous hold", events: 2, previousInterval: refRepeatInterval, want: 166 * time.Millisecond},
		{name: "one steady sample is enough", events: 3, holdInterval: refRepeatInterval, want: 166 * time.Millisecond},
		{name: "this hold's sample wins over previous", events: 3, holdInterval: 100 * time.Millisecond, previousInterval: refRepeatInterval, want: 200 * time.Millisecond},
		{name: "fast repeat rate hits the floor", events: 10, holdInterval: 30 * time.Millisecond, want: quitHoldMinReleaseGap},
		{name: "slow repeat rate is capped", events: 10, holdInterval: 400 * time.Millisecond, want: quitHoldReleaseGap},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := quitHoldReleaseGapFor(tt.events, tt.holdInterval, tt.previousInterval); got != tt.want {
				t.Errorf("quitHoldReleaseGapFor(%d, %v, %v) = %v, want %v", tt.events, tt.holdInterval, tt.previousInterval, got, tt.want)
			}
		})
	}
}

// TestProgressQuitHoldExcludesInitialDelayFromInterval guards that the
// press-to-first-repeat gap, the terminal's initial repeat delay, never
// counts as a steady interval: if it did, the adaptive threshold would
// always be inflated back to the generous gap.
func TestProgressQuitHoldExcludesInitialDelayFromInterval(t *testing.T) {
	a := &App{}
	simulateQHold(a, time.Now(), refInitialDelay, refRepeatInterval, 3)
	if a.quitHoldMaxInterval != refRepeatInterval {
		t.Fatalf("quitHoldMaxInterval = %v, want %v (the steady interval, not the initial delay)", a.quitHoldMaxInterval, refRepeatInterval)
	}
}

// TestProgressQuitHoldKeepsLargestInterval guards that jitter can only
// lengthen the threshold: one slow repeat raises quitHoldMaxInterval and
// faster ones after it don't lower it again.
func TestProgressQuitHoldKeepsLargestInterval(t *testing.T) {
	a := &App{}
	now := time.Now()
	simulateQHold(a, now.Add(-200*time.Millisecond), 400*time.Millisecond, 50*time.Millisecond, 2)
	a.progressQuitHold(now.Add(-80 * time.Millisecond)) // a 120ms jittery gap
	a.progressQuitHold(now.Add(-30 * time.Millisecond))
	a.progressQuitHold(now)
	if a.quitHoldMaxInterval != 120*time.Millisecond {
		t.Fatalf("quitHoldMaxInterval = %v, want 120ms (the largest steady interval)", a.quitHoldMaxInterval)
	}
}

// TestProgressQuitHoldSkipsBunchedIntervals guards that events arriving
// bunched together (e.g. flushed at once through a multiplexer) aren't
// mistaken for a near-zero repeat rate, which would shrink the
// threshold to the floor on a terminal that actually repeats slowly.
func TestProgressQuitHoldSkipsBunchedIntervals(t *testing.T) {
	a := &App{}
	start := time.Now().Add(-time.Second)
	a.progressQuitHold(start)
	a.progressQuitHold(start.Add(refInitialDelay))
	a.progressQuitHold(start.Add(refInitialDelay + 2*time.Millisecond))
	if a.quitHoldMaxInterval != 0 {
		t.Fatalf("quitHoldMaxInterval = %v after a bunched 2ms interval, want it left unsampled", a.quitHoldMaxInterval)
	}
}

// TestCheckQuitHoldReleaseUsesAdaptiveGap guards the user-visible point
// of the adaptive threshold at the reference repeat rate (threshold 2 *
// 83ms = 166ms): once a steady interval is known, a release registers
// after a couple of repeat intervals rather than the full generous gap,
// both for an unconfirmed hold (reset) and a confirmed one (quit), while
// a shorter gap leaves the hold alone.
func TestCheckQuitHoldReleaseUsesAdaptiveGap(t *testing.T) {
	tests := []struct {
		name         string
		sinceLastKey time.Duration
		confirmed    bool
		wantHeld     bool
		wantQuit     bool
	}{
		{name: "gap under threshold keeps hold", sinceLastKey: 120 * time.Millisecond, wantHeld: true},
		{name: "gap over threshold resets hold", sinceLastKey: 200 * time.Millisecond, wantHeld: false},
		{name: "confirmed hold under threshold doesn't quit", sinceLastKey: 120 * time.Millisecond, confirmed: true, wantHeld: true},
		{name: "confirmed hold over threshold quits", sinceLastKey: 200 * time.Millisecond, confirmed: true, wantHeld: true, wantQuit: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &App{}
			simulateQHold(a, time.Now().Add(-tt.sinceLastKey), refInitialDelay, refRepeatInterval, 1)
			a.quitConfirmed = tt.confirmed
			a.checkQuitHoldRelease()
			if held := !a.quitHoldStart.IsZero(); held != tt.wantHeld {
				t.Errorf("hold in progress = %v, want %v", held, tt.wantHeld)
			}
			if a.quit != tt.wantQuit {
				t.Errorf("quit = %v, want %v", a.quit, tt.wantQuit)
			}
		})
	}
}

// TestCancelJustAfterFirstRepeat guards the case that motivated
// carrying the repeat interval between holds: releasing mid-fade lands
// right around the first repeat, before this hold has a steady sample of
// its own. On a session's first hold that still waits the generous gap
// (nothing has been measured yet); on a later hold, the previous hold's
// interval lets the release register just as quickly as later in a hold.
func TestCancelJustAfterFirstRepeat(t *testing.T) {
	const sinceFirstRepeat = 200 * time.Millisecond

	first := &App{}
	simulateQHold(first, time.Now().Add(-sinceFirstRepeat), refInitialDelay, refRepeatInterval, 0)
	first.checkQuitHoldRelease()
	if first.quitHoldStart.IsZero() {
		t.Fatal("first hold of the session reset 200ms after its first repeat; want the generous gap, since no interval is known yet")
	}

	later := &App{}
	simulateQHold(later, time.Now().Add(-5*time.Second), refInitialDelay, refRepeatInterval, 5)
	later.resetQuitHold()
	simulateQHold(later, time.Now().Add(-sinceFirstRepeat), refInitialDelay, refRepeatInterval, 0)
	later.checkQuitHoldRelease()
	if !later.quitHoldStart.IsZero() {
		t.Fatal("later hold still held 200ms after its first repeat; want the previous hold's interval to set a tight threshold")
	}
}

// TestResetQuitHoldKeepsOnlyRepeatInterval guards what a reset clears:
// the hold's own progress and samples, but not the measured repeat
// interval carried to the next hold, and never the pre-repeat window's
// generous gap, which no measurement can safely shorten.
func TestResetQuitHoldKeepsOnlyRepeatInterval(t *testing.T) {
	a := &App{}
	simulateQHold(a, time.Now(), refInitialDelay, refRepeatInterval, 3)
	a.resetQuitHold()
	if a.quitHoldEvents != 0 || a.quitHoldMaxInterval != 0 {
		t.Fatalf("after reset: events = %d, hold interval = %v; want both cleared", a.quitHoldEvents, a.quitHoldMaxInterval)
	}
	if a.quitRepeatInterval != refRepeatInterval {
		t.Fatalf("after reset: carried interval = %v, want %v kept", a.quitRepeatInterval, refRepeatInterval)
	}
	a.progressQuitHold(time.Now())
	if got := quitHoldReleaseGapFor(a.quitHoldEvents, a.quitHoldMaxInterval, a.quitRepeatInterval); got != quitHoldReleaseGap {
		t.Fatalf("release gap after a fresh press = %v, want the generous %v until the first repeat", got, quitHoldReleaseGap)
	}
}
