// Package session persists the open-files list (SPEC.md §6.5) across
// runs: written when dirtree exits, read back when it next starts in
// the same root directory, so quitting — deliberately or by accident —
// never costs the user the files they had open.
package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// version is bumped whenever State's on-disk shape changes
// incompatibly; Load treats a file with any other version as absent
// rather than guessing at how to migrate it.
const version = 1

// State is one root directory's saved session.
type State struct {
	Version int    `json:"version"`
	Root    string `json:"root"`
	// Displayed is the displayed file's path rather than its index, so
	// it still identifies the right file when an earlier one in Files
	// fails to reopen and is dropped.
	Displayed string `json:"displayed,omitempty"`
	Files     []File `json:"files"`
}

// File is one open file's saved state, in list order.
type File struct {
	Path string `json:"path"`
	// TopLine is the 1-based source line at the top of a text file's
	// viewport, rather than its display-row scroll offset: the latter
	// depends on how lines wrapped at the terminal width of the run
	// that saved it, which the next run needn't share.
	TopLine   int   `json:"top_line,omitempty"`
	HexOffset int64 `json:"hex_offset,omitempty"`
	CopyMode  bool  `json:"copy_mode,omitempty"`
}

// FilePath returns where root's session is stored:
// $XDG_STATE_HOME/dirtree/sessions/<hash of root>.json, with
// XDG_STATE_HOME defaulting to ~/.local/state per the XDG base
// directory spec. The same location is used on every platform,
// including macOS, as is conventional for terminal tools.
func FilePath(root string) (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(base) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(base, "dirtree", "sessions", hex.EncodeToString(sum[:16])+".json"), nil
}

// Load reads the session for root from path. A missing file is
// reported as an error satisfying errors.Is(err, fs.ErrNotExist); a
// file that can't be parsed, has an unknown version, or belongs to a
// different root is also an error, and callers are expected to treat
// every error the same way, as "no saved session."
func Load(path, root string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, err
	}
	if s.Version != version {
		return State{}, fmt.Errorf("unsupported session version %d", s.Version)
	}
	if s.Root != root {
		return State{}, errors.New("session belongs to a different root")
	}
	return s, nil
}

// Save writes s to path, creating its directory if needed. The write
// goes to a temporary file that's then renamed over path, so a crash
// or kill mid-write leaves the previous session intact rather than a
// truncated one.
func Save(path string, s State) error {
	s.Version = version
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".session-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
