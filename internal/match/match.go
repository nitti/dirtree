// Package match implements the query-matching rules used across the
// app: quick open's glob/substring rule (SPEC.md §4.1) and jump to
// file's simpler prefix rule (SPEC.md §4.3).
package match

import (
	"path/filepath"
	"strings"
)

// Matches reports whether candidate matches query under quick open's
// rule (SPEC.md §4.1): glob matching when the query contains a
// shell-wildcard character, case-insensitive substring matching
// otherwise. An empty query matches everything.
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

// PrefixMatches reports whether candidate starts with query,
// case-insensitively — jump to file's matching rule (SPEC.md §4.3),
// used against each visible row's own leaf name rather than a full
// path. Unlike Matches, an empty query matches nothing: jump to file
// treats an empty query as "haven't looked yet," not "matches
// everything" (SPEC.md §4.3).
func PrefixMatches(query, candidate string) bool {
	if query == "" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(candidate), strings.ToLower(query))
}
