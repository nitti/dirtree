// Package preview implements file reading, best-effort syntax
// highlighting, and line wrapping for the preview pane (SPEC.md §8).
package preview

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"unicode/utf8"
)

// DefaultByteCap is the read cap in bytes, in the neighborhood of the
// prototype's 1,000,000-byte cap (SPEC.md §8).
const DefaultByteCap = 1_000_000

// ErrText extracts the underlying message from a failed-open error,
// dropping the "open <path>: " prefix Go's os package adds to a
// *fs.PathError — the path is already visible via whichever row this
// message ends up displayed inline on (SPEC.md §2.2, §5.2), so repeating
// it there would be redundant. Exported so other packages with the same
// "show an OS error inline on a row" need (e.g. content search, §9.1) can
// reuse it instead of reimplementing the same prefix-stripping.
func ErrText(err error) string {
	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		return pe.Err.Error()
	}
	return err.Error()
}

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

// tabWidth is the fixed tab-stop width tabs are expanded to (SPEC.md
// §8), matching the common terminal/`less` default rather than any
// particular editor's configurable indent width.
const tabWidth = 8

// expandTabs replaces each tab in line with spaces up to the next
// tab-stop column (SPEC.md §8): raw tab runes are otherwise rendered as
// a single narrow column rather than the width a terminal would
// actually give them, visibly breaking alignment for any tab-indented
// file. Tab stops are computed in rune columns, resetting at the start
// of each line (line has no embedded newlines by construction, since
// this runs per already-split line).
func expandTabs(line string) string {
	if !strings.Contains(line, "\t") {
		return line
	}
	var b strings.Builder
	col := 0
	for _, r := range line {
		if r == '\t' {
			spaces := tabWidth - (col % tabWidth)
			b.WriteString(strings.Repeat(" ", spaces))
			col += spaces
			continue
		}
		b.WriteRune(r)
		col++
	}
	return b.String()
}

// linesFromBytes decodes data as UTF-8 (replacing invalid sequences),
// splits it into lines, and expands tabs (SPEC.md §8), appending a
// truncation marker line if the read was capped short of the file's
// actual size.
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
	for i, l := range lines {
		lines[i] = expandTabs(l)
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
		return LoadResult{Failed: true, Message: ErrText(err)}
	}
	if bytes.IndexByte(data, 0) != -1 {
		return LoadResult{Failed: true, Message: "binary file, preview not available"}
	}

	lines := linesFromBytes(data, truncated, cap)
	segs := highlightOrPlain(path, lines)
	return LoadResult{Lines: lines, Segs: segs}
}

// ReadRange reads up to n bytes starting at offset from path, returning
// fewer bytes if the file is shorter than offset+n (down to zero for an
// offset at or past EOF) — the hex view's own read primitive (SPEC.md
// §2.1a): unlike ReadCapped/ReadLines/Load, which always read from the
// start of the file, a hex view only ever needs the byte range covering
// its current on-screen viewport, regardless of the file's total size,
// so this seeks directly to offset rather than reading (and discarding)
// everything before it.
func ReadRange(path string, offset int64, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	buf := make([]byte, n)
	read, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	return buf[:read], nil
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
