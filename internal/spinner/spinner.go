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

// MinDurationSkip tracks the badge's minimum-display-duration skip
// (SPEC.md §10): opening jump mode while indexing is already done means
// the user has directly seen indexing is ready, so continuing to hold
// the badge on the spinner for the rest of the minimum display duration
// would be actively misleading rather than a perceptibility safeguard.
// Once applied, the moment the skip was recorded — not indexing's real
// (possibly long-past) completion time — is treated as when indexing
// finished.
type MinDurationSkip struct {
	Active  bool
	Elapsed time.Duration
}

// NoteIndexAlreadyDone applies the skip if indexing is done and the
// skip isn't already active, recording elapsed (time since indexing
// started) as the moment to treat as its completion time. A no-op if
// the skip is already active or indexing isn't done yet.
func (s *MinDurationSkip) NoteIndexAlreadyDone(done bool, elapsed time.Duration) {
	if done && !s.Active {
		s.Active = true
		s.Elapsed = elapsed
	}
}

// Reset clears the skip; call whenever a new indexing cycle starts
// (e.g. a live-refresh-triggered rebuild), since the flash-prevention
// floor is meaningful again for that fresh run.
func (s *MinDurationSkip) Reset() {
	s.Active = false
	s.Elapsed = 0
}

// BadgeDecision is the bottom-right delayed-loading badge's full
// decision logic (SPEC.md §10), kept as a pure function of
// elapsed/sinceDone durations so it can be unit tested without waiting
// on real indexing timing. hiddenPrefix is how many of text's leading
// runes should be skipped when drawing, to animate the completion
// message fading away left-to-right while its right edge stays
// anchored in place.
//
// The full sequence: nothing is shown until indexing has been running
// long enough to be perceptible (threshold); then the spinner shows,
// guaranteed visible for at least minDisplayDuration even if indexing
// genuinely finishes sooner (real directories often do, in
// microseconds — without this floor the spinner could cross the
// threshold and complete in the same frame, an unreadable flash); then
// it hands off to the "indexing complete" message for displayDuration;
// then that message fades out over fadeDuration. If indexing finishes
// before ever crossing threshold, none of this is shown at all — the
// spinner was never perceptible, so announcing its completion would be
// exactly the flashing-chrome-for-instant-work distraction the
// threshold exists to avoid.
//
// debugAlwaysShow (DebugAlwaysShow: a build-time-only escape hatch,
// never set in a shipped build) bypasses only threshold — the spinner
// appears the instant indexing starts — while every other timing above
// behaves exactly as it would otherwise.
//
// skip is the MinDurationSkip described above; when Active, it drops
// the minimum-display-duration floor entirely, jumping straight to the
// completion step as of skip.Elapsed rather than indexing's real
// completion time.
func BadgeDecision(elapsed, sinceDone time.Duration, done, debugAlwaysShow bool, skip MinDurationSkip, threshold, minDisplayDuration, displayDuration, fadeDuration time.Duration, fps float64, message string) (text string, hiddenPrefix int, ok bool) {
	// doneAt is the wall-clock time since indexing started at which it
	// actually finished; meaningless (and unused) while still running.
	doneAt := elapsed - sinceDone

	var crossedThreshold bool
	switch {
	case debugAlwaysShow:
		crossedThreshold = true
	case done:
		crossedThreshold = doneAt >= threshold
	default:
		crossedThreshold = elapsed >= threshold
	}
	if !crossedThreshold {
		return "", 0, false
	}

	if done {
		if skip.Active {
			doneAt = skip.Elapsed
		} else if doneAt < minDisplayDuration {
			doneAt = minDisplayDuration
		}
		done = elapsed >= doneAt
		sinceDone = elapsed - doneAt
	}

	if !done {
		frame := Frame(elapsed, fps, DefaultFrames)
		return "indexing " + string(frame), 0, true
	}

	phase, faded := Completion(sinceDone, displayDuration, fadeDuration, len(message))
	if phase == CompletionHidden {
		return "", 0, false
	}
	return message, faded, true
}
