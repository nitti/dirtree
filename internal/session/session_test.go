package session

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Session persistence (TESTING.md "Session persistence (§6.5)") ---

func TestSaveThenLoadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "s.json")
	want := State{
		Root:      "/r",
		Displayed: "/r/b",
		Files: []File{
			{Path: "/r/a", TopLine: 12, CopyMode: true},
			{Path: "/r/b", HexOffset: 0x40},
		},
	}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path, "/r")
	if err != nil {
		t.Fatal(err)
	}
	if got.Displayed != want.Displayed || len(got.Files) != 2 || got.Files[0] != want.Files[0] || got.Files[1] != want.Files[1] {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, want)
	}
}

func TestLoadMissingFileIsNotExist(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "none.json"), "/r")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected fs.ErrNotExist, got %v", err)
	}
}

func TestLoadRejectsInvalidFiles(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "corrupt", content: "{not json"},
		{name: "wrong version", content: `{"version": 99, "root": "/r"}`},
		{name: "different root", content: `{"version": 1, "root": "/other"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "s.json")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path, "/r"); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestSaveLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")
	for range 2 {
		if err := Save(path, State{Root: "/r"}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "s.json" {
		t.Fatalf("expected only s.json, got %v", entries)
	}
}

func TestFilePath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/state")
	a, err := FilePath("/r/one")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := FilePath("/r/two")
	if !strings.HasPrefix(a, "/state/dirtree/sessions/") || !strings.HasSuffix(a, ".json") {
		t.Fatalf("unexpected path %q", a)
	}
	if a == b {
		t.Fatal("expected distinct roots to get distinct session files")
	}

	t.Setenv("XDG_STATE_HOME", "relative/ignored")
	t.Setenv("HOME", "/home/u")
	c, err := FilePath("/r/one")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c, "/home/u/.local/state/dirtree/sessions/") {
		t.Fatalf("expected fallback to ~/.local/state, got %q", c)
	}
}
