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

// TestQuickOpenDrawSuppressesHeaderLegendWhenHelpVisible guards
// SPEC.md §5.4: while the help overlay is showing, the header keeps
// its "QUICK OPEN" mode label but its legend collapses to the single
// canvas.HideKeysLegend entry.
func TestQuickOpenDrawSuppressesHeaderLegendWhenHelpVisible(t *testing.T) {
	dir := t.TempDir()

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	w, h := 60, 10
	sim.SetSize(w, h)

	idx := index.Start(dir, noopIgnorer{})
	waitIndexDone(t, idx)

	v := &QuickOpen{Shared: &Shared{Files: openfiles.New(), Canvas: canvas.New(sim), Idx: idx, HelpVisible: true}}
	v.Draw(w, h)
	sim.Show()

	row := rowText(sim, 0, w)
	if !strings.Contains(row, "QUICK OPEN") || !strings.HasSuffix(strings.TrimRight(row, " "), "[?] hide keys") {
		t.Errorf("header = %q, want QUICK OPEN label plus only [?] hide keys", row)
	}
	if got := v.CurrentLegend(); &got[0] != &quickOpenLegend[0] {
		t.Errorf("CurrentLegend() = %v, want quickOpenLegend", got)
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
	if !strings.HasPrefix(row, "> txt") || strings.Contains(row, "txt.") {
		t.Errorf("query row = %q, want no ghost text appended for a non-prefix match", row)
	}
	if !strings.HasSuffix(strings.TrimRight(row, " "), "1 of 1 files") {
		t.Errorf("query row = %q, want the file summary still shown alongside the absent ghost text", row)
	}

	const listTop = 2
	if style := cellStyle(sim, 0, listTop); style != canvas.StyleFindCurrent {
		t.Errorf("sole match row style = %v, want StyleFindCurrent even without ghost text", style)
	}
}

// TestQuickOpenTabCompletesToCommonPrefix guards SPEC.md §4.2's
// shell-tab-completion Tab action: with more than one match sharing a
// literal prefix beyond what's been typed, Tab narrows the query to
// that longest common prefix and recomputes matches.
func TestQuickOpenTabCompletesToCommonPrefix(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sub/one.txt", "sub/two.txt", "other.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	idx := index.Start(dir, noopIgnorer{})
	waitIndexDone(t, idx)

	v := &QuickOpen{
		// "s" matches sub/one.txt and sub/two.txt (via "sub") but not
		// other.txt; their shared prefix is "sub/".
		Shared: &Shared{Files: openfiles.New(), RootPath: dir, Idx: idx},
		Query:  "s",
		Matches: []index.Entry{
			{AbsPath: filepath.Join(dir, "sub", "one.txt")},
			{AbsPath: filepath.Join(dir, "sub", "two.txt")},
		},
	}
	v.HandleKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))

	want := "sub/"
	if v.Query != want {
		t.Errorf("Query = %q, want %q", v.Query, want)
	}
	if len(v.Matches) != 2 || v.Selected != 0 {
		t.Errorf("Matches/Selected = %v/%d, want both matches still present and selection reset", v.Matches, v.Selected)
	}
}

// TestQuickOpenTabNoOpWhenMatchesDivergeImmediately guards Tab's no-op
// case when the current matches share no more of a prefix than the
// query itself already is — nothing left to complete to.
func TestQuickOpenTabNoOpWhenMatchesDivergeImmediately(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"one.txt", "onetwo.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	idx := index.Start(dir, noopIgnorer{})
	waitIndexDone(t, idx)

	v := &QuickOpen{
		// "one.txt" and "onetwo.txt" share only "one" — no longer
		// than the query already typed.
		Shared: &Shared{Files: openfiles.New(), RootPath: dir, Idx: idx},
		Query:  "one",
		Matches: []index.Entry{
			{AbsPath: filepath.Join(dir, "one.txt")},
			{AbsPath: filepath.Join(dir, "onetwo.txt")},
		},
	}
	v.HandleKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))

	if v.Query != "one" {
		t.Errorf("Query = %q, want unchanged %q", v.Query, "one")
	}
}

// TestQuickOpenTabNoOpWhenQueryIsNotACommonPrefix guards Tab's no-op
// case when the query wasn't itself a literal prefix of every current
// match — quick open's substring/glob matching (§4.1) can reach a
// match some other way, in which case there's no well-defined "common
// continuation" of the typed text to extend.
func TestQuickOpenTabNoOpWhenQueryIsNotACommonPrefix(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"one.txt", "two.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	idx := index.Start(dir, noopIgnorer{})
	waitIndexDone(t, idx)

	v := &QuickOpen{
		// "t" is a substring match on both (".txt") but not a prefix
		// of either.
		Shared: &Shared{Files: openfiles.New(), RootPath: dir, Idx: idx},
		Query:  "t",
		Matches: []index.Entry{
			{AbsPath: filepath.Join(dir, "one.txt")},
			{AbsPath: filepath.Join(dir, "two.txt")},
		},
	}
	v.HandleKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))

	if v.Query != "t" {
		t.Errorf("Query = %q, want unchanged %q", v.Query, "t")
	}
}

// TestQuickOpenTabNoOpWithSoleMatch guards Tab's no-op case when the
// query already narrows to exactly one match — Return already opens it
// directly, so completing its name first would only add a keystroke.
func TestQuickOpenTabNoOpWithSoleMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "one.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx := index.Start(dir, noopIgnorer{})
	waitIndexDone(t, idx)

	v := &QuickOpen{
		Shared:  &Shared{Files: openfiles.New(), RootPath: dir, Idx: idx},
		Query:   "one",
		Matches: []index.Entry{{AbsPath: path}},
	}
	v.HandleKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))

	if v.Query != "one" {
		t.Errorf("Query = %q, want unchanged %q", v.Query, "one")
	}
}

// TestQuickOpenTabNoOpWithNoMatches guards Tab's no-op case when the
// query matches nothing at all — an empty match set has nothing to
// complete to.
func TestQuickOpenTabNoOpWithNoMatches(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx := index.Start(dir, noopIgnorer{})
	waitIndexDone(t, idx)

	v := &QuickOpen{
		Shared:  &Shared{Files: openfiles.New(), RootPath: dir, Idx: idx},
		Query:   "nomatch",
		Matches: nil,
	}
	v.HandleKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))

	if v.Query != "nomatch" {
		t.Errorf("Query = %q, want unchanged %q", v.Query, "nomatch")
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
