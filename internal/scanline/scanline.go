// Package scanline factors out the line-by-line file scanning loop
// duplicated across internal/search, internal/preview, and internal/find:
// each independently wrapped a bufio.Reader in a
// `for { line, err := r.ReadString('\n'); ... }` loop, tracking the byte
// offset each line started at and trimming its trailing newline (a CRLF
// file's "\r" included) the same way, just to feed the result into very
// different per-line logic (a content-search match, a highlighted-preview
// line, an in-file find match).
package scanline

import (
	"bufio"
	"context"
	"io"
)

// Line is one line read by Scan: its text with the trailing newline (and
// a preceding "\r", for a CRLF-terminated file) already trimmed, and the
// byte offset within the stream it started at.
type Line struct {
	Text   string
	Offset int64
}

// Scan reads r line by line, calling yield for every line read —
// including a final line with no terminating newline, but never an empty
// read past a clean EOF. It stops at EOF and returns nil, or returns the
// first non-EOF read error encountered. If yield returns false, Scan
// stops early and returns nil, letting a caller cut a scan short (a
// satisfied match, a fixed line budget) without that being reported as
// an error.
func Scan(r *bufio.Reader, yield func(Line) bool) error {
	return ScanWhile(nil, r, yield)
}

// ScanWhile is Scan with an additional ctx.Done() check made before each
// read attempt, including the first — unlike a check inside yield, which
// only ever runs after a line has already been read off r. Callers cutting
// a scan short based on cancellation that can fire between reads, not
// just between lines, need this so an already-canceled ctx reliably stops
// the scan before it reads one more line, rather than racing whatever's
// left in the buffer. A nil ctx behaves exactly like Scan (always
// continue).
func ScanWhile(ctx context.Context, r *bufio.Reader, yield func(Line) bool) error {
	var pos int64
	for {
		if ctx != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
		}
		offset := pos
		raw, rerr := r.ReadString('\n')
		pos += int64(len(raw))
		if len(raw) > 0 {
			if !yield(Line{Text: trimTrailingNewline(raw), Offset: offset}) {
				return nil
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				return nil
			}
			return rerr
		}
	}
}

// trimTrailingNewline strips a line's trailing "\n" (and a preceding
// "\r", for a CRLF-terminated file), the same way ReadLines/Load's
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
