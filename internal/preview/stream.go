package preview

import (
	"bufio"
	"os"
	"sync"
	"time"
)

// StreamIndex is a per-open-file background pass that records the byte
// offset of every line's start (docs/STREAMING_PREVIEW_DESIGN.md §3, §4).
// Stage 1 only builds this index — it is not yet consulted by reading,
// highlighting, or wrapping, which stay exactly as they are today; its
// purpose for now is goto-line's block/allow gating (SPEC.md §2.1) and
// the file-legend "building…" spinner (SPEC.md §5.2), ahead of the
// windowed-read work that will actually depend on it. Deliberately the
// same Start/Snapshot/Elapsed shape as internal/index.Index, for the
// same reason that package uses it: no shared mutable state between the
// UI goroutine and the background one, so no locking is needed beyond
// the snapshot's own accessor methods.
type StreamIndex struct {
	mu        sync.RWMutex
	offsets   []int64
	done      bool
	startTime time.Time
	doneTime  time.Time
}

// StartStream kicks off building path's line-offset index in a
// background goroutine and returns immediately; the interactive UI must
// not block on it. A file that can't be opened or read partway through
// simply stops with whatever offsets it already collected and marks
// itself done — the entry it belongs to already had a successful read
// via Load by the time this is started (SPEC.md §2.2), so this is a
// best-effort re-read of the same path, not a second correctness gate.
func StartStream(path string) *StreamIndex {
	s := &StreamIndex{startTime: time.Now()}
	go func() {
		offsets := scanLineOffsets(path)
		s.mu.Lock()
		s.offsets = offsets
		s.done = true
		s.doneTime = time.Now()
		s.mu.Unlock()
	}()
	return s
}

// scanLineOffsets streams path line by line, recording the byte offset
// each line starts at, the same bufio.Reader.ReadString('\n') shape
// internal/search's scanFile already uses for streamed content search
// (docs/STREAMING_PREVIEW_DESIGN.md §4) rather than a new pattern.
func scanLineOffsets(path string) []int64 {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	var offsets []int64
	r := bufio.NewReader(f)
	var pos int64
	for {
		offset := pos
		line, err := r.ReadString('\n')
		pos += int64(len(line))
		switch {
		case len(line) > 0:
			offsets = append(offsets, offset)
		case offset == 0:
			// Empty file: still one (empty) line, matching
			// linesFromBytes' "empty result set becomes a single
			// empty line" rule (SPEC.md §2.1).
			offsets = append(offsets, offset)
		}
		if err != nil {
			break
		}
	}
	return offsets
}

// Snapshot returns the line-start byte offsets collected so far (nil if
// not yet done), the number of lines that implies, and whether the
// background pass has completed.
func (s *StreamIndex) Snapshot() (offsets []int64, lineCount int, done bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.offsets, len(s.offsets), s.done
}

// Done reports whether the background pass has completed, without the
// caller needing the rest of Snapshot's return values — used by
// goto-line's block/allow decision (SPEC.md §2.1) and the file-legend
// spinner (SPEC.md §5.2).
func (s *StreamIndex) Done() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.done
}

// Elapsed returns how much wall-clock time has passed since the
// background pass started, for the file-legend spinner's perceptibility
// threshold (SPEC.md §5.2, §5.3).
func (s *StreamIndex) Elapsed() time.Duration {
	return time.Since(s.startTime)
}

// SinceDone returns how much wall-clock time has passed since the
// background pass finished, meaningless (and reported as 0) while it's
// still running — used by the file-legend spinner's minimum-display-
// duration floor (SPEC.md §5.3), the same shape internal/index.Index's
// own SinceDone gives the corner badge.
func (s *StreamIndex) SinceDone() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.done {
		return 0
	}
	return time.Since(s.doneTime)
}
