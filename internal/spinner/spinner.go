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
