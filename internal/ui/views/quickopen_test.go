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

// cellStyle returns the style of the cell at (x, y) after Show.
func cellStyle(sim tcell.SimulationScreen, x, y int) tcell.Style {
	cells, width, _ := sim.GetContents()
	return cells[y*width+x].Style
}

// TestQuickOpenDrawShowsGhostTextForPrefixMatch guards SPEC.md §4.2's
// ghost-text autocomplete: once the query unambiguously matches a
// single file *and* is a literal prefix of that file's path, the
// remainder renders dimmed immediately after the typed text, and the
// sole match's row gets the secondary StyleFindCurrent highlight
// (distinct from plain cursor-position StyleSelected).
func TestQuickOpenDrawShowsGhostTextForPrefixMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "one.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
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
		Matches: []index.Entry{{AbsPath: path}},
	}
	v.Draw(w, h)
	sim.Show()

	row := rowText(sim, 1, w)
	if !strings.HasPrefix(row, "> one.txt") {
		t.Fatalf("query row = %q, want the ghost text \".txt\" appended after the typed query", row)
	}
	promptLen := len([]rune("> one"))
	if style := cellStyle(sim, promptLen, 1); style != canvas.StyleQueryGhost {
		t.Errorf("ghost text style = %v, want StyleQueryGhost", style)
	}
	if style := cellStyle(sim, 0, 1); style != canvas.StyleSearchInput {
		t.Errorf("typed-query style = %v, want StyleSearchInput", style)
	}

	const listTop = 2
	if style := cellStyle(sim, 0, listTop); style != canvas.StyleFindCurrent {
		t.Errorf("sole match row style = %v, want StyleFindCurrent", style)
	}
}

// TestQuickOpenDrawSuppressesGhostTextForMidPathMatch guards the
// resolved ambiguity in SPEC.md §4.2: quick open's substring rule can
// uniquely match a query that isn't a prefix of the matched path at
// all — ghost text must not appear in that case (nothing correct to
// autocomplete to), but the secondary highlight still applies, since
// "unambiguously matches one file" only depends on match count, not on
// whether ghost text happens to be renderable for it.
func TestQuickOpenDrawSuppressesGhostTextForMidPathMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "one.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
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
		// "txt" is a substring match on one.txt but not a prefix of it.
		Shared:  &Shared{Files: openfiles.New(), Canvas: canvas.New(sim), RootPath: dir, Idx: idx},
		Query:   "txt",
		Matches: []index.Entry{{AbsPath: path}},
	}
	v.Draw(w, h)
	sim.Show()

	row := rowText(sim, 1, w)
	if strings.TrimRight(row, " ") != "> txt" {
		t.Errorf("query row = %q, want no ghost text appended for a non-prefix match", row)
	}

	const listTop = 2
	if style := cellStyle(sim, 0, listTop); style != canvas.StyleFindCurrent {
		t.Errorf("sole match row style = %v, want StyleFindCurrent even without ghost text", style)
	}
}
