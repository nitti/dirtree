// Package index implements the background full-tree index used by quick
// open and jump to file (SPEC.md §4.1): a flat, recursively-walked list
// of every path under the root, built without touching the interactive
// tree's node objects so it can safely run concurrently with the UI.
package index

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nitti/dirtree/internal/asyncjob"
)

// Ignorer decides whether a candidate path should be skipped, matching
// the same rules used by the interactive tree (SPEC.md §3).
type Ignorer interface {
	Match(relPath string, isDir bool) bool
}

// Entry is one indexed path.
//
// Entry deliberately does not store a root-relative display path
// alongside AbsPath: it's trivially derivable from AbsPath plus the
// index's scan root, and storing both as separate resident strings for
// the app's entire lifetime would roughly double the index's memory
// footprint for no benefit — on a 100k-file tree that's on the order of
// ten-plus megabytes of duplicate string data. Callers compute it via
// RelPath below.
type Entry struct {
	AbsPath string
	IsDir   bool
}

// RelPath returns e's path relative to root as a root-relative,
// slash-delimited display path, computed on demand instead of stored.
func (e Entry) RelPath(root string) string {
	return relSlashPath(root, e.AbsPath)
}

// Index holds the background-built path list and its build status,
// safe for concurrent reads via the accessor methods (SPEC.md §6, §10).
type Index struct {
	asyncjob.State
	entries []Entry
}

// Start kicks off building the index in a background goroutine and
// returns immediately; the interactive UI must not block on it
// (SPEC.md §6).
func Start(rootPath string, ignorer Ignorer) *Index {
	idx := &Index{State: asyncjob.New()}
	go func() {
		entries := build(rootPath, ignorer)
		idx.Lock()
		idx.entries = entries
		idx.MarkDone()
		idx.Unlock()
	}()
	return idx
}

// Rebuild re-walks rootPath in the background and atomically replaces
// the index's entries once the walk completes, for the live-refresh
// behavior in SPEC.md §6a: when the tree on disk changes, quick open's
// and jump to file's results should eventually reflect it too. It
// resets done/startTime the same way Start does, so the existing
// delayed-loading-indicator logic (SPEC.md §10) applies unchanged — a
// fast rebuild stays invisible, a slow one (re-walking a huge tree)
// shows the same spinner/"indexing…" state a fresh Start would.
func (idx *Index) Rebuild(rootPath string, ignorer Ignorer) {
	idx.Lock()
	idx.Reset()
	idx.Unlock()

	go func() {
		entries := build(rootPath, ignorer)
		idx.Lock()
		idx.entries = entries
		idx.MarkDone()
		idx.Unlock()
	}()
}

// Snapshot returns the current entries (nil if not yet done) and
// whether building has completed.
func (idx *Index) Snapshot() ([]Entry, bool) {
	idx.RLock()
	defer idx.RUnlock()
	return idx.entries, idx.IsDone()
}

// Elapsed returns how much wall-clock time has passed since indexing
// started, for the delayed-loading-indicator logic in SPEC.md §10.
func (idx *Index) Elapsed() time.Duration {
	return idx.State.Elapsed()
}

// SinceDone returns how much wall-clock time has passed since indexing
// last finished, and whether it has finished at all — used to drive the
// "indexing complete" completion message and its fade-out in SPEC.md
// §10. The duration is meaningless (and reported as 0) while indexing
// is still running.
func (idx *Index) SinceDone() (time.Duration, bool) {
	idx.RLock()
	defer idx.RUnlock()
	return idx.State.SinceDone()
}

// build walks rootPath depth-first, applying the same skip/ignore rules
// as interactive listing, guarding against symlink cycles, and returns
// the result sorted by root-relative slash path, case-insensitively.
func build(rootPath string, ignorer Ignorer) []Entry {
	visited := map[string]bool{}
	var entries []Entry

	var walk func(dir string)
	walk = func(dir string) {
		realDir, err := filepath.EvalSymlinks(dir)
		if err != nil {
			realDir = dir
		}
		if visited[realDir] {
			return
		}
		visited[realDir] = true

		dirEntries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range dirEntries {
			if e.Name() == ".git" {
				continue
			}
			childPath := filepath.Join(dir, e.Name())
			isDir := e.IsDir()
			if !isDir && e.Type()&os.ModeSymlink != 0 {
				if info, statErr := os.Stat(childPath); statErr == nil {
					isDir = info.IsDir()
				}
			}
			rel := relSlashPath(rootPath, childPath)
			if ignorer != nil && ignorer.Match(rel, isDir) {
				continue
			}
			entries = append(entries, Entry{AbsPath: childPath, IsDir: isDir})
			if isDir {
				walk(childPath)
			}
		}
	}
	walk(rootPath)

	sort.SliceStable(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].RelPath(rootPath)) < strings.ToLower(entries[j].RelPath(rootPath))
	})
	return entries
}

func relSlashPath(root, target string) string {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		rel = target
	}
	return filepath.ToSlash(rel)
}
