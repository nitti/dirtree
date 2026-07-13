package ignore

import (
	"os"
	"path/filepath"
	"testing"
)

func writeGitignore(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNoGitignoreMeansNoFiltering(t *testing.T) {
	dir := t.TempDir()
	m := Load(dir)
	if m.Match("anything.log", false) {
		t.Fatal("expected no filtering without a .gitignore")
	}
}

func TestSimplePatternExcludesAtAnyDepth(t *testing.T) {
	dir := t.TempDir()
	writeGitignore(t, dir, "*.log\n")
	m := Load(dir)
	if !m.Match("a.log", false) {
		t.Fatal("expected a.log excluded")
	}
	if !m.Match("sub/a.log", false) {
		t.Fatal("expected sub/a.log excluded")
	}
	if m.Match("a.txt", false) {
		t.Fatal("expected a.txt not excluded")
	}
}

func TestNegationReincludes(t *testing.T) {
	dir := t.TempDir()
	writeGitignore(t, dir, "*.log\n!keep.log\n")
	m := Load(dir)
	if !m.Match("other.log", false) {
		t.Fatal("expected other.log excluded")
	}
	if m.Match("keep.log", false) {
		t.Fatal("expected keep.log re-included by negation")
	}
}

func TestDirectoryOnlyPatternDoesNotExcludeSameNamedFile(t *testing.T) {
	dir := t.TempDir()
	writeGitignore(t, dir, "build/\n")
	m := Load(dir)
	if m.Match("build", false) {
		t.Fatal("expected file named 'build' not excluded by directory-only pattern")
	}
	if !m.Match("build", true) {
		t.Fatal("expected directory named 'build' excluded by directory-only pattern")
	}
}

func TestAnchoredPatternOnlyMatchesFromRoot(t *testing.T) {
	dir := t.TempDir()
	writeGitignore(t, dir, "/vendor\n")
	m := Load(dir)
	if !m.Match("vendor", false) {
		t.Fatal("expected root-level vendor excluded")
	}
	if m.Match("sub/vendor", false) {
		t.Fatal("expected nested vendor NOT excluded by anchored pattern")
	}
}

func TestMalformedGitignoreDoesNotCrash(t *testing.T) {
	dir := t.TempDir()
	writeGitignore(t, dir, "[unclosed\n")
	m := Load(dir)
	// Should not panic; result may or may not match, just must not raise.
	_ = m.Match("anything", false)
}

func writeDirtreeIgnore(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".dirtreeignore"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDirtreeIgnoreUsesSamePatternSyntax(t *testing.T) {
	dir := t.TempDir()
	writeDirtreeIgnore(t, dir, "*.secret\n")
	m := LoadDirtreeIgnore(dir)
	if !m.Match("a.secret", false) {
		t.Fatal("expected .dirtreeignore to exclude a.secret")
	}
	if m.Match("a.txt", false) {
		t.Fatal("expected .dirtreeignore not to exclude a.txt")
	}
}

func TestNoDirtreeIgnoreMeansNoFiltering(t *testing.T) {
	dir := t.TempDir()
	m := LoadDirtreeIgnore(dir)
	if m.Match("anything", false) {
		t.Fatal("expected no filtering without a .dirtreeignore")
	}
}

func TestLoadAllCombinesBothFilesUnion(t *testing.T) {
	dir := t.TempDir()
	writeGitignore(t, dir, "*.log\n")
	writeDirtreeIgnore(t, dir, "*.secret\n")
	m := LoadAll(dir)
	if !m.Match("a.log", false) {
		t.Fatal("expected .gitignore pattern still excluded via LoadAll")
	}
	if !m.Match("a.secret", false) {
		t.Fatal("expected .dirtreeignore pattern excluded via LoadAll")
	}
	if m.Match("a.txt", false) {
		t.Fatal("expected non-matching path not excluded")
	}
}

func TestLoadAllNegationsDoNotCrossFiles(t *testing.T) {
	dir := t.TempDir()
	// .gitignore excludes *.log; .dirtreeignore tries to re-include one.
	// Negation only applies within its own file's rule set.
	writeGitignore(t, dir, "*.log\n")
	writeDirtreeIgnore(t, dir, "!keep.log\n")
	m := LoadAll(dir)
	if !m.Match("keep.log", false) {
		t.Fatal("expected a negation in .dirtreeignore not to re-include a path .gitignore excluded")
	}
}
