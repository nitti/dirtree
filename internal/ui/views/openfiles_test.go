package views

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/ui/canvas"
)

// TestOpenFilesLegendTier1FitsMinTerminalWidth guards SPEC.md §6.4's
// minimum terminal size against §5.2's legend tiering: the open-files
// dropdown's legend, in both its single-page and multi-page forms,
// must have priority-1 (never-dropped) text that fits within
// canvas.MinTerminalWidth on its own, with no left-hand label —
// otherwise a terminal at exactly the enforced minimum would still see
// a clipped legend.
func TestOpenFilesLegendTier1FitsMinTerminalWidth(t *testing.T) {
	named := map[string][]canvas.LegendEntry{
		"openFilesLegend(false)": openFilesLegend(false),
		"openFilesLegend(true)":  openFilesLegend(true),
	}
	for name, entries := range named {
		tier1 := canvas.LegendString(canvas.KeepUpToPriority(entries, 1))
		if n := len([]rune(tier1)); n > canvas.MinTerminalWidth {
			t.Errorf("%s's priority-1 text is %d runes, exceeding MinTerminalWidth (%d): %q", name, n, canvas.MinTerminalWidth, tier1)
		}
	}
}

// TestOpenFilesRemoveLastEntryReturnsToEmptyPreview guards SPEC.md
// §2.3: removing the open-files list's last remaining entry with `x`
// auto-closes the overlay straight to the primary preview view's empty
// state (OverlayNone), not the browser — the browser only auto-opens
// at startup (§1), not as a result of this in-session action.
func TestOpenFilesRemoveLastEntryReturnsToEmptyPreview(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "one.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := openfiles.New()
	if res := files.Open(path, 1<<20); res.Outcome != openfiles.Opened {
		t.Fatalf("Open failed: %s", res.Message)
	}

	overlay := OverlayOpenFiles
	v := &OpenFiles{Shared: &Shared{Files: files, Overlay: &overlay}}

	v.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))

	if len(files.Entries) != 0 {
		t.Fatalf("Entries = %d, want 0 after removing the only open file", len(files.Entries))
	}
	if overlay != OverlayNone {
		t.Errorf("Overlay = %v, want OverlayNone after emptying the open-files list", overlay)
	}
}

// TestOpenFilesRemoveNonLastEntryStaysOnOverlay guards the
// not-emptied path against the same change: removing an entry while
// others remain open must leave the overlay exactly as it was (it
// only auto-closes when the removal empties the list, above).
func TestOpenFilesRemoveNonLastEntryStaysOnOverlay(t *testing.T) {
	dir := t.TempDir()
	path1 := filepath.Join(dir, "one.txt")
	path2 := filepath.Join(dir, "two.txt")
	for _, p := range []string{path1, path2} {
		if err := os.WriteFile(p, []byte("hello\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files := openfiles.New()
	if res := files.Open(path1, 1<<20); res.Outcome != openfiles.Opened {
		t.Fatalf("Open failed: %s", res.Message)
	}
	if res := files.Open(path2, 1<<20); res.Outcome != openfiles.Opened {
		t.Fatalf("Open failed: %s", res.Message)
	}

	overlay := OverlayOpenFiles
	v := &OpenFiles{Shared: &Shared{Files: files, Overlay: &overlay}, Selected: 0}

	v.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))

	if len(files.Entries) != 1 {
		t.Fatalf("Entries = %d, want 1 after removing one of two open files", len(files.Entries))
	}
	if overlay != OverlayOpenFiles {
		t.Errorf("Overlay = %v, want it to stay OverlayOpenFiles since the list isn't empty", overlay)
	}
}
