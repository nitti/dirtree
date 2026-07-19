// Package views holds the per-view types that own dirtree's interactive
// state, key handling, and drawing: the primary preview, browser, quick
// open, open-files-list, and content search views (SPEC.md §5.1). Each
// view embeds *Shared for the state and drawing surface it needs from
// the coordinator (package ui's App) — see Shared's own doc comment for
// why that's a plain struct rather than an interface.
package views

// Overlay identifies which overlay, if any, is currently active over the
// primary preview view (SPEC.md §5.1).
type Overlay int

// The Overlay values, in the same order the primary preview view reaches
// each of them from (SPEC.md §5.1): none active (the preview view
// itself), then browser, quick open, open-files-list, and content
// search.
const (
	OverlayNone Overlay = iota
	OverlayBrowser
	OverlayQuickOpen
	OverlayOpenFiles
	OverlaySearch
)
