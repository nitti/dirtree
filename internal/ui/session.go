package ui

import (
	"path/filepath"

	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/preview"
	"github.com/nitti/dirtree/internal/session"
)

// RestoreSession reopens the open-files list a previous run in this
// root saved to path (SPEC.md §6.5), and remembers path so Run saves
// this run's list back there on exit. Missing or unreadable session
// state just means starting empty. Saved files that can no longer be
// opened are dropped, and named in a toast so their disappearance from
// the list isn't silent; nothing is shown when everything reopened.
func (a *App) RestoreSession(path string) {
	a.sessionPath = path
	s, err := session.Load(path, a.rootPath)
	if err != nil {
		return
	}
	var failed []string
	// A saved displayed file that fails to reopen hands display off to
	// the next surviving file, or the last one if none follow it — the
	// same neighbor Remove picks when the displayed entry is closed.
	displayed := -1
	handOff := false
	for _, f := range s.Files {
		res := a.files.Restore(f.Path, previewByteCap)
		if res.Outcome == openfiles.Failed {
			failed = append(failed, filepath.Base(f.Path))
			handOff = handOff || f.Path == s.Displayed
			continue
		}
		e := res.Entry
		if e.Tier == preview.TierBinary {
			e.HexOffset = f.HexOffset
		} else if f.TopLine > 1 {
			e.RestoreLine = f.TopLine
		}
		e.CopyMode = f.CopyMode
		if f.Path == s.Displayed || handOff {
			displayed = len(a.files.Entries) - 1
			handOff = false
		}
	}
	if displayed < 0 {
		displayed = len(a.files.Entries) - 1
	}
	a.files.Display(displayed)
	if len(failed) > 0 {
		msg, boldRanges := namesToastMessage(failed, " couldn't be reopened from last session")
		a.showToast(msg, boldRanges...)
	}
}

// sessionState captures the open-files list for saving (SPEC.md §6.5).
func (a *App) sessionState() session.State {
	s := session.State{Root: a.rootPath}
	for _, e := range a.files.Entries {
		f := session.File{Path: e.Path, CopyMode: e.CopyMode}
		if e.Tier == preview.TierBinary {
			f.HexOffset = e.HexOffset
		} else {
			f.TopLine = e.TopLine()
		}
		s.Files = append(s.Files, f)
	}
	if e := a.files.DisplayedEntry(); e != nil {
		s.Displayed = e.Path
	}
	return s
}

// saveSession writes the open-files list to the path RestoreSession was
// given, if any. Best-effort: a failure (read-only home directory, a
// throwaway container) must never stand in the way of quitting, and
// there's no screen left to report it on.
func (a *App) saveSession() {
	if a.sessionPath == "" {
		return
	}
	_ = session.Save(a.sessionPath, a.sessionState())
}
