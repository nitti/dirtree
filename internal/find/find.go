// Package find implements in-file search over an already-open file's
// lines (SPEC.md §2.4): a `less`-style "/query" that locates every
// case-insensitive substring match so the UI can highlight them and
// step between them. Unlike internal/search (which scans many files'
// content in the background for a single first-match-per-file
// result), this operates on one file already held in memory and
// returns every match, so it needs no cancellation/concurrency of its
// own.
package find

import "strings"

// Match is one location where a query was found, in rune coordinates
// so it composes correctly with the wrapped display rows built from
// the same lines (internal/preview's per-line segments/wrapping use
// rune-indexed columns throughout).
type Match struct {
	Line int // 0-based source line index
	Col  int // 0-based rune column within the source line
	Len  int // match length, in runes
}

// InLines returns every case-insensitive substring match of query
// across lines, in source order (line, then column). An empty query
// matches nothing, the same convention internal/search uses — scanning
// for an empty string would be pure wasted work with no useful result.
func InLines(lines []string, query string) []Match {
	var matches []Match
	if query == "" {
		return matches
	}
	q := []rune(strings.ToLower(query))

	for lineIdx, line := range lines {
		runes := []rune(strings.ToLower(line))
		for i := 0; i+len(q) <= len(runes); i++ {
			if runeSliceEqual(runes[i:i+len(q)], q) {
				matches = append(matches, Match{Line: lineIdx, Col: i, Len: len(q)})
			}
		}
	}
	return matches
}

func runeSliceEqual(a, b []rune) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
