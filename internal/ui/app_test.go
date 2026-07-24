package ui

import (
	"testing"
	"time"
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

// TestProgressQuitHoldQuitsOnceDurationElapsed backdates quitHoldStart
// to simulate quitHoldDuration having already elapsed since the hold
// began (SPEC.md §5.3's elapsed-time-driven discipline: this is
// verified with a synthetic duration, not a real sleep), then confirms
// the next `q` key-repeat event triggers the actual quit.
func TestProgressQuitHoldQuitsOnceDurationElapsed(t *testing.T) {
	a := &App{}
	a.progressQuitHold()
	a.quitHoldStart = time.Now().Add(-quitHoldDuration)
	a.progressQuitHold()
	if !a.quit {
		t.Fatal("progressQuitHold did not quit once held for quitHoldDuration")
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
// `q` is only detectable as a gap in its key-repeat stream exceeding
// quitHoldReleaseGap.
func TestCheckQuitHoldReleaseResetsAfterGap(t *testing.T) {
	a := &App{}
	a.progressQuitHold()
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
	a.progressQuitHold()
	a.quitHoldLastKey = time.Now().Add(-(quitHoldReleaseGap - 50*time.Millisecond))
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
