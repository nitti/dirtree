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
	Path  string
	Lines []string
	Segs  [][]preview.Segment

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

	// CopyMode strips the preview's line-number gutter and syntax-color
	// styling for this entry specifically (SPEC.md §2.1), so a terminal
	// mouse selection over its content grabs exactly the file's own
	// characters. Per entry rather than global, like the rest of this
	// struct's state, so switching files doesn't carry a copy-mode
	// preference that had nothing to do with the file you're switching
	// to.
	CopyMode bool
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

// Open implements SPEC.md §2.2's open semantics for a resolved absolute
// path: reuse an existing entry without re-reading or moving it if the
// path is already open; otherwise read/highlight the file (capBytes is
// the byte cap, SPEC.md §2.1) and, on success, append a new entry at
// the end with scroll reset to the top and mark it displayed. A read
// error or binary content is a failed result: no entry is created and
// the currently-displayed entry (if any) is left unchanged.
func (l *List) Open(path string, capBytes int64) OpenResult {
	for i, e := range l.Entries {
		if e.Path == path {
			l.Displayed = i
			return OpenResult{Outcome: Opened, Entry: e}
		}
	}

	res := preview.Load(path, capBytes)
	if res.Failed {
		return OpenResult{Outcome: Failed, Message: res.Message}
	}

	e := &Entry{Path: path, Lines: res.Lines, Segs: res.Segs, FindCurrent: -1}
	if info, err := os.Stat(path); err == nil {
		e.ModTime = info.ModTime()
	}
	l.Entries = append(l.Entries, e)
	l.Displayed = len(l.Entries) - 1
	return OpenResult{Outcome: Opened, Entry: e}
}

// Reload checks every open entry against current disk state (SPEC.md
// §6.1a) and re-reads any whose mtime has changed since it was last
// loaded or reloaded, replacing its content in place — entry identity,
// list position, and displayed status are all untouched, so this never
// disturbs list order or which entry is currently shown. The wrap
// cache and any in-file find state are invalidated, since both are
// derived from content that just changed; Scroll is left as-is and
// self-clamps the next time the entry is scrolled or displayed.
//
// An entry whose file can no longer be stat'd or read (deleted,
// permission lost, now binary) is left with its last-known content
// untouched — it simply goes stale, per §6.1's existing handling for a
// currently-open file whose underlying path disappears.
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
		res := preview.Load(e.Path, capBytes)
		if res.Failed {
			continue
		}
		e.Lines = res.Lines
		e.Segs = res.Segs
		e.ModTime = info.ModTime()
		e.Rows = nil
		e.FirstRow = nil
		e.RowsWidth = 0
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
