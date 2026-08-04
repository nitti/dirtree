package views

import (
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/entry"
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
// and there's nothing further for App to do. ActionQuitKey does not
// mean "quit now" — it means `q` was pressed this frame, and App's
// dispatcher, which owns the hold-to-quit timing (SPEC.md §5.2), should
// progress that gesture; only continuously holding `q` for the full
// hold duration actually quits.
const (
	ActionNone Action = iota
	ActionOpenBrowser
	ActionOpenFiles
	ActionOpenQuickOpen
	ActionOpenSearch
	ActionQuitKey
)

// Preview holds the primary preview view's own state (SPEC.md §2.1,
// §2.4): the goto-line prompt and in-file find prompt. The displayed
// entry's scroll position and wrapped-row cache live on the
// entry.Entry itself instead, since they're tracked per open file
// rather than per view.
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

	// TopBumpPath/TopBumpFlashStart and BottomBumpPath/BottomBumpFlashStart
	// track a scroll attempt (Up/Page Up past the first line, or
	// Down/Page Down past the last) that pushed further than the
	// currently-displayed entry's content allows (SPEC.md §2.1): the
	// fileView's DrawContent implementation briefly reverses the
	// corresponding edge row's video as a "you've hit the end" cue.
	// Same self-expiring time.Time-field pattern
	// as GotoBlockedPath/GotoBlockedFlashStart above, path-keyed for the
	// same reason — so switching to a different entry within the flash
	// window doesn't carry a stale flash over onto it.
	TopBumpPath          string
	TopBumpFlashStart    time.Time
	BottomBumpPath       string
	BottomBumpFlashStart time.Time

	FindPromptOpen bool
	FindInput      string

	// HexFindPromptOpen/HexFindInput are the hex view's own find prompt
	// state (SPEC.md §2.1a), separate from FindPromptOpen/FindInput above
	// since the two prompts are reachable from mutually-exclusive tiers
	// (a displayed entry is never both a text entry and a TierBinary
	// one) but need independent open/closed state regardless of which
	// entry happens to be displayed when a key arrives.
	HexFindPromptOpen bool
	HexFindInput      string
}

// HandleKey handles input at the primary preview view when no overlay
// is active: reaching the browser, quick open, and open-files overlays,
// preview scrolling/goto-line/in-file-find, and quitting (SPEC.md §7).
// `b` only opens the browser overlay (SPEC.md §5.1); closing it back to
// this view is Escape's job alone, handled by the browser view itself
// (quick open, opened by `o`, is likewise not a toggle). Escape does
// not quit and there is no overlay to back out of here (holding `q` is
// the only way to quit, so an accidental Escape press, or a stray tap
// of `q`, can't lose the session's open-files state) — its only effect
// at this view is fileView.ClearFind, clearing an active in-file find
// if there is one, and otherwise remaining a no-op. Reaching another view
// (`b`/Tab/`o`/`s`) and progressing the hold-to-quit gesture (`q`) are
// reported via the returned Action so App's dispatcher, which owns
// Overlay transitions, QuickOpen/Search's own Open() setup, and the
// hold-to-quit timing, can perform them.
func (v *Preview) HandleKey(ev *tcell.EventKey) Action {
	if v.GotoPromptOpen {
		v.handleGotoPromptKey(ev)
		return ActionNone
	}
	if v.FindPromptOpen {
		v.handleFindPromptKey(ev)
		return ActionNone
	}
	if v.HexFindPromptOpen {
		v.handleHexFindPromptKey(ev)
		return ActionNone
	}

	e := v.Files.DisplayedEntry()
	_, isHex := e.(*entry.HexEntry)
	fv := fileViewFor(e)

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
		return ActionQuitKey
	case ev.Key() == tcell.KeyUp:
		fv.Scroll(v, e, -1)
	case ev.Key() == tcell.KeyDown:
		fv.Scroll(v, e, 1)
	case ev.Key() == tcell.KeyPgUp:
		fv.Scroll(v, e, -v.viewportHeight())
	case ev.Key() == tcell.KeyPgDn:
		fv.Scroll(v, e, v.viewportHeight())
	case ev.Key() == tcell.KeyHome:
		fv.JumpStart(v, e)
	case ev.Key() == tcell.KeyEnd:
		fv.JumpEnd(v, e)
	case ev.Rune() == 'g':
		switch {
		case isHex:
			v.GotoPromptOpen = true
			v.GotoInput = ""
		case e != nil:
			te := e.(*entry.TextEntry)
			if gotoLineBlocked(te.Stream != nil, te.Stream != nil && te.Stream.Done()) {
				v.GotoBlockedPath = te.Path()
				v.GotoBlockedFlashStart = time.Now()
			} else {
				v.GotoPromptOpen = true
				v.GotoInput = ""
			}
		}
	case ev.Rune() == '/':
		switch {
		case isHex:
			v.HexFindPromptOpen = true
			v.HexFindInput = ""
		case e != nil:
			v.FindPromptOpen = true
			v.FindInput = ""
		}
	case ev.Rune() == 'c':
		fv.ToggleCopyMode(v, e)
	case ev.Rune() == 'n':
		fv.FindStep(v, e, 1)
	case ev.Rune() == 'N':
		fv.FindStep(v, e, -1)
	case ev.Key() == tcell.KeyEscape:
		fv.ClearFind(v, e)
	}
	return ActionNone
}
