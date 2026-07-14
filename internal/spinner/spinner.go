// Package spinner implements the delayed-loading-indicator logic from
// SPEC.md §10: suppress the indexing spinner until it's been running
// long enough to be perceptible, and derive a deterministic frame from
// elapsed wall-clock time.
package spinner

import "time"

// DefaultFrames is a small fixed set of animation frames.
var DefaultFrames = []rune{'|', '/', '-', '\\'}

// ShouldShow reports whether the loading indicator should be visible:
// never once indexing is done, and only once elapsed has reached the
// threshold while indexing is still running.
func ShouldShow(done bool, elapsed, threshold time.Duration) bool {
	if done {
		return false
	}
	return elapsed >= threshold
}

// Frame returns the deterministic animation frame for the given
// elapsed time, cycling frames at fps frames per second.
func Frame(elapsed time.Duration, fps float64, frames []rune) rune {
	if len(frames) == 0 {
		return ' '
	}
	step := int(elapsed.Seconds() * fps)
	idx := step % len(frames)
	if idx < 0 {
		idx += len(frames)
	}
	return frames[idx]
}

// CompletionPhase describes what, if anything, the "indexing complete"
// completion message should show at a given point after indexing
// finished.
type CompletionPhase int

const (
	// CompletionHidden means nothing should be drawn.
	CompletionHidden CompletionPhase = iota
	// CompletionMessage means the full message should be drawn.
	CompletionMessage
	// CompletionFading means the message is mid fade-out; see the
	// hiddenPrefix return value of Completion for how much of it.
	CompletionFading
)

// Completion computes the completion-message phase and, while fading,
// how many of the message's leading runes have faded away, for
// sinceDone elapsed since indexing finished. The message is shown in
// full for displayDuration, then fades out over fadeDuration by
// disappearing left-to-right — the earliest runes vanish first while
// the message's right edge/anchor stays put — until nothing remains.
func Completion(sinceDone, displayDuration, fadeDuration time.Duration, messageLen int) (CompletionPhase, int) {
	if sinceDone < displayDuration {
		return CompletionMessage, 0
	}
	fadeElapsed := sinceDone - displayDuration
	if fadeElapsed >= fadeDuration {
		return CompletionHidden, messageLen
	}
	if fadeDuration <= 0 {
		return CompletionHidden, messageLen
	}
	frac := float64(fadeElapsed) / float64(fadeDuration)
	hidden := int(frac * float64(messageLen))
	return CompletionFading, hidden
}
