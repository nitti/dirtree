package views

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/entry"
	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/preview"
	"github.com/nitti/dirtree/internal/search"
	"github.com/nitti/dirtree/internal/spinner"
	"github.com/nitti/dirtree/internal/tree"
	"github.com/nitti/dirtree/internal/ui/canvas"
)

// searchLegend documents the content search overlay's actions (SPEC.md
// §9.2): Return opens the selected row (jumping to its line if it's a
// hit row) and leaves the overlay open, for opening several hits in a
// row without re-triggering the search each time — Escape is what
// closes the overlay; up/down move the flattened row list (arrow keys
// already cover this, so it's not spelled out as its own legend entry
// alongside left/right); left/right collapse/expand a file's hit rows;
// tab/shift-tab jump the selection to the next/previous file's row,
// skipping over that file's own hit rows; pgup/pgdn page the row list;
// ctrl+r toggles regex mode; ctrl+u clears the query.
var searchLegend = []canvas.LegendEntry{
	{Text: "[return] open", Priority: 1},
	{Text: "[left/right] expand/collapse", Priority: 2},
	{Text: "[tab/shift-tab] next/prev file", Priority: 3},
	{Text: "[pgup/pgdn] page", Priority: 3},
	{Text: "[ctrl+r] regex", Priority: 2},
	{Text: "[ctrl+u] clear", Priority: 3},
	{Text: "[esc] close", Priority: 1},
}

// SearchOutcome is what a background content-search scan (SPEC.md §9.1)
// sends back once it finishes (or is canceled), tagged with the
// generation it was started for so a stale result from a superseded
// query can be discarded rather than clobbering a newer one.
type SearchOutcome struct {
	gen     int
	results []search.FileResult
	err     error // non-nil only for a ModeRegex compile failure (defensive: RecomputeSearch already validates synchronously before dispatching)
}

// searchRow is one flattened, displayable row of the content search
// overlay's results (SPEC.md §9.2): either a file row (one per
// FileResult, always present) or a hit row (one per matching line
// within that file, present only while the file is expanded). Hit rows
// are addressed by (file, hit) rather than a flat index into a
// concatenated slice so toggling one file's disclosure state doesn't
// require renumbering anything.
type searchRow struct {
	file  int // index into Search.Results
	hit   int // index into Results[file].Hits; only meaningful when isHit
	isHit bool
}

// Search holds the content search overlay's state (SPEC.md §9).
type Search struct {
	*Shared

	Query     string
	Regex     bool                // ModeRegex vs. ModeSubstring toggle (ctrl+r)
	Results   []search.FileResult // nil while not yet searched (empty query, waiting on index, or a scan in flight), distinct from "searched, zero matches"
	Error     string              // non-empty only for an invalid regex query (ModeRegex); takes precedence over Results' nil/empty distinction
	Collapsed map[string]bool     // AbsPath -> collapsed; absent (or false) means expanded, so results start expanded by default
	Selected  int                 // index into v.rows(), not directly into Results
	Scroll    int

	// ErrorPath/ErrorMessage/ErrorFlashStart mirror the browser's own
	// error-flash fields for content search's open-into-list action
	// (§2.2, §9.2, §5.3): AbsPath of the file row whose open attempt
	// most recently failed (a hit row's failure attributes to its
	// parent file row, the same way the on-open flash already does),
	// the failure message, and when to stop flashing it red. Cleared
	// whenever the query changes.
	ErrorPath       string
	ErrorMessage    string
	ErrorFlashStart time.Time

	Gen       int
	Cancel    context.CancelFunc
	ScanStart time.Time // when the in-flight scan (Cancel != nil) started, for the spinner shown once it runs long enough to be noticeable

	FlashPath  string // AbsPath of the file row most recently opened via performOpen, for a brief post-open flash
	FlashStart time.Time

	Done chan SearchOutcome

	// LastOpenedPath/LastOpenedEntry/LastOpenedLine are performOpen's
	// outbox for two coordinator-level concerns — revealing the opened
	// file in the browser (shared with quick open, see
	// QuickOpen.LastOpenedPath's doc comment for why this isn't
	// search's job to perform itself) and jumping the preview to the
	// opened hit's line, which needs preview-owned wrap-cache state
	// content search has no access to until the preview view is
	// extracted (a later stage). LastOpenedEntry is nil except in the
	// one frame between a successful open and the coordinator consuming
	// it.
	LastOpenedPath  string
	LastOpenedEntry entry.Entry
	LastOpenedLine  int
}

// Open resets content search's per-open transient state (SPEC.md §9.1),
// reachable only from the primary preview view: browse, quick open, and
// content search are mutually exclusive (§5.1), so Escape always
// returns to preview. The query, results, selection, and per-file
// disclosure state are deliberately left untouched: content search
// persists across close/reopen (Escape closes the overlay without
// discarding it) and is only reset by the user explicitly clearing the
// query themselves (backspacing it to empty, or typing a new one).
func (v *Search) Open() {
	v.ErrorPath = ""
}

// rows flattens the current search results into displayable rows
// (SPEC.md §9.2): one file row per result, followed by that file's hit
// rows in source order when it's expanded (i.e. not present in
// Collapsed, so results are expanded by default). This is recomputed on
// demand rather than cached, since it's cheap and only ever needed while
// rendering or handling a keypress.
func (v *Search) rows() []searchRow {
	var rows []searchRow
	for fi, r := range v.Results {
		rows = append(rows, searchRow{file: fi})
		if v.Collapsed[r.AbsPath] {
			continue
		}
		for hi := range r.Hits {
			rows = append(rows, searchRow{file: fi, hit: hi, isHit: true})
		}
	}
	return rows
}

// toggleDisclosure flips the expanded/collapsed state of the file the
// row at index i belongs to (SPEC.md §9.2), keeping the selection on
// that same file's row so collapsing a file you're inside of doesn't
// strand the selection.
func (v *Search) toggleDisclosure(i int) {
	rows := v.rows()
	if i < 0 || i >= len(rows) {
		return
	}
	fi := rows[i].file
	path := v.Results[fi].AbsPath
	if v.Collapsed == nil {
		v.Collapsed = make(map[string]bool)
	}
	v.Collapsed[path] = !v.Collapsed[path]
	v.Selected = v.fileRowIndex(fi)
}

// fileRowIndex returns the row index of the file row for Results[fi] in
// the current flattened row list.
func (v *Search) fileRowIndex(fi int) int {
	rows := v.rows()
	for i, row := range rows {
		if !row.isHit && row.file == fi {
			return i
		}
	}
	return 0
}

// listHeight returns the search overlay's row-list viewport height,
// mirroring Draw's own listTop math (header row + query input row) so
// Page-Up/Page-Down move by the same number of rows the list actually
// shows.
func (v *Search) listHeight() int {
	_, h := v.Canvas.Size()
	return h - 2 // header row + query input row
}

// RecomputeSearch (re)starts the background content scan for the
// current query (SPEC.md §9.1): any scan still running for the previous
// query is canceled first, so only the most recently typed query's
// result is ever applied. An empty query performs no scan at all. If
// the background index hasn't finished building yet, the scan is
// deferred — App.Run's ticker case retries once it has — rather than
// scanning a partial candidate set. In regex mode, the query is
// compiled synchronously first — cheap, and lets an invalid pattern be
// reported immediately as Error without spawning a scan at all
// (search.Run would otherwise reject it the same way, just one
// goroutine-hop later). The scan itself always runs in a background
// goroutine regardless of mode — even a slow regex over a large tree
// never blocks the UI thread, since results only arrive back via Done
// in App.Run's main select loop.
func (v *Search) RecomputeSearch() {
	v.cancel()
	v.Selected = 0
	v.Scroll = 0
	v.ErrorPath = ""
	v.Error = ""
	v.Collapsed = nil

	if v.Query == "" {
		v.Results = nil
		return
	}

	mode := search.ModeSubstring
	if v.Regex {
		mode = search.ModeRegex
		if _, err := search.CompileRegex(v.Query); err != nil {
			v.Error = err.Error()
			v.Results = nil
			return
		}
	}

	entries, done := v.Idx.Snapshot()
	if !done {
		v.Results = nil
		return
	}

	v.Results = nil
	candidates := make([]search.Candidate, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir {
			candidates = append(candidates, search.Candidate{AbsPath: e.AbsPath, RelPath: e.RelPath(v.RootPath)})
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	v.Cancel = cancel
	v.Gen++
	v.ScanStart = time.Now()
	gen, query := v.Gen, v.Query
	go func() {
		results, err := search.Run(ctx, query, mode, candidates)
		v.Done <- SearchOutcome{gen: gen, results: results, err: err}
	}()
}

// clear resets the query and results back to empty (SPEC.md §9.2's
// Ctrl+U), the explicit "clear it yourself" action — since Escape
// deliberately no longer discards search state (Open), this is the only
// way to reset a query short of backspacing it out one character at a
// time. The regex-mode toggle is left as-is; it's a standing
// preference, not part of the query being cleared.
func (v *Search) clear() {
	v.Query = ""
	v.RecomputeSearch()
}

// cancel stops any in-flight background scan without applying its
// (now-stale) result, so superseding the query doesn't leave wasted
// work running against a tree that could be large.
func (v *Search) cancel() {
	if v.Cancel != nil {
		v.Cancel()
		v.Cancel = nil
	}
}

// ApplyOutcome applies a background scan's result (SPEC.md §9.1), called
// by App.Run when one arrives on Done. Stale outcomes (superseded by a
// newer query before this one finished) are discarded via the
// generation check.
func (v *Search) ApplyOutcome(out SearchOutcome) {
	if out.gen != v.Gen {
		return
	}
	v.Cancel = nil
	if out.err != nil {
		v.Error = out.err.Error()
		v.Results = nil
	} else {
		v.Results = out.results
	}
	if rows := v.rows(); v.Selected >= len(rows) {
		v.Selected = max(len(rows)-1, 0)
	}
}

// HandleKey implements the content search overlay's input handling
// (SPEC.md §9.2). Unlike quick open/jump to file, space is never an
// action key — it always types a literal space into the query, since
// content search queries are plain text rather than path fragments.
func (v *Search) HandleKey(ev *tcell.EventKey) {
	switch {
	case ev.Key() == tcell.KeyEscape:
		// The query, results, and any still-running scan are deliberately
		// left alone: content search persists across close/reopen (see
		// Open), so Escape only leaves the overlay rather than discarding
		// its state.
		*v.Overlay = OverlayNone
	case ev.Key() == tcell.KeyEnter:
		// Opening always leaves the overlay open (SPEC.md §9.2) — Escape
		// is what closes it (and preserves state when it does) — so
		// several hits can be opened in a row without re-entering the
		// search each time.
		v.performOpen()
	case ev.Key() == tcell.KeyCtrlR:
		v.Regex = !v.Regex
		v.RecomputeSearch()
	case ev.Key() == tcell.KeyCtrlU:
		v.clear()
	case ev.Key() == tcell.KeyDown:
		if rows := v.rows(); len(rows) > 0 {
			v.Selected = tree.MoveSelection(v.Selected, 1, len(rows))
		}
	case ev.Key() == tcell.KeyUp:
		if rows := v.rows(); len(rows) > 0 {
			v.Selected = tree.MoveSelection(v.Selected, -1, len(rows))
		}
	case ev.Key() == tcell.KeyTab:
		if rows := v.rows(); len(rows) > 0 && len(v.Results) > 0 {
			fi := tree.MoveSelection(rows[v.Selected].file, 1, len(v.Results))
			v.Selected = v.fileRowIndex(fi)
		}
	case ev.Key() == tcell.KeyBacktab:
		if rows := v.rows(); len(rows) > 0 && len(v.Results) > 0 {
			fi := tree.MoveSelection(rows[v.Selected].file, -1, len(v.Results))
			v.Selected = v.fileRowIndex(fi)
		}
	case ev.Key() == tcell.KeyPgDn:
		if rows := v.rows(); len(rows) > 0 {
			v.Selected = tree.MoveSelectionClamped(v.Selected, v.listHeight(), len(rows))
		}
	case ev.Key() == tcell.KeyPgUp:
		if rows := v.rows(); len(rows) > 0 {
			v.Selected = tree.MoveSelectionClamped(v.Selected, -v.listHeight(), len(rows))
		}
	case ev.Key() == tcell.KeyLeft:
		rows := v.rows()
		if v.Selected >= 0 && v.Selected < len(rows) {
			row := rows[v.Selected]
			if row.isHit || !v.Collapsed[v.Results[row.file].AbsPath] {
				v.toggleDisclosure(v.fileRowIndex(row.file))
			}
		}
	case ev.Key() == tcell.KeyRight:
		rows := v.rows()
		if v.Selected >= 0 && v.Selected < len(rows) {
			row := rows[v.Selected]
			if !row.isHit && v.Collapsed[v.Results[row.file].AbsPath] {
				v.toggleDisclosure(v.Selected)
			}
		}
	case ev.Key() == tcell.KeyBackspace, ev.Key() == tcell.KeyBackspace2:
		if len(v.Query) > 0 {
			r := []rune(v.Query)
			v.Query = string(r[:len(r)-1])
		}
		v.RecomputeSearch()
	case ev.Rune() != 0 && ev.Key() == tcell.KeyRune:
		v.Query += string(ev.Rune())
		v.RecomputeSearch()
	}
}

// performOpen implements Return (SPEC.md §9.2): open the selected row's
// file into the open-files list per §2.2's open semantics, leaving the
// overlay open so several hits can be opened in a row without
// re-entering the search — Escape is what closes the overlay (and, per
// Open/HandleKey, preserves its state when it does). A "failed" result
// (e.g. the file changed or was removed between scanning and opening)
// leaves the overlay open with the failure recorded against that file's
// path (ErrorPath/ErrorMessage) and rendered inline on its file row by
// rowLabel, plus a brief red flash (§2.2, §5.3) — attributed to the
// parent file row even when opened from one of its hit rows, the same
// as the on-open flash below. On success, the opened file's row also
// gets a brief flash (FlashPath/FlashStart, drawn in Draw) as an
// on-open confirmation distinct from the lasting "already open"
// indicator every open file's row shows regardless of whether it was
// just opened this way; the reveal-in-browser and jump-to-line side
// effects are left for App's dispatcher via LastOpenedPath/Entry/Line
// (see their doc comment).
func (v *Search) performOpen() {
	rows := v.rows()
	if v.Selected < 0 || v.Selected >= len(rows) {
		return
	}
	row := rows[v.Selected]
	result := v.Results[row.file]

	res := v.Files.Open(result.AbsPath, preview.DefaultByteCap)
	if res.Outcome != openfiles.Opened {
		v.ErrorPath = result.AbsPath
		v.ErrorMessage = res.Message
		v.ErrorFlashStart = time.Now()
		return
	}
	v.ErrorPath = ""
	v.FlashPath = result.AbsPath
	v.FlashStart = time.Now()

	// A file row jumps to its first hit (the file's earliest match); a
	// hit row jumps to that specific line (SPEC.md §9.2).
	line := result.Hits[0].LineNum
	if row.isHit {
		line = result.Hits[row.hit].LineNum
	}
	v.LastOpenedPath = result.AbsPath
	v.LastOpenedEntry = res.Entry
	v.LastOpenedLine = line
}

// searchSummary renders a "N hits across N files" right-hand summary
// for the query input row (SPEC.md §9.2), counting only files that
// actually contributed at least one hit — a file present in results
// solely because it hit a scan issue (a timeout or read error, §9.1)
// contributes no hits and isn't counted as one of the files either.
func searchSummary(results []search.FileResult) string {
	var hits, files int
	for _, r := range results {
		if len(r.Hits) == 0 {
			continue
		}
		hits += len(r.Hits)
		files++
	}
	hitWord, fileWord := "hit", "file"
	if hits != 1 {
		hitWord = "hits"
	}
	if files != 1 {
		fileWord = "files"
	}
	return fmt.Sprintf("%d %s across %d %s", hits, hitWord, files, fileWord)
}

// CurrentLegend returns content search's own header legend, for the
// help overlay (§5.4) to mirror.
func (v *Search) CurrentLegend() []canvas.LegendEntry {
	return searchLegend
}

// Draw renders the content search overlay (SPEC.md §9.2): a header row
// (title plus keybinding legend), the query input on its own row
// directly below, and a two-level list — one row per matching file,
// disclosing (unless collapsed) its own matching-line rows below it —
// or a placeholder while there's nothing to show yet.
func (v *Search) Draw(w, h int) {
	legend := searchLegend
	if v.HelpVisible {
		legend = canvas.HideKeysLegend
	}
	v.Canvas.DrawHeaderMode(w, "SEARCH", legend)
	prompt := "> "
	if v.Regex {
		prompt = "[regex] > "
	}
	queryRow := prompt + v.Query
	if v.Results != nil {
		if text, ok := canvas.FitPair(w, queryRow, searchSummary(v.Results)); ok {
			queryRow = text
		}
	}
	v.Canvas.DrawText(0, 1, w, queryRow, canvas.StyleSearchInput)

	const listTop = 2
	listHeight := h - listTop

	switch {
	case v.Query == "":
		v.Canvas.DrawText(0, listTop, w, canvas.CenterPad("type to search file contents", w), canvas.StyleNormal)
	case v.Error != "":
		v.Canvas.DrawText(0, listTop, w, canvas.CenterPad("invalid regex: "+v.Error, w), canvas.StyleError)
	case v.Results == nil:
		// SPEC.md §9.1: covers both "index not done yet" and "a scan for
		// this query is still running" — either way, nothing has been
		// found yet, which is a different state from genuinely zero
		// matches, so this must not render as "no matches." Scanning
		// itself always runs in a background goroutine (never on this
		// draw/input thread), so a slow scan over a large tree never
		// blocks keystrokes; this spinner is purely feedback that it's
		// still working, mirroring the background-index badge (§5.2).
		_, indexDone := v.Idx.Snapshot()
		var msg string
		switch {
		case !indexDone:
			msg = "indexing…"
		case v.Cancel == nil:
			// A scan hasn't been (re)started yet this draw (e.g. the very
			// first frame after a keystroke); avoid flashing a spinner
			// frame for a scan that isn't actually running.
			msg = "searching…"
		case time.Since(v.ScanStart) < canvas.SpinnerThreshold:
			msg = "searching…"
		default:
			frame := spinner.Frame(time.Since(v.ScanStart), canvas.SpinnerFPS, spinner.DefaultFrames)
			msg = "searching " + string(frame)
		}
		v.Canvas.DrawText(0, listTop, w, canvas.CenterPad(msg, w), canvas.StyleNormal)
	case len(v.Results) == 0:
		v.Canvas.DrawText(0, listTop, w, canvas.CenterPad("no matches", w), canvas.StyleNormal)
	default:
		rows := v.rows()
		if listHeight > 0 {
			if v.Selected < v.Scroll {
				v.Scroll = v.Selected
			}
			if v.Selected >= v.Scroll+listHeight {
				v.Scroll = v.Selected - listHeight + 1
			}
		}
		for line := range listHeight {
			i := v.Scroll + line
			if i >= len(rows) {
				break
			}
			row := rows[i]
			label, issueStart, nameStart, nameLen := v.rowLabel(row)
			fileErrored := !row.isHit && v.ErrorPath != "" && v.Results[row.file].AbsPath == v.ErrorPath
			if fileErrored {
				label += " [" + v.ErrorMessage + "]"
			}
			style := canvas.StyleNormal
			switch {
			case fileErrored && time.Since(v.ErrorFlashStart) < canvas.FlashDuration:
				style = canvas.StyleFlashError
			case i == v.Selected:
				style = canvas.StyleSelected
			case !row.isHit && v.Results[row.file].AbsPath == v.FlashPath && time.Since(v.FlashStart) < canvas.FlashDuration:
				style = canvas.StyleFlash
			}
			v.Canvas.DrawText(0, listTop+line, w, label, style)
			// A file row's own name is bolded when it actually
			// contributed at least one hit, distinguishing it at a
			// glance from a file listed solely for a scan issue (§9.1)
			// with nothing found — layered on top of the row's own
			// style (selection/flash/error) rather than replacing it.
			if nameStart >= 0 {
				name := []rune(label)[nameStart : nameStart+nameLen]
				v.Canvas.DrawText(nameStart, listTop+line, nameLen, string(name), style.Bold(true))
			}
			// A persistent scan issue (SPEC.md §9.1's Issue) is rendered
			// in the same contrasting error style the invalid-regex
			// message above uses, layered on top of the row's own style
			// rather than replacing it, so the row's selection/flash
			// state stays visible while the problem still stands out.
			if issueStart >= 0 {
				suffix := []rune(label)[issueStart:]
				v.Canvas.DrawText(issueStart, listTop+line, w-issueStart, string(suffix), canvas.StyleError)
			}
		}
	}
}

// rowLabel renders one flattened search-result row (SPEC.md §9.2): a
// file row shows a disclosure indicator, an "already open" indicator
// (●) when the open-files list already has that file open, then its
// root-relative path and hit count; a hit row is indented under its
// file and shows its 1-based line number and (trimmed) text. The open
// indicator is deliberately file-row-only, not repeated on each hit row
// underneath, since "open" is a per-file fact. issueStart is the rune
// index within the returned label where an appended scan-issue message
// begins (SPEC.md §9.1's Issue), or -1 if the row has none — the caller
// uses it to render that suffix in a contrasting error style without a
// second, separately-tracked copy of the message. nameStart/nameLen
// similarly bound a file row's own path text (the marker plus the
// open-indicator plus two spaces always precede it, fixed at 4 runes),
// or (-1, 0) for a hit row or a file row with zero hits — the caller
// uses this to bold the name of every file that actually contributed a
// hit, distinguishing it at a glance from a file listed solely for a
// scan issue (§9.1) with nothing found.
func (v *Search) rowLabel(row searchRow) (label string, issueStart, nameStart, nameLen int) {
	r := v.Results[row.file]
	if row.isHit {
		h := r.Hits[row.hit]
		// Indented past where the owning file row's own path text starts
		// (marker + open-indicator + two spaces = 4 columns), so a hit
		// row reads as visually nested under its file rather than lining
		// up with it.
		return fmt.Sprintf("      %d: %s", h.LineNum, strings.TrimSpace(h.LineText)), -1, -1, 0
	}
	marker := "▾"
	if v.Collapsed[r.AbsPath] {
		marker = "▸"
	}
	open := " "
	if v.Files.IsOpen(r.AbsPath) {
		open = "●"
	}
	plural := "es"
	if len(r.Hits) == 1 {
		plural = ""
	}
	base := fmt.Sprintf("%s %s %s", marker, open, r.RelPath)
	nameStart, nameLen = -1, 0
	if len(r.Hits) > 0 {
		nameStart, nameLen = 4, len([]rune(r.RelPath))
	}
	switch {
	case r.Issue == "":
		return fmt.Sprintf("%s (%d match%s)", base, len(r.Hits), plural), -1, nameStart, nameLen
	case len(r.Hits) > 0:
		prefix := fmt.Sprintf("%s (%d match%s) — ", base, len(r.Hits), plural)
		return prefix + r.Issue, len([]rune(prefix)), nameStart, nameLen
	default:
		prefix := base + " — "
		return prefix + r.Issue, len([]rune(prefix)), nameStart, nameLen
	}
}
