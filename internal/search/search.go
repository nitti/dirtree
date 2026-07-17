// Package search implements the background content-search feature
// (SPEC.md §9): given a query string, scan every indexed file's
// content (up to a byte cap) for a case-insensitive match — either a
// plain substring or, in regex mode, a regular expression — and
// return, per matching file, every line that matched. Like
// internal/index, this operates on raw paths only and shares no mutable
// state with the interactive tree's node objects, so it can run in a
// background goroutine concurrently with the UI.
package search

import (
	"bytes"
	"context"
	"regexp"
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

// Hit is one matching line within a file.
type Hit struct {
	LineNum  int // 1-based
	LineText string
}

// FileResult is one file whose content contains the query, plus every
// matching line within it (SPEC.md §9.2), in source order.
type FileResult struct {
	AbsPath string
	RelPath string
	Hits    []Hit
}

// Mode selects how the query string is interpreted (SPEC.md §9.1).
type Mode int

const (
	// ModeSubstring is a plain case-insensitive substring match.
	ModeSubstring Mode = iota
	// ModeRegex compiles the query as a case-insensitive regular
	// expression (Go's RE2 syntax) and matches it against each line.
	ModeRegex
)

// CompileRegex compiles query the same way Run does in ModeRegex — case
// insensitive, RE2 syntax — so callers can validate a query and surface
// a compile error (e.g. inline in the UI) without running a scan.
func CompileRegex(query string) (*regexp.Regexp, error) {
	return regexp.Compile("(?i)" + query)
}

// Run scans candidates for query under mode, stopping early if ctx is
// canceled (e.g. a newer query or mode superseded this one), and
// returns per-file results sorted by RelPath, each with every matching
// line in that file. An empty query matches nothing (SPEC.md §9.1) —
// unlike jump mode's path matcher, scanning every file's content for an
// empty query would be pure wasted work with no useful result. The
// returned slice is never nil on success, so callers can distinguish
// "searched, zero matches" from "not yet searched." In ModeRegex, an
// invalid query returns a non-nil error and no results, without
// scanning any candidate.
func Run(ctx context.Context, query string, mode Mode, candidates []Candidate, byteCap int64) ([]FileResult, error) {
	results := make([]FileResult, 0, 8)
	if query == "" {
		return results, nil
	}

	var needle []byte
	var re *regexp.Regexp
	if mode == ModeRegex {
		compiled, err := CompileRegex(query)
		if err != nil {
			return nil, err
		}
		re = compiled
	} else {
		needle = []byte(strings.ToLower(query))
	}

	for _, c := range candidates {
		select {
		case <-ctx.Done():
			return results, nil
		default:
		}
		if r, ok := scanFile(c, needle, re, byteCap); ok {
			results = append(results, r)
		}
	}

	sort.SliceStable(results, func(i, j int) bool {
		return strings.ToLower(results[i].RelPath) < strings.ToLower(results[j].RelPath)
	})
	return results, nil
}

// scanFile reads up to byteCap bytes of c's file and reports every
// matching line, if any: a substring match against needle (case folded
// via bytes.ToLower) when re is nil, otherwise a regex match against
// re. A file whose capped read contains a NUL byte is treated as
// binary and never matched, the same check §2.2 uses to detect a
// binary open — content search never reports a match inside a file the
// preview couldn't show anyway. A read failure (permission denied,
// deleted mid-scan, etc.) is silently skipped rather than surfaced,
// since this is a best-effort scan over many files, not a single
// user-directed open.
func scanFile(c Candidate, needle []byte, re *regexp.Regexp, byteCap int64) (FileResult, bool) {
	data, binary, err := preview.ReadCapped(c.AbsPath, byteCap)
	if err != nil || binary {
		return FileResult{}, false
	}
	var hits []Hit
	for i, line := range bytes.Split(data, []byte("\n")) {
		matched := false
		if re != nil {
			matched = re.Match(line)
		} else {
			matched = bytes.Contains(bytes.ToLower(line), needle)
		}
		if matched {
			hits = append(hits, Hit{LineNum: i + 1, LineText: string(line)})
		}
	}
	if len(hits) == 0 {
		return FileResult{}, false
	}
	return FileResult{AbsPath: c.AbsPath, RelPath: c.RelPath, Hits: hits}, true
}
