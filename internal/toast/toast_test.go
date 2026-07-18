package toast

import (
	"testing"
	"time"
)

func TestShownDuringDisplayWindow(t *testing.T) {
	phase, hidden := Decide(0, 2*time.Second, 400*time.Millisecond, 18)
	if phase != Shown || hidden != 0 {
		t.Fatalf("expected full message at elapsed=0, got phase=%v hidden=%d", phase, hidden)
	}
	phase, hidden = Decide(1900*time.Millisecond, 2*time.Second, 400*time.Millisecond, 18)
	if phase != Shown || hidden != 0 {
		t.Fatalf("expected full message just under display duration, got phase=%v hidden=%d", phase, hidden)
	}
}

func TestFadesAfterDisplayWindow(t *testing.T) {
	phase, hidden := Decide(2*time.Second, 2*time.Second, 400*time.Millisecond, 20)
	if phase != Fading || hidden != 0 {
		t.Fatalf("expected fading to start at exactly the display duration with nothing hidden yet, got phase=%v hidden=%d", phase, hidden)
	}
	phase, hidden = Decide(2*time.Second+200*time.Millisecond, 2*time.Second, 400*time.Millisecond, 20)
	if phase != Fading || hidden != 10 {
		t.Fatalf("expected half the message hidden halfway through the fade, got phase=%v hidden=%d", phase, hidden)
	}
}

func TestHiddenAfterFadeCompletes(t *testing.T) {
	phase, hidden := Decide(2*time.Second+400*time.Millisecond, 2*time.Second, 400*time.Millisecond, 20)
	if phase != Hidden || hidden != 20 {
		t.Fatalf("expected fully hidden once fade duration elapses, got phase=%v hidden=%d", phase, hidden)
	}
	phase, hidden = Decide(10*time.Second, 2*time.Second, 400*time.Millisecond, 20)
	if phase != Hidden || hidden != 20 {
		t.Fatalf("expected fully hidden long after fade completes, got phase=%v hidden=%d", phase, hidden)
	}
}

func TestZeroFadeDurationIsImmediatelyHiddenAfterDisplay(t *testing.T) {
	phase, hidden := Decide(2*time.Second, 2*time.Second, 0, 20)
	if phase != Hidden || hidden != 20 {
		t.Fatalf("expected immediate hide with zero fade duration, got phase=%v hidden=%d", phase, hidden)
	}
}
