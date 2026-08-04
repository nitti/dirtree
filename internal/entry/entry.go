// Package entry owns one open file's tier-specific state and the logic
// that decides/re-decides which tier it belongs to (SPEC.md §2.1,
// §2.1a, §2.2, §6.1a) — reading the file, checking for binary content,
// comparing size against the highlighting ceiling, and constructing or
// reloading the right concrete entry accordingly. Kept free of any
// terminal-rendering dependency, same as internal/openfiles, so it's
// unit-testable without a terminal.
//
// This package deliberately exports no interface: TextEntry/HexEntry
// are concrete types, and Open/Reload return any rather than a
// producer-declared abstraction, so that every consumer (internal/
// openfiles.List, internal/ui/views) declares its own interface sized
// to exactly what it needs and asserts down to it — "accept
// interfaces, return structs," not the other way around. That's the
// whole point of this package existing separately from openfiles: it
// owns tier-deciding logic and the tier-specific data, but never
// dictates how a consumer is allowed to talk about it.
package entry

import (
	"os"
	"time"

	"github.com/nitti/dirtree/internal/find"
	"github.com/nitti/dirtree/internal/hexfind"
	"github.com/nitti/dirtree/internal/preview"
)

// EntryInfo is the state common to every open-files entry regardless of
// tier: its resolved path, decided tier, and on-disk snapshot as of the
// last successful load/reload. Embedded directly into TextEntry/
// HexEntry so same-package code and a type-asserted caller in
// internal/ui/views read these fields directly (te.Tier, te.ModTime,
// te.Size). Path is the one exception: TextEntry/HexEntry both define a
// Path() method (to satisfy whichever small interface a consumer
// package declares for itself — see the package doc comment) which,
// per Go's embedding rules, shadows EntryInfo's own Path field for
// direct promoted access (te.Path means the method, not the field);
// reach the field itself via the method, or via the whole embedded
// EntryInfo value (te.EntryInfo) when constructing/replacing it
// wholesale, never via a single te.EntryInfo.Path assignment mixed
// with the promoted name elsewhere — this file's Open/Reload always
// replace EntryInfo as one unit for exactly that reason.
type EntryInfo struct {
	Path    string
	Tier    preview.Tier
	ModTime time.Time
	Size    int64
}

// TextEntry is the state of a text-tier (TierHighlighted or
// TierPlainText) entry: its resident/windowed content, scroll and
// wrap-cache, in-file find, and copy mode.
type TextEntry struct {
	EntryInfo

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
}

// HexEntry is the state of a preview.TierBinary (hex view) entry: its
// viewport offset and hex-view find state.
type HexEntry struct {
	EntryInfo

	// HexOffset is the byte offset of the first byte shown in the hex
	// view's viewport — the TierBinary analog of TextEntry.Scroll's
	// display-row offset, kept as a separate field rather than
	// overloading Scroll since the two count fundamentally different
	// things (rows vs. bytes), and a tier flip (Reload) always
	// discards whichever state applied to the old tier anyway.
	HexOffset int64

	// Hex-view find state (SPEC.md §2.1a), the TierBinary analog of
	// TextEntry's FindQuery/FindMatches/FindCurrent/FindWrapNote —
	// kept as its own separate set of fields rather than shared with
	// them, since hexfind.Match addresses content by byte offset
	// rather than the line/rune-column pair find.Match uses, and the
	// two coordinate systems don't compose. HexFindScan is always used
	// for a TierBinary entry's find (never left nil the way FindScan
	// is for a TierHighlighted entry, which searches its resident
	// Lines synchronously instead): a hex view never holds a file's
	// full byte content resident regardless of size, so every hex find
	// is a background scan.
	HexFindQuery    string
	HexFindMatches  []hexfind.Match
	HexFindCurrent  int
	HexFindWrapNote string
	HexFindScan     *hexfind.Scan
}

func (e *TextEntry) Path() string { return e.EntryInfo.Path }
func (e *HexEntry) Path() string  { return e.EntryInfo.Path }

// Close cancels a still-running in-file find scan, if any — called by
// List.Remove so a removed entry doesn't leak a background goroutine.
func (e *TextEntry) Close() {
	if e.FindScan != nil {
		e.FindScan.Cancel()
		e.FindScan = nil
	}
}

// Close cancels a still-running hex-find scan, if any.
func (e *HexEntry) Close() {
	if e.HexFindScan != nil {
		e.HexFindScan.Cancel()
		e.HexFindScan = nil
	}
}

// SyncContent copies a TierHighlighted entry's full decoded/highlighted
// content out of its background stream into Lines/Segs, once that
// pass has finished — a no-op if Lines is already populated, if the
// stream isn't done yet, or if e is TierPlainText, which never builds
// full content in the stream at all (docs/STREAMING_PREVIEW_DESIGN.md
// §2, §8). Safe to call every frame; callers (ContentReady below, and
// tests that need to observe reloaded content) call it before reading
// Lines/Segs rather than assuming Open/Reload populated them
// synchronously, since that work now happens in the background.
func (e *TextEntry) SyncContent() {
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

// ContentReady syncs e's content from its background stream if not
// already done (a cheap no-op once synced, or while the pass is still
// running) and reports whether e's tier-appropriate content is
// available to render, scroll, goto-line, or find against: for
// TierHighlighted, once Lines is populated; for TierPlainText, once the
// background pass has finished. This gates TierPlainText's windowed
// reading on that same "pass fully done" signal goto-line already
// gates on (SPEC.md §2.1, docs/STREAMING_PREVIEW_DESIGN.md §4), rather
// than the design's more ambitious progressive-availability aspiration
// ("a jump ahead of where the pass has reached can still show
// plain-text content immediately") — a deliberate simplification,
// flagged in SPEC.md.
func (e *TextEntry) ContentReady() bool {
	if e.Tier == preview.TierHighlighted {
		e.SyncContent()
		return e.Lines != nil
	}
	return e.Stream != nil && e.Stream.Done()
}

// Evict frees e's resident Lines/Segs — the memory-heavy
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
// independently bounded by windowLines, internal/ui/views/scroll.go),
// or for an entry whose content isn't resident yet (e.g. its
// background pass hasn't finished, or it was already evicted) —
// nothing to evict, and restarting an already-running or
// not-yet-started pass would just be wasted work.
func (e *TextEntry) Evict() {
	if e.Tier != preview.TierHighlighted || e.Lines == nil {
		return
	}
	e.Lines = nil
	e.Segs = nil
	e.Stream = preview.StartStream(e.Path(), e.Tier)
}

// Evict is a no-op for the hex tier: a hex view never holds a file's
// full byte content resident regardless of size (SPEC.md §2.1a), so
// there is never anything to evict.
func (e *HexEntry) Evict() {}

// Open reads path (capBytes bounds the check, SPEC.md §2.1), decides
// tier from content/size, and returns the right concrete entry — a
// *TextEntry or *HexEntry, boxed as any since this package declares no
// shared interface for a caller to name; callers assert the returned
// value down to whatever minimal interface (or concrete type) they
// need. Callers (openfiles.List.Open) are responsible for dedup-by-path
// — this always reads and constructs fresh.
func Open(path string, capBytes int64) (any, error) {
	_, binary, err := preview.ReadCapped(path, capBytes)
	if err != nil {
		return nil, err
	}

	info := EntryInfo{Path: path}
	var size int64
	if fi, err := os.Stat(path); err == nil {
		info.ModTime = fi.ModTime()
		size = fi.Size()
	}
	info.Size = size

	if binary {
		info.Tier = preview.TierBinary
		return &HexEntry{EntryInfo: info, HexFindCurrent: -1}, nil
	}
	info.Tier = preview.TierFor(size)
	return &TextEntry{EntryInfo: info, FindCurrent: -1, Stream: preview.StartStream(path, info.Tier)}, nil
}

// Reload re-stats e's own path and, if its mtime has changed since it
// was last loaded/reloaded, re-reads and re-decides tier the same way
// Open does — not treated as sticky from whenever the entry was first
// opened. This falls out of the Stat call already made to compare
// mtimes: re-checking size against the ceiling at that same moment
// costs nothing extra, and reload is the only mechanism by which
// dirtree ever learns a file changed at all, so it's the only point
// where a stale tier decision could otherwise ever be corrected.
// Without this, a file that grows well past the ceiling after being
// opened would keep being fully read and highlighted in the background
// on every reload for the rest of the session, exactly the unbounded
// cost the ceiling exists to prevent; a file that shrinks back under
// the ceiling is symmetrically promoted back to full highlighting.
//
// changed reports whether anything happened at all. mtime unchanged
// reports changed=false, err=nil (nothing to do). A file that can no
// longer be stat'd or read (deleted or permission lost) also reports
// changed=false, err=nil — it simply goes stale, its last-known
// content left untouched, per SPEC.md §6.1's existing handling for a
// currently-open file whose underlying path disappears; this is not
// treated as an error condition by the caller. A file that has become
// binary is not treated this way: like Open, it re-tiers into
// preview.TierBinary (a hex view) instead of going stale, the same as
// any other tier flip below.
//
// If the tier actually flips (including a TierHighlighted<->
// TierPlainText flip, both still "text"), updated is a brand-new
// concrete object — the old TextEntry/HexEntry's state is discarded
// entirely rather than carrying any of its fields forward, so
// Scroll/HexOffset reset to the top falls out naturally (a display-row
// index means something different under each tier's model: an index
// into a whole-file wrap cache for TierHighlighted vs. into a small
// on-screen window for TierPlainText, §8, vs. a byte offset for
// TierBinary, so there's no meaningful way to carry the old value
// across the change), and CopyMode resets to false for the same reason
// (it's a fresh TextEntry like any other flip, not a carried-forward
// one). If the tier does NOT flip, updated is the SAME object e,
// mutated in place: the wrap cache and any in-file find state are
// invalidated (both are derived from content that just changed), but
// Scroll/HexOffset and (for a TextEntry) CopyMode are left exactly as
// they were, self-clamping the next time the entry is scrolled or
// displayed.
//
// A tier flip is the one case where the caller (openfiles.List.Reload)
// gets back a different object than it passed in — it must replace its
// own reference (e.g. a list slot) with updated rather than assuming e
// itself was mutated. Any other code holding an older reference across
// such a reload (internal/ui/views/search.go's LastOpenedEntry, for
// instance) goes stale until it independently re-fetches from the
// list — a narrow, accepted consequence of an entry being two
// genuinely separate concrete types now rather than one mutable
// struct.
//
// e is any (see the package doc comment) — the caller passes back
// whatever Open (or a previous Reload) gave it; this function is the
// one place that actually knows how to inspect it, via the same
// type-switch-to-concrete-type idiom used throughout this package,
// never via a shared interface.
func Reload(e any, capBytes int64) (updated any, changed bool, err error) {
	var path string
	var curModTime time.Time
	var curTier preview.Tier
	switch ent := e.(type) {
	case *TextEntry:
		path, curModTime, curTier = ent.Path(), ent.ModTime, ent.Tier
	case *HexEntry:
		path, curModTime, curTier = ent.Path(), ent.ModTime, ent.Tier
	}

	info, statErr := os.Stat(path)
	if statErr != nil {
		return e, false, nil
	}
	if info.ModTime().Equal(curModTime) {
		return e, false, nil
	}

	_, binary, readErr := preview.ReadCapped(path, capBytes)
	if readErr != nil {
		return e, false, nil
	}

	newTier := preview.TierBinary
	if !binary {
		newTier = preview.TierFor(info.Size())
	}
	flipped := newTier != curTier
	newInfo := EntryInfo{Path: path, Tier: newTier, ModTime: info.ModTime(), Size: info.Size()}

	if binary {
		he, ok := e.(*HexEntry)
		if flipped || !ok {
			he = &HexEntry{HexFindCurrent: -1}
		} else {
			if he.HexFindScan != nil {
				he.HexFindScan.Cancel()
			}
			he.HexFindQuery = ""
			he.HexFindMatches = nil
			he.HexFindCurrent = -1
			he.HexFindWrapNote = ""
			he.HexFindScan = nil
		}
		he.EntryInfo = newInfo
		return he, true, nil
	}

	stream := preview.StartStream(path, newTier)
	te, ok := e.(*TextEntry)
	if flipped || !ok {
		te = &TextEntry{FindCurrent: -1}
	} else {
		if te.FindScan != nil {
			te.FindScan.Cancel()
		}
		te.Lines = nil
		te.Segs = nil
		te.WindowStartLine = 0
		te.Rows = nil
		te.FirstRow = nil
		te.RowsWidth = 0
		te.FindQuery = ""
		te.FindMatches = nil
		te.FindCurrent = -1
		te.FindWrapNote = ""
		te.FindScan = nil
	}
	te.Stream = stream
	te.EntryInfo = newInfo
	return te, true, nil
}
