package find

import (
	"reflect"
	"testing"
)

func TestInLinesEmptyQueryMatchesNothing(t *testing.T) {
	got := InLines([]string{"hello world"}, "")
	if len(got) != 0 {
		t.Fatalf("expected no matches for empty query, got %v", got)
	}
}

func TestInLinesSingleMatch(t *testing.T) {
	got := InLines([]string{"hello world"}, "world")
	want := []Match{{Line: 0, Col: 6, Len: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestInLinesMultipleMatchesSameLine(t *testing.T) {
	got := InLines([]string{"foo foo foo"}, "foo")
	want := []Match{
		{Line: 0, Col: 0, Len: 3},
		{Line: 0, Col: 4, Len: 3},
		{Line: 0, Col: 8, Len: 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestInLinesMatchesAcrossLines(t *testing.T) {
	got := InLines([]string{"apple", "banana", "apple pie"}, "apple")
	want := []Match{
		{Line: 0, Col: 0, Len: 5},
		{Line: 2, Col: 0, Len: 5},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestInLinesCaseInsensitive(t *testing.T) {
	got := InLines([]string{"Hello HELLO hello"}, "hello")
	if len(got) != 3 {
		t.Fatalf("expected 3 case-insensitive matches, got %d: %v", len(got), got)
	}
}

func TestInLinesOverlappingMatchesAllReported(t *testing.T) {
	// "aaa" contains overlapping occurrences of "aa" at columns 0 and 1.
	got := InLines([]string{"aaa"}, "aa")
	want := []Match{
		{Line: 0, Col: 0, Len: 2},
		{Line: 0, Col: 1, Len: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestInLinesNoMatch(t *testing.T) {
	got := InLines([]string{"hello world"}, "xyz")
	if len(got) != 0 {
		t.Fatalf("expected no matches, got %v", got)
	}
}

func TestInLinesMultibyteRunes(t *testing.T) {
	// Query column offsets must be in runes, not bytes, so a match after
	// a multi-byte rune still lands on the right column.
	got := InLines([]string{"héllo world"}, "world")
	want := []Match{{Line: 0, Col: 6, Len: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
