// Package openfiles implements the open-files list, the primary state
// the rest of the UI operates on (SPEC.md §2.2): an ordered collection
// of files opened during the session, plus which one (if any) is
// currently displayed, kept free of any terminal-rendering dependency
// so it's unit-testable.
package openfiles

import (
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
	l.Entries = append(l.Entries, e)
	l.Displayed = len(l.Entries) - 1
	return OpenResult{Outcome: Opened, Entry: e}
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
