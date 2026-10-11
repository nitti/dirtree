package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/session"
)

// --- Session persistence (TESTING.md "Session persistence (§6.5)") ---

// newSessionApp returns an App over a fresh root containing the named
// files, without the background index/watcher New starts — restore and
// save only touch the open-files list.
func newSessionApp(t *testing.T, names ...string) (*App, string) {
	t.Helper()
	root := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(root, n), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &App{rootPath: root, files: openfiles.New()}, root
}

func saveState(t *testing.T, s session.State) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.json")
	if err := session.Save(path, s); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRestoreSessionReopensListInOrder(t *testing.T) {
	a, root := newSessionApp(t, "a", "b", "c")
	path := saveState(t, session.State{
		Root:      root,
		Displayed: filepath.Join(root, "b"),
		Files: []session.File{
			{Path: filepath.Join(root, "c")},
			{Path: filepath.Join(root, "b"), TopLine: 9, CopyMode: true},
			{Path: filepath.Join(root, "a")},
		},
	})

	a.RestoreSession(path)
	var got []string
	for _, e := range a.files.Entries {
		got = append(got, filepath.Base(e.Path))
	}
	if strings.Join(got, ",") != "c,b,a" {
		t.Fatalf("expected order c,b,a, got %v", got)
	}
	e := a.files.DisplayedEntry()
	if e == nil || filepath.Base(e.Path) != "b" {
		t.Fatalf("expected b displayed, got %+v", e)
	}
	if e.RestoreLine != 9 || !e.CopyMode {
		t.Fatalf("expected b's TopLine/CopyMode restored, got RestoreLine=%d CopyMode=%v", e.RestoreLine, e.CopyMode)
	}
	if a.toastMessage != "" {
		t.Fatalf("expected no toast when everything reopened, got %q", a.toastMessage)
	}
}

func TestRestoreSessionToastsFilesThatFailedToReopen(t *testing.T) {
	a, root := newSessionApp(t, "a")
	path := saveState(t, session.State{
		Root: root,
		Files: []session.File{
			{Path: filepath.Join(root, "a")},
			{Path: filepath.Join(root, "gone1")},
			{Path: filepath.Join(root, "gone2")},
		},
		Displayed: filepath.Join(root, "a"),
	})

	a.RestoreSession(path)
	if len(a.files.Entries) != 1 {
		t.Fatalf("expected only the surviving file restored, got %d", len(a.files.Entries))
	}
	if !strings.Contains(a.toastMessage, "gone1, gone2") {
		t.Fatalf("expected the toast to name both missing files, got %q", a.toastMessage)
	}
}

func TestRestoreSessionHandsOffFailedDisplayedFile(t *testing.T) {
	tests := []struct {
		name      string
		order     []string
		displayed string
		want      string
	}{
		{name: "to next surviving", order: []string{"a", "gone", "b"}, displayed: "gone", want: "b"},
		{name: "to previous when last", order: []string{"a", "b", "gone"}, displayed: "gone", want: "b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, root := newSessionApp(t, "a", "b")
			s := session.State{Root: root, Displayed: filepath.Join(root, tt.displayed)}
			for _, n := range tt.order {
				s.Files = append(s.Files, session.File{Path: filepath.Join(root, n)})
			}
			a.RestoreSession(saveState(t, s))
			if e := a.files.DisplayedEntry(); e == nil || filepath.Base(e.Path) != tt.want {
				t.Fatalf("expected %s displayed, got %+v", tt.want, e)
			}
		})
	}
}

func TestRestoreSessionMissingFileStartsEmpty(t *testing.T) {
	a, _ := newSessionApp(t)
	path := filepath.Join(t.TempDir(), "none.json")
	a.RestoreSession(path)
	if len(a.files.Entries) != 0 || a.files.Displayed != -1 || a.toastMessage != "" {
		t.Fatal("expected an empty session with no toast")
	}
	if a.sessionPath != path {
		t.Fatal("expected the path remembered for saving on exit")
	}
}

func TestSaveSessionRoundTripsThroughRestore(t *testing.T) {
	a, root := newSessionApp(t, "a", "b")
	a.sessionPath = filepath.Join(t.TempDir(), "s.json")
	a.files.Open(filepath.Join(root, "a"), previewByteCap)
	a.files.Open(filepath.Join(root, "b"), previewByteCap)
	a.files.Entries[1].CopyMode = true
	a.files.Display(0)
	a.saveSession()

	b, _ := newSessionApp(t)
	b.rootPath = root
	b.RestoreSession(a.sessionPath)
	if len(b.files.Entries) != 2 || b.files.Displayed != 0 || !b.files.Entries[1].CopyMode {
		t.Fatalf("expected the list, displayed file, and copy mode to survive a save/restore, got %+v", b.files.Entries)
	}
}
