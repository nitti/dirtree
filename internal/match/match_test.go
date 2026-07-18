package match

import "testing"

func TestEmptyQueryMatchesEverything(t *testing.T) {
	if !Matches("", "anything") {
		t.Fatal("expected empty query to match")
	}
}

func TestPlainQueryIsSubstringMatch(t *testing.T) {
	if !Matches("foo", "some/FOObar.txt") {
		t.Fatal("expected case-insensitive substring match")
	}
	if Matches("zzz", "some/foobar.txt") {
		t.Fatal("expected no match")
	}
}

func TestWildcardQueryIsGlobMatch(t *testing.T) {
	if !Matches("*.txt", "notes.txt") {
		t.Fatal("expected glob match on entire candidate")
	}
	if !Matches("*/*.txt", "sub/notes.txt") {
		t.Fatal("expected a glob explicitly covering the path segments to match")
	}
	if Matches("*.md", "notes.txt") {
		t.Fatal("expected non-matching glob to fail")
	}
}

func TestPrefixMatchesEmptyQueryMatchesNothing(t *testing.T) {
	if PrefixMatches("", "anything") {
		t.Fatal("expected empty query to match nothing")
	}
}

func TestPrefixMatchesCaseInsensitivePrefix(t *testing.T) {
	if !PrefixMatches("Read", "README.md") {
		t.Fatal("expected case-insensitive prefix match")
	}
	if !PrefixMatches("read", "README.md") {
		t.Fatal("expected case-insensitive prefix match")
	}
}

func TestPrefixMatchesRejectsNonPrefixSubstring(t *testing.T) {
	if PrefixMatches("me", "README.md") {
		t.Fatal("expected a mid-string substring to not match under prefix rules")
	}
}
