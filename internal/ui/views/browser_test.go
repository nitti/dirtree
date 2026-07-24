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

// TestBrowserCurrentLegendReflectsJumpMode guards CurrentLegend's
// switch (SPEC.md §5.4), which the help overlay relies on: jumpLegend
// while jump to file is active, browserLegend otherwise.
func TestBrowserCurrentLegendReflectsJumpMode(t *testing.T) {
	v := &Browser{}
	if got := v.CurrentLegend(); &got[0] != &browserLegend[0] {
		t.Errorf("CurrentLegend() with JumpActive=false = %v, want browserLegend", got)
	}
	v.JumpActive = true
	if got := v.CurrentLegend(); &got[0] != &jumpLegend[0] {
		t.Errorf("CurrentLegend() with JumpActive=true = %v, want jumpLegend", got)
	}
}

// TestBrowserDrawSuppressesHeaderLegendWhenHelpVisible guards SPEC.md
// §5.4: while the help overlay is showing, the browser's own header
// keeps its "BROWSE" mode label but its legend collapses to the single
// canvas.HideKeysLegend entry, in both jump-active and plain-browsing
// states.
func TestBrowserDrawSuppressesHeaderLegendWhenHelpVisible(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := tree.NewRoot(dir, noopIgnorer{})
	root.LoadChildren(dir, noopIgnorer{})

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	w, h := 60, 10
	sim.SetSize(w, h)

	v := &Browser{
		Shared:   &Shared{Root: root, RootPath: dir, Files: openfiles.New(), Canvas: canvas.New(sim), HelpVisible: true},
		Selected: root,
	}

	v.Draw(w, h)
	sim.Show()
	row := rowText(sim, 0, w)
	if !strings.Contains(row, "BROWSE") || !strings.HasSuffix(strings.TrimRight(row, " "), "[?] hide keys") {
		t.Errorf("browser header = %q, want BROWSE label plus only [?] hide keys", row)
	}

	v.JumpActive = true
	v.JumpScope = root
	v.Draw(w, h)
	sim.Show()
	row = rowText(sim, 0, w)
	if !strings.Contains(row, "BROWSE") || !strings.HasSuffix(strings.TrimRight(row, " "), "[?] hide keys") {
		t.Errorf("browser header (jump active) = %q, want BROWSE label plus only [?] hide keys", row)
	}
}
