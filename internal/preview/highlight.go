package preview

import (
	"path/filepath"
	"strings"
	"unicode"
)

// Category is one of a small fixed set of display categories for a
// highlighted text fragment (SPEC.md §8).
type Category string

// Display categories for a highlighted text fragment.
const (
	CategoryText     Category = "text"
	CategoryComment  Category = "comment"
	CategoryString   Category = "string"
	CategoryNumber   Category = "number"
	CategoryKeyword  Category = "keyword"
	CategoryFunction Category = "function"
	CategoryOperator Category = "operator"
)

// Segment is one (text, category) fragment of a highlighted line.
type Segment struct {
	Text     string
	Category Category
}

// langRules describes a minimal, pragmatic per-line lexer for one
// language: comment-start markers, string delimiters, and a keyword
// set. It is not a general tokenizer — only a stable, reasonable
// per-line categorization (SPEC.md §8).
type langRules struct {
	commentPrefixes []string
	stringDelims    string
	keywords        map[string]bool
}

var extensionRules = map[string]*langRules{
	".sh":   shellRules,
	".bash": shellRules,
	".py":   pythonRules,
	".go":   goRules,
	".yaml": yamlRules,
	".yml":  yamlRules,
	".json": jsonRules,
	".md":   markdownRules,
	".tf":   hclRules,
	".hcl":  hclRules,
	".cue":  cueRules,
}

var shellRules = &langRules{
	commentPrefixes: []string{"#"},
	stringDelims:    `"'`,
	keywords: keywordSet("if", "then", "else", "elif", "fi", "for", "while", "do", "done",
		"case", "esac", "function", "return", "local", "export", "in", "until", "select"),
}

var pythonRules = &langRules{
	commentPrefixes: []string{"#"},
	stringDelims:    `"'`,
	keywords: keywordSet("def", "class", "return", "if", "elif", "else", "for", "while",
		"import", "from", "as", "with", "try", "except", "finally", "raise", "pass",
		"break", "continue", "lambda", "yield", "global", "nonlocal", "async", "await",
		"and", "or", "not", "in", "is", "None", "True", "False"),
}

var goRules = &langRules{
	commentPrefixes: []string{"//"},
	stringDelims:    "\"`",
	keywords: keywordSet("func", "package", "import", "return", "if", "else", "for", "range",
		"switch", "case", "default", "break", "continue", "var", "const", "type", "struct",
		"interface", "map", "chan", "go", "defer", "select", "nil", "true", "false"),
}

var yamlRules = &langRules{
	commentPrefixes: []string{"#"},
	stringDelims:    `"'`,
	keywords:        keywordSet("true", "false", "null"),
}

var jsonRules = &langRules{
	stringDelims: `"`,
	keywords:     keywordSet("true", "false", "null"),
}

var markdownRules = &langRules{
	stringDelims: "`",
}

var hclRules = &langRules{
	commentPrefixes: []string{"#", "//"},
	stringDelims:    `"`,
	keywords:        keywordSet("resource", "variable", "output", "module", "provider", "locals", "data", "true", "false", "null"),
}

var cueRules = &langRules{
	commentPrefixes: []string{"//"},
	stringDelims:    `"`,
	keywords:        keywordSet("package", "import", "true", "false", "null"),
}

func keywordSet(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

var dockerfileNames = map[string]bool{"Dockerfile": true}

var dockerfileRules = &langRules{
	commentPrefixes: []string{"#"},
	stringDelims:    `"'`,
	keywords: keywordSet("FROM", "RUN", "CMD", "LABEL", "EXPOSE", "ENV", "ADD", "COPY",
		"ENTRYPOINT", "VOLUME", "USER", "WORKDIR", "ARG", "ONBUILD", "STOPSIGNAL", "HEALTHCHECK", "SHELL"),
}

// rulesFor picks a rule-set by file extension or basename, falling back
// to a shebang-line sniff for extensionless scripts, per SPEC.md §8.
// Returns nil if no rule-set matches, meaning the caller should render
// plain text.
func rulesFor(path string, firstLine string) *langRules {
	base := filepath.Base(path)
	if dockerfileNames[base] {
		return dockerfileRules
	}
	ext := strings.ToLower(filepath.Ext(path))
	if r, ok := extensionRules[ext]; ok {
		return r
	}
	if strings.HasPrefix(firstLine, "#!") {
		switch {
		case strings.Contains(firstLine, "python"):
			return pythonRules
		case strings.Contains(firstLine, "bash"), strings.Contains(firstLine, "/sh"), strings.HasSuffix(firstLine, "sh"):
			return shellRules
		}
	}
	return nil
}

// Highlight categorizes every line of path's content, producing exactly
// one segment list per source line. It returns nil if no rule-set
// matches the file (the caller should fall back to plain text), and
// never fails — highlighting errors degrade to a single text segment
// per line instead (SPEC.md §8).
func Highlight(path string, lines []string) [][]Segment {
	firstLine := ""
	if len(lines) > 0 {
		firstLine = lines[0]
	}
	rules := rulesFor(path, firstLine)
	if rules == nil {
		return nil
	}

	out := make([][]Segment, len(lines))
	for i, line := range lines {
		out[i] = highlightLine(line, rules)
	}
	return out
}

// highlightLine performs a single-pass, stateless-per-line
// categorization. It does not track multi-line strings or comments —
// a deliberate simplification per SPEC.md §8's "reasonable, stable"
// bar, not a general tokenizer.
func highlightLine(line string, rules *langRules) []Segment {
	var segs []Segment
	runes := []rune(line)
	i := 0
	n := len(runes)

	flush := func(text string, cat Category) {
		if text == "" {
			return
		}
		if len(segs) > 0 && segs[len(segs)-1].Category == cat {
			segs[len(segs)-1].Text += text
			return
		}
		segs = append(segs, Segment{Text: text, Category: cat})
	}

	for i < n {
		rest := string(runes[i:])

		if matchedPrefix := matchAny(rest, rules.commentPrefixes); matchedPrefix != "" {
			flush(rest, CategoryComment)
			break
		}

		c := runes[i]

		if strings.ContainsRune(rules.stringDelims, c) {
			j := i + 1
			for j < n && runes[j] != c {
				if runes[j] == '\\' && j+1 < n {
					j++
				}
				j++
			}
			if j < n {
				j++ // include closing delimiter
			}
			flush(string(runes[i:j]), CategoryString)
			i = j
			continue
		}

		if unicode.IsDigit(c) {
			j := i
			for j < n && (unicode.IsDigit(runes[j]) || runes[j] == '.' || runes[j] == 'e' || runes[j] == 'E' || runes[j] == 'x' || unicode.IsLetter(runes[j])) {
				j++
			}
			flush(string(runes[i:j]), CategoryNumber)
			i = j
			continue
		}

		if unicode.IsLetter(c) || c == '_' {
			j := i
			for j < n && (unicode.IsLetter(runes[j]) || unicode.IsDigit(runes[j]) || runes[j] == '_') {
				j++
			}
			word := string(runes[i:j])
			switch {
			case rules.keywords[word]:
				flush(word, CategoryKeyword)
			case j < n && runes[j] == '(':
				flush(word, CategoryFunction)
			default:
				flush(word, CategoryText)
			}
			i = j
			continue
		}

		if unicode.IsSpace(c) {
			flush(string(c), CategoryText)
			i++
			continue
		}

		// Punctuation/operator run.
		j := i
		for j < n && !unicode.IsLetter(runes[j]) && !unicode.IsDigit(runes[j]) && !unicode.IsSpace(runes[j]) && runes[j] != '_' && !strings.ContainsRune(rules.stringDelims, runes[j]) {
			j++
		}
		if j == i {
			j++
		}
		flush(string(runes[i:j]), CategoryOperator)
		i = j
	}

	if len(segs) == 0 {
		segs = []Segment{{Text: "", Category: CategoryText}}
	}
	return segs
}

func matchAny(s string, prefixes []string) string {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return p
		}
	}
	return ""
}
