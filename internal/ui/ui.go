// Package ui implements the terminal-rendering layer: the tcell draw
// loop, input handling, resize polling, and escape-timeout
// configuration (SPEC.md §6.2, §6.3). Unlike internal/tree,
// internal/openfiles, internal/match, internal/preview,
// internal/layout, and internal/spinner, this package is not expected
// to be unit-tested — verification is manual, in a real terminal.
//
// This is a placeholder: the interactive App built against the prior
// "tree explorer is primary" view model was removed when SPEC.md was
// redrafted around the open-files-list primary view (see README.md's
// Status section). The staged rebuild against the current spec is in
// progress; New/Run are stubbed until that work lands.
package ui

import "errors"

// App holds all interactive state for a running session.
type App struct {
	rootPath string
}

// New builds a new App rooted at rootPath. It does not touch the
// terminal yet; call Run to start the interactive session.
func New(rootPath string) *App {
	return &App{rootPath: rootPath}
}

// Run configures the terminal and drives the main loop until the user
// quits.
func (a *App) Run() error {
	return errors.New("dirtree: interactive UI not yet implemented against the current spec (see README.md Status)")
}
