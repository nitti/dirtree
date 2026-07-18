// Package tree implements the lazily-loaded directory tree, its
// depth-first flattening, and the navigation semantics described in
// SPEC.md §2 and §5. It has no dependency on any terminal-rendering
// library so it can be unit-tested without a real terminal.
package tree

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/nitti/dirtree/internal/match"
)

// errText extracts the underlying message from a directory-listing
// error, dropping the "open <path>: " prefix Go's os package adds to a
// *fs.PathError — the path is already visible via the row this message
// is displayed inline on (SPEC.md §2.2, §5.2), so repeating it would be
// redundant.
func errText(err error) string {
	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		return pe.Err.Error()
	}
	return err.Error()
}

// Node is one entry in the lazily-loaded tree.
type Node struct {
	Path     string // absolute path
	Name     string // display name (basename, or full path for a root whose basename is empty)
	Depth    int
	Parent   *Node
	IsDir    bool
	Expanded bool
	Children []*Node // nil until loaded; loading is idempotent
	// Err is non-empty if the most recent operation on this node failed:
	// listing its children (directories, set by LoadChildren/refreshChildren
	// below) or, for a file, the UI's last attempt to open it (set
	// directly by internal/ui on a failed open, per SPEC.md §2.2) — both
	// are rendered the same way (browserLabel's inline `[error]` suffix,
	// with a brief attention-flash, SPEC.md §5.3) since both are "the
	// last thing this node's owner tried to do with it didn't work."
	Err string

	loaded bool
}

// Ignorer decides whether a candidate path should be skipped during
// directory listing and index building (SPEC.md §3). relPath is
// root-relative, slash-delimited; isDir is appended with a trailing
// slash by implementations that need it for directory-only patterns.
type Ignorer interface {
	Match(relPath string, isDir bool) bool
}

// noopIgnorer never excludes anything.
type noopIgnorer struct{}

func (noopIgnorer) Match(string, bool) bool { return false }

// NewRoot builds the root node for absPath, loads its children, and
// marks it expanded, per SPEC.md §2's root-node initialization rule.
// ignorer may be nil, in which case nothing beyond ".git" is skipped.
func NewRoot(absPath string, ignorer Ignorer) *Node {
	if ignorer == nil {
		ignorer = noopIgnorer{}
	}
	name := filepath.Base(absPath)
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = absPath
	}
	root := &Node{
		Path:  absPath,
		Name:  name,
		Depth: 0,
		IsDir: true,
	}
	root.LoadChildren(absPath, ignorer)
	root.Expanded = true
	return root
}

// LoadChildren populates n.Children by listing the directory at n.Path
// on disk. rootPath is the tree root's absolute path, needed to compute
// each candidate's root-relative path for ignore matching. Loading an
// already-successfully-loaded node is a no-op (SPEC.md §2); a node whose
// last load attempt failed (n.Err set) is retried instead of staying
// permanently stuck with a stale error — e.g. the user fixes the
// permission problem and collapses/re-expands the directory, rather
// than only being able to recover via a live-refresh (§6.1) picking up
// the fix on its own.
func (n *Node) LoadChildren(rootPath string, ignorer Ignorer) {
	if !n.IsDir || (n.loaded && n.Err == "") {
		return
	}
	n.loaded = true

	entries, err := os.ReadDir(n.Path)
	if err != nil {
		n.Err = errText(err)
		n.Children = nil
		return
	}
	n.Err = ""

	if ignorer == nil {
		ignorer = noopIgnorer{}
	}

	children := make([]*Node, 0, len(entries))
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		childPath := filepath.Join(n.Path, e.Name())
		isDir := e.IsDir()
		if !isDir && e.Type()&os.ModeSymlink != 0 {
			if info, statErr := os.Stat(childPath); statErr == nil {
				isDir = info.IsDir()
			}
		}
		rel := relSlashPath(rootPath, childPath)
		if ignorer.Match(rel, isDir) {
			continue
		}
		children = append(children, &Node{
			Path:   childPath,
			Name:   e.Name(),
			Depth:  n.Depth + 1,
			Parent: n,
			IsDir:  isDir,
		})
	}

	sortEntries(children)
	n.Children = children
}

// Loaded reports whether n's children have been listed at least once
// (SPEC.md §2's lazy-loading rule), i.e. whether n is a candidate for
// RefreshTree to re-list.
func (n *Node) Loaded() bool {
	return n.loaded
}

// RefreshTree re-lists every directory in the tree rooted at n that has
// already been loaded at least once, merging each fresh listing into the
// existing Children slice so that an entry whose path is unchanged keeps
// its original Node object — and therefore its expanded state and any
// already-loaded subtree — rather than being rebuilt from scratch. This
// is what lets the UI live-refresh as files are added, moved, or removed
// on disk (SPEC.md §6a) while preserving in-place selection and
// disclosure state the same way expand/collapse already does (§5's
// "keep it selected by identity" rule). Directories never visited by the
// user are left alone: they'll pick up current disk state whenever they
// are eventually expanded, same as today.
//
// It returns the paths of any directories whose listing newly started
// failing during this refresh (Err was empty before, non-empty after) —
// e.g. a directory that lost read permission out from under the running
// session. A directory that already had an error, or still lists fine,
// isn't included: the per-node inline error indicator (§5.2) already
// covers the steady-state case, so this is only for the transition a
// caller might want to surface as a one-off notification (SPEC.md §6.1),
// not the ongoing state.
func RefreshTree(n *Node, rootPath string, ignorer Ignorer) []string {
	if !n.IsDir || !n.loaded {
		return nil
	}
	hadErr := n.Err != ""
	n.refreshChildren(rootPath, ignorer)
	var newlyErrored []string
	if n.Err != "" && !hadErr {
		newlyErrored = append(newlyErrored, n.Path)
	}
	for _, c := range n.Children {
		newlyErrored = append(newlyErrored, RefreshTree(c, rootPath, ignorer)...)
	}
	return newlyErrored
}

// refreshChildren re-lists n's directory and merges the result into
// n.Children by path: an entry present both before and after (with the
// same directory-ness) reuses its existing *Node; anything else is a
// fresh Node. Entries no longer present on disk are dropped.
func (n *Node) refreshChildren(rootPath string, ignorer Ignorer) {
	entries, err := os.ReadDir(n.Path)
	if err != nil {
		n.Err = errText(err)
		n.Children = nil
		return
	}

	if ignorer == nil {
		ignorer = noopIgnorer{}
	}

	existing := make(map[string]*Node, len(n.Children))
	for _, c := range n.Children {
		existing[c.Path] = c
	}

	children := make([]*Node, 0, len(entries))
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		childPath := filepath.Join(n.Path, e.Name())
		isDir := e.IsDir()
		if !isDir && e.Type()&os.ModeSymlink != 0 {
			if info, statErr := os.Stat(childPath); statErr == nil {
				isDir = info.IsDir()
			}
		}
		rel := relSlashPath(rootPath, childPath)
		if ignorer.Match(rel, isDir) {
			continue
		}
		if old, ok := existing[childPath]; ok && old.IsDir == isDir {
			children = append(children, old)
			continue
		}
		children = append(children, &Node{
			Path:   childPath,
			Name:   e.Name(),
			Depth:  n.Depth + 1,
			Parent: n,
			IsDir:  isDir,
		})
	}

	sortEntries(children)
	n.Children = children
	n.Err = ""
}

// stillInTree reports whether n is still reachable from the tree
// structure, i.e. still present in its parent's current Children slice
// (recursively up to the root, which is always present). A node dropped
// by refreshChildren remains a valid Go object (anything still holding a
// pointer to it, like the UI's current selection, won't crash) but stops
// satisfying this check.
func stillInTree(n *Node) bool {
	if n.Parent == nil {
		return true
	}
	if !slices.Contains(n.Parent.Children, n) {
		return false
	}
	return stillInTree(n.Parent)
}

// NearestSurviving walks up from n through its ancestors, returning the
// first one still reachable in the tree. Used after RefreshTree to
// re-anchor the UI's selection when the previously-selected node was
// deleted out from under it (SPEC.md §6a). The root is always reachable,
// so this never returns nil.
func NearestSurviving(n *Node) *Node {
	for cur := n; cur != nil; cur = cur.Parent {
		if stillInTree(cur) {
			return cur
		}
	}
	return nil
}

// relSlashPath renders target relative to root as a POSIX slash path,
// regardless of host path-separator conventions (SPEC.md §3, §6).
func relSlashPath(root, target string) string {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		rel = target
	}
	return filepath.ToSlash(rel)
}

// sortEntries sorts directories first, then case-insensitively by name
// (SPEC.md §4).
func sortEntries(nodes []*Node) {
	sort.SliceStable(nodes, func(i, j int) bool {
		a, b := nodes[i], nodes[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
}

// Flatten returns the currently-visible, depth-first, pre-order
// flattening of the tree rooted at n (SPEC.md §2).
func (n *Node) Flatten() []*Node {
	out := []*Node{n}
	if n.IsDir && n.Expanded {
		for _, c := range n.Children {
			out = append(out, c.Flatten()...)
		}
	}
	return out
}

// JumpMatches returns, in display order, every node in root's current
// flattened row list (Flatten) whose own leaf Name case-insensitively
// prefix-matches query — jump to file's candidate set and matching rule
// (SPEC.md §4.3). An empty query returns no matches (jump to file
// treats that as "haven't looked yet," not "matches everything" —
// match.PrefixMatches' own empty-query rule already encodes this).
func JumpMatches(root *Node, query string) []*Node {
	flat := root.Flatten()
	matches := make([]*Node, 0, len(flat))
	for _, n := range flat {
		if match.PrefixMatches(query, n.Name) {
			matches = append(matches, n)
		}
	}
	return matches
}

// RevealPath walks down from root following relSlashPath's segments,
// expanding every intermediate ancestor directory (loading children as
// needed) so the target becomes visible, and returns the target node.
// It returns nil if the path doesn't exist under root or falls outside
// it entirely. Used to reveal a quick open/content search result in the
// browser (SPEC.md §4.2, §9.2) — unlike jump to file (§4.3), which
// deliberately never expands anything since its candidates are already
// scoped to what's visible, quick open and content search search the
// whole tree regardless of current disclosure state, so there's no
// other way for the browser to reflect what they just opened.
func RevealPath(root *Node, rootPath, targetAbsPath string, ignorer Ignorer) *Node {
	rel := relSlashPath(rootPath, targetAbsPath)
	if rel == "." {
		return root
	}
	if rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
		return nil
	}
	segments := strings.Split(rel, "/")
	current := root
	for i, seg := range segments {
		if !current.IsDir {
			return nil
		}
		current.Expand(rootPath, ignorer)
		var next *Node
		for _, c := range current.Children {
			if c.Name == seg {
				next = c
				break
			}
		}
		if next == nil {
			return nil
		}
		if i < len(segments)-1 {
			next.Expand(rootPath, ignorer)
		}
		current = next
	}
	return current
}

// Expand marks a collapsed directory expanded, loading its children if
// needed. A no-op on files. rootPath is passed through to LoadChildren.
func (n *Node) Expand(rootPath string, ignorer Ignorer) {
	if !n.IsDir {
		return
	}
	n.LoadChildren(rootPath, ignorer)
	n.Expanded = true
}

// Collapse marks a directory not-expanded, idempotently. A no-op on
// files.
func (n *Node) Collapse() {
	if !n.IsDir {
		return
	}
	n.Expanded = false
}

// ToggleExpand expands a collapsed directory or collapses an expanded
// one.
func (n *Node) ToggleExpand(rootPath string, ignorer Ignorer) {
	if !n.IsDir {
		return
	}
	if n.Expanded {
		n.Collapse()
	} else {
		n.Expand(rootPath, ignorer)
	}
}

// MoveRight implements SPEC.md §5's Right-arrow semantics and returns
// the node that should end up selected.
func (n *Node) MoveRight(rootPath string, ignorer Ignorer) *Node {
	if !n.IsDir {
		return n
	}
	if !n.Expanded {
		n.Expand(rootPath, ignorer)
		return n
	}
	if len(n.Children) > 0 {
		return n.Children[0]
	}
	return n
}

// MoveLeft implements SPEC.md §5's Left-arrow semantics and returns the
// node that should end up selected. It also mutates expand state as
// described (collapsing the current or parent node).
func (n *Node) MoveLeft() *Node {
	if n.IsDir {
		if n.Expanded {
			n.Collapse()
			return n
		}
		if n.Parent != nil {
			return n.Parent
		}
		return n
	}
	// File: jump to parent and collapse it in the same keypress.
	if n.Parent != nil {
		n.Parent.Collapse()
		return n.Parent
	}
	return n
}

// MoveSelection returns the new selected index after moving delta from
// current within a list of the given count, wrapping at both ends
// (SPEC.md §5). count == 0 returns 0 without dividing by zero.
func MoveSelection(current, delta, count int) int {
	if count <= 0 {
		return 0
	}
	next := (current + delta) % count
	if next < 0 {
		next += count
	}
	return next
}

// MoveSelectionClamped returns the new selected index after moving delta
// from current within a list of the given count, clamped at the first/
// last index rather than wrapping (SPEC.md §4.2's quick open Page Up/
// Page Down, and §2.3's open-files list Page Up/Down). count == 0
// returns 0 without dividing by zero.
func MoveSelectionClamped(current, delta, count int) int {
	if count <= 0 {
		return 0
	}
	next := current + delta
	if next < 0 {
		return 0
	}
	if next > count-1 {
		return count - 1
	}
	return next
}

// RelativeDisplayPath renders target's path relative to root as a
// POSIX slash-delimited string (SPEC.md §6, §"Path display and reveal").
func RelativeDisplayPath(root, target string) string {
	return relSlashPath(root, target)
}
