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
