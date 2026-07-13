// Package ignore implements the minimal, dependency-free .gitignore
// pattern subset described in SPEC.md §3.
package ignore

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type rule struct {
	pattern  string // pattern text, without leading '!' or trailing '/'
	negate   bool
	anchored bool // contains a '/' other than a trailing one
	dirOnly  bool // had a trailing '/'
}

// Matcher holds a parsed, ordered set of gitignore rules.
type Matcher struct {
	rules []rule
}

// Load reads and parses the .gitignore file directly inside rootDir, if
// present and readable. Any problem (missing file, read error) yields
// an empty Matcher that matches nothing — never an error, per SPEC.md
// §3's "never crash or block startup" requirement.
func Load(rootDir string) *Matcher {
	f, err := os.Open(filepath.Join(rootDir, ".gitignore"))
	if err != nil {
		return &Matcher{}
	}
	defer f.Close()

	m := &Matcher{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		m.rules = append(m.rules, parseLine(trimmed))
	}
	return m
}

func parseLine(line string) rule {
	r := rule{}
	if strings.HasPrefix(line, "!") {
		r.negate = true
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") {
		r.dirOnly = true
		line = strings.TrimSuffix(line, "/")
	}
	// A pattern containing '/' anywhere but a trailing slash is
	// anchored to the root per SPEC.md §3 — including a leading '/',
	// which must be checked before stripping it.
	if strings.Contains(line, "/") {
		r.anchored = true
	}
	line = strings.TrimPrefix(line, "/")
	r.pattern = line
	return r
}

// Match reports whether relPath (root-relative, slash-delimited) is
// excluded by the loaded pattern set. isDir should be true when the
// candidate is itself a directory, so directory-only patterns work.
func (m *Matcher) Match(relPath string, isDir bool) bool {
	if m == nil || len(m.rules) == 0 {
		return false
	}
	tested := relPath
	if isDir {
		tested += "/"
	}

	matched := false
	for _, r := range m.rules {
		if r.matches(tested, relPath) {
			matched = !r.negate
		}
	}
	return matched
}

// matches tests a single rule against the candidate. tested is the
// path with a trailing slash appended for directories (used for
// dir-only patterns); relPath is the bare relative path (used for
// basename matching of non-anchored patterns).
func (r rule) matches(tested, relPath string) bool {
	if r.dirOnly && !strings.HasSuffix(tested, "/") {
		return false
	}

	if r.anchored {
		ok, _ := path.Match(r.pattern, relPath)
		if ok {
			return true
		}
		// Directory-only anchored pattern: also allow matching a
		// path nested under it, e.g. pattern "build/" excluding
		// "build/output.txt".
		if r.dirOnly {
			prefix := r.pattern + "/"
			if strings.HasPrefix(relPath, prefix) {
				return true
			}
		}
		return false
	}

	// Unanchored: match the basename at any depth.
	segments := strings.Split(relPath, "/")
	for i, seg := range segments {
		ok, _ := path.Match(r.pattern, seg)
		if ok {
			if r.dirOnly && i != len(segments)-1 {
				// An intermediate directory segment matching a
				// dir-only pattern still excludes everything
				// beneath it.
				return true
			}
			if r.dirOnly && i == len(segments)-1 && !strings.HasSuffix(tested, "/") {
				return false
			}
			return true
		}
	}
	return false
}
