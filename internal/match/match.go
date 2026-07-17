// Package match implements the query-matching rule shared by quick open
// and jump to file (SPEC.md §4.1), and any other filtering use: glob
// matching when the query contains a shell-wildcard character,
// case-insensitive substring matching otherwise.
package match

import (
	"path/filepath"
	"strings"
)

// Matches reports whether candidate matches query under SPEC.md §7's
// rules. An empty query matches everything.
func Matches(query, candidate string) bool {
	if query == "" {
		return true
	}
	if strings.ContainsAny(query, "*?[") {
		ok, err := filepath.Match(strings.ToLower(query), strings.ToLower(candidate))
		return err == nil && ok
	}
	return strings.Contains(strings.ToLower(candidate), strings.ToLower(query))
}
