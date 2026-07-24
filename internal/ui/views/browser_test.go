package views

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/tree"
	"github.com/nitti/dirtree/internal/ui/canvas"
)

// TestBrowserLegendsTier1FitMinTerminalWidth guards SPEC.md §6.4's
// minimum terminal size against §5.2's legend tiering: the browser's
// own legend and jump-to-file mode's legend must each have priority-1
// (never-dropped) text that fits within canvas.MinTerminalWidth on its
// own, with no left-hand label — otherwise a terminal at exactly the
// enforced minimum would still see a clipped legend.
func TestBrowserLegendsTier1FitMinTerminalWidth(t *testing.T) {
	named := map[string][]canvas.LegendEntry{
		"browserLegend": browserLegend,
		"jumpLegend":    jumpLegend,
	}
	for name, entries := range named {
		tier1 := canvas.LegendString(canvas.KeepUpToPriority(entries, 1))
		if n := len([]rune(tier1)); n > canvas.MinTerminalWidth {
			t.Errorf("%s's priority-1 text is %d runes, exceeding MinTerminalWidth (%d): %q", name, n, canvas.MinTerminalWidth, tier1)
		}
	}
}

// TestBrowserJumpDrawShowsGhostTextForSoleMatch guards SPEC.md §4.3's
// ghost-text autocomplete: jump to file's matching rule (a
// case-insensitive prefix match on each row's leaf name) means a sole
// match's query is always a literal prefix of its Name, so ghost text
// should always render for it (unlike quick open, §4.2, where it's
// conditional) — and that row gets the secondary StyleFindCurrent
// highlight, distinct from plain cursor-position StyleSelected.
func TestBrowserJumpDrawShowsGhostTextForSoleMatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := tree.NewRoot(dir, noopIgnorer{})
	root.LoadChildren(dir, noopIgnorer{})
	if len(root.Children) != 1 {
		t.Fatalf("test setup: got %d children, want 1", len(root.Children))
	}
	match := root.Children[0]

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	w, h := 60, 10
	sim.SetSize(w, h)

	v := &Browser{
		Shared:      &Shared{Root: root, RootPath: dir, Files: openfiles.New(), Canvas: canvas.New(sim)},
		Selected:    match,
		JumpActive:  true,
		JumpScope:   root,
		JumpQuery:   "one",
		JumpMatches: []*tree.Node{match},
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

	const browserTop = 2
	if style := cellStyle(sim, 0, browserTop); style != canvas.StyleFindCurrent {
		t.Errorf("sole match row style = %v, want StyleFindCurrent", style)
	}
}
