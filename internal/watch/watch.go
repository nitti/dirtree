// Package watch drives the live-refresh behavior in SPEC.md §6a: it
// notifies the UI when a directory it has already visited changes on
// disk, so the tree, selection, and background index can be kept in
// sync as files are added, moved, or deleted. fsnotify is a
// statically-linkable, dependency-free-at-runtime library (compiled
// into the binary, no interpreter or dynamic library needed at
// startup), consistent with the "no required runtime dependencies"
// constraint in CLAUDE.md.
//
// This package is terminal-rendering-adjacent (it wraps an OS
// notification facility) and, like internal/ui, is verified manually
// rather than by unit test.
package watch

import (
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher batches change notifications from any number of watched
// directories into a single debounced signal channel, so a burst of
// related fs events (e.g. an editor's save-via-temp-file-and-rename, or
// a multi-file `git checkout`) triggers one refresh instead of many.
type Watcher struct {
	w       *fsnotify.Watcher
	Events  chan struct{}
	watched map[string]bool
}

// New starts a Watcher whose Events channel fires no more often than
// once per debounce window following the most recent underlying fs
// event. Returns an error if the OS notification facility can't be
// initialized (e.g. inotify instance/watch limits) — callers should
// treat live refresh as best-effort and continue without it rather than
// failing startup, matching the tolerant handling of unreadable
// .gitignore/.dirtreeignore files elsewhere in the app.
func New(debounce time.Duration) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		w:       fw,
		Events:  make(chan struct{}, 1),
		watched: map[string]bool{},
	}
	go w.loop(debounce)
	return w, nil
}

func (w *Watcher) loop(debounce time.Duration) {
	var timer *time.Timer
	var timerC <-chan time.Time
	for {
		select {
		case _, ok := <-w.w.Events:
			if !ok {
				return
			}
			if timer == nil {
				timer = time.NewTimer(debounce)
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(debounce)
			}
			timerC = timer.C
		case <-timerC:
			timerC = nil
			select {
			case w.Events <- struct{}{}:
			default:
			}
		case _, ok := <-w.w.Errors:
			if !ok {
				return
			}
			// Best-effort: a watch error (e.g. a watched directory
			// disappearing) is not fatal to the session.
		}
	}
}

// Add starts watching dir for changes to its immediate contents, if not
// already watched. Best-effort and idempotent: an error (permission
// denied, or dir removed between being loaded and being watched here) is
// swallowed rather than surfaced, the same tolerant posture the tree and
// index take toward unlistable directories (SPEC.md §2, §3).
func (w *Watcher) Add(dir string) {
	if w.watched[dir] {
		return
	}
	if err := w.w.Add(dir); err == nil {
		w.watched[dir] = true
	}
}

// Close stops the underlying OS watch and terminates the debounce loop.
func (w *Watcher) Close() error {
	return w.w.Close()
}
