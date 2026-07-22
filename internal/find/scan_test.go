package find

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTemp(t *testing.T, content []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitScanDone(t *testing.T, s *Scan) []Match {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if matches, done := s.Snapshot(); done {
			return matches
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("scan did not finish in time")
	return nil
}

func TestStartScanFindsMatchesAcrossLines(t *testing.T) {
	path := writeTemp(t, []byte("apple\nbanana\napple pie\n"))
	matches := waitScanDone(t, StartScan(path, "apple"))
	want := []Match{{Line: 0, Col: 0, Len: 5}, {Line: 2, Col: 0, Len: 5}}
	if len(matches) != len(want) {
		t.Fatalf("got %v, want %v", matches, want)
	}
	for i := range want {
		if matches[i] != want[i] {
			t.Fatalf("got %v, want %v", matches, want)
		}
	}
}

func TestStartScanCaseInsensitive(t *testing.T) {
	path := writeTemp(t, []byte("Hello HELLO hello\n"))
	matches := waitScanDone(t, StartScan(path, "hello"))
	if len(matches) != 3 {
		t.Fatalf("expected 3 case-insensitive matches, got %d: %v", len(matches), matches)
	}
}

func TestStartScanEmptyQueryMatchesNothing(t *testing.T) {
	path := writeTemp(t, []byte("hello world\n"))
	matches := waitScanDone(t, StartScan(path, ""))
	if len(matches) != 0 {
		t.Fatalf("expected no matches for an empty query, got %v", matches)
	}
}

func TestStartScanNoMatch(t *testing.T) {
	path := writeTemp(t, []byte("hello world\n"))
	matches := waitScanDone(t, StartScan(path, "xyz"))
	if len(matches) != 0 {
		t.Fatalf("expected no matches, got %v", matches)
	}
}

func TestStartScanNonexistentFileFinishesDoneWithNoMatches(t *testing.T) {
	dir := t.TempDir()
	matches := waitScanDone(t, StartScan(filepath.Join(dir, "nope.txt"), "anything"))
	if len(matches) != 0 {
		t.Fatalf("expected no matches for a file that can't be opened, got %v", matches)
	}
}

func TestScanCancelStopsFurtherProgress(t *testing.T) {
	// A canceled scan still reports done, just with only whatever
	// partial progress (if any) it made before cancellation — callers
	// rely on Cancel to stop wasted background work, not to guarantee a
	// particular partial result.
	path := writeTemp(t, []byte("needle\n"))
	s := StartScan(path, "needle")
	s.Cancel()
	// Whether or not the goroutine had already finished by the time
	// Cancel ran, Snapshot must still eventually report done rather than
	// hanging forever.
	waitScanDone(t, s)
}

func TestScanFileForMatchesRespectsCancellation(t *testing.T) {
	// Directly exercises the streaming loop's cancellation check with an
	// already-canceled context, independent of any timing race with the
	// background goroutine StartScan itself spawns.
	path := writeTemp(t, []byte("needle needle needle\n"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	matches := scanFileForMatches(ctx, path, "needle")
	if len(matches) != 0 {
		t.Fatalf("expected an already-canceled scan to make no progress, got %v", matches)
	}
}

func TestScanSinceStartElapsedIsNonNegative(t *testing.T) {
	path := writeTemp(t, []byte("a\n"))
	s := StartScan(path, "a")
	waitScanDone(t, s)
	if s.Elapsed() < 0 {
		t.Fatalf("expected a non-negative elapsed duration, got %v", s.Elapsed())
	}
}
