package views

import (
	"time"

	"github.com/nitti/dirtree/internal/ui/canvas"
)

// Each view's Animating method reports whether anything it would draw
// right now is time-driven (SPEC.md §5.3) — a flash still within
// canvas.FlashDuration, a spinner, or background work whose result Draw
// consumes — so App's main loop knows to tick at its faster animation
// cadence rather than its idle one (SPEC.md §6.2). Erring toward true
// only costs a few extra redraws; erring toward false makes an
// animation stutter or a flash linger past its end, so each check
// mirrors the condition its Draw code tests rather than approximating it.

// flashing reports whether a flash that started at start is still
// visible at now.
func flashing(start, now time.Time) bool {
	return !start.IsZero() && now.Sub(start) < canvas.FlashDuration
}

// Animating reports whether the displayed entry's title bar or content
// is mid-animation: an edge-bump or goto-blocked flash, a still-running
// stream pass (whose "building…" spinner, and for TierHighlighted whose
// finished content, Draw picks up), or an in-file find scan.
func (v *Preview) Animating(now time.Time) bool {
	if flashing(v.TopBumpFlashStart, now) || flashing(v.BottomBumpFlashStart, now) || flashing(v.GotoBlockedFlashStart, now) {
		return true
	}
	e := v.Files.DisplayedEntry()
	if e == nil {
		return false
	}
	if e.FindScan != nil || e.HexFindScan != nil {
		return true
	}
	if e.Stream != nil {
		done := e.Stream.Done()
		if !done {
			return true
		}
		return streamBuildingVisible(e.Stream.Elapsed(), e.Stream.SinceDone(), done, canvas.SpinnerThreshold, streamSpinnerMinDisplayDuration)
	}
	return false
}

// Animating reports whether a post-open or error flash is showing on
// any browser row.
func (v *Browser) Animating(now time.Time) bool {
	if flashing(v.FlashStart, now) {
		return true
	}
	for _, start := range v.ErrorFlashes {
		if flashing(start, now) {
			return true
		}
	}
	return false
}

// Animating reports whether quick open's failed-open row flash is
// showing. Waiting on the background index is App's concern, since it
// already tracks that for the corner badge.
func (v *QuickOpen) Animating(now time.Time) bool {
	return flashing(v.ErrorFlashStart, now)
}

// Animating reports whether a scan is in flight (its spinner) or a
// post-open/error row flash is showing.
func (v *Search) Animating(now time.Time) bool {
	return v.Cancel != nil || flashing(v.FlashStart, now) || flashing(v.ErrorFlashStart, now)
}
