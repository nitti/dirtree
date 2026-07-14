package ui

import (
	"testing"
	"time"

	"github.com/nitti/dirtree/internal/index"
)

func waitIndexDone(t *testing.T, idx *index.Index) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, done := idx.Snapshot(); done {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("index did not finish building in time")
}

func TestBadgeTextShowsCompletionMessageJustAfterIndexingFinishes(t *testing.T) {
	a := testApp(t)
	a.idx = index.Start(a.rootPath, nil)
	waitIndexDone(t, a.idx)

	text, hiddenPrefix, ok := a.badgeText()
	if !ok || text != completionMessage || hiddenPrefix != 0 {
		t.Fatalf("expected full completion message right after finishing, got text=%q hiddenPrefix=%d ok=%v", text, hiddenPrefix, ok)
	}
}

func TestBadgeTextHidesCompletionMessageAfterFadeOut(t *testing.T) {
	a := testApp(t)
	a.idx = index.Start(a.rootPath, nil)
	waitIndexDone(t, a.idx)

	time.Sleep(completionDisplayDuration + completionFadeDuration + 50*time.Millisecond)

	_, _, ok := a.badgeText()
	if ok {
		t.Fatal("expected completion message to be gone well after its display+fade window")
	}
}
