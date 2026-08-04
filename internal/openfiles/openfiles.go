// Package openfiles implements the open-files list, the primary state
// the rest of the UI operates on (SPEC.md §2.2): an ordered collection
// of files opened during the session, plus which one (if any) is
// currently displayed, kept free of any terminal-rendering dependency
// so it's unit-testable. List itself knows nothing about tiers or an
// entry's own content/state — it operates purely through the minimal
// internal/entry.Entry interface (Path/Close/Evict), delegating all
// per-file tier-deciding/reload logic to that package. What List does
// own is genuine list mechanics: order, paging, displayed index,
// dedup-by-path, and the resident-content LRU policy (below).
package openfiles

import (
	"path/filepath"

	"github.com/nitti/dirtree/internal/entry"
	"github.com/nitti/dirtree/internal/preview"
)

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
	Entry   entry.Entry
}

// List is the ordered open-files list plus which entry, if any, is
// currently displayed.
type List struct {
	Entries   []entry.Entry
	Displayed int // index into Entries, or -1 if none

	// resident tracks which entries currently hold resident content
	// (in practice, only a TierHighlighted entry's Lines/Segs — but
	// List itself doesn't know or care which entries that is, only that
	// Evict() is safe to call on any of them), most-recently-displayed
	// first, capped at ResidentCap (below) — the LRU eviction
	// bookkeeping behind the memory-footprint bound entry.TextEntry's
	// own SyncContent/Evict implement.
	resident []entry.Entry
}

// ResidentCap bounds how many entries' fully-decoded/highlighted content
// are kept memory-resident at once, LRU-evicted beyond that. Without a
// cap, every TierHighlighted file ever opened in a session stays fully
// resident until manually closed (`x`), even though only the
// currently-displayed entry is ever on screen — a handful of files near
// HighlightCeiling left open can retain hundreds of MB for no benefit.
// Kept deliberately small, the same "simple fixed constant, tuned later
// against real usage" stance internal/ui/views/scroll.go's
// windowLines/windowMargin already take, so ordinary quick tab-switching
// among a few recently-viewed files still finds their content resident
// (no re-read/re-highlight), while a session that accumulates many open
// files doesn't retain all of them forever.
const ResidentCap = 4

// touchResident marks e as the most-recently-displayed entry for
// residency purposes, evicting the least-recently-displayed entry's
// resident content once more than ResidentCap entries have been
// displayed. Called every time an entry becomes displayed — newly
// opened, reused by path, or switched to — so the resident set always
// reflects actual recent viewing, not list order or open order.
func (l *List) touchResident(e entry.Entry) {
	for i, r := range l.resident {
		if r == e {
			l.resident = append(l.resident[:i], l.resident[i+1:]...)
			break
		}
	}
	l.resident = append([]entry.Entry{e}, l.resident...)
	for len(l.resident) > ResidentCap {
		last := len(l.resident) - 1
		l.resident[last].Evict()
		l.resident = l.resident[:last]
	}
}

// forgetResident drops e from residency tracking without evicting its
// content — for Remove, where e is leaving the list entirely, so there's
// nothing left to evict content *from*, but the stale reference must not
// be left in l.resident or it would keep e (and its content) reachable,
// and thus never collected, for as long as the list itself lives.
func (l *List) forgetResident(e entry.Entry) {
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
func (l *List) DisplayedEntry() entry.Entry {
	if l.Displayed < 0 || l.Displayed >= len(l.Entries) {
		return nil
	}
	return l.Entries[l.Displayed]
}

// Open implements SPEC.md §2.2's open semantics for a resolved absolute
// path: reuse an existing entry without re-reading or moving it if the
// path is already open (List's own dedup-by-path concern); otherwise
// delegate entirely to entry.Open, which checks the file (capBytes
// bounds this check, SPEC.md §2.1) for a read error — a failed result,
// with no entry created and the currently-displayed entry (if any) left
// unchanged — decides tier, and starts whatever background pass that
// tier needs. Binary content is not a failure: entry.Open returns a
// TierBinary entry (a hex view) instead.
func (l *List) Open(path string, capBytes int64) OpenResult {
	for i, e := range l.Entries {
		if e.Path() == path {
			l.Displayed = i
			l.touchResident(e)
			return OpenResult{Outcome: Opened, Entry: e}
		}
	}

	e, err := entry.Open(path, capBytes)
	if err != nil {
		return OpenResult{Outcome: Failed, Message: preview.ErrText(err)}
	}

	l.Entries = append(l.Entries, e)
	l.Displayed = len(l.Entries) - 1
	l.touchResident(e)
	return OpenResult{Outcome: Opened, Entry: e}
}

// Reload checks every open entry against current disk state (SPEC.md
// §6.1a) by delegating to entry.Reload, which re-stats each entry's own
// path, re-reads and re-decides tier if it changed, and reports whether
// anything actually happened — List itself no longer stats paths,
// compares mtimes, or knows anything about tiers. Entry identity, list
// position, and displayed status are all untouched by a reload that
// doesn't flip tier (entry.Reload mutates that same object in place);
// one that does flip tier hands back a different concrete object, which
// this simply writes into the same list slot — list order and displayed
// index still never move either way.
//
// Returns the base name of each entry that was actually reloaded, in
// list order (nil if none changed), for callers that want to surface a
// transient notification.
func (l *List) Reload(capBytes int64) []string {
	var reloaded []string
	for i, e := range l.Entries {
		updated, changed, err := entry.Reload(e, capBytes)
		if err != nil || !changed {
			continue
		}
		l.Entries[i] = updated
		reloaded = append(reloaded, filepath.Base(updated.Path()))
	}
	return reloaded
}

// IsOpen reports whether path already has an entry in the list.
func (l *List) IsOpen(path string) bool {
	for _, e := range l.Entries {
		if e.Path() == path {
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
// selected entry — and returns the overlay's new selected index. Calls
// the removed entry's Close() first, canceling any in-flight background
// scan it was holding rather than leaving it to finish unread.
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
	l.Entries[i].Close()
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
