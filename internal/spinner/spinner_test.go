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
