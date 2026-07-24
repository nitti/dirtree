package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/index"
	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/ui/canvas"
	"github.com/nitti/dirtree/internal/ui/views"
)

// noopIgnorer matches nothing.
type noopIgnorer struct{}

func (noopIgnorer) Match(_ string, _ bool) bool { return false }

// newTestApp builds a minimal App wired the same way App.New wires its
// views' Shared pointers, but without starting a background index or
// touching a real terminal — enough for the pure dispatch/composition
// logic this file tests (textInputActive, helpTitleRows,
// helpLegendGroups, and the `?` toggle in handleKey).
func newTestApp(t *testing.T, w, h int) *App {
	t.Helper()
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(w, h)

	idx := index.Start(t.TempDir(), noopIgnorer{})
	a := &App{rootPath: "/root", overlay: views.OverlayNone, files: openfiles.New(), idx: idx}
	a.shared = &views.Shared{Files: a.files, Idx: idx, Canvas: canvas.New(sim)}
	a.QuickOpen.Shared = a.shared
	a.Search.Shared = a.shared
	a.Browser.Shared = a.shared
	a.Preview.Shared = a.shared
	a.OpenFiles.Shared = a.shared
	return a
}

// TestTextInputActive guards the exact set of contexts where every
// printable rune, including helpToggleKey ('?', also a quick open glob
// wildcard, SPEC.md §4.2), must be left for the underlying view to
// treat as literal query input rather than being intercepted as the
// help toggle (§5.4).
func TestTextInputActive(t *testing.T) {
	cases := []struct {
		name  string
		setup func(a *App)
		want  bool
	}{
		{"primary preview, idle", func(_ *App) {}, false},
		{"primary preview, find prompt open", func(a *App) { a.Preview.FindPromptOpen = true }, true},
		{"browser, not jumping", func(a *App) { a.overlay = views.OverlayBrowser }, false},
		{"browser, jump to file active", func(a *App) {
			a.overlay = views.OverlayBrowser
			a.Browser.JumpActive = true
		}, true},
		{"quick open", func(a *App) { a.overlay = views.OverlayQuickOpen }, true},
		{"content search", func(a *App) { a.overlay = views.OverlaySearch }, true},
		{"open-files overlay", func(a *App) { a.overlay = views.OverlayOpenFiles }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newTestApp(t, 60, 15)
			c.setup(a)
			if got := a.textInputActive(); got != c.want {
				t.Errorf("textInputActive() = %v, want %v", got, c.want)
			}
		})
	}
}

// TestHandleKeyTogglesHelpVisible guards the `?` interception itself
// (SPEC.md §5.4): toggles HelpVisible when the context is free (not
// text-input-active), leaves it untouched — falling through to the
// underlying view instead — when it isn't.
func TestHandleKeyTogglesHelpVisible(t *testing.T) {
	t.Run("free context toggles", func(t *testing.T) {
		a := newTestApp(t, 60, 15)
		a.handleKey(tcell.NewEventKey(tcell.KeyRune, '?', tcell.ModNone))
		if !a.shared.HelpVisible {
			t.Fatal("expected HelpVisible=true after one `?` press in a free context")
		}
		a.handleKey(tcell.NewEventKey(tcell.KeyRune, '?', tcell.ModNone))
		if a.shared.HelpVisible {
			t.Fatal("expected HelpVisible=false after a second `?` press (toggle)")
		}
	})

	t.Run("text-input context leaves it untouched and types instead", func(t *testing.T) {
		a := newTestApp(t, 60, 15)
		a.overlay = views.OverlayQuickOpen
		a.handleKey(tcell.NewEventKey(tcell.KeyRune, '?', tcell.ModNone))
		if a.shared.HelpVisible {
			t.Fatal("expected HelpVisible to stay false while quick open owns text input")
		}
		if a.QuickOpen.Query != "?" {
			t.Errorf("QuickOpen.Query = %q, want %q (the `?` should have been typed literally)", a.QuickOpen.Query, "?")
		}
	})
}

// TestHelpTitleRows guards how many top-of-screen rows the help
// overlay must stay below (SPEC.md §5.4) in each context — 1 for a
// single header row, 2 whenever a second row (a query-input row, or
// the file title bar) also occupies the top of the screen.
func TestHelpTitleRows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "one.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		setup func(a *App)
		want  int
	}{
		{"primary preview, no file displayed", func(_ *App) {}, 1},
		{"primary preview, file displayed", func(a *App) {
			if res := a.files.Open(path, 1<<20); res.Outcome != openfiles.Opened {
				t.Fatalf("Open failed: %s", res.Message)
			}
		}, 2},
		{"browser, not jumping", func(a *App) { a.overlay = views.OverlayBrowser }, 1},
		{"browser, jump active", func(a *App) {
			a.overlay = views.OverlayBrowser
			a.Browser.JumpActive = true
		}, 2},
		{"quick open", func(a *App) { a.overlay = views.OverlayQuickOpen }, 2},
		{"content search", func(a *App) { a.overlay = views.OverlaySearch }, 2},
		{"open-files overlay", func(a *App) { a.overlay = views.OverlayOpenFiles }, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newTestApp(t, 60, 15)
			c.setup(a)
			if got := a.helpTitleRows(); got != c.want {
				t.Errorf("helpTitleRows() = %d, want %d", got, c.want)
			}
		})
	}
}

// TestHelpLegendGroupsOrderAndCount guards the group structure itself
// (SPEC.md §5.4): main title bar first, then whatever secondary title
// bar(s) the current context has, in on-screen top-to-bottom order.
// Group *contents* are covered separately by each view's own
// CurrentLegend/CurrentFileLegend tests.
func TestHelpLegendGroupsOrderAndCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "one.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("primary preview, no file displayed: just the main group", func(t *testing.T) {
		a := newTestApp(t, 60, 15)
		groups := a.helpLegendGroups()
		if len(groups) != 1 {
			t.Fatalf("got %d groups, want 1", len(groups))
		}
		if &groups[0][0] != &previewLegend[0] {
			t.Error("groups[0] should be previewLegend")
		}
	})

	t.Run("primary preview, file displayed: main then file title bar", func(t *testing.T) {
		a := newTestApp(t, 60, 15)
		if res := a.files.Open(path, 1<<20); res.Outcome != openfiles.Opened {
			t.Fatalf("Open failed: %s", res.Message)
		}
		groups := a.helpLegendGroups()
		if len(groups) != 2 {
			t.Fatalf("got %d groups, want 2", len(groups))
		}
		if &groups[0][0] != &previewLegend[0] {
			t.Error("groups[0] should be previewLegend")
		}
		fileLegend, ok := a.Preview.CurrentFileLegend()
		if !ok || &groups[1][0] != &fileLegend[0] {
			t.Error("groups[1] should be the file title bar's CurrentFileLegend")
		}
	})

	t.Run("primary preview, file displayed, goto prompt open: three groups", func(t *testing.T) {
		a := newTestApp(t, 60, 15)
		if res := a.files.Open(path, 1<<20); res.Outcome != openfiles.Opened {
			t.Fatalf("Open failed: %s", res.Message)
		}
		a.Preview.GotoPromptOpen = true
		groups := a.helpLegendGroups()
		if len(groups) != 3 {
			t.Fatalf("got %d groups, want 3 (main, file title bar, goto prompt)", len(groups))
		}
		gotoLegend, ok := a.Preview.GotoPromptLegend()
		if !ok || &groups[2][0] != &gotoLegend[0] {
			t.Error("groups[2] should be GotoPromptLegend")
		}
	})

	t.Run("browser: just its own header group", func(t *testing.T) {
		a := newTestApp(t, 60, 15)
		a.overlay = views.OverlayBrowser
		groups := a.helpLegendGroups()
		if len(groups) != 1 {
			t.Fatalf("got %d groups, want 1", len(groups))
		}
		if &groups[0][0] != &a.Browser.CurrentLegend()[0] {
			t.Error("groups[0] should be Browser.CurrentLegend()")
		}
	})

	t.Run("open-files overlay: main then the popup's own legend", func(t *testing.T) {
		a := newTestApp(t, 60, 15)
		a.overlay = views.OverlayOpenFiles
		groups := a.helpLegendGroups()
		if len(groups) != 2 {
			t.Fatalf("got %d groups, want 2", len(groups))
		}
		if &groups[0][0] != &previewLegend[0] {
			t.Error("groups[0] should be previewLegend")
		}
		want := a.OpenFiles.CurrentLegend()
		if len(groups[1]) != len(want) || groups[1][0].Text != want[0].Text {
			t.Errorf("groups[1] = %v, want OpenFiles.CurrentLegend() = %v", groups[1], want)
		}
	})
}

// TestDrawHelpRendersBoxAtUpperRightBelowTitleRows is an end-to-end
// rendering check (SPEC.md §5.4): the help overlay draws a bordered
// "keys" popup flush with the right edge, immediately below the
// occupied title bar row(s), containing every group's entry text in
// order with a blank separator line between groups.
func TestDrawHelpRendersBoxAtUpperRightBelowTitleRows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "one.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w, h := 80, 20
	a := newTestApp(t, w, h)
	if res := a.files.Open(path, 1<<20); res.Outcome != openfiles.Opened {
		t.Fatalf("Open failed: %s", res.Message)
	}
	a.shared.HelpVisible = true

	a.drawHelp(w, h)
	a.shared.Canvas.Show()

	sim := a.shared.Canvas.Screen.(tcell.SimulationScreen)
	titleRows := a.helpTitleRows()

	// The box's top border sits on the row right after the title bars,
	// right-aligned flush to the terminal's right edge.
	topRow := rowText(sim, titleRows, w)
	if !strings.Contains(topRow, "┌ keys") {
		t.Fatalf("row %d = %q, want the box's top border with the \"keys\" title", titleRows, topRow)
	}
	if !strings.HasSuffix(strings.TrimRight(topRow, " "), "┐") {
		t.Errorf("row %d = %q, want the box flush against the right edge", titleRows, topRow)
	}

	full := ""
	for y := titleRows; y < h; y++ {
		full += rowText(sim, y, w) + "\n"
	}
	for _, want := range []string{"[tab] switch files", "[o] quick open", "[b] browse", "[s] search", "[q] quit", "[/] find", "[g] goto line", "[c] copy mode"} {
		if !strings.Contains(full, want) {
			t.Errorf("help box content missing entry %q; box text:\n%s", want, full)
		}
	}
}

// rowText returns row y of sim as a plain string, w columns wide.
func rowText(sim tcell.SimulationScreen, y, w int) string {
	cells, width, _ := sim.GetContents()
	var b strings.Builder
	for x := 0; x < w && x < width; x++ {
		c := cells[y*width+x]
		if len(c.Runes) > 0 {
			b.WriteRune(c.Runes[0])
		} else {
			b.WriteRune(' ')
		}
	}
	return b.String()
}
