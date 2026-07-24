package ui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/ui/views"
)

// TestProgressQuitHoldStartsTimerWithoutQuitting guards the first half
// of the hold-to-quit gesture (SPEC.md §5.2): a single `q` event starts
// the hold timer but does not, on its own, quit — only continuously
// holding for the full quitHoldDuration does.
func TestProgressQuitHoldStartsTimerWithoutQuitting(t *testing.T) {
	a := &App{}
	a.progressQuitHold()
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
	a.progressQuitHold()
	a.quitHoldStart = time.Now().Add(-quitHoldDuration)
	a.progressQuitHold()
	if !a.quitConfirmed {
		t.Fatal("progressQuitHold did not confirm the gesture once held for quitHoldDuration")
	}
	if a.quit {
		t.Fatal("progressQuitHold quit immediately on confirmation; want it deferred to a detected release")
	}
}

// TestCheckQuitHoldReleaseQuitsOnceConfirmedAndReleased guards the
// second half of the deferred-quit behavior: once a hold is confirmed,
// a subsequently-detected release (a gap in the `q` repeat stream) is
// what actually quits, using the tight steady-cadence gap since a
// confirmed hold has necessarily already seen several repeats.
func TestCheckQuitHoldReleaseQuitsOnceConfirmedAndReleased(t *testing.T) {
	a := &App{}
	a.progressQuitHold()
	a.progressQuitHold()
	a.quitConfirmed = true
	a.quitHoldLastKey = time.Now().Add(-(quitHoldReleaseGapSteady + time.Millisecond))
	a.checkQuitHoldRelease()
	if !a.quit {
		t.Fatal("checkQuitHoldRelease did not quit once a confirmed hold's repeat stream went quiet")
	}
}

// TestCheckQuitHoldReleaseDoesNotQuitConfirmedHoldStillRepeating is
// TestCheckQuitHoldReleaseQuitsOnceConfirmedAndReleased's counterpart:
// a confirmed hold with `q` still actively repeating (last event well
// within the steady gap) must not quit yet, however long it's already
// been confirmed for — only an actual release does.
func TestCheckQuitHoldReleaseDoesNotQuitConfirmedHoldStillRepeating(t *testing.T) {
	a := &App{}
	a.progressQuitHold()
	a.progressQuitHold()
	a.quitConfirmed = true
	a.quitHoldLastKey = time.Now().Add(-(quitHoldReleaseGapSteady - 20*time.Millisecond))
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
	a.progressQuitHold()
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
// `q` is only detectable as a gap in its key-repeat stream exceeding the
// release-gap threshold. With only a single `q` event so far (no repeat
// yet observed), that threshold is the generous initial one.
func TestCheckQuitHoldReleaseResetsAfterGap(t *testing.T) {
	a := &App{}
	a.progressQuitHold()
	a.quitHoldLastKey = time.Now().Add(-(quitHoldReleaseGapInitial + time.Millisecond))
	a.checkQuitHoldRelease()
	if !a.quitHoldStart.IsZero() {
		t.Fatal("checkQuitHoldRelease did not reset a hold whose repeat stream went quiet")
	}
}

// TestCheckQuitHoldReleaseKeepsHoldWithinGap is
// TestCheckQuitHoldReleaseResetsAfterGap's counterpart: a gap still
// under the initial threshold (e.g. a slow OS initial-repeat delay
// before the first auto-repeat has even fired) must not be mistaken for
// a release.
func TestCheckQuitHoldReleaseKeepsHoldWithinGap(t *testing.T) {
	a := &App{}
	a.progressQuitHold()
	a.quitHoldLastKey = time.Now().Add(-(quitHoldReleaseGapInitial - 50*time.Millisecond))
	a.checkQuitHoldRelease()
	if a.quitHoldStart.IsZero() {
		t.Fatal("checkQuitHoldRelease reset a hold still within the initial release-gap threshold")
	}
}

// TestCheckQuitHoldReleaseUsesTightGapOnceRepeating guards the
// responsiveness fix for releasing partway through an active hold: once
// a steady repeat cadence is established (a second `q` event has
// already arrived), a gap that would've been well within the generous
// initial threshold — but exceeds the much tighter steady one — is
// correctly read as a release, so the header reverts within a frame or
// two of the actual key-up rather than after the initial threshold's
// full window.
func TestCheckQuitHoldReleaseUsesTightGapOnceRepeating(t *testing.T) {
	a := &App{}
	a.progressQuitHold()
	a.progressQuitHold()
	gap := (quitHoldReleaseGapInitial + quitHoldReleaseGapSteady) / 2
	if gap <= quitHoldReleaseGapSteady || gap >= quitHoldReleaseGapInitial {
		t.Fatalf("test setup: gap %v isn't strictly between the steady and initial thresholds", gap)
	}
	a.quitHoldLastKey = time.Now().Add(-gap)
	a.checkQuitHoldRelease()
	if !a.quitHoldStart.IsZero() {
		t.Fatal("checkQuitHoldRelease used the generous initial gap instead of the tight steady one once a repeat was already observed")
	}
}

// TestCheckQuitHoldReleaseKeepsHoldWithinTightGapOnceRepeating is
// TestCheckQuitHoldReleaseUsesTightGapOnceRepeating's counterpart: once
// repeating, a gap still under the tight steady threshold (the ordinary
// spacing between OS key-repeat events) must not be mistaken for a
// release.
func TestCheckQuitHoldReleaseKeepsHoldWithinTightGapOnceRepeating(t *testing.T) {
	a := &App{}
	a.progressQuitHold()
	a.progressQuitHold()
	a.quitHoldLastKey = time.Now().Add(-(quitHoldReleaseGapSteady - 20*time.Millisecond))
	a.checkQuitHoldRelease()
	if a.quitHoldStart.IsZero() {
		t.Fatal("checkQuitHoldRelease reset a hold still within the steady release-gap threshold")
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
	a.quitHoldRepeats = 2
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
