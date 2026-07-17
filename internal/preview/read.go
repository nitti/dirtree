// Package preview implements file reading, best-effort syntax
// highlighting, and line wrapping for the preview pane (SPEC.md §8).
package preview

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// DefaultByteCap is the read cap in bytes, in the neighborhood of the
// prototype's 1,000,000-byte cap (SPEC.md §8).
const DefaultByteCap = 1_000_000

// readCapped reads up to cap bytes from the start of path, reporting
// whether the file's actual size exceeds cap. It is the single read
// shared by ReadLines and Load, so the binary/error checks in each are
// derived from the same bytes rather than a second read of the file.
func readCapped(path string, cap int64) (data []byte, truncated bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}

	buf := make([]byte, cap)
	n, rerr := f.Read(buf)
	if rerr != nil && rerr != io.EOF && n == 0 {
		return nil, false, rerr
	}
	return buf[:n], info.Size() > cap, nil
}

// linesFromBytes decodes data as UTF-8 (replacing invalid sequences)
// and splits it into lines per SPEC.md §8, appending a truncation
// marker line if the read was capped short of the file's actual size.
func linesFromBytes(data []byte, truncated bool, cap int64) []string {
	text := decodeUTF8Lenient(data)
	lines := strings.Split(text, "\n")
	// strings.Split on "a\nb\n" yields a trailing "" element for the
	// final newline; drop it so we don't render a phantom extra line,
	// unless the file is genuinely empty.
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	if truncated {
		lines = append(lines, fmt.Sprintf("(truncated at %d bytes)", cap))
	}
	return lines
}

// ReadLines reads up to cap bytes from the start of path and splits the
// result into lines per SPEC.md §8. It never returns an error: read
// failures, binary content, and truncation are all represented as
// content within the returned lines. Use Load instead when the caller
// needs to distinguish a failed read from a successful one (SPEC.md
// §3's open semantics).
func ReadLines(path string, cap int64) []string {
	data, truncated, err := readCapped(path, cap)
	if err != nil {
		return []string{fmt.Sprintf("(could not read file: %v)", err)}
	}
	if bytes.IndexByte(data, 0) != -1 {
		return []string{"binary file, preview not available"}
	}
	return linesFromBytes(data, truncated, cap)
}

// ReadCapped reads up to cap bytes from the start of path and reports
// whether the content contains a NUL byte, using the same capped-read
// and binary-detection rule ReadLines/Load use (SPEC.md §2.2) — for
// callers (e.g. content search, §9.1) that need that same check without
// also wanting decoded lines or highlighting.
func ReadCapped(path string, cap int64) (data []byte, binary bool, err error) {
	data, _, err = readCapped(path, cap)
	if err != nil {
		return nil, false, err
	}
	return data, bytes.IndexByte(data, 0) != -1, nil
}

// LoadResult is the outcome of Load: either a failed open (an
// explanatory message, no entry-worthy content) or a successful read
// with lines and best-effort highlighting ready for wrapping, per
// SPEC.md §3's open semantics.
type LoadResult struct {
	Failed  bool
	Message string
	Lines   []string
	Segs    [][]Segment
}

// Load reads and highlights path per the byte cap, distinguishing a
// failed open (an OS-level read error, or binary content detected via a
// NUL byte) from a successful one. Both checks are derived from the
// same capped read used for normal preview loading (SPEC.md §3) — not a
// second, separate read. On success, Segs falls back to plain-text
// segments if no highlighting rule-set matched the file, so callers
// never need to special-case a nil highlighting result.
func Load(path string, cap int64) LoadResult {
	data, truncated, err := readCapped(path, cap)
	if err != nil {
		return LoadResult{Failed: true, Message: err.Error()}
	}
	if bytes.IndexByte(data, 0) != -1 {
		return LoadResult{Failed: true, Message: "binary file, preview not available"}
	}

	lines := linesFromBytes(data, truncated, cap)
	segs := Highlight(path, lines)
	if segs == nil {
		segs = make([][]Segment, len(lines))
		for i, l := range lines {
			segs[i] = []Segment{{Text: l, Category: CategoryText}}
		}
	} else {
		segs = AlignSegmentsToLines(segs, len(lines))
	}
	return LoadResult{Lines: lines, Segs: segs}
}

// decodeUTF8Lenient decodes data as UTF-8, replacing invalid sequences
// with the Unicode replacement character rather than failing.
func decodeUTF8Lenient(data []byte) string {
	if utf8.Valid(data) {
		return string(data)
	}
	var b strings.Builder
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		b.WriteRune(r)
		data = data[size:]
	}
	return b.String()
}
