// Package search implements the background content-search feature
// (SPEC.md §9): given a plain string query, scan every indexed file's
// content (up to a byte cap) for a case-insensitive substring match and
// return the files that contain it. Like internal/index, this operates
// on raw paths only and shares no mutable state with the interactive
// tree's node objects, so it can run in a background goroutine
// concurrently with the UI.
package search

import (
	"bytes"
	"context"
	"sort"
	"strings"

	"github.com/nitti/dirtree/internal/preview"
)

// Candidate is one file to scan, provided by the caller (typically a
// snapshot of the background index's non-directory entries).
type Candidate struct {
	AbsPath string
	RelPath string
}

// Match is one file whose content contains the query, plus its first
// matching line for display context.
type Match struct {
	AbsPath  string
	RelPath  string
	LineNum  int // 1-based
	LineText string
}

// Run scans candidates for query, stopping early if ctx is canceled
// (e.g. a newer query superseded this one), and returns matches sorted
// by RelPath. An empty query matches nothing (SPEC.md §9.1) — unlike
// jump mode's path matcher, scanning every file's content for an empty
// query would be pure wasted work with no useful result. The returned
// slice is never nil, so callers can distinguish "searched, zero
// matches" from "not yet searched."
func Run(ctx context.Context, query string, candidates []Candidate, byteCap int64) []Match {
	matches := make([]Match, 0, 8)
	if query == "" {
		return matches
	}
	q := []byte(strings.ToLower(query))

	for _, c := range candidates {
		select {
		case <-ctx.Done():
			return matches
		default:
		}
		if m, ok := scanFile(c, q, byteCap); ok {
			matches = append(matches, m)
		}
	}

	sort.SliceStable(matches, func(i, j int) bool {
		return strings.ToLower(matches[i].RelPath) < strings.ToLower(matches[j].RelPath)
	})
	return matches
}

// scanFile reads up to byteCap bytes of c's file and reports its first
// line containing q (case-insensitive substring), if any. A file whose
// capped read contains a NUL byte is treated as binary and never
// matched, the same check §2.2 uses to detect a binary open — content
// search never reports a match inside a file the preview couldn't show
// anyway. A read failure (permission denied, deleted mid-scan, etc.) is
// silently skipped rather than surfaced, since this is a best-effort
// scan over many files, not a single user-directed open.
func scanFile(c Candidate, q []byte, byteCap int64) (Match, bool) {
	data, binary, err := preview.ReadCapped(c.AbsPath, byteCap)
	if err != nil || binary {
		return Match{}, false
	}
	for i, line := range bytes.Split(data, []byte("\n")) {
		if bytes.Contains(bytes.ToLower(line), q) {
			return Match{AbsPath: c.AbsPath, RelPath: c.RelPath, LineNum: i + 1, LineText: string(line)}, true
		}
	}
	return Match{}, false
}
