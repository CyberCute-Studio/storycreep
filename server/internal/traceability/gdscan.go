package traceability

import "strings"

var gdLang = &language{
	comment: "#",
	isTest: func(name string) bool {
		return strings.HasPrefix(name, "test_") && strings.HasSuffix(name, ".gd")
	},
	read: readGD,
}

// readGD reads a GDScript file. Its test declarations are the GUT test
// functions, func test_…(). GDScript has no block comments, but a
// multi-line string that starts a line serves as one, so its lines are
// returned as a block comment.
func readGD(_, src string, isTest bool) source {
	var s source
	line := 1
	code := false // a token is on the line before i
	prev := ""    // the last word or punctuation read, to find "func test_…"
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '\n':
			line++
			code = false
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '#':
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				end = len(src) - i
			}
			s.comments = append(s.comments, comment{line: line, text: lineCommentText(src[i:i+end], "#"), ownLine: !code})
			i += end
		case strings.HasPrefix(src[i:], `"""`), strings.HasPrefix(src[i:], `'''`):
			quote := src[i : i+3]
			end := i + 3
			for end < len(src) && !strings.HasPrefix(src[end:], quote) {
				if src[end] == '\\' {
					end++
				}
				end++
			}
			end = min(end+3, len(src))
			text := src[i:end]
			if !code {
				s.comments = append(s.comments, blockComment(text, line, quote, quote)...)
			}
			line += strings.Count(text, "\n")
			code = true
			prev = ""
			i = end
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(src) && src[j] != c && src[j] != '\n' {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			if j < len(src) && src[j] == c {
				j++
			}
			j = min(j, len(src))
			line += strings.Count(src[i:j], "\n") // escaped line breaks
			code = true
			prev = ""
			i = j
		case isIdentByte(c):
			j := i
			for j < len(src) && isIdentByte(src[j]) {
				j++
			}
			word := src[i:j]
			if isTest && prev == "func" && strings.HasPrefix(word, "test_") {
				s.decls = append(s.decls, decl{line: line, name: word})
			}
			code = true
			prev = word
			i = j
		default:
			code = true
			prev = string(c)
			i++
		}
	}
	return s
}
