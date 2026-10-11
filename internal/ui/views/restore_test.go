package views

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nitti/dirtree/internal/openfiles"
)

// --- Session persistence (TESTING.md "Session persistence (§6.5)") ---

func writeLines(t *testing.T, n int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "f.txt")
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRestoreLineAppliedOnceContentIsReady(t *testing.T) {
	files := openfiles.New()
	res := files.Restore(writeLines(t, 50), 1<<20)
	e := res.Entry
	e.RestoreLine = 20
	files.Display(0)
	waitEntryReady(t, e)

	v := newTestPreview(files, 60, 10)
	v.drawContent(0, 0, 60, 10)
	if got := e.TopLine(); got != 20 {
		t.Fatalf("expected the restored line 20 at the top, got %d", got)
	}
	if e.RestoreLine != 0 {
		t.Fatalf("expected RestoreLine cleared once applied, got %d", e.RestoreLine)
	}
}

func TestRestoreLineAppliedForTierPlainText(t *testing.T) {
	files, e := openTierPlainText(t, 50)
	e.RestoreLine = 30
	v := newTestPreview(files, 60, 10)
	v.drawContent(0, 0, 60, 10)
	if got := e.TopLine(); got != 30 {
		t.Fatalf("expected the restored line 30 at the top, got %d", got)
	}
}

func TestRestoreLineClampedToShorterFile(t *testing.T) {
	files := openfiles.New()
	e := files.Restore(writeLines(t, 5), 1<<20).Entry
	e.RestoreLine = 500
	files.Display(0)
	waitEntryReady(t, e)

	v := newTestPreview(files, 60, 10)
	v.drawContent(0, 0, 60, 10)
	if e.RestoreLine != 0 || e.Scroll != 0 {
		t.Fatalf("expected a past-the-end restore to clamp into the file, got RestoreLine=%d Scroll=%d", e.RestoreLine, e.Scroll)
	}
}

// TestScrollBeforeFirstDrawMovesFromRestoredLine covers content
// becoming ready between frames: a scroll key handled before the next
// draw must move relative to the restored position, not be overridden
// by it.
func TestScrollBeforeFirstDrawMovesFromRestoredLine(t *testing.T) {
	files := openfiles.New()
	e := files.Restore(writeLines(t, 50), 1<<20).Entry
	e.RestoreLine = 20
	files.Display(0)
	waitEntryReady(t, e)

	v := newTestPreview(files, 60, 10)
	v.scroll(2)
	v.drawContent(0, 0, 60, 10)
	if got := e.TopLine(); got != 22 {
		t.Fatalf("expected top line 22 after scrolling 2 from restored line 20, got %d", got)
	}
}
