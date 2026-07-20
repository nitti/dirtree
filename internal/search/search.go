// Package search implements the background content-search feature
// (SPEC.md §9): given a query string, stream every indexed file's
// content for a case-insensitive match — either a plain substring or,
// in regex mode, a regular expression — and return, per matching file,
// every line that matched. Like internal/index, this operates on raw
// paths only and shares no mutable state with the interactive tree's
// node objects, so it can run in a background goroutine concurrently
// with the UI.
package search

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nitti/dirtree/internal/preview"
)

// perFileTimeout bounds how long a single candidate's scan is allowed to
// run before it's cut short and reported as an Issue (SPEC.md §9.1): once
// there's no byte cap, scan time scales with file size, so this is the
// safety valve that keeps one huge/slow file from stalling a whole scan.
// A var, not a const, so tests can shrink it.
var perFileTimeout = 5 * time.Second

// maxConcurrentScans bounds how many candidates are scanned at once
// (SPEC.md §9.1). The candidate set is fully known up front (a snapshot
// of the background index), so this is a simple semaphore-gated fan-out
// rather than a persistent worker pool pulling from a queue. A var, not a
// const, so tests can shrink or widen it.
var maxConcurrentScans = 16

// binarySniffLen is how many leading bytes of a candidate are checked for
// a NUL byte before scanning proceeds (SPEC.md §2.2's binary check),
// decoupled from any content byte cap now that scanning itself is
// unbounded.
const binarySniffLen = 8192

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

// FileResult is one file whose content contains the query (or that
// couldn't be fully scanned), plus every matching line found before any
// problem, in source order (SPEC.md §9.2).
type FileResult struct {
	AbsPath string
	RelPath string
	Hits    []Hit

	// Issue is non-empty when the file couldn't be (fully) scanned — a
	// read error partway through (including on open, e.g. permission
	// denied) or the per-file scan timeout — so the caller can surface it
	// inline on this file's row (SPEC.md §9.2) rather than silently
	// dropping or truncating the result. Any Hits collected before the
	// problem are still included. A binary file is not an Issue: like
	// preview's own binary handling, it's treated as an intentional
	// non-match, not a failure, so it never produces a FileResult at all.
	Issue string
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

// Run scans candidates for query under mode, with up to maxConcurrentScans
// running at once, stopping early if ctx is canceled (e.g. a newer query
// or mode superseded this one), and returns per-file results sorted by
// RelPath, each with every matching line in that file found before any
// problem. An empty query matches nothing (SPEC.md §9.1) — unlike jump
// mode's path matcher, scanning every file's content for an empty query
// would be pure wasted work with no useful result. The returned slice is
// never nil on success, so callers can distinguish "searched, zero
// matches" from "not yet searched." In ModeRegex, an invalid query
// returns a non-nil error and no results, without scanning any candidate.
func Run(ctx context.Context, query string, mode Mode, candidates []Candidate) ([]FileResult, error) {
	if query == "" {
		return make([]FileResult, 0), nil
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

	// Dispatch runs in its own goroutine, concurrently with the collector
	// loop below: sem bounds how many scanFile calls run at once, and a
	// full sem blocks dispatch until a slot frees, which only happens
	// once that candidate's result (if any) has been sent on resultCh —
	// so dispatch and collection must run concurrently, not dispatch
	// fully then collect, or a full semaphore and an unread resultCh
	// deadlock each other once candidates outnumber maxConcurrentScans.
	resultCh := make(chan FileResult)
	sem := make(chan struct{}, maxConcurrentScans)
	go func() {
		var wg sync.WaitGroup
		for _, c := range candidates {
			select {
			case <-ctx.Done():
				wg.Wait()
				close(resultCh)
				return
			default:
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(c Candidate) {
				defer wg.Done()
				defer func() { <-sem }()
				if r, ok := scanFile(ctx, c, needle, re); ok {
					resultCh <- r
				}
			}(c)
		}
		wg.Wait()
		close(resultCh)
	}()

	results := make([]FileResult, 0, 8)
	for r := range resultCh {
		results = append(results, r)
	}

	sort.SliceStable(results, func(i, j int) bool {
		return strings.ToLower(results[i].RelPath) < strings.ToLower(results[j].RelPath)
	})
	return results, nil
}

// scanFile streams c's file line by line and reports every matching
// line, if any: a substring match against needle (case folded via
// bytes.ToLower) when re is nil, otherwise a regex match against re. The
// leading binarySniffLen bytes are checked for a NUL byte the same way
// §2.2 detects a binary open — a binary file is skipped silently (ok is
// false, no Issue), the same as before streaming. A read failure after
// open (permission denied, deleted mid-scan) or the per-file timeout
// elapsing produces a FileResult with Issue set and whatever Hits were
// found first, rather than being silently dropped — this is best-effort
// scanning over many files, but the caller still deserves to know a
// result may be incomplete (SPEC.md §9.1, §9.2).
func scanFile(ctx context.Context, c Candidate, needle []byte, re *regexp.Regexp) (FileResult, bool) {
	fctx, cancel := context.WithTimeout(ctx, perFileTimeout)
	defer cancel()

	f, err := os.Open(c.AbsPath)
	if err != nil {
		if ctx.Err() != nil {
			return FileResult{}, false
		}
		return FileResult{AbsPath: c.AbsPath, RelPath: c.RelPath, Issue: preview.ErrText(err)}, true
	}
	defer func() { _ = f.Close() }()

	reader := bufio.NewReaderSize(f, binarySniffLen)
	sniff, err := reader.Peek(binarySniffLen)
	if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
		if ctx.Err() != nil {
			return FileResult{}, false
		}
		return FileResult{AbsPath: c.AbsPath, RelPath: c.RelPath, Issue: preview.ErrText(err)}, true
	}
	if bytes.IndexByte(sniff, 0) != -1 {
		return FileResult{}, false
	}

	var hits []Hit
	lineNum := 0
	for {
		select {
		case <-fctx.Done():
			if ctx.Err() != nil {
				return FileResult{}, false
			}
			return FileResult{AbsPath: c.AbsPath, RelPath: c.RelPath, Hits: hits, Issue: fmt.Sprintf("scan timed out after %s, results may be incomplete", perFileTimeout)}, true
		default:
		}

		line, rerr := reader.ReadString('\n')
		if line != "" || rerr == nil {
			lineNum++
			text := strings.TrimSuffix(line, "\n")
			matched := false
			if re != nil {
				matched = re.MatchString(text)
			} else {
				matched = bytes.Contains(bytes.ToLower([]byte(text)), needle)
			}
			if matched {
				hits = append(hits, Hit{LineNum: lineNum, LineText: text})
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			if ctx.Err() != nil {
				return FileResult{}, false
			}
			return FileResult{AbsPath: c.AbsPath, RelPath: c.RelPath, Hits: hits, Issue: preview.ErrText(rerr)}, true
		}
	}

	if len(hits) == 0 {
		return FileResult{}, false
	}
	return FileResult{AbsPath: c.AbsPath, RelPath: c.RelPath, Hits: hits}, true
}
