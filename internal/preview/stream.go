package preview

import (
	"bufio"
	"io"
	"os"
	"time"

	"github.com/nitti/dirtree/internal/asyncjob"
)

// StreamIndex is a per-open-file background pass building the
// infrastructure the streaming-preview design needs regardless of tier
// (docs/STREAMING_PREVIEW_DESIGN.md §3, §4): the byte offset of every
// line's start, and — for a TierHighlighted file only — its fully
// decoded, tab-expanded, highlighted content, since that tier still
// keeps everything resident (§2, §6). A TierPlainText file's content is
// deliberately not built here at all: it's read on demand, windowed,
// once this pass is done (§8) — this stage gates that windowed reading
// on the same "pass fully finished" signal goto-line already gates on
// (§4), rather than the design's more ambitious progressive-availability
// aspiration, as a deliberate simplification flagged in SPEC.md.
//
// Deliberately the same Start/Snapshot/Elapsed shape as
// internal/index.Index, for the same reason that package uses it: no
// shared mutable state between the UI goroutine and the background one,
// so no locking is needed beyond the snapshot's own accessor methods.
type StreamIndex struct {
	asyncjob.State
	tier    Tier
	offsets []int64
	lines   []string    // TierHighlighted only
	segs    [][]Segment // TierHighlighted only
}

// StartStream kicks off building path's background pass for the given
// tier in a background goroutine and returns immediately; the
// interactive UI must not block on it. A file that can't be opened or
// read partway through simply stops with whatever it already collected
// and marks itself done — the entry it belongs to already had a
// successful open-time check by the time this is started (SPEC.md
// §2.2), so this is a best-effort re-read of the same path, not a
// second correctness gate.
func StartStream(path string, tier Tier) *StreamIndex {
	s := &StreamIndex{State: asyncjob.New(), tier: tier}
	go func() {
		offsets, lines, segs := scanFile(path, tier)
		s.Lock()
		s.offsets = offsets
		s.lines = lines
		s.segs = segs
		s.MarkDone()
		s.Unlock()
	}()
	return s
}

// scanFile streams path line by line, recording the byte offset each
// line starts at (the same bufio.Reader.ReadString('\n') shape
// internal/search's scanFile already uses for streamed content search,
// docs/STREAMING_PREVIEW_DESIGN.md §4), and — for a TierHighlighted
// file, whose size is bounded by HighlightCeiling — also decoding,
// tab-expanding, and highlighting the full content, the same work
// Load's synchronous path did, just performed here in the background.
func scanFile(path string, tier Tier) (offsets []int64, lines []string, segs [][]Segment) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, nil
	}
	defer func() { _ = f.Close() }()

	var rawLines []string
	r := bufio.NewReader(f)
	var pos int64
	for {
		offset := pos
		line, rerr := r.ReadString('\n')
		pos += int64(len(line))
		switch {
		case len(line) > 0:
			offsets = append(offsets, offset)
			if tier == TierHighlighted {
				rawLines = append(rawLines, trimTrailingNewline(line))
			}
		case offset == 0:
			// Empty file: still one (empty) line, matching
			// linesFromBytes' "empty result set becomes a single
			// empty line" rule (SPEC.md §2.1).
			offsets = append(offsets, offset)
			if tier == TierHighlighted {
				rawLines = append(rawLines, "")
			}
		}
		if rerr != nil {
			break
		}
	}

	if tier != TierHighlighted {
		return offsets, nil, nil
	}
	if len(rawLines) == 0 {
		rawLines = []string{""}
	}
	for i, l := range rawLines {
		rawLines[i] = expandTabs(l)
	}
	segs = Highlight(path, rawLines)
	if segs == nil {
		segs = make([][]Segment, len(rawLines))
		for i, l := range rawLines {
			segs[i] = []Segment{{Text: l, Category: CategoryText}}
		}
	} else {
		segs = AlignSegmentsToLines(segs, len(rawLines))
	}
	return offsets, rawLines, segs
}

// trimTrailingNewline strips a line's trailing "\n" (and a preceding
// "\r", for a CRLF-terminated file) the same way ReadLines/Load's
// strings.Split-based decoding drops the newline delimiter itself.
func trimTrailingNewline(line string) string {
	line = trimSuffixByte(line, '\n')
	line = trimSuffixByte(line, '\r')
	return line
}

func trimSuffixByte(s string, b byte) string {
	if len(s) > 0 && s[len(s)-1] == b {
		return s[:len(s)-1]
	}
	return s
}

// Snapshot returns the line-start byte offsets collected so far (nil if
// not yet done), the number of lines that implies, and whether the
// background pass has completed.
func (s *StreamIndex) Snapshot() (offsets []int64, lineCount int, done bool) {
	s.RLock()
	defer s.RUnlock()
	return s.offsets, len(s.offsets), s.IsDone()
}

// Content returns the full decoded/highlighted lines and segments for a
// TierHighlighted stream (nil, nil if not yet done, or if this stream is
// TierPlainText — that tier never builds full content here at all).
func (s *StreamIndex) Content() (lines []string, segs [][]Segment) {
	s.RLock()
	defer s.RUnlock()
	return s.lines, s.segs
}

// Tier returns the tier this stream was started with.
func (s *StreamIndex) Tier() Tier {
	return s.tier
}

// Done reports whether the background pass has completed, without the
// caller needing the rest of Snapshot's return values — used by
// goto-line's block/allow decision (SPEC.md §2.1) and the file-legend
// spinner (SPEC.md §5.2).
func (s *StreamIndex) Done() bool {
	s.RLock()
	defer s.RUnlock()
	return s.IsDone()
}

// Elapsed returns how much wall-clock time has passed since the
// background pass started, for the file-legend spinner's perceptibility
// threshold (SPEC.md §5.2, §5.3).
func (s *StreamIndex) Elapsed() time.Duration {
	return s.State.Elapsed()
}

// SinceDone returns how much wall-clock time has passed since the
// background pass finished, meaningless (and reported as 0) while it's
// still running — used by the file-legend spinner's minimum-display-
// duration floor (SPEC.md §5.3), the same shape internal/index.Index's
// own SinceDone gives the corner badge.
func (s *StreamIndex) SinceDone() time.Duration {
	s.RLock()
	defer s.RUnlock()
	d, _ := s.State.SinceDone()
	return d
}

// ReadWindow reads count source lines starting at the 0-based startLine,
// seeking directly to its byte offset (offsets, from a finished
// TierPlainText stream's Snapshot) rather than reading from the start of
// the file — the payoff the line-offset index exists for (§4, §8).
// Lines are decoded and tab-expanded the same way as the rest of the
// preview pipeline, but never highlighted (TierPlainText never
// highlights, §2). Returns fewer than count lines, possibly zero, if
// startLine is at or past the end of the file.
func ReadWindow(path string, offsets []int64, startLine, count int) ([]string, error) {
	if startLine < 0 || startLine >= len(offsets) || count <= 0 {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Seek(offsets[startLine], io.SeekStart); err != nil {
		return nil, err
	}
	r := bufio.NewReader(f)
	lines := make([]string, 0, count)
	for i := 0; i < count; i++ {
		line, rerr := r.ReadString('\n')
		if len(line) > 0 {
			lines = append(lines, expandTabs(trimTrailingNewline(line)))
		}
		if rerr != nil {
			break
		}
	}
	return lines, nil
}
