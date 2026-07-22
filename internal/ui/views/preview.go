package views

import (
	"time"

	"github.com/gdamore/tcell/v2"
)

// Action is a one-shot signal Preview.HandleKey returns for the
// handful of keys that need something only App's dispatcher can do —
// transitioning to a view Preview holds no reference to (browser,
// open-files), running QuickOpen/Search's own Open() setup, or quitting
// (App.quit isn't part of Shared, since nothing else needs to touch it).
// Every other view's HandleKey can perform its own transitions directly
// through the shared *Overlay pointer; this return-value form is
// specific to the primary view because it's the one place five
// different exits converge, and a return value scales to that better
// than five more shared pointer fields on Preview would.
type Action int

// The Action values: ActionNone means HandleKey handled the key itself
// and there's nothing further for App to do.
const (
	ActionNone Action = iota
	ActionOpenBrowser
	ActionOpenFiles
	ActionOpenQuickOpen
	ActionOpenSearch
	ActionQuit
)

// Preview holds the primary preview view's own state (SPEC.md §2.1,
// §2.4): the goto-line prompt and in-file find prompt. The displayed
// entry's scroll position and wrapped-row cache live per-entry on
// openfiles.Entry instead, since they're tracked per open file rather
// than per view.
type Preview struct {
	*Shared

	GotoPromptOpen bool
	GotoInput      string

	// GotoBlockedPath/GotoBlockedFlashStart track a `g` pressed while the
	// displayed entry's background stream pass (docs/STREAMING_PREVIEW_
	// DESIGN.md §4) isn't done yet: the goto-line prompt doesn't open at
	// all, and the file title bar instead flashes red and shows an
	// inline "still indexing" message, the same red-flash-plus-inline-
	// message convention SPEC.md §2.2/§5.2 already uses for a failed file
	// open. GotoBlockedPath is keyed by path so switching to a different
	// entry doesn't carry a stale flash/message over; the message itself
	// is shown only while that same entry's stream is still not done —
	// once the pass finishes, the reason for it no longer holds, so it
	// stops being shown without needing anything to explicitly clear it.
	GotoBlockedPath       string
	GotoBlockedFlashStart time.Time

	FindPromptOpen bool
	FindInput      string
}

// HandleKey handles input at the primary preview view when no overlay
// is active: reaching the browser, quick open, and open-files overlays,
// preview scrolling/goto-line/in-file-find, and quitting (SPEC.md §7).
// `b` only opens the browser overlay (SPEC.md §5.1); closing it back to
// this view is Escape's job alone, handled by the browser view itself
// (quick open, opened by `o`, is likewise not a toggle). Escape does
// not quit and there is no overlay to back out of here (`q` is the only
// way to quit, so an accidental Escape press can't lose the session's
// open-files state) — its only effect at this view is clearFind,
// clearing an active in-file find if there is one, and otherwise
// remaining a no-op. Reaching another view (`b`/Tab/`o`/`s`) and
// quitting (`q`) are reported via the returned Action so App's
// dispatcher, which owns Overlay transitions and QuickOpen/Search's own
// Open() setup, can perform them.
func (v *Preview) HandleKey(ev *tcell.EventKey) Action {
	if v.GotoPromptOpen {
		v.handleGotoPromptKey(ev)
		return ActionNone
	}
	if v.FindPromptOpen {
		v.handleFindPromptKey(ev)
		return ActionNone
	}

	switch {
	case ev.Rune() == 'b':
		return ActionOpenBrowser
	case ev.Key() == tcell.KeyTab:
		return ActionOpenFiles
	case ev.Rune() == 'o':
		return ActionOpenQuickOpen
	case ev.Rune() == 's':
		return ActionOpenSearch
	case ev.Rune() == 'q':
		return ActionQuit
	case ev.Key() == tcell.KeyUp:
		v.scroll(-1)
	case ev.Key() == tcell.KeyDown:
		v.scroll(1)
	case ev.Key() == tcell.KeyPgUp:
		v.scroll(-v.viewportHeight())
	case ev.Key() == tcell.KeyPgDn:
		v.scroll(v.viewportHeight())
	case ev.Rune() == 'g':
		if e := v.Files.DisplayedEntry(); e != nil {
			if gotoLineBlocked(e.Stream != nil, e.Stream != nil && e.Stream.Done()) {
				v.GotoBlockedPath = e.Path
				v.GotoBlockedFlashStart = time.Now()
			} else {
				v.GotoPromptOpen = true
				v.GotoInput = ""
			}
		}
	case ev.Rune() == '/':
		if v.Files.DisplayedEntry() != nil {
			v.FindPromptOpen = true
			v.FindInput = ""
		}
	case ev.Rune() == 'c':
		if e := v.Files.DisplayedEntry(); e != nil {
			e.CopyMode = !e.CopyMode
		}
	case ev.Rune() == 'n':
		v.findStep(1)
	case ev.Rune() == 'N':
		v.findStep(-1)
	case ev.Key() == tcell.KeyEscape:
		v.clearFind()
	}
	return ActionNone
}
