package views

import (
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/match"
	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/preview"
	"github.com/nitti/dirtree/internal/tree"
	"github.com/nitti/dirtree/internal/ui/canvas"
)

// browserLegend documents the browser overlay's own actions (SPEC.md
// §3.4): Return opens the selected file or discloses (expands/
// collapses) a selected directory, left/right collapse/expand a
// directory (or move to its parent/first child), pgup/pgdn page the row
// list, `/` enters jump to file (§4.3), Escape closes the overlay (the
// sole close key — `b` is not a toggle here).
var browserLegend = []canvas.LegendEntry{
	{Text: "[return] open/expand", Priority: 1},
	{Text: "[left/right] expand/collapse", Priority: 2},
	{Text: "[pgup/pgdn] page", Priority: 3},
	{Text: "[/] jump to file", Priority: 2},
	{Text: "[esc] close", Priority: 1},
}

// jumpLegend documents jump-to-file mode's own actions (SPEC.md §4.3):
// Return leaves jump mode keeping the current selection, Tab/Shift-Tab
// cycle the match set, `/` slash-to-expand narrows the jump scope into
// a single-matching directory, Ctrl+U clears the query, Escape cancels
// back to the selection/scroll jump mode was entered with. Tab/Shift-Tab
// is priority 2, not 1, like quick open's and content search's own
// match-cycling entries — keeping every legend's priority-1-only text
// under the terminal-size floor (§6.4) means the "done"/"cancel" pair
// stays legible even at the enforced minimum, where this entry's own
// long text (it names both directions) would otherwise still overflow
// and clip.
var jumpLegend = []canvas.LegendEntry{
	{Text: "[return] done", Priority: 1},
	{Text: "[tab/shift-tab] next/prev match", Priority: 2},
	{Text: "[/] expand", Priority: 2},
	{Text: "[ctrl+u] clear", Priority: 2},
	{Text: "[esc] cancel", Priority: 1},
}

// Browser holds the browser overlay's state (SPEC.md §3.4),
// including jump-to-file typing mode (§4.3) — not a separate overlay,
// just a mode layered on top of the browser's own row list, so its
// fields live here rather than on their own type.
type Browser struct {
	*Shared

	Selected   *tree.Node
	Scroll     int
	FlashPath  string // absolute path of the file row most recently opened via performOpen, for a brief post-open flash
	FlashStart time.Time
	// ErrorFlashes tracks a brief red flash (canvas.StyleFlashError) for
	// any node whose most recent operation failed — a directory that
	// newly started failing to list during a live-refresh (SPEC.md
	// §6.1), or a file that failed to open (§2.2, e.g. permission
	// denied or binary). Unlike FlashPath above, the flash itself is
	// only the attention-grabbing part and always decays; the error
	// text it's drawing attention to (tree.Node.Err, rendered inline by
	// label for both cases) does not fade with it and stays until
	// overwritten by a later successful operation on that same node.
	// Keyed by path since more than one directory could newly error in
	// the same debounced live-refresh batch.
	ErrorFlashes map[string]time.Time

	// Jump-to-file typing mode (SPEC.md §4.3): App.overlay stays
	// OverlayBrowser throughout, and the browser's own row list keeps
	// rendering. JumpPrevSelected/JumpPrevScroll capture the browser's
	// selection/scroll at the moment `/` was pressed, so Escape can
	// restore them exactly.
	JumpActive       bool
	JumpQuery        string
	JumpDisclosed    string       // slash-to-expand path segments committed so far (e.g. "internal/ui/"), shown ahead of JumpQuery so disclosing a directory doesn't read as losing what was typed (SPEC.md §4.3)
	JumpMatches      []*tree.Node // recomputed on every query change from JumpScope.Flatten(); rows both files and directories
	JumpSelected     int          // index into JumpMatches
	JumpPrevSelected *tree.Node
	JumpPrevScroll   int
	JumpScope        *tree.Node // matching root for JumpMatches: Root normally, or a directory drilled into via slash-to-expand (SPEC.md §4.3)
}

// FlagErrorFlashes starts a brief red flash (SPEC.md §2.2, §6.1, §5.3)
// for each path in paths, drawing attention to the inline error text
// label already renders for it (tree.Node.Err) without the error text
// itself fading — only the flash decays. Used both for a live-refresh's
// newly-erroring directories (App.handleFSChange, several paths at
// once) and a single failed file-open (performOpen). Also prunes any
// previously-flashed path whose flash has already finished, so the map
// doesn't grow unbounded over a long-running session.
func (v *Browser) FlagErrorFlashes(paths []string) {
	now := time.Now()
	for p, start := range v.ErrorFlashes {
		if now.Sub(start) >= canvas.FlashDuration {
			delete(v.ErrorFlashes, p)
		}
	}
	for _, p := range paths {
		v.ErrorFlashes[p] = now
	}
}

// Reveal expands the browser tree's disclosure state down to absPath and
// selects it, so opening a file from quick open or content search
// (which search the whole tree regardless of what's currently expanded)
// leaves the browser showing where that file lives the next time it's
// opened (SPEC.md §4.2, §9.2) — not gated on the browser overlay
// actually being open right now, purely updating its state for later.
func (v *Browser) Reveal(absPath string) {
	if n := tree.RevealPath(v.Root, v.RootPath, absPath, v.Ignorer); n != nil {
		v.Selected = n
	}
}

// HandleKey implements the browser overlay's navigation and open actions
// (SPEC.md §3.4). Escape is the sole key that closes the browser back to
// the primary preview view (SPEC.md §5.1); `b` only opens it from there
// and has no effect once inside. While jump-to-file typing mode (§4.3)
// is active, every key below is bypassed in favor of handleJumpKey,
// since jump mode repurposes all of them as query input.
// `o` and `s` have no effect here — browse, quick open, and content
// search are mutually exclusive (SPEC.md §5.1): reaching another one
// requires closing the browser back to the primary preview view first.
func (v *Browser) HandleKey(ev *tcell.EventKey) {
	if v.JumpActive {
		v.handleJumpKey(ev)
		return
	}

	flat := v.Root.Flatten()
	idx := indexOf(flat, v.Selected)

	switch {
	case ev.Key() == tcell.KeyUp:
		v.Selected = flat[tree.MoveSelection(idx, -1, len(flat))]
	case ev.Key() == tcell.KeyDown:
		v.Selected = flat[tree.MoveSelection(idx, 1, len(flat))]
	case ev.Key() == tcell.KeyRight:
		target := v.Selected
		v.Selected = target.MoveRight(v.RootPath, v.Ignorer)
		// Flash on every attempt that ends in an error, not just the
		// first: MoveRight/Expand retries the listing each time (a
		// still-failing directory never gets marked Expanded, SPEC.md
		// §3.1), so a still-broken directory the user keeps trying to
		// disclose should keep giving feedback each time, not just once.
		if target.Err != "" {
			v.FlagErrorFlashes([]string{target.Path()})
		}
	case ev.Key() == tcell.KeyLeft:
		v.Selected = v.Selected.MoveLeft()
	case ev.Key() == tcell.KeyPgDn:
		v.Selected = flat[tree.MoveSelectionClamped(idx, v.listHeight(), len(flat))]
	case ev.Key() == tcell.KeyPgUp:
		v.Selected = flat[tree.MoveSelectionClamped(idx, -v.listHeight(), len(flat))]
	case ev.Key() == tcell.KeyEnter:
		v.performOpen()
	case ev.Rune() == '/':
		v.openJump()
	case ev.Key() == tcell.KeyEscape:
		*v.Overlay = OverlayNone
	}
}

// openJump enters jump-to-file typing mode (SPEC.md §4.3) from within
// the browser: not an overlay change (Overlay stays OverlayBrowser),
// just a mode layered on top of it that remembers the current
// selection/scroll so Escape can restore them.
func (v *Browser) openJump() {
	v.JumpActive = true
	v.JumpQuery = ""
	v.JumpDisclosed = ""
	v.JumpMatches = nil
	v.JumpSelected = 0
	v.JumpPrevSelected = v.Selected
	v.JumpPrevScroll = v.Scroll
	v.JumpScope = v.Root
}

// recomputeJumpMatches rebuilds JumpMatches from the current query
// against JumpScope's live flattened row list (SPEC.md §4.3's candidate
// set: whatever the tree's current expand/collapse state already
// exposes, files and directories alike), matching each row's leaf name
// by case-insensitive prefix. JumpScope is normally the tree root, but
// narrows after a slash-to-expand drill-down (see handleJumpKey).
func (v *Browser) recomputeJumpMatches() {
	v.JumpSelected = 0
	v.JumpMatches = tree.JumpMatches(v.JumpScope, v.JumpQuery)
}

// jumpGhostSuffix returns jump to file's ghost-text autocomplete
// suffix (SPEC.md §4.3): the remainder of the sole match's leaf name
// after JumpQuery, once the query unambiguously identifies exactly one
// visible row. Jump to file's matching rule (match.PrefixMatches, via
// tree.JumpMatches) already guarantees JumpQuery is a literal
// case-insensitive prefix of the match's Name by construction, so
// match.Remainder always succeeds here — reused anyway so both this
// and quick open's own ghost text (§4.2) share one implementation of
// "the literal continuation of what was typed."
func (v *Browser) jumpGhostSuffix() string {
	if len(v.JumpMatches) != 1 {
		return ""
	}
	suffix, ok := match.Remainder(v.JumpQuery, v.JumpMatches[0].Name)
	if !ok {
		return ""
	}
	return suffix
}

// handleJumpKey implements jump-to-file typing mode's input handling
// (SPEC.md §4.3): every printable rune is query input, Tab/Shift-Tab (or
// Down/Up) cycle among current matches, and Return/Escape are the only
// ways out.
func (v *Browser) handleJumpKey(ev *tcell.EventKey) {
	switch {
	case ev.Key() == tcell.KeyEscape:
		v.Selected = v.JumpPrevSelected
		v.Scroll = v.JumpPrevScroll
		v.JumpActive = false
	case ev.Key() == tcell.KeyEnter:
		v.JumpActive = false
	case ev.Key() == tcell.KeyTab, ev.Key() == tcell.KeyDown:
		if len(v.JumpMatches) > 0 {
			v.JumpSelected = tree.MoveSelection(v.JumpSelected, 1, len(v.JumpMatches))
			v.Selected = v.JumpMatches[v.JumpSelected]
		}
	case ev.Key() == tcell.KeyBacktab, ev.Key() == tcell.KeyUp:
		if len(v.JumpMatches) > 0 {
			v.JumpSelected = tree.MoveSelection(v.JumpSelected, -1, len(v.JumpMatches))
			v.Selected = v.JumpMatches[v.JumpSelected]
		}
	case ev.Key() == tcell.KeyBackspace, ev.Key() == tcell.KeyBackspace2:
		if len(v.JumpQuery) > 0 {
			r := []rune(v.JumpQuery)
			v.JumpQuery = string(r[:len(r)-1])
		}
		v.recomputeJumpMatches()
		if len(v.JumpMatches) > 0 {
			v.Selected = v.JumpMatches[0]
		}
	case ev.Key() == tcell.KeyCtrlU:
		v.JumpQuery = ""
		v.recomputeJumpMatches()
		if len(v.JumpMatches) > 0 {
			v.Selected = v.JumpMatches[0]
		}
	case ev.Rune() == '/' && ev.Key() == tcell.KeyRune && len(v.JumpMatches) == 1 && v.JumpMatches[0].IsDir:
		// Slash-to-expand (SPEC.md §4.3): when the query already
		// uniquely identifies a directory, `/` disclosed it and
		// re-scopes matching to its descendants instead of being
		// consumed as a literal query character (which could never
		// match anyway, since no filename contains `/`). This is jump
		// to file's one deliberate exception to "never expands or
		// collapses anything" — it only ever expands, never collapses,
		// and only on this explicit unique-match+`/` signal.
		// JumpDisclosed accumulates the committed segment (JumpQuery,
		// which named this directory, plus the slash) so it keeps
		// showing ahead of the next segment's query rather than
		// vanishing — clearing JumpQuery to start the next segment must
		// not read as losing what was already typed.
		target := v.JumpMatches[0]
		target.Expand(v.RootPath, v.Ignorer)
		if target.Err != "" {
			v.FlagErrorFlashes([]string{target.Path()})
		} else {
			v.JumpScope = target
			v.JumpDisclosed += v.JumpQuery + "/"
			v.JumpQuery = ""
			v.Selected = target
			v.recomputeJumpMatches()
		}
	case ev.Rune() != 0 && ev.Key() == tcell.KeyRune:
		v.JumpQuery += string(ev.Rune())
		v.recomputeJumpMatches()
		if len(v.JumpMatches) > 0 {
			v.Selected = v.JumpMatches[0]
		}
	}
}

// performOpen implements Enter from the browser, per SPEC.md §3.4 and
// §2.2's open-failure signaling: on a directory, it discloses (toggles
// expand/collapse) it, the same as pressing `/` then Enter would scope
// into it — a still-failing directory records its error the same way
// Right-arrow's MoveRight does; on a file, an "opened" result displays
// it in the primary preview view without closing the browser, so
// several files can be queued up in a row before returning to the
// preview; a "failed" result leaves the browser open with the failure
// recorded on the node itself (tree.Node.Err, label's existing inline
// `[error]` rendering) and a brief red flash (ErrorFlashes/
// canvas.StyleFlashError, §5.3) — the same treatment a directory that
// newly fails to list gets from a live-refresh (§6.1), unified since
// both are "the last operation on this node failed." On success, the
// opened row also gets a brief flash (FlashPath/FlashStart, drawn in
// Draw) as an on-open confirmation, the same treatment content search
// gives its own rows — distinct from the lasting "●" open indicator
// every open file's row shows regardless of when it was opened.
func (v *Browser) performOpen() {
	if v.Selected.IsDir {
		v.Selected.ToggleExpand(v.RootPath, v.Ignorer)
		if v.Selected.Err != "" {
			v.FlagErrorFlashes([]string{v.Selected.Path()})
		}
		return
	}
	res := v.Files.Open(v.Selected.Path(), preview.DefaultByteCap)
	if res.Outcome != openfiles.Opened {
		v.Selected.Err = res.Message
		v.FlagErrorFlashes([]string{v.Selected.Path()})
		return
	}
	v.Selected.Err = ""
	v.FlashPath = v.Selected.Path()
	v.FlashStart = time.Now()
}

// listHeight returns the browser's row-list viewport height in
// non-jump-mode (SPEC.md §3.4's Page-Up/Page-Down), mirroring Draw's own
// layout math (a single header row) so paging moves by the same number
// of rows the list actually shows.
func (v *Browser) listHeight() int {
	_, h := v.Canvas.Size()
	return h - 1 // header row
}

// Draw renders the browser full-screen (SPEC.md §5.1): a header with its
// mode label/legend, the jump-to-file query on its own input row
// directly below when active (the same convention quick open and
// content search use), and the flat row list filling the rest of the
// screen.
func (v *Browser) Draw(w, h int) {
	browserTop := 1
	if v.JumpActive {
		// SPEC.md §5.2: while jump-to-file (§4.3) is active, the header's
		// left side keeps showing the "BROWSE" mode label — jump to file
		// is a typing mode layered on the browser, not a distinct mode of
		// its own, per §4.3 — and the query itself moves to its own input
		// row directly below the header, the same convention quick open
		// and content search already use for their own queries, prefixed
		// `> ` the same way theirs are. Any path segments already
		// disclosed via slash-to-expand (JumpDisclosed) render ahead of
		// the live query so committing a segment doesn't read as losing
		// what was typed.
		v.Canvas.DrawHeaderMode(w, "BROWSE", jumpLegend)
		prompt := "> " + v.JumpDisclosed + v.JumpQuery
		v.Canvas.DrawText(0, 1, w, prompt, canvas.StyleSearchInput)
		if ghost := v.jumpGhostSuffix(); ghost != "" {
			x := len([]rune(prompt))
			if x < w {
				v.Canvas.DrawText(x, 1, w-x, ghost, canvas.StyleQueryGhost)
			}
		}
		browserTop = 2
	} else {
		v.Canvas.DrawHeaderMode(w, "BROWSE", browserLegend)
	}

	v.drawList(0, browserTop, w, h-browserTop)
}

// drawList renders the browser's currently-visible flattened list
// (SPEC.md §3.1, §5.2), keeping the selected row scrolled into view.
func (v *Browser) drawList(x0, y0, w, h int) {
	if h < 1 {
		return
	}
	flat := v.Root.Flatten()
	selIdx := indexOf(flat, v.Selected)

	if selIdx < v.Scroll {
		v.Scroll = selIdx
	}
	if selIdx >= v.Scroll+h {
		v.Scroll = selIdx - h + 1
	}
	if v.Scroll < 0 {
		v.Scroll = 0
	}

	var isMatch map[*tree.Node]bool
	if v.JumpActive && len(v.JumpMatches) > 0 {
		isMatch = make(map[*tree.Node]bool, len(v.JumpMatches))
		for _, m := range v.JumpMatches {
			isMatch[m] = true
		}
	}

	for row := range h {
		i := v.Scroll + row
		if i >= len(flat) {
			break
		}
		n := flat[i]
		// Computed once per row: n.Path() walks up to the tree root
		// (see internal/tree/node.go), and it's checked against two
		// separate flash conditions below.
		nPath := n.Path()
		style := canvas.StyleNormal
		switch {
		// SPEC.md §6.1: same reasoning as the open-flash case below —
		// the errored directory could well be the currently-selected
		// row, and reverse-video would otherwise mask the flash
		// entirely, so it takes precedence even over selection.
		case isErrorFlashing(v.ErrorFlashes, nPath):
			style = canvas.StyleFlashError
		// SPEC.md §5.2: the flash takes precedence here, unlike content
		// search's own flash/selected precedence — Return never moves
		// the browser's selection (§3.4), so the just-opened row is
		// always the already-selected row; if StyleSelected won here the
		// same way it does in content search, the flash would be
		// permanently masked by reverse-video and never actually visible.
		case nPath == v.FlashPath && time.Since(v.FlashStart) < canvas.FlashDuration:
			style = canvas.StyleFlash
		case v.JumpActive && len(v.JumpMatches) == 1:
			// JumpQuery unambiguously identifies this one row (§4.3) —
			// distinct from plain cursor-position StyleSelected, which
			// n == v.Selected would otherwise also match here
			// (handleJumpKey always assigns the sole match to
			// Selected).
			style = canvas.StyleFindCurrent
		case n == v.Selected:
			style = canvas.StyleSelected
		case isMatch[n]:
			style = canvas.StyleFindMatch
		}
		v.Canvas.DrawText(x0, y0+row, w, v.label(n), style)
	}
}

// isErrorFlashing reports whether path's brief red error flash (SPEC.md
// §6.1) is still within its display window.
func isErrorFlashing(flashes map[string]time.Time, path string) bool {
	start, ok := flashes[path]
	return ok && time.Since(start) < canvas.FlashDuration
}

// label renders one browser row's indentation, expand/collapse marker
// ("▾"/"▸", the same glyphs content search's own file rows use for the
// same concept, §9.2), lasting "●" open indicator (files already in the
// open-files list, SPEC.md §2.2/§5.2 — also shared with content search's
// file rows), name, and any per-node error indicator.
func (v *Browser) label(n *tree.Node) string {
	indent := strings.Repeat("  ", n.Depth())
	marker := "  "
	if n.IsDir {
		if n.Expanded {
			marker = "▾ "
		} else {
			marker = "▸ "
		}
	}
	openMarker := " "
	if v.Files.IsOpen(n.Path()) {
		openMarker = "●"
	}
	label := indent + marker + openMarker + " " + n.Name
	if n.Err != "" {
		label += " [" + n.Err + "]"
	}
	return label
}

func indexOf(list []*tree.Node, n *tree.Node) int {
	for i, c := range list {
		if c == n {
			return i
		}
	}
	return 0
}
