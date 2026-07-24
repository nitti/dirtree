package views

import (
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/index"
	"github.com/nitti/dirtree/internal/match"
	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/preview"
	"github.com/nitti/dirtree/internal/tree"
	"github.com/nitti/dirtree/internal/ui/canvas"
)

// quickOpenLegend documents the quick open overlay's actions (SPEC.md
// §4.2): Return opens the selected match into the open-files list, Page
// Up/Down move the selection by a page (up/down move it by one row,
// already covered by arrow keys and so not spelled out here), Ctrl+U
// clears the query, Escape cancels back to the primary preview view.
var quickOpenLegend = []canvas.LegendEntry{
	{Text: "[return] open", Priority: 1},
	{Text: "[pgup/pgdn] page", Priority: 3},
	{Text: "[ctrl+u] clear", Priority: 3},
	{Text: "[esc] cancel", Priority: 1},
}

// QuickOpen holds the quick open overlay's state (SPEC.md §4.2): a
// live text filter over the background index (internal/index), reduced
// to matching file entries only (directories can't be opened, but still
// narrow the query via their own path text).
type QuickOpen struct {
	*Shared

	Query    string
	Matches  []index.Entry // nil while the background index isn't done yet, distinct from "genuinely zero matches"
	Selected int
	Scroll   int
	// ErrorPath/ErrorMessage/ErrorFlashStart mirror the browser's own
	// error-flash fields for quick open (§2.2, §5.3): Matches has no
	// stable per-entry identity to attach a persistent error to (it's
	// rebuilt from the index on every keystroke), so the failed match's
	// path and message are tracked here instead, appended inline to that
	// row wherever it still appears and flashed red the same way.
	// Cleared whenever the query changes, since a new query can't say
	// anything about a stale open-failure from a previous one.
	ErrorPath       string
	ErrorMessage    string
	ErrorFlashStart time.Time
	// LastOpenedPath is set by performOpen on a successful open and
	// consumed by App's dispatcher right after HandleKey returns, which
	// reveals it in the browser (a coordinator-level concern: keeping
	// the browser's disclosure/selection in sync with files opened from
	// elsewhere, not something quick open needs to know how to do
	// itself). Cleared once consumed.
	LastOpenedPath string
}

// Open resets quick open's query/match state and recomputes matches; the
// caller (App.openQuickOpen) has already set the overlay to
// OverlayQuickOpen. Per SPEC.md §5.2, opening it while indexing is
// already done means the user has directly seen indexing is ready, so it
// short-circuits the badge's minimum-display-duration floor.
func (v *QuickOpen) Open() {
	v.Query = ""
	v.Selected = 0
	v.recomputeMatches()
	if _, done := v.Idx.Snapshot(); done {
		v.BadgeSkip.NoteIndexAlreadyDone(true, v.Idx.Elapsed())
	}
}

// listHeight returns quick open's match-list viewport height in rows,
// mirroring Draw's own layout math (header row and query input row) so
// Page-Up/Page-Down move by the same number of rows the list actually
// shows.
func (v *QuickOpen) listHeight() int {
	_, h := v.Canvas.Size()
	return h - 2 // header row + query input row
}

// recomputeMatches rebuilds Matches from the current query against the
// background index (SPEC.md §4.1), resetting match-selection and scroll
// to the top — used only where the query itself just changed (typing,
// backspace) or the overlay just opened, per §4.2's "reset
// match-selection and scroll to the top" rule. Also clears any stale
// open-failure recorded against the previous query's matches (§2.2),
// since a new query can't say anything about it.
func (v *QuickOpen) recomputeMatches() {
	v.Selected = 0
	v.Scroll = 0
	v.ErrorPath = ""
	v.RefreshMatches()
}

// RefreshMatches rebuilds Matches from the current query against the
// background index (SPEC.md §4.1), without disturbing Selected/Scroll —
// used for background refreshes (the index finishing or a live-refresh
// rebuild, and the periodic tick that catches the index finishing while
// quick open sits open with an unchanged query) where the query hasn't
// changed, so the user's current position in the list shouldn't jump.
// Selected is clamped in case the match count shrank out from under it;
// Scroll is left for drawList's own clamp to reconcile against the
// (possibly new) selected index and match count next frame. While the
// index hasn't finished building, matches are nil/unavailable rather
// than an empty "no matches" result (SPEC.md §5.2). Directory entries
// are excluded: quick open can only ever open a file (§4.2), and a
// directory match still narrows the query via the query's own
// substring/glob matching against every file's full path, so nothing is
// lost by never surfacing the directory's own row.
func (v *QuickOpen) RefreshMatches() {
	entries, done := v.Idx.Snapshot()
	if !done {
		v.Matches = nil
		return
	}
	matches := make([]index.Entry, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir && match.Matches(v.Query, e.RelPath(v.RootPath)) {
			matches = append(matches, e)
		}
	}
	v.Matches = matches
	if v.Selected >= len(matches) {
		v.Selected = max(len(matches)-1, 0)
	}
}

// handleTypingKey handles quick open's navigation/query-editing keys
// (SPEC.md §4.2), reporting whether it consumed the event; the caller
// handles Escape and Enter itself.
func (v *QuickOpen) handleTypingKey(ev *tcell.EventKey) bool {
	switch {
	case ev.Key() == tcell.KeyDown:
		if len(v.Matches) > 0 {
			v.Selected = tree.MoveSelection(v.Selected, 1, len(v.Matches))
		}
	case ev.Key() == tcell.KeyUp:
		if len(v.Matches) > 0 {
			v.Selected = tree.MoveSelection(v.Selected, -1, len(v.Matches))
		}
	case ev.Key() == tcell.KeyPgUp:
		if len(v.Matches) > 0 {
			v.Selected = tree.MoveSelectionClamped(v.Selected, -v.listHeight(), len(v.Matches))
		}
	case ev.Key() == tcell.KeyPgDn:
		if len(v.Matches) > 0 {
			v.Selected = tree.MoveSelectionClamped(v.Selected, v.listHeight(), len(v.Matches))
		}
	case ev.Key() == tcell.KeyBackspace, ev.Key() == tcell.KeyBackspace2:
		if len(v.Query) > 0 {
			r := []rune(v.Query)
			v.Query = string(r[:len(r)-1])
		}
		v.Selected = 0
		v.recomputeMatches()
	case ev.Key() == tcell.KeyCtrlU:
		v.Query = ""
		v.Selected = 0
		v.recomputeMatches()
	case ev.Rune() != 0 && ev.Key() == tcell.KeyRune:
		v.Query += string(ev.Rune())
		v.Selected = 0
		v.recomputeMatches()
	default:
		return false
	}
	return true
}

// HandleKey implements quick open's input handling (SPEC.md §4.2): a
// single action, opening the selected match into the open-files list.
// Escape returns to the primary preview view; `o` has no special meaning
// here — it's a live text filter, and "o" is much too common a letter to
// double as a close key without breaking ordinary typing.
func (v *QuickOpen) HandleKey(ev *tcell.EventKey) {
	switch {
	case ev.Key() == tcell.KeyEscape:
		*v.Overlay = OverlayNone
	case ev.Key() == tcell.KeyEnter:
		v.performOpen()
	default:
		v.handleTypingKey(ev)
	}
}

// performOpen implements quick open's Enter action (SPEC.md §4.2): open
// the selected match per §2.2's open semantics. An opened result closes
// the overlay, landing on the primary preview view, and records the path
// in LastOpenedPath for App's dispatcher to reveal in the browser; a
// failed result leaves quick open open with the failure recorded against
// that match's path (ErrorPath/ErrorMessage) and rendered inline on its
// row by drawList, plus a brief red flash (§2.2, §5.3) — the same
// treatment the browser's own open failures get.
func (v *QuickOpen) performOpen() {
	if len(v.Matches) == 0 {
		return
	}
	target := v.Matches[v.Selected]
	res := v.Files.Open(target.AbsPath, preview.DefaultByteCap)
	if res.Outcome != openfiles.Opened {
		v.ErrorPath = target.AbsPath
		v.ErrorMessage = res.Message
		v.ErrorFlashStart = time.Now()
		return
	}
	v.LastOpenedPath = target.AbsPath
	*v.Overlay = OverlayNone
}

// Draw renders the quick open overlay (SPEC.md §4.2, §5.2): a header
// with its single action (Return opens the selected match), the query on
// its own row directly below (the same input-row convention content
// search uses, §9.2), and the flat match list.
func (v *QuickOpen) Draw(w, h int) {
	v.Canvas.DrawHeaderMode(w, "QUICK OPEN", quickOpenLegend)
	prompt := "> " + v.Query
	v.Canvas.DrawText(0, 1, w, prompt, canvas.StyleSearchInput)
	if ghost := v.queryGhostSuffix(); ghost != "" {
		x := len([]rune(prompt))
		if x < w {
			v.Canvas.DrawText(x, 1, w-x, ghost, canvas.StyleQueryGhost)
		}
	}
	v.drawList(w, h)
}

// queryGhostSuffix returns the ghost-text autocomplete suffix for the
// query row (SPEC.md §4.2): the literal remainder of the sole match's
// path once v.Query unambiguously identifies exactly one file *and* is
// a literal prefix of that file's path — quick open's glob/substring
// matching rule (internal/match.Matches) means a unique match's query
// is often not a prefix of anything at all (e.g. "match" uniquely
// matching "internal/match/match.go" starts mid-path), and
// match.Remainder deliberately declines to invent a remainder in that
// case rather than autocompleting to a continuation the user didn't
// actually type the start of.
func (v *QuickOpen) queryGhostSuffix() string {
	if len(v.Matches) != 1 {
		return ""
	}
	suffix, ok := match.Remainder(v.Query, v.Matches[0].RelPath(v.RootPath))
	if !ok {
		return ""
	}
	return suffix
}

// drawList renders quick open's flat, root-relative match list (SPEC.md
// §4.1's index, §5.2's indexing/no-matches placeholder), with any
// failed-open match (§2.2) showing its failure message inline, appended
// the same way tree.Node.Err is in the browser, and flashing red the
// same brief window (§5.3).
func (v *QuickOpen) drawList(w, h int) {
	const listTop = 2
	listHeight := h - listTop

	_, done := v.Idx.Snapshot()
	switch {
	case !done:
		// SPEC.md §5.2: during the pre-threshold grace period this is
		// indistinguishable from "still indexing" at the pure-decision
		// level, so the match-list area stays blank either way rather
		// than claiming "no matches" before indexing has even looked.
		v.Canvas.DrawText(0, listTop, w, canvas.CenterPad("indexing…", w), canvas.StyleNormal)
	case len(v.Matches) == 0:
		v.Canvas.DrawText(0, listTop, w, canvas.CenterPad("no matches", w), canvas.StyleNormal)
	default:
		if listHeight > 0 {
			if v.Selected < v.Scroll {
				v.Scroll = v.Selected
			}
			if v.Selected >= v.Scroll+listHeight {
				v.Scroll = v.Selected - listHeight + 1
			}
		}
		for row := range listHeight {
			i := v.Scroll + row
			if i >= len(v.Matches) {
				break
			}
			m := v.Matches[i]
			open := " "
			if v.Files.IsOpen(m.AbsPath) {
				open = "●"
			}
			label := open + " " + m.RelPath(v.RootPath)
			errored := v.ErrorPath != "" && m.AbsPath == v.ErrorPath
			if errored {
				label += " [" + v.ErrorMessage + "]"
			}
			style := canvas.StyleNormal
			switch {
			case errored && time.Since(v.ErrorFlashStart) < canvas.FlashDuration:
				style = canvas.StyleFlashError
			case len(v.Matches) == 1:
				// The query unambiguously identifies this one file
				// (§4.2) — distinct from plain cursor-position
				// StyleSelected, which i == v.Selected would otherwise
				// also match here (Selected is always 0, the sole
				// match, once len(Matches) == 1).
				style = canvas.StyleFindCurrent
			case i == v.Selected:
				style = canvas.StyleSelected
			}
			v.Canvas.DrawText(0, listTop+row, w, label, style)
		}
	}
}
