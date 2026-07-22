// Package asyncjob factors out the start/done/elapsed bookkeeping shared
// by every background job in this codebase — internal/index.Index,
// internal/preview.StreamIndex, and internal/find.Scan all independently
// reimplemented the same "mutex + done bool + startTime + doneTime"
// shape so a background goroutine could publish a result without any
// shared mutable state with the UI goroutine beyond a snapshot's own
// accessor methods (SPEC.md §6, §10). State is meant to be embedded by
// value into a job struct, which then guards its own payload fields with
// the same embedded lock rather than a second one.
package asyncjob

import (
	"sync"
	"time"
)

// State is the embeddable start/done bookkeeping for a background job.
// The zero value is not ready to use — construct one with New() so
// startTime is set — but once embedded, a job struct can call the
// promoted Lock/Unlock/RLock/RUnlock methods to guard its own fields
// alongside State's, keeping everything under one lock.
type State struct {
	mu        sync.RWMutex
	done      bool
	startTime time.Time
	doneTime  time.Time
}

// New returns a State with startTime set to now, ready to embed into a
// job struct before its background goroutine is started.
func New() State {
	return State{startTime: time.Now()}
}

func (s *State) Lock()    { s.mu.Lock() }
func (s *State) Unlock()  { s.mu.Unlock() }
func (s *State) RLock()   { s.mu.RLock() }
func (s *State) RUnlock() { s.mu.RUnlock() }

// Reset clears done/doneTime and sets startTime to now, for restarting a
// job in place (e.g. Index.Rebuild) without discarding the embedding
// struct's own mutex. Callers must hold the write lock.
func (s *State) Reset() {
	s.done = false
	s.doneTime = time.Time{}
	s.startTime = time.Now()
}

// MarkDone records completion time and flips done to true. Callers must
// hold the write lock, typically alongside setting their own payload
// fields so the whole update is atomic from a reader's perspective.
func (s *State) MarkDone() {
	s.done = true
	s.doneTime = time.Now()
}

// IsDone reports whether the job has completed. Callers must hold at
// least a read lock.
func (s *State) IsDone() bool {
	return s.done
}

// Elapsed returns how much wall-clock time has passed since the job
// started. Safe to call without holding the lock: startTime is set once
// before the background goroutine starts and only otherwise changed by
// Reset, under the same no-lock convention the original Index/StreamIndex/
// Scan types used.
func (s *State) Elapsed() time.Duration {
	return time.Since(s.startTime)
}

// SinceDone returns how much wall-clock time has passed since the job
// finished, and whether it has finished at all. The duration is
// meaningless (and reported as 0) while still running. Callers must hold
// at least a read lock.
func (s *State) SinceDone() (time.Duration, bool) {
	if !s.done {
		return 0, false
	}
	return time.Since(s.doneTime), true
}
