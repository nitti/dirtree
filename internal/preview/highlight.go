package preview

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
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

// categoryFor collapses chroma's fine-grained token taxonomy down to
// dirtree's small fixed display-category set (SPEC.md §8 deliberately
// keeps the rendered palette small and stable rather than exposing
// chroma's full token vocabulary to the renderer).
func categoryFor(t chroma.TokenType) Category {
	switch {
	case t.InCategory(chroma.Comment):
		return CategoryComment
	case t.InSubCategory(chroma.LiteralString):
		return CategoryString
	case t.InSubCategory(chroma.LiteralNumber):
		return CategoryNumber
	case t.InCategory(chroma.Keyword):
		return CategoryKeyword
	case t.InSubCategory(chroma.NameFunction):
		return CategoryFunction
	case t.InCategory(chroma.Operator), t.InCategory(chroma.Punctuation):
		return CategoryOperator
	// Markup-oriented token types (chiefly from the markdown lexer) have
	// no matching category above — dirtree's fixed set was designed for
	// code, not prose — so they're deliberately folded onto the nearest
	// existing category rather than left as plain text.
	case t == chroma.GenericHeading, t == chroma.GenericSubheading, t == chroma.GenericStrong:
		return CategoryKeyword
	case t == chroma.GenericEmph, t == chroma.NameTag:
		return CategoryFunction
	case t == chroma.NameAttribute, t == chroma.GenericInserted:
		return CategoryString
	case t == chroma.GenericDeleted:
		return CategoryComment
	default:
		return CategoryText
	}
}

// lexerFor picks a chroma lexer by filename, falling back to a
// content-sniff (which also covers shebang lines) when the filename
// alone doesn't identify a language. Returns nil if chroma has no
// better guess than its generic plaintext lexer, meaning the caller
// should render plain text rather than force a highlight (SPEC.md §8).
func lexerFor(path string, text string) chroma.Lexer {
	if l := lexers.Match(path); l != nil {
		return l
	}
	if l := lexers.Analyse(text); l != nil && l != lexers.Fallback {
		return l
	}
	return nil
}

// Highlight categorizes every line of path's content, producing exactly
// one segment list per source line. It returns nil if no lexer matches
// the file (the caller should fall back to plain text), and never fails
// — highlighting errors degrade to a single text segment per line
// instead (SPEC.md §8).
func Highlight(path string, lines []string) [][]Segment {
	text := strings.Join(lines, "\n")
	lexer := lexerFor(path, text)
	if lexer == nil {
		return nil
	}

	iter, err := lexer.Tokenise(nil, text)
	if err != nil {
		return nil
	}

	out := make([][]Segment, len(lines))
	for i := range out {
		out[i] = []Segment{}
	}

	lineIdx := 0
	flush := func(text string, cat Category) {
		if text == "" || lineIdx >= len(out) {
			return
		}
		segs := out[lineIdx]
		if len(segs) > 0 && segs[len(segs)-1].Category == cat {
			segs[len(segs)-1].Text += text
		} else {
			segs = append(segs, Segment{Text: text, Category: cat})
		}
		out[lineIdx] = segs
	}

	for _, tok := range iter.Tokens() {
		cat := categoryFor(tok.Type)
		for part := range strings.SplitSeq(tok.Value, "\n") {
			if part != "" {
				flush(part, cat)
			}
			// A "\n" in the token stream ends the current source line;
			// strings.Split yields one extra part per newline, so every
			// separator (not just the last part) advances lineIdx.
		}
		if n := strings.Count(tok.Value, "\n"); n > 0 {
			lineIdx += n
		}
	}

	for i, segs := range out {
		if len(segs) == 0 {
			out[i] = []Segment{{Text: "", Category: CategoryText}}
		}
	}
	return out
}
