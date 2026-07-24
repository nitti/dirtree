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

func TestRemainderReturnsLiteralContinuation(t *testing.T) {
	rem, ok := Remainder("Read", "README.md")
	if !ok {
		t.Fatal("expected query to be recognized as a case-insensitive prefix")
	}
	if rem != "ME.md" {
		t.Fatalf("remainder = %q, want %q", rem, "ME.md")
	}
}

func TestRemainderCaseInsensitive(t *testing.T) {
	rem, ok := Remainder("readme", "README.MD")
	if !ok || rem != ".MD" {
		t.Fatalf("Remainder(%q, %q) = (%q, %v), want (%q, true)", "readme", "README.MD", rem, ok, ".MD")
	}
}

func TestRemainderRejectsMidStringMatch(t *testing.T) {
	// SPEC.md §4.2: quick open's substring rule can uniquely match a
	// query that isn't a prefix of the matched path at all (e.g. "match"
	// against "internal/match/match.go") — Remainder must not invent a
	// remainder from wherever the substring happened to occur.
	if _, ok := Remainder("match", "internal/match/match.go"); ok {
		t.Fatal("expected no remainder for a non-prefix (mid-string) match")
	}
}

func TestRemainderRejectsQueryLongerThanCandidate(t *testing.T) {
	if _, ok := Remainder("READMEE", "README"); ok {
		t.Fatal("expected no remainder when query is longer than candidate")
	}
}

func TestRemainderEmptyQueryReturnsWholeCandidate(t *testing.T) {
	rem, ok := Remainder("", "README.md")
	if !ok || rem != "README.md" {
		t.Fatalf("Remainder(\"\", %q) = (%q, %v), want (%q, true)", "README.md", rem, ok, "README.md")
	}
}

func TestCommonPrefixCompletionExtendsToSharedPrefix(t *testing.T) {
	completed, ok := CommonPrefixCompletion("s", []string{"sub/one.txt", "sub/two.txt"})
	if !ok || completed != "sub/" {
		t.Fatalf("CommonPrefixCompletion(%q, ...) = (%q, %v), want (%q, true)", "s", completed, ok, "sub/")
	}
}

func TestCommonPrefixCompletionCaseInsensitive(t *testing.T) {
	completed, ok := CommonPrefixCompletion("SU", []string{"sub/one.txt", "SUB/two.txt"})
	if !ok || completed != "sub/" {
		t.Fatalf("CommonPrefixCompletion(%q, ...) = (%q, %v), want (%q, true) using the first candidate's casing", "SU", completed, ok, "sub/")
	}
}

func TestCommonPrefixCompletionRejectsNonPrefixQuery(t *testing.T) {
	// "t" is a substring match on both candidates (".txt") but not a
	// prefix of either — SPEC.md §4.2's substring/glob matching means
	// there's no well-defined "common continuation" of the query here.
	if _, ok := CommonPrefixCompletion("t", []string{"one.txt", "two.txt"}); ok {
		t.Fatal("expected no completion when query isn't a prefix of every candidate")
	}
}

func TestCommonPrefixCompletionRejectsNoFurtherExtension(t *testing.T) {
	// Candidates diverge immediately after the query itself — nothing
	// left to complete to.
	if _, ok := CommonPrefixCompletion("one", []string{"one.txt", "onetwo.txt"}); ok {
		t.Fatal("expected no completion when the shared prefix is no longer than the query")
	}
}

func TestCommonPrefixCompletionRejectsEmptyCandidates(t *testing.T) {
	if _, ok := CommonPrefixCompletion("anything", nil); ok {
		t.Fatal("expected no completion with an empty candidate set")
	}
}
