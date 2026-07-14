package spinner

import (
	"testing"
	"time"
)

func TestHiddenWhenDone(t *testing.T) {
	if ShouldShow(true, 10*time.Second, 250*time.Millisecond) {
		t.Fatal("expected hidden once done, regardless of elapsed")
	}
}

func TestHiddenUnderThreshold(t *testing.T) {
	if ShouldShow(false, 100*time.Millisecond, 250*time.Millisecond) {
		t.Fatal("expected hidden under threshold")
	}
}

func TestShownAtOrAboveThreshold(t *testing.T) {
	if !ShouldShow(false, 250*time.Millisecond, 250*time.Millisecond) {
		t.Fatal("expected shown exactly at threshold")
	}
	if !ShouldShow(false, 251*time.Millisecond, 250*time.Millisecond) {
		t.Fatal("expected shown above threshold")
	}
}

func TestFrameDeterministic(t *testing.T) {
	frames := []rune{'a', 'b', 'c', 'd'}
	f1 := Frame(300*time.Millisecond, 10, frames)
	f2 := Frame(300*time.Millisecond, 10, frames)
	if f1 != f2 {
		t.Fatal("expected same elapsed time to produce same frame")
	}
}

func TestFrameAdvances(t *testing.T) {
	frames := []rune{'a', 'b', 'c', 'd'}
	f1 := Frame(0, 10, frames)
	f2 := Frame(150*time.Millisecond, 10, frames) // > one 100ms frame interval at 10fps
	if f1 == f2 {
		t.Fatal("expected frame to advance across a full frame interval")
	}
}

func TestCompletionShownDuringDisplayWindow(t *testing.T) {
	phase, hidden := Completion(0, 2*time.Second, 400*time.Millisecond, 18)
	if phase != CompletionMessage || hidden != 0 {
		t.Fatalf("expected full message at sinceDone=0, got phase=%v hidden=%d", phase, hidden)
	}
	phase, hidden = Completion(1900*time.Millisecond, 2*time.Second, 400*time.Millisecond, 18)
	if phase != CompletionMessage || hidden != 0 {
		t.Fatalf("expected full message just under display duration, got phase=%v hidden=%d", phase, hidden)
	}
}

func TestCompletionFadesAfterDisplayWindow(t *testing.T) {
	phase, hidden := Completion(2*time.Second, 2*time.Second, 400*time.Millisecond, 20)
	if phase != CompletionFading || hidden != 0 {
		t.Fatalf("expected fading to start at exactly the display duration with nothing hidden yet, got phase=%v hidden=%d", phase, hidden)
	}
	phase, hidden = Completion(2*time.Second+200*time.Millisecond, 2*time.Second, 400*time.Millisecond, 20)
	if phase != CompletionFading || hidden != 10 {
		t.Fatalf("expected half the message hidden halfway through the fade, got phase=%v hidden=%d", phase, hidden)
	}
}

func TestCompletionHiddenAfterFadeCompletes(t *testing.T) {
	phase, hidden := Completion(2*time.Second+400*time.Millisecond, 2*time.Second, 400*time.Millisecond, 20)
	if phase != CompletionHidden || hidden != 20 {
		t.Fatalf("expected fully hidden once fade duration elapses, got phase=%v hidden=%d", phase, hidden)
	}
	phase, hidden = Completion(10*time.Second, 2*time.Second, 400*time.Millisecond, 20)
	if phase != CompletionHidden || hidden != 20 {
		t.Fatalf("expected fully hidden long after fade completes, got phase=%v hidden=%d", phase, hidden)
	}
}

// --- MinDurationSkip (TESTING.md indexing-delay/spinner-suppression group) ---

func TestMinDurationSkipAppliesWhenDoneAndNotAlreadyActive(t *testing.T) {
	var s MinDurationSkip
	s.NoteIndexAlreadyDone(true, 400*time.Millisecond)
	if !s.Active || s.Elapsed != 400*time.Millisecond {
		t.Fatalf("expected skip applied at 400ms, got %+v", s)
	}
}

func TestMinDurationSkipNoOpWhenNotDone(t *testing.T) {
	var s MinDurationSkip
	s.NoteIndexAlreadyDone(false, 400*time.Millisecond)
	if s.Active {
		t.Fatal("expected no skip while indexing is still running")
	}
}

func TestMinDurationSkipDoesNotOverwriteOnceActive(t *testing.T) {
	s := MinDurationSkip{Active: true, Elapsed: 100 * time.Millisecond}
	s.NoteIndexAlreadyDone(true, 999*time.Millisecond)
	if s.Elapsed != 100*time.Millisecond {
		t.Fatalf("expected already-active skip's recorded moment left unchanged, got %v", s.Elapsed)
	}
}

func TestMinDurationSkipReset(t *testing.T) {
	s := MinDurationSkip{Active: true, Elapsed: 100 * time.Millisecond}
	s.Reset()
	if s.Active || s.Elapsed != 0 {
		t.Fatalf("expected skip cleared, got %+v", s)
	}
}

// --- BadgeDecision (TESTING.md "Indexing-delay / spinner-suppression
// logic (§5.2)") ---

const (
	testThreshold  = 250 * time.Millisecond
	testMinDisplay = 1 * time.Second
	testDisplayDur = 2 * time.Second
	testFadeDur    = 400 * time.Millisecond
	testFPS        = 10.0
	testMessage    = "indexing complete"
)

func decide(elapsed, sinceDone time.Duration, done, debug bool, skip MinDurationSkip) (string, int, bool) {
	return BadgeDecision(elapsed, sinceDone, done, debug, skip, testThreshold, testMinDisplay, testDisplayDur, testFadeDur, testFPS, testMessage)
}

func TestBadgeDecisionHidesSpinnerBelowThreshold(t *testing.T) {
	_, _, ok := decide(100*time.Millisecond, 0, false, false, MinDurationSkip{})
	if ok {
		t.Fatal("expected spinner hidden below threshold")
	}
}

func TestBadgeDecisionShowsSpinnerAboveThreshold(t *testing.T) {
	text, _, ok := decide(300*time.Millisecond, 0, false, false, MinDurationSkip{})
	if !ok || text == "" {
		t.Fatalf("expected spinner shown above threshold, got text=%q ok=%v", text, ok)
	}
}

func TestBadgeDecisionHidesCompletionMessageForFastIndexing(t *testing.T) {
	// Indexing started, ran for 50ms (well under the 250ms threshold),
	// and finished 10ms ago: elapsed=60ms, sinceDone=10ms, so it was
	// done at the 50ms mark — never long enough to show the spinner.
	_, _, ok := decide(60*time.Millisecond, 10*time.Millisecond, true, false, MinDurationSkip{})
	if ok {
		t.Fatal("expected no completion message when indexing finished before the spinner threshold")
	}
}

func TestBadgeDecisionExtendsSpinnerToMinimumDisplayDuration(t *testing.T) {
	text, _, ok := decide(400*time.Millisecond, 100*time.Millisecond, true, false, MinDurationSkip{})
	if !ok || text == testMessage {
		t.Fatalf("expected spinner still extended toward the minimum display duration, got text=%q ok=%v", text, ok)
	}
}

func TestBadgeDecisionHandsOffAtMinimumDisplayDuration(t *testing.T) {
	text, hiddenPrefix, ok := decide(1*time.Second, 700*time.Millisecond, true, false, MinDurationSkip{})
	if !ok || text != testMessage || hiddenPrefix != 0 {
		t.Fatalf("expected completion message right at the minimum display duration, got text=%q hiddenPrefix=%d ok=%v", text, hiddenPrefix, ok)
	}
}

func TestBadgeDecisionShowsCompletionMessageForSlowIndexing(t *testing.T) {
	text, hiddenPrefix, ok := decide(1500*time.Millisecond, 0, true, false, MinDurationSkip{})
	if !ok || text != testMessage || hiddenPrefix != 0 {
		t.Fatalf("expected full completion message right after slow indexing finishes, got text=%q hiddenPrefix=%d ok=%v", text, hiddenPrefix, ok)
	}
}

func TestBadgeDecisionHidesCompletionMessageAfterFadeOut(t *testing.T) {
	sinceDone := testDisplayDur + testFadeDur + 50*time.Millisecond
	_, _, ok := decide(1500*time.Millisecond+sinceDone, sinceDone, true, false, MinDurationSkip{})
	if ok {
		t.Fatal("expected completion message to be gone well after its display+fade window")
	}
}

func TestBadgeDecisionDebugModeBypassesThreshold(t *testing.T) {
	text, _, ok := decide(10*time.Millisecond, 0, false, true, MinDurationSkip{})
	if !ok || text == "" {
		t.Fatalf("expected spinner shown immediately in debug mode despite being under the threshold, got text=%q ok=%v", text, ok)
	}
}

func TestBadgeDecisionDebugModeStillEnforcesMinimumDisplayDuration(t *testing.T) {
	text, _, ok := decide(500*time.Millisecond, 499*time.Millisecond, true, true, MinDurationSkip{})
	if !ok || text == testMessage {
		t.Fatalf("expected spinner still showing before the minimum display duration elapses in debug mode, got text=%q ok=%v", text, ok)
	}
}

func TestBadgeDecisionDebugModeCompletesSequenceAfterMinimumDisplayDuration(t *testing.T) {
	text, hiddenPrefix, ok := decide(1200*time.Millisecond, 1199*time.Millisecond, true, true, MinDurationSkip{})
	if !ok || text != testMessage || hiddenPrefix != 0 {
		t.Fatalf("expected completion message once the minimum display duration elapses in debug mode, got text=%q hiddenPrefix=%d ok=%v", text, hiddenPrefix, ok)
	}
}

func TestBadgeDecisionSkipMinDurationJumpsStraightToCompletion(t *testing.T) {
	skip := MinDurationSkip{Active: true, Elapsed: 400 * time.Millisecond}
	text, hiddenPrefix, ok := decide(400*time.Millisecond, 100*time.Millisecond, true, false, skip)
	if !ok || text != testMessage || hiddenPrefix != 0 {
		t.Fatalf("expected completion message immediately at the moment of the skip, got text=%q hiddenPrefix=%d ok=%v", text, hiddenPrefix, ok)
	}
}

func TestBadgeDecisionSkipMinDurationIgnoresStaleRealCompletionTime(t *testing.T) {
	// Real indexing finished almost instantly (doneAt=1ms), but the
	// spinner had been held by the minimum-display-duration floor for
	// well over the completion message's entire display+fade window
	// (sinceDone is huge) by the time the skip happens. The skip must be
	// evaluated relative to its own recorded moment (which here is
	// "now"), not that stale sinceDone, so the completion message must
	// show in full rather than disappear.
	elapsed := 10 * time.Second
	sinceDone := elapsed - time.Millisecond
	skip := MinDurationSkip{Active: true, Elapsed: elapsed}
	text, hiddenPrefix, ok := decide(elapsed, sinceDone, true, true, skip)
	if !ok || text != testMessage || hiddenPrefix != 0 {
		t.Fatalf("expected completion message at the moment of the skip despite a stale real completion time, got text=%q hiddenPrefix=%d ok=%v", text, hiddenPrefix, ok)
	}
}

func TestBadgeDecisionSkipMinDurationStillRespectsCompletionFade(t *testing.T) {
	skipElapsedAt := 400 * time.Millisecond
	skip := MinDurationSkip{Active: true, Elapsed: skipElapsedAt}
	elapsed := skipElapsedAt + testDisplayDur + testFadeDur + 50*time.Millisecond
	_, _, ok := decide(elapsed, elapsed-time.Millisecond, true, false, skip)
	if ok {
		t.Fatal("expected completion message to be gone well after its display+fade window, counted from the skip instant")
	}
}
