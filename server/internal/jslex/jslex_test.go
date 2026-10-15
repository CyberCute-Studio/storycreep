package jslex

import (
	"slices"
	"strings"
	"testing"
)

// kinds renders tokens compactly, as kind:text, for comparison.
func kinds(toks []Token) string {
	var parts []string
	for _, t := range toks {
		parts = append(parts, string(t.Kind)+":"+t.Text)
	}
	return strings.Join(parts, " ")
}

func TestLex(t *testing.T) {
	tests := []struct {
		name, src, want string
	}{
		{"call with a string", `t("board.empty")`, `i:t (:( s:board.empty (:)`},
		{"single quotes and escapes", `t('caf\u00e9 \'x\'')`, `i:t (:( s:café 'x' (:)`},
		{"template without substitutions", "t(`a.b`)", `i:t (:( s:a.b (:)`},
		{"template with a substitution", "t(`a.${k}.b`)", `i:t (:( t: i:k e: (:)`},
		{"nested template", "`${`x${y}`}`", `t: t: i:y e: e:`},
		{"regular expression", `x = /a"b/g; y`, `i:x (:= r:/a"b/g (:; i:y`},
		{"division", `a / b / c`, `i:a (:/ i:b (:/ i:c`},
		{"regular expression after return", `return /x/`, `i:return r:/x/`},
		{"comment markers in a string", `"// not a comment" + '/* nor this */'`, `s:// not a comment (:+ s:/* nor this */`},
		{"unpaired quote", `<p>Don't</p>`, `(:< i:p (:> i:Don (:' i:t (:< (:/ i:p (:>`},
		{"number", `1.5e3 + 0x1F`, `n:1.5e3 (:+ n:0x1F`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks, _, unclosed := Lex(tt.src)
			if got := kinds(toks); got != tt.want {
				t.Errorf("Lex(%q) = %s, want %s", tt.src, got, tt.want)
			}
			if unclosed != 0 {
				t.Errorf("Lex(%q) reports an unclosed literal on line %d", tt.src, unclosed)
			}
		})
	}
}

func TestLexComments(t *testing.T) {
	src := "// own line\nx(); // after code\n/* block\n   on two lines */ y\n`a\nb`"
	toks, comments, unclosed := Lex(src)
	want := []Comment{
		{Line: 1, Text: "// own line", OwnLine: true},
		{Line: 2, Text: "// after code"},
		{Line: 3, Text: "/* block\n   on two lines */", Block: true},
	}
	if !slices.Equal(comments, want) {
		t.Errorf("comments = %+v, want %+v", comments, want)
	}
	if unclosed != 0 {
		t.Errorf("unclosed = %d, want 0", unclosed)
	}
	// y is on line 4, after the block comment's line break, and the template
	// literal starts on line 5.
	if got := toks[len(toks)-2]; !got.Is(Ident, "y") || got.Line != 4 {
		t.Errorf("token %+v, want y on line 4", got)
	}
	if got := toks[len(toks)-1]; got.Kind != String || got.Text != "a\nb" || got.Line != 5 {
		t.Errorf("token %+v, want the template literal on line 5", got)
	}
}

func TestLexUnclosed(t *testing.T) {
	for src, line := range map[string]int{
		"a\n`never closed":    2,
		"a\n/* never closed":  2,
		"`${\n  x\n":          1,
		"ok\n\"one line only": 0,
	} {
		if _, _, got := Lex(src); got != line {
			t.Errorf("Lex(%q) reports line %d unclosed, want %d", src, got, line)
		}
	}
}
