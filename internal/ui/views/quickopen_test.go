package views

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/index"
	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/ui/canvas"
)

// TestQuickOpenLegendTier1FitsMinTerminalWidth guards SPEC.md §6.4's
// minimum terminal size against §5.2's legend tiering: quick open's
// priority-1 (never-dropped) legend text must fit within
// canvas.MinTerminalWidth on its own, with no left-hand label —
// otherwise a terminal at exactly the enforced minimum would still see
// a clipped legend.
func TestQuickOpenLegendTier1FitsMinTerminalWidth(t *testing.T) {
	tier1 := canvas.LegendString(canvas.KeepUpToPriority(quickOpenLegend, 1))
	if n := len([]rune(tier1)); n > canvas.MinTerminalWidth {
		t.Errorf("quickOpenLegend's priority-1 text is %d runes, exceeding MinTerminalWidth (%d): %q", n, canvas.MinTerminalWidth, tier1)
	}
}

// noopIgnorer matches nothing, so index.Start walks a temp dir's files
// exactly as created.
type noopIgnorer struct{}

func (noopIgnorer) Match(_ string, _ bool) bool { return false }

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

// TestQuickOpenDrawListShowsOpenIndicator guards SPEC.md §2.2/§4.2's
// open-file feedback convention (already applied to the browser and
// content search) being extended to quick open's own flat match list:
// a match already present in the open-files list gets the same "●"
// marker inline before its path, and one not open gets a blank
// placeholder space instead, so the column stays aligned either way.
func TestQuickOpenDrawListShowsOpenIndicator(t *testing.T) {
	dir := t.TempDir()
	openPath := filepath.Join(dir, "one.txt")
	closedPath := filepath.Join(dir, "two.txt")
	for _, p := range []string{openPath, closedPath} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	w, h := 60, 10
	sim.SetSize(w, h)

	files := openfiles.New()
	if res := files.Open(openPath, 1<<20); res.Outcome != openfiles.Opened {
		t.Fatalf("Open failed: %s", res.Message)
	}

	idx := index.Start(dir, noopIgnorer{})
	waitIndexDone(t, idx)

	v := &QuickOpen{
		Shared:  &Shared{Files: files, Canvas: canvas.New(sim), RootPath: dir, Idx: idx},
		Matches: []index.Entry{{AbsPath: openPath}, {AbsPath: closedPath}},
	}
	v.drawList(w, h)
	sim.Show()

	const listTop = 2
	openRow := rowText(sim, listTop, w)
	if !strings.HasPrefix(openRow, "● one.txt") {
		t.Errorf("open match row = %q, want it to start with the open indicator", openRow)
	}
	closedRow := rowText(sim, listTop+1, w)
	if !strings.HasPrefix(closedRow, "  two.txt") {
		t.Errorf("closed match row = %q, want a blank placeholder in place of the open indicator", closedRow)
	}
}

// TestQuickOpenSummaryCountsFilesOnly guards quickOpenSummary's total:
// it counts the background index's non-directory entries regardless of
// the current query, so it always reads as the full file count the
// query is narrowing down from, not whatever's currently matching.
func TestQuickOpenSummaryCountsFilesOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.txt", "two.txt", filepath.Join("sub", "three.txt")} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	idx := index.Start(dir, noopIgnorer{})
	waitIndexDone(t, idx)

	if got := quickOpenSummary(idx, 1); got != "1 of 3 files" {
		t.Errorf("quickOpenSummary(idx, 1) = %q, want %q", got, "1 of 3 files")
	}
}

// TestQuickOpenDrawShowsFileSummary guards the query row's rendered
// "N of N files" summary (SPEC.md §4.2): once the background index has
// finished (Matches is non-nil, even if empty), the row's right side
// shows how many of the index's files currently match the query out of
// its total file count.
func TestQuickOpenDrawShowsFileSummary(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"one.txt", "two.txt", "three.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	w, h := 60, 10
	sim.SetSize(w, h)

	idx := index.Start(dir, noopIgnorer{})
	waitIndexDone(t, idx)

	v := &QuickOpen{
		Shared:  &Shared{Files: openfiles.New(), Canvas: canvas.New(sim), RootPath: dir, Idx: idx},
		Query:   "one",
		Matches: []index.Entry{{AbsPath: filepath.Join(dir, "one.txt")}},
	}
	v.Draw(w, h)
	sim.Show()

	row := rowText(sim, 1, w)
	if !strings.HasPrefix(row, "> one") {
		t.Fatalf("query row = %q, want it to start with the prompt and query", row)
	}
	if !strings.HasSuffix(strings.TrimRight(row, " "), "1 of 3 files") {
		t.Fatalf("query row = %q, want it to end with the file summary", row)
	}
}
