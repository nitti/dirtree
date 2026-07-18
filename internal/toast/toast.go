// Package toast implements the generic "show a transient message, then
// fade it out left-to-right from an anchor point" primitive described in
// SPEC.md §5.3. It started as the delayed-loading indicator's
// completion-message fade (internal/spinner's BadgeDecision) and was
// factored out here once a second caller (the transient-error toast,
// SPEC.md §6.1) needed the exact same timing math, so both share one
// implementation instead of reimplementing it.
package toast

import "time"

// Phase describes what, if anything, a toast should show at a given
// point after it was triggered.
type Phase int

const (
	// Hidden means nothing should be drawn.
	Hidden Phase = iota
	// Shown means the full message should be drawn.
	Shown
	// Fading means the message is mid fade-out; see the hiddenPrefix
	// return value of Decide for how much of it.
	Fading
)

// Decide computes a toast's phase and, while fading, how many of the
// message's leading runes have faded away, for elapsed time since the
// toast was triggered. The message is shown in full for displayDuration,
// then fades out over fadeDuration by disappearing left-to-right — the
// earliest runes vanish first while the message's right edge/anchor
// stays put — until nothing remains.
func Decide(elapsed, displayDuration, fadeDuration time.Duration, messageLen int) (Phase, int) {
	if elapsed < displayDuration {
		return Shown, 0
	}
	fadeElapsed := elapsed - displayDuration
	if fadeDuration <= 0 || fadeElapsed >= fadeDuration {
		return Hidden, messageLen
	}
	frac := float64(fadeElapsed) / float64(fadeDuration)
	hidden := int(frac * float64(messageLen))
	return Fading, hidden
}
