package hexfind

import (
	"bytes"
	"context"
	"io"
	"os"
	"time"

	"github.com/nitti/dirtree/internal/asyncjob"
)

// scanChunkSize is how much of the file is read into memory at once by
// a background scan (below) — a hex view never holds a binary file's
// full content resident regardless of size (SPEC.md §2.1a), so a query
// against a large file must be streamed in bounded chunks rather than
// read whole, the same "bounded, not proportional to file size" property
// StartStream's line-offset pass already has for text tiers.
const scanChunkSize = 1 << 20 // 1MB

// Scan is a background, cancelable byte-pattern search of a single
// file's raw content (SPEC.md §2.1a), mirroring the same Start/Snapshot/
// Elapsed shape internal/find.Scan, internal/preview.StreamIndex, and
// internal/index.Index already use, for the same reason: no shared
// mutable state between the UI goroutine and the background one.
// Unlike internal/find.Scan, which only runs for a TierPlainText entry
// (TierHighlighted's find searches its already-resident Lines directly),
// hex-view find always scans in the background: a TierBinary entry never
// holds its file's full content resident, regardless of size.
type Scan struct {
	asyncjob.State
	matches []Match
	cancel  context.CancelFunc
}

// StartScan kicks off a background scan of path for query's literal
// bytes and returns immediately. Cancel stops it early — used when a
// newer query, or clearing the find, supersedes it before it finishes.
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
// itself done (with whatever partial matches it had collected so far).
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
// started, for the hex view's own perceptibility-threshold spinner
// (SPEC.md §5.2, §5.3).
func (s *Scan) Elapsed() time.Duration {
	return s.State.Elapsed()
}

// scanFileForMatches streams path in bounded, overlapping chunks
// (scanChunkSize, overlapping by len(query)-1 bytes so a match spanning
// a chunk boundary is never missed), returning every literal match of
// query's bytes in ascending offset order — or whatever was found so far
// if ctx is canceled partway through. An empty query matches nothing,
// the same convention internal/find.Scan uses.
func scanFileForMatches(ctx context.Context, path, query string) []Match {
	q := []byte(query)
	if len(q) == 0 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	overlap := len(q) - 1
	buf := make([]byte, scanChunkSize+overlap)
	var matches []Match
	var base int64
	carry := 0
	for {
		select {
		case <-ctx.Done():
			return matches
		default:
		}
		n, rerr := io.ReadFull(f, buf[carry:])
		total := carry + n
		for i := 0; i+len(q) <= total; i++ {
			if bytes.Equal(buf[i:i+len(q)], q) {
				matches = append(matches, Match{Offset: base + int64(i), Len: int64(len(q))})
			}
		}
		if rerr != nil {
			return matches
		}
		if overlap > 0 {
			copy(buf[:overlap], buf[total-overlap:total])
		}
		base += int64(total - overlap)
		carry = overlap
	}
}
