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

// ReadLines reads up to cap bytes from the start of path and splits the
// result into lines per SPEC.md §8. It never returns an error: read
// failures, binary content, and truncation are all represented as
// content within the returned lines.
func ReadLines(path string, cap int64) []string {
	f, err := os.Open(path)
	if err != nil {
		return []string{fmt.Sprintf("(could not read file: %v)", err)}
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return []string{fmt.Sprintf("(could not read file: %v)", err)}
	}

	buf := make([]byte, cap)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF && n == 0 {
		return []string{fmt.Sprintf("(could not read file: %v)", err)}
	}
	data := buf[:n]

	if bytes.IndexByte(data, 0) != -1 {
		return []string{"binary file, preview not available"}
	}

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

	if info.Size() > cap {
		lines = append(lines, fmt.Sprintf("(truncated at %d bytes)", cap))
	}

	return lines
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
