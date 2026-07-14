package ui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/ignore"
	"github.com/nitti/dirtree/internal/index"
)

func waitIndexDone(t *testing.T, idx *index.Index) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, done := idx.Snapshot(); done {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("index did not finish building in time")
}

func TestBadgeTextHidesCompletionMessageForInstantIndexing(t *testing.T) {
	a := testApp(t)
	a.idx = index.Start(a.rootPath, nil)
	waitIndexDone(t, a.idx)

	// A tiny test directory indexes in microseconds — well under the
	// spinner's perceptibility threshold, so the spinner itself never
	// showed. The completion message must not appear either: announcing
	// the completion of work nobody saw start would be exactly the
	// flashing-chrome-for-instant-work problem the threshold exists to
	// avoid.
	_, _, ok := a.badgeText()
	if ok {
		t.Fatal("expected no completion message for indexing that finished under the perceptibility threshold")
	}
}

func TestBadgeDecisionHidesSpinnerBelowThreshold(t *testing.T) {
	text, hiddenPrefix, ok := badgeDecision(100*time.Millisecond, 0, false, false, false, 0)
	if ok {
		t.Fatalf("expected spinner hidden below threshold, got text=%q hiddenPrefix=%d", text, hiddenPrefix)
	}
}

func TestBadgeDecisionShowsSpinnerAboveThreshold(t *testing.T) {
	text, _, ok := badgeDecision(300*time.Millisecond, 0, false, false, false, 0)
	if !ok || text == "" {
		t.Fatalf("expected spinner shown above threshold, got text=%q ok=%v", text, ok)
	}
}

func TestBadgeDecisionHidesCompletionMessageForFastIndexing(t *testing.T) {
	// Indexing started, ran for 50ms (well under the 250ms threshold),
	// and finished 10ms ago: elapsed=60ms, sinceDone=10ms, so it was
	// done at the 50ms mark — never long enough to show the spinner.
	_, _, ok := badgeDecision(60*time.Millisecond, 10*time.Millisecond, true, false, false, 0)
	if ok {
		t.Fatal("expected no completion message when indexing finished before the spinner threshold")
	}
}

func TestBadgeDecisionExtendsSpinnerToMinimumDisplayDuration(t *testing.T) {
	// Real indexing crossed the threshold at 300ms (so the spinner
	// legitimately started) but finished shortly after — well short of
	// the 1s minimum display duration. At elapsed=400ms (sinceDone=100ms,
	// so doneAt=300ms) the spinner must still be showing, not the
	// completion message, since holding it to the minimum isn't done yet.
	text, _, ok := badgeDecision(400*time.Millisecond, 100*time.Millisecond, true, false, false, 0)
	if !ok || text == completionMessage {
		t.Fatalf("expected spinner still extended toward the minimum display duration, got text=%q ok=%v", text, ok)
	}
}

func TestBadgeDecisionHandsOffAtMinimumDisplayDuration(t *testing.T) {
	// Same real completion (doneAt=300ms) as above, but now elapsed has
	// reached exactly the 1s minimum display duration (sinceDone=700ms):
	// the spinner's forced minimum has now elapsed, so it should hand off
	// to the completion message in full.
	text, hiddenPrefix, ok := badgeDecision(1*time.Second, 700*time.Millisecond, true, false, false, 0)
	if !ok || text != completionMessage || hiddenPrefix != 0 {
		t.Fatalf("expected completion message right at the minimum display duration, got text=%q hiddenPrefix=%d ok=%v", text, hiddenPrefix, ok)
	}
}

func TestBadgeDecisionShowsCompletionMessageForSlowIndexing(t *testing.T) {
	// Indexing ran for 1.5s (over both the threshold and the minimum
	// display duration) and just finished, so no extension is needed —
	// the completion message shows immediately.
	text, hiddenPrefix, ok := badgeDecision(1500*time.Millisecond, 0, true, false, false, 0)
	if !ok || text != completionMessage || hiddenPrefix != 0 {
		t.Fatalf("expected full completion message right after slow indexing finishes, got text=%q hiddenPrefix=%d ok=%v", text, hiddenPrefix, ok)
	}
}

func TestBadgeDecisionHidesCompletionMessageAfterFadeOut(t *testing.T) {
	sinceDone := completionDisplayDuration + completionFadeDuration + 50*time.Millisecond
	_, _, ok := badgeDecision(1500*time.Millisecond+sinceDone, sinceDone, true, false, false, 0)
	if ok {
		t.Fatal("expected completion message to be gone well after its display+fade window")
	}
}

func TestBadgeDecisionDebugModeBypassesThreshold(t *testing.T) {
	text, _, ok := badgeDecision(10*time.Millisecond, 0, false, true, false, 0)
	if !ok || text == "" {
		t.Fatalf("expected spinner shown immediately in debug mode despite being under the threshold, got text=%q ok=%v", text, ok)
	}
}

func TestBadgeDecisionDebugModeStillEnforcesMinimumDisplayDuration(t *testing.T) {
	// Real indexing finished almost instantly (doneAt=1ms), but only
	// 500ms of elapsed time has passed — still under the 1s minimum
	// display duration, so even in debug mode the spinner must still be
	// showing rather than the completion message.
	text, _, ok := badgeDecision(500*time.Millisecond, 499*time.Millisecond, true, true, false, 0)
	if !ok || text == completionMessage {
		t.Fatalf("expected spinner still showing before the minimum display duration elapses in debug mode, got text=%q ok=%v", text, ok)
	}
}

func TestBadgeDecisionDebugModeCompletesSequenceAfterMinimumDisplayDuration(t *testing.T) {
	// Same near-instant real completion as above, but now 1.2s has
	// elapsed — past the 1s minimum display duration — so it should have
	// handed off to the completion message.
	text, hiddenPrefix, ok := badgeDecision(1200*time.Millisecond, 1199*time.Millisecond, true, true, false, 0)
	if !ok || text != completionMessage || hiddenPrefix != 0 {
		t.Fatalf("expected completion message once the minimum display duration elapses in debug mode, got text=%q hiddenPrefix=%d ok=%v", text, hiddenPrefix, ok)
	}
}

func TestBadgeDecisionSkipMinDurationJumpsStraightToCompletion(t *testing.T) {
	// Real completion happened at 300ms, well short of the 1s floor. The
	// skip happened at elapsed=400ms (skipElapsedAt=400ms), and we're
	// evaluating right at that same instant (elapsed=400ms, sinceDone
	// irrelevant/inconsistent with real completion here since the skip
	// treats skipElapsedAt as the completion instant instead) — the badge
	// should show the completion message in full immediately, as if
	// indexing had just finished at the moment of the skip.
	text, hiddenPrefix, ok := badgeDecision(400*time.Millisecond, 100*time.Millisecond, true, false, true, 400*time.Millisecond)
	if !ok || text != completionMessage || hiddenPrefix != 0 {
		t.Fatalf("expected completion message immediately at the moment of the skip, got text=%q hiddenPrefix=%d ok=%v", text, hiddenPrefix, ok)
	}
}

func TestBadgeDecisionSkipMinDurationIgnoresStaleRealCompletionTime(t *testing.T) {
	// Regression test for the debug-mode bug: real indexing finished
	// almost instantly (doneAt=1ms), but the spinner had been held by the
	// minimum-display-duration floor for well over the completion
	// message's entire display+fade window (sinceDone is huge) by the
	// time the skip happens. Using that real, stale sinceDone directly
	// would make spinner.Completion report CompletionHidden and the badge
	// would vanish the instant jump mode was opened. Instead, the skip
	// must be evaluated relative to skipElapsedAt (the moment of the
	// skip), which is "now" here, so the completion message must show in
	// full rather than disappear.
	elapsed := 10 * time.Second
	sinceDone := elapsed - time.Millisecond // real doneAt=1ms, ages ago
	text, hiddenPrefix, ok := badgeDecision(elapsed, sinceDone, true, true, true, elapsed)
	if !ok || text != completionMessage || hiddenPrefix != 0 {
		t.Fatalf("expected completion message at the moment of the skip despite a stale real completion time, got text=%q hiddenPrefix=%d ok=%v", text, hiddenPrefix, ok)
	}
}

func TestBadgeDecisionSkipMinDurationStillRespectsCompletionFade(t *testing.T) {
	// skipMinDuration only relocates the completion instant to
	// skipElapsedAt; the completion message's own display+fade timing
	// still applies normally counting forward from there.
	skipElapsedAt := 400 * time.Millisecond
	elapsed := skipElapsedAt + completionDisplayDuration + completionFadeDuration + 50*time.Millisecond
	_, _, ok := badgeDecision(elapsed, elapsed-time.Millisecond, true, false, true, skipElapsedAt)
	if ok {
		t.Fatal("expected completion message to be gone well after its display+fade window, counted from the skip instant")
	}
}

func TestOpeningJumpModeWhileDoneSkipsBadgeMinDuration(t *testing.T) {
	a := testApp(t)
	a.idx = index.Start(a.rootPath, nil)
	waitIndexDone(t, a.idx)

	if a.badgeSkipMinDuration {
		t.Fatal("expected badgeSkipMinDuration unset before jump mode is opened")
	}
	a.handleTreeKey(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone))
	if !a.badgeSkipMinDuration {
		t.Fatal("expected badgeSkipMinDuration to be set after opening jump mode while indexing was already done")
	}
	if a.badgeSkipElapsedAt <= 0 {
		t.Fatalf("expected badgeSkipElapsedAt to be recorded as a positive elapsed duration, got %v", a.badgeSkipElapsedAt)
	}
}

func TestFSChangeResetsBadgeSkipMinDuration(t *testing.T) {
	a := testApp(t)
	a.ignorer = ignore.NewMulti()
	a.idx = index.Start(a.rootPath, nil)
	waitIndexDone(t, a.idx)
	a.badgeSkipMinDuration = true
	a.badgeSkipElapsedAt = time.Second

	a.handleFSChange()
	if a.badgeSkipMinDuration {
		t.Fatal("expected badgeSkipMinDuration to be reset once a new indexing cycle starts")
	}
	if a.badgeSkipElapsedAt != 0 {
		t.Fatalf("expected badgeSkipElapsedAt to be reset once a new indexing cycle starts, got %v", a.badgeSkipElapsedAt)
	}
}
