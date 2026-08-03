// Package openfiles implements the open-files list, the primary state
// the rest of the UI operates on (SPEC.md §2.2): an ordered collection
// of files opened during the session, plus which one (if any) is
// currently displayed, kept free of any terminal-rendering dependency
// so it's unit-testable.
package openfiles

import (
	"os"
	"path/filepath"
	"time"

	"github.com/nitti/dirtree/internal/find"
	"github.com/nitti/dirtree/internal/preview"
)

// Entry is one open file: its resolved absolute path, loaded preview
// content, and its own independent scroll/goto-line/in-file-find state
// (SPEC.md §2.1, §2.2, §2.4).
type Entry struct {
	Path string

	// Tier is decided from the file's size (SPEC.md §2.1's ceiling) at
	// open time, and re-decided the same way on every reload that
	// changes the file (docs/STREAMING_PREVIEW_DESIGN.md §2) — not
	// sticky across the entry's lifetime.
	Tier preview.Tier

	// Lines/Segs hold this entry's resident content. For a
	// preview.TierHighlighted entry, this is the whole file, populated
	// from Stream once its background pass finishes (nil until then).
	// For a preview.TierPlainText entry, this is only the current
	// on-screen window — refetched as scrolling moves outside it — and
	// WindowStartLine is the 0-based count of source lines before
	// Lines[0] (always 0 for TierHighlighted, whose Lines always starts
	// at the file's first line).
	Lines           []string
	Segs            [][]preview.Segment
	WindowStartLine int

	// Stream is the background pass kicked off when this entry was
	// opened (docs/STREAMING_PREVIEW_DESIGN.md §3, §4): it builds the
	// line-offset index unconditionally, and — for a TierHighlighted
	// entry — the full decoded/highlighted content consumed into
	// Lines/Segs above once done. It's what goto-line gates itself on
	// (SPEC.md §2.1), what the file title bar's "building…" indicator
	// reflects (SPEC.md §5.2), and, for TierPlainText, what windowed
	// reads seek through (§8).
	Stream *preview.StreamIndex

	// ModTime is the file's mtime as of the last successful load or
	// reload (SPEC.md §6.1a), used by Reload to detect whether the file
	// has changed on disk since without re-reading its content on every
	// check.
	ModTime time.Time

	// Scroll is the entry's display-row scroll offset, preserved across
	// switches to other entries and restored exactly when this one is
	// displayed again.
	Scroll int

	// Rows/FirstRow/RowsWidth cache the last-computed line-wrap for this
	// entry (SPEC.md §2.1's "recomputed whenever the available width
	// changes"); RowsWidth is the width the cache was computed for, 0
	// meaning "not yet computed."
	Rows      []preview.DisplayRow
	FirstRow  map[int]int
	RowsWidth int

	// In-file find state (SPEC.md §2.4), independent per entry like
	// scroll/goto-line above. FindQuery is the last executed search
	// ("" means no active find); FindMatches is every match it found,
	// in source order; FindCurrent indexes the currently-highlighted
	// match into FindMatches (-1 if there are none); FindWrapNote is a
	// transient "wrapped to top/bottom" note set by the most recent
	// next/previous step that crossed an end of the match list, cleared
	// by the next step that doesn't.
	FindQuery    string
	FindMatches  []find.Match
	FindCurrent  int
	FindWrapNote string

	// FindScan is a TierPlainText entry's in-progress background find
	// scan (docs/STREAMING_PREVIEW_DESIGN.md §9), non-nil only while one
	// is running: that tier never holds full file content resident, so
	// FindMatches can't be computed synchronously from Lines the way a
	// TierHighlighted entry's find already is. Cleared once its result
	// has been consumed into FindMatches, or by Reload/clearing an
	// active find, either of which cancels it first if still running.
	FindScan *find.Scan

	// CopyMode strips the preview's line-number gutter and syntax-color
	// styling for this entry specifically (SPEC.md §2.1), so a terminal
	// mouse selection over its content grabs exactly the file's own
	// characters. Per entry rather than global, like the rest of this
	// struct's state, so switching files doesn't carry a copy-mode
	// preference that had nothing to do with the file you're switching
	// to.
	CopyMode bool

	// Size and HexOffset are meaningful only for a preview.TierBinary
	// entry (see Tier's doc comment). Size is the file's byte length as
	// of the last successful load/reload, sized once at open/reload time
	// rather than re-stat'd every frame; it sizes the hex view's offset
	// gutter and bounds HexOffset's top end. HexOffset is the byte offset
	// of the first byte shown in the hex view's viewport — the TierBinary
	// analog of Scroll's display-row offset for text tiers, kept as a
	// separate field rather than overloading Scroll since the two count
	// fundamentally different things (rows vs. bytes) and a tier flip
	// (Reload) always resets whichever one applied to the old tier.
	Size      int64
	HexOffset int64
}

// Outcome is the result of an open attempt (SPEC.md §2.2).
type Outcome int

const (
	// Opened means an entry now exists (new or reused) and is displayed.
	Opened Outcome = iota
	// Failed means no entry was created or changed; Message explains why.
	Failed
)

// OpenResult is the outcome of List.Open.
type OpenResult struct {
	Outcome Outcome
	Message string
	Entry   *Entry
}

// List is the ordered open-files list plus which entry, if any, is
// currently displayed.
type List struct {
	Entries   []*Entry
	Displayed int // index into Entries, or -1 if none

	// resident tracks which entries currently hold a TierHighlighted
	// entry's resident Lines/Segs, most-recently-displayed first,
	// capped at ResidentCap (below) — the LRU eviction bookkeeping
	// behind SyncContent's memory-footprint bound. Never touches
	// TierPlainText entries, which never hold full content resident to
	// begin with.
	resident []*Entry
}

// ResidentCap bounds how many entries' fully-decoded/highlighted content
// (Lines/Segs) are kept memory-resident at once, LRU-evicted beyond
// that. Without a cap, every TierHighlighted file ever opened in a
// session stays fully resident until manually closed (`x`), even though
// only the currently-displayed entry is ever on screen — a handful of
// files near HighlightCeiling left open can retain hundreds of MB for
// no benefit. Kept deliberately small, the same "simple fixed constant,
// tuned later against real usage" stance internal/ui/views/scroll.go's
// windowLines/windowMargin already take, so ordinary quick tab-switching
// among a few recently-viewed files still finds their content resident
// (no re-read/re-highlight), while a session that accumulates many open
// files doesn't retain all of them forever.
const ResidentCap = 4

// touchResident marks e as the most-recently-displayed entry for
// residency purposes, evicting the least-recently-displayed entry's
// Lines/Segs once more than ResidentCap entries have been displayed.
// Called every time an entry becomes displayed — newly opened, reused
// by path, or switched to — so the resident set always reflects actual
// recent viewing, not list order or open order.
func (l *List) touchResident(e *Entry) {
	for i, r := range l.resident {
		if r == e {
			l.resident = append(l.resident[:i], l.resident[i+1:]...)
			break
		}
	}
	l.resident = append([]*Entry{e}, l.resident...)
	for len(l.resident) > ResidentCap {
		last := len(l.resident) - 1
		l.resident[last].evictContent()
		l.resident = l.resident[:last]
	}
}

// forgetResident drops e from residency tracking without evicting its
// content — for Remove, where e is leaving the list entirely, so there's
// nothing left to evict content *from*, but the stale pointer must not
// be left in l.resident or it would keep e (and its content) reachable,
// and thus never collected, for as long as the list itself lives.
func (l *List) forgetResident(e *Entry) {
	for i, r := range l.resident {
		if r == e {
			l.resident = append(l.resident[:i], l.resident[i+1:]...)
			return
		}
	}
}

// New returns an empty open-files list.
func New() *List {
	return &List{Displayed: -1}
}

// DisplayedEntry returns the currently-displayed entry, or nil.
func (l *List) DisplayedEntry() *Entry {
	if l.Displayed < 0 || l.Displayed >= len(l.Entries) {
		return nil
	}
	return l.Entries[l.Displayed]
}

// SyncContent copies a TierHighlighted entry's full decoded/highlighted
// content out of its background stream into Lines/Segs, once that
// pass has finished — a no-op if Lines is already populated, if the
// stream isn't done yet, or if e is TierPlainText, which never builds
// full content in the stream at all (docs/STREAMING_PREVIEW_DESIGN.md
// §2, §8). Safe to call every frame; callers (the preview view, and
// tests that need to observe reloaded content) call it before reading
// Lines/Segs rather than assuming Open/Reload populated them
// synchronously, since that work now happens in the background.
func (e *Entry) SyncContent() {
	if e.Tier != preview.TierHighlighted || e.Lines != nil || e.Stream == nil {
		return
	}
	lines, segs := e.Stream.Content()
	if lines == nil {
		return
	}
	e.Lines = lines
	e.Segs = segs
}

// evictContent frees e's resident Lines/Segs — the memory-heavy
// decoded/highlighted copy of a TierHighlighted file's full content,
// SyncContent's counterpart — and restarts e's background Stream so
// SyncContent transparently rebuilds them (re-read plus re-highlight,
// the same work Open/Reload already do) the next time e is displayed,
// rather than leaving Lines/Segs permanently nil. Note this restart
// means switching back to an evicted entry briefly shows the same
// "building preview…" state a freshly opened file does, until the new
// background pass finishes — normally fast, but a real (and deliberate)
// memory-for-latency tradeoff for a file that takes a while to
// highlight, same as the original open did.
//
// A no-op for a TierPlainText entry, which never holds full content
// resident to begin with (it holds only an on-screen window,
// independently bounded by windowLines), or for an entry whose content
// isn't resident yet (e.g. its background pass hasn't finished, or it
// was already evicted) — nothing to evict, and restarting an
// already-running or not-yet-started pass would just be wasted work.
func (e *Entry) evictContent() {
	if e.Tier != preview.TierHighlighted || e.Lines == nil {
		return
	}
	e.Lines = nil
	e.Segs = nil
	e.Stream = preview.StartStream(e.Path, e.Tier)
}

// Open implements SPEC.md §2.2's open semantics for a resolved absolute
// path: reuse an existing entry without re-reading or moving it if the
// path is already open; otherwise check the file (capBytes bounds this
// check, SPEC.md §2.1) for a read error — a failed result, with no entry
// created and the currently-displayed entry (if any) left unchanged.
// Binary content is not a failure: it opens as a preview.TierBinary
// entry (a hex view) instead. Otherwise, decide the new entry's tier
// from its size (docs/STREAMING_PREVIEW_DESIGN.md §5) and start its
// background content/index pass (§3, §4) rather than reading/
// highlighting synchronously here: the entry appears immediately and
// lights up once that pass reports progress (SPEC.md §2.1, §5.2).
func (l *List) Open(path string, capBytes int64) OpenResult {
	for i, e := range l.Entries {
		if e.Path == path {
			l.Displayed = i
			l.touchResident(e)
			return OpenResult{Outcome: Opened, Entry: e}
		}
	}

	_, binary, err := preview.ReadCapped(path, capBytes)
	if err != nil {
		return OpenResult{Outcome: Failed, Message: preview.ErrText(err)}
	}

	e := &Entry{Path: path, FindCurrent: -1}
	var size int64
	if info, err := os.Stat(path); err == nil {
		e.ModTime = info.ModTime()
		size = info.Size()
	}
	e.Size = size
	if binary {
		e.Tier = preview.TierBinary
	} else {
		e.Tier = preview.TierFor(size)
		e.Stream = preview.StartStream(path, e.Tier)
	}
	l.Entries = append(l.Entries, e)
	l.Displayed = len(l.Entries) - 1
	l.touchResident(e)
	return OpenResult{Outcome: Opened, Entry: e}
}

// Reload checks every open entry against current disk state (SPEC.md
// §6.1a) and re-reads any whose mtime has changed since it was last
// loaded or reloaded, replacing its content in place — entry identity,
// list position, and displayed status are all untouched, so this never
// disturbs list order or which entry is currently shown. The wrap
// cache and any in-file find state are invalidated, since both are
// derived from content that just changed; Scroll is left as-is and
// self-clamps the next time the entry is scrolled or displayed, unless
// the reload also flips the entry's tier (below), in which case Scroll
// is reset to the top instead.
//
// The entry's tier (docs/STREAMING_PREVIEW_DESIGN.md §2, §5) is
// re-decided here from the file's current on-disk size, the same way
// it's decided at open time — not treated as sticky from whenever the
// entry was first opened. This falls out of the Stat call already made
// to compare mtimes: re-checking size against the ceiling at that same
// moment costs nothing extra, and reload is the only mechanism by which
// dirtree ever learns a file changed at all, so it's the only point
// where a stale tier decision could otherwise ever be corrected. Without
// this, a file that grows well past the ceiling after being opened would
// keep being fully read and highlighted in the background on every
// reload for the rest of the session, exactly the unbounded cost the
// ceiling exists to prevent; a file that shrinks back under the ceiling
// is symmetrically promoted back to full highlighting. A tier flip
// resets Scroll to the top: a display-row index means something
// different under each tier's model (an index into a whole-file wrap
// cache for TierHighlighted vs. into a small on-screen window for
// TierPlainText, §8), so there's no meaningful way to carry the old
// value across the change — it would either point at an arbitrary,
// unrelated row or fall past the end of a much smaller window.
//
// An entry whose file can no longer be stat'd or read (deleted or
// permission lost) is left with its last-known content untouched — it
// simply goes stale, per §6.1's existing handling for a currently-open
// file whose underlying path disappears. A file that has become binary
// is not treated this way: like Open, it re-tiers into preview.TierBinary
// (a hex view) instead of going stale, the same as any other tier flip
// below.
//
// Returns the base name of each entry that was actually reloaded, in
// list order (nil if none changed), for callers that want to surface a
// transient notification.
func (l *List) Reload(capBytes int64) []string {
	var reloaded []string
	for _, e := range l.Entries {
		info, err := os.Stat(e.Path)
		if err != nil || info.ModTime().Equal(e.ModTime) {
			continue
		}
		_, binary, err := preview.ReadCapped(e.Path, capBytes)
		if err != nil {
			continue
		}

		e.Size = info.Size()
		newTier := preview.TierBinary
		if !binary {
			newTier = preview.TierFor(info.Size())
		}
		if newTier != e.Tier {
			e.Tier = newTier
			e.Scroll = 0
			e.HexOffset = 0
		}
		e.Lines = nil
		e.Segs = nil
		e.WindowStartLine = 0
		e.ModTime = info.ModTime()
		if binary {
			e.Stream = nil
		} else {
			e.Stream = preview.StartStream(e.Path, e.Tier)
		}
		e.Rows = nil
		e.FirstRow = nil
		e.RowsWidth = 0
		if e.FindScan != nil {
			e.FindScan.Cancel()
			e.FindScan = nil
		}
		e.FindQuery = ""
		e.FindMatches = nil
		e.FindCurrent = -1
		e.FindWrapNote = ""
		reloaded = append(reloaded, filepath.Base(e.Path))
	}
	return reloaded
}

// IsOpen reports whether path already has an entry in the list.
func (l *List) IsOpen(path string) bool {
	for _, e := range l.Entries {
		if e.Path == path {
			return true
		}
	}
	return false
}

// Display marks the entry at i as displayed without changing list
// order (SPEC.md §2.2's "displaying an entry never changes list
// order"). No-op if i is out of range.
func (l *List) Display(i int) {
	if i < 0 || i >= len(l.Entries) {
		return
	}
	l.Displayed = i
	l.touchResident(l.Entries[i])
}

// Remove removes the entry at i — the open-files-list overlay's own
// selected index, since §2.3's `x` always removes the currently
// selected entry — and returns the overlay's new selected index.
//
// If the removed entry was not displayed, the displayed entry and its
// state are unaffected and only the list shrinks; the returned index
// prefers the entry that was next after the removed one, falling back
// to the new last entry if the removed one was last. If the removed
// entry was displayed, the adjacent surviving entry (next, or previous
// if it was last) becomes displayed, and the returned index follows it
// — which happens to be the same clamped position in both cases.
func (l *List) Remove(i int) (newSelected int) {
	if i < 0 || i >= len(l.Entries) {
		return clampIndex(l.Displayed, len(l.Entries))
	}

	wasDisplayed := i == l.Displayed
	l.forgetResident(l.Entries[i])
	l.Entries = append(l.Entries[:i], l.Entries[i+1:]...)

	if len(l.Entries) == 0 {
		l.Displayed = -1
		return 0
	}

	newIdx := i
	if newIdx >= len(l.Entries) {
		newIdx = len(l.Entries) - 1
	}
	switch {
	case wasDisplayed:
		l.Displayed = newIdx
	case l.Displayed > i:
		l.Displayed--
	}
	return newIdx
}

// MoveUp moves the entry at i one position toward the top of the list,
// swapping it with its current neighbor, and returns its new index.
// No-op (returns i unchanged) if i is the first entry or out of range
// (SPEC.md §2.3: reordering does not wrap).
func (l *List) MoveUp(i int) int {
	if i <= 0 || i >= len(l.Entries) {
		return i
	}
	l.swap(i, i-1)
	return i - 1
}

// MoveDown moves the entry at i one position toward the bottom of the
// list, swapping it with its current neighbor, and returns its new
// index. No-op (returns i unchanged) if i is the last entry or out of
// range.
func (l *List) MoveDown(i int) int {
	if i < 0 || i >= len(l.Entries)-1 {
		return i
	}
	l.swap(i, i+1)
	return i + 1
}

func (l *List) swap(a, b int) {
	l.Entries[a], l.Entries[b] = l.Entries[b], l.Entries[a]
	switch l.Displayed {
	case a:
		l.Displayed = b
	case b:
		l.Displayed = a
	}
}

func clampIndex(i, count int) int {
	if count == 0 {
		return 0
	}
	if i < 0 {
		return 0
	}
	if i >= count {
		return count - 1
	}
	return i
}

// MoveUpPage moves the entry at i up to pageSize positions toward the
// top of the list (SPEC.md §2.3's Shift-Page-Up bulk reorder) by
// repeating MoveUp, stopping early — rather than wrapping — the moment
// a step is a no-op (i.e. i has reached the first entry). Returns the
// entry's final index.
func (l *List) MoveUpPage(i, pageSize int) int {
	for range pageSize {
		next := l.MoveUp(i)
		if next == i {
			break
		}
		i = next
	}
	return i
}

// MoveDownPage moves the entry at i up to pageSize positions toward the
// bottom of the list (SPEC.md §2.3's Shift-Page-Down bulk reorder), the
// mirror of MoveUpPage.
func (l *List) MoveDownPage(i, pageSize int) int {
	for range pageSize {
		next := l.MoveDown(i)
		if next == i {
			break
		}
		i = next
	}
	return i
}

// PageSize is the number of open-files entries shown per page in the
// open-files-list overlay's dropdown (SPEC.md §2.3): fixed so each
// visible row can be labeled with a single digit (0-9) for direct
// select-and-open.
const PageSize = 10

// Page returns the 0-based page index i falls on for the given page
// size (SPEC.md §2.3). Page is deliberately not stored anywhere — it's
// derived fresh from the selected index and the list's current length
// every time it's needed, the same "recompute from current state"
// discipline the rest of the app's layout follows, so a page never
// needs to be kept in sync with reorders/removals happening elsewhere.
func Page(i, pageSize int) int {
	if pageSize <= 0 || i < 0 {
		return 0
	}
	return i / pageSize
}

// PageCount returns the total number of pages for count entries and the
// given page size (at least 1, so an empty or single-page list still
// reports one page).
func PageCount(count, pageSize int) int {
	if count <= 0 || pageSize <= 0 {
		return 1
	}
	return (count-1)/pageSize + 1
}

// PageBounds returns the [start, end) index bounds of page p within a
// list of count entries at the given page size.
func PageBounds(p, pageSize, count int) (start, end int) {
	start = p * pageSize
	if start > count {
		start = count
	}
	end = start + pageSize
	if end > count {
		end = count
	}
	return start, end
}

// SelectPage returns the selected index after jumping a full page
// forward/backward (delta = +1/-1) from i's current page, landing on
// the target page's first entry (SPEC.md §2.3's Page Up/Down). Clamped
// at the first/last page rather than wrapping — a no-op, returning i
// unchanged, past either end or on an empty list.
func SelectPage(i, delta, pageSize, count int) int {
	if count == 0 {
		return i
	}
	p := Page(i, pageSize) + delta
	if p < 0 || p >= PageCount(count, pageSize) {
		return i
	}
	start, _ := PageBounds(p, pageSize, count)
	return start
}

// SelectDigit returns the entry index for digit d (0-9) on i's current
// page, and whether that position actually holds an entry (SPEC.md
// §2.3's digit-key instant open, a no-op past the current page's last
// row — e.g. a short final page).
func SelectDigit(i, d, pageSize, count int) (idx int, ok bool) {
	start, end := PageBounds(Page(i, pageSize), pageSize, count)
	idx = start + d
	return idx, idx >= start && idx < end
}
