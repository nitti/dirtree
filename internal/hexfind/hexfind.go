// Package hexfind implements the hex view's own find (SPEC.md §2.1a): a
// byte-sequence/ASCII-substring search against a file's raw bytes,
// distinct from internal/find's line/rune-oriented, case-insensitive
// in-file find — case-insensitivity and line/column addressing are both
// meaningless for arbitrary binary content, so this operates on byte
// offsets directly rather than sharing internal/find's Match shape.
package hexfind

import "bytes"

// Match is one location where a query's literal bytes were found, in
// byte-offset coordinates (SPEC.md §2.1a) — the hex view's own analog of
// internal/find.Match, using a flat file offset instead of a line/column
// pair, since a hex view addresses content by byte position, not source
// line.
type Match struct {
	Offset int64
	Len    int64
}

// InBytes returns every literal match of query's bytes within data, in
// ascending offset order. An empty query matches nothing, the same
// convention internal/find.InLines uses.
func InBytes(data []byte, query string) []Match {
	if query == "" {
		return nil
	}
	q := []byte(query)
	var matches []Match
	for i := 0; i+len(q) <= len(data); i++ {
		if bytes.Equal(data[i:i+len(q)], q) {
			matches = append(matches, Match{Offset: int64(i), Len: int64(len(q))})
		}
	}
	return matches
}
