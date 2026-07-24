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

// Remainder reports the part of candidate left over after query,
// case-insensitively, when query is a literal prefix of candidate —
// the ghost-text autocomplete rule shared by jump to file (SPEC.md
// §4.3, where every match is already a leaf-name prefix match by
// construction) and quick open (SPEC.md §4.2, whose glob/substring
// rule means a unique match's query is often *not* a prefix of its
// path at all, e.g. "match" uniquely matching "internal/match/match.go"
// starts mid-path — Remainder deliberately reports ok=false rather than
// inventing a "remainder from wherever it matched" for that case, so a
// caller only ever autocompletes the literal, unambiguous continuation
// of what the user actually typed). Rune-based throughout so ghost text
// is never cut mid-rune for non-ASCII names. An empty query is
// trivially a prefix of anything, ok=true, remainder=candidate.
func Remainder(query, candidate string) (remainder string, ok bool) {
	q, c := []rune(query), []rune(candidate)
	if len(q) > len(c) {
		return "", false
	}
	if !strings.EqualFold(string(c[:len(q)]), query) {
		return "", false
	}
	return string(c[len(q):]), true
}

// CommonPrefixCompletion extends query to the longest literal,
// case-insensitive prefix shared by every one of candidates — quick
// open's Tab-to-complete rule (SPEC.md §4.2), for narrowing a still-
// ambiguous query (more than one current match) the same way shell tab
// completion fills in an unambiguous common continuation without
// requiring every character to be typed by hand. ok is false, and
// completed is meaningless, whenever there's nothing useful to do:
// candidates is empty, query isn't itself a case-insensitive prefix of
// every candidate (quick open's substring/glob matching, §4.1, can
// reach a match some other way than at its start, in which case
// there's no well-defined "common continuation" to extend the typed
// query with), or the shared prefix is no longer than query already is
// (nothing new to add). Rune-based throughout so completion is never
// cut mid-rune for non-ASCII names.
func CommonPrefixCompletion(query string, candidates []string) (completed string, ok bool) {
	if len(candidates) == 0 {
		return "", false
	}
	common := []rune(candidates[0])
	for _, c := range candidates[1:] {
		common = commonPrefixRunes(common, []rune(c))
		if len(common) == 0 {
			break
		}
	}
	q := []rune(query)
	if len(common) <= len(q) || !strings.EqualFold(string(common[:len(q)]), query) {
		return "", false
	}
	return string(common), true
}

// commonPrefixRunes returns the longest case-insensitive common prefix
// of a and b, in a's original casing.
func commonPrefixRunes(a, b []rune) []rune {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && strings.EqualFold(string(a[i]), string(b[i])) {
		i++
	}
	return a[:i]
}
