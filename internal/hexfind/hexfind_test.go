package hexfind

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInBytesEmptyQueryMatchesNothing(t *testing.T) {
	if got := InBytes([]byte("abcabc"), ""); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestInBytesFindsEveryLiteralMatch(t *testing.T) {
	data := []byte("abXYabXYab")
	got := InBytes(data, "XY")
	want := []Match{{Offset: 2, Len: 2}, {Offset: 6, Len: 2}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestInBytesIsCaseSensitive(t *testing.T) {
	got := InBytes([]byte("abXYab"), "xy")
	if got != nil {
		t.Fatalf("expected no match for differing case, got %v", got)
	}
}

func writeTemp(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// waitScanDone polls s until it reports done or the deadline elapses,
// mirroring internal/openfiles's own waitSynced pattern for a background
// job's test helper.
func waitScanDone(t *testing.T, s *Scan) ([]Match, bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if matches, done := s.Snapshot(); done {
			return matches, true
		}
		time.Sleep(time.Millisecond)
	}
	return nil, false
}

func TestStartScanFindsMatchesInSmallFile(t *testing.T) {
	path := writeTemp(t, []byte("abXYabXYab"))
	s := StartScan(path, "XY")
	matches, done := waitScanDone(t, s)
	if !done {
		t.Fatal("scan did not finish in time")
	}
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %v", matches)
	}
}

func TestStartScanFindsMatchSpanningChunkBoundary(t *testing.T) {
	// Build content larger than scanChunkSize with the query straddling
	// the boundary, so the sliding-window overlap is actually exercised.
	query := "BOUNDARYMARK"
	pad := make([]byte, scanChunkSize-len(query)/2)
	for i := range pad {
		pad[i] = 'a'
	}
	content := append(append([]byte{}, pad...), query...)
	path := writeTemp(t, content)

	s := StartScan(path, query)
	matches, done := waitScanDone(t, s)
	if !done {
		t.Fatal("scan did not finish in time")
	}
	if len(matches) != 1 || matches[0].Offset != int64(len(pad)) {
		t.Fatalf("expected one match at offset %d, got %v", len(pad), matches)
	}
}

func TestScanCancelStillMarksDone(t *testing.T) {
	path := writeTemp(t, []byte("abcabc"))
	s := StartScan(path, "abc")
	s.Cancel()
	if _, done := waitScanDone(t, s); !done {
		t.Fatal("canceled scan never marked done")
	}
}
