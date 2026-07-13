// Package tree implements the lazily-loaded directory tree, its
// depth-first flattening, and the navigation semantics described in
// SPEC.md §2 and §5. It has no dependency on any terminal-rendering
// library so it can be unit-tested without a real terminal.
package tree

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Node is one entry in the lazily-loaded tree.
type Node struct {
	Path     string // absolute path
	Name     string // display name (basename, or full path for a root whose basename is empty)
	Depth    int
	Parent   *Node
	IsDir    bool
	Expanded bool
	Children []*Node // nil until loaded; loading is idempotent
	Err      string  // non-empty if listing this node's children failed

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
// already-loaded node is a no-op (SPEC.md §2).
func (n *Node) LoadChildren(rootPath string, ignorer Ignorer) {
	if !n.IsDir || n.loaded {
		return
	}
	n.loaded = true

	entries, err := os.ReadDir(n.Path)
	if err != nil {
		n.Err = err.Error()
		n.Children = nil
		return
	}

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

// RelativeDisplayPath renders target's path relative to root as a
// POSIX slash-delimited string (SPEC.md §6, §"Path display and reveal").
func RelativeDisplayPath(root, target string) string {
	return relSlashPath(root, target)
}

// RevealPath walks down from root following relSlashPath's segments,
// expanding every intermediate ancestor directory (loading children as
// needed) so the target becomes visible, and returns the target node.
// It returns nil if the path doesn't exist under root or falls outside
// it entirely.
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
