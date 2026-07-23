package find

import (
	"bufio"
	"context"
	"os"
	"strings"
	"time"

	"github.com/nitti/dirtree/internal/asyncjob"
)

// Scan is a background, cancelable line-by-line search of a single
// file's content (docs/STREAMING_PREVIEW_DESIGN.md §9), for in-file find
// over a preview.TierPlainText entry whose full content isn't resident
// (InLines needs the whole file in memory, which that tier deliberately
// never holds). Mirrors the same Start/Snapshot/Elapsed shape
// internal/preview's StreamIndex and internal/index's Index already
// use, for the same reason: no shared mutable state between the UI
// goroutine and the background one, so no locking is needed beyond the
// snapshot's own accessor methods.
type Scan struct {
	asyncjob.State
	matches []Match
	cancel  context.CancelFunc
}

// StartScan kicks off a background scan of path for query (the same
// case-insensitive substring semantics InLines uses) and returns
// immediately. Cancel stops it early — used when a newer query, or
// clearing the find, supersedes it before it finishes.
func StartScan(path, query string) *Scan {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Scan{State: asyncjob.New(), cancel: cancel}
	go func() {
		matches := scanFileForMatches(ctx, path, query)
		s.Lock()
		s.matches = matches
		s.MarkDone()
		s.Unlock()
	}()
	return s
}

// Cancel stops the scan early; a canceled scan still eventually marks
// itself done (with whatever partial matches it had collected), it just
// stops making further progress — callers that no longer care simply
// drop their *Scan reference rather than needing Cancel to guarantee
// immediate cleanup.
func (s *Scan) Cancel() {
	s.cancel()
}

// Snapshot returns the matches found so far (nil until done) and
// whether the scan has finished, whether it ran to completion or was
// canceled partway through.
func (s *Scan) Snapshot() (matches []Match, done bool) {
	s.RLock()
	defer s.RUnlock()
	return s.matches, s.IsDone()
}

// Elapsed returns how much wall-clock time has passed since the scan
// started, for the find status area's own perceptibility-threshold
// spinner (SPEC.md §5.2, §5.3, docs/STREAMING_PREVIEW_DESIGN.md §9).
func (s *Scan) Elapsed() time.Duration {
	return s.State.Elapsed()
}

// scanFileForMatches streams path line by line (the same
// bufio.Reader.ReadString('\n') shape internal/search's and
// internal/preview's own streaming scans already use), returning every
// case-insensitive substring match of query in source order — or
// whatever was found so far if ctx is canceled partway through. An empty
// query matches nothing, the same convention InLines uses.
func scanFileForMatches(ctx context.Context, path, query string) []Match {
	if query == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	q := []rune(strings.ToLower(query))
	var matches []Match
	r := bufio.NewReader(f)
	lineNum := 0
	for {
		select {
		case <-ctx.Done():
			return matches
		default:
		}
		line, rerr := r.ReadString('\n')
		if len(line) > 0 {
			runes := []rune(strings.ToLower(trimTrailingNewline(line)))
			for i := 0; i+len(q) <= len(runes); i++ {
				if runeSliceEqual(runes[i:i+len(q)], q) {
					matches = append(matches, Match{Line: lineNum, Col: i, Len: len(q)})
				}
			}
			lineNum++
		}
		if rerr != nil {
			break
		}
	}
	return matches
}

func trimTrailingNewline(line string) string {
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	return line
}
