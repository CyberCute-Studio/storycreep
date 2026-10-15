package traceability

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/CyberCute-Studio/storycreep/server/internal/doccheck"
)

var goLang = &language{
	comment:  "//",
	isTest:   func(name string) bool { return strings.HasSuffix(name, "_test.go") },
	read:     readGo,
	declares: goDeclares,
}

// goDeclares reports whether Go source text, such as commented-out code,
// declares a test or a subtest: "func TestX" or "t.Run(". It reads the text
// token by token, because a fragment of code does not parse as a file.
func goDeclares(_, text string) bool {
	fset := token.NewFileSet()
	var s scanner.Scanner
	s.Init(fset.AddFile("", -1, len(text)), []byte(text), func(token.Position, string) {}, 0)
	type item struct {
		tok token.Token
		lit string
	}
	var prev [3]item // the last three tokens, the latest last
	for {
		_, tok, lit := s.Scan()
		switch {
		case tok == token.EOF:
			return false
		case tok == token.IDENT && prev[2].tok == token.FUNC && isGoTest(lit):
			return true
		case tok == token.LPAREN && prev[2] == item{token.IDENT, "Run"} && prev[1].tok == token.PERIOD && prev[0].tok == token.IDENT:
			return true
		}
		prev = [3]item{prev[1], prev[2], {tok, lit}}
	}
}

// isGoTest reports whether a function name declares a Go test, benchmark,
// or fuzz test, by go test's rule: the prefix, then nothing or a character
// that is not a lower-case letter, as in TestX, Test_x, or TestÜber.
func isGoTest(name string) bool {
	if name == "TestMain" {
		return false
	}
	for _, prefix := range []string{"Test", "Benchmark", "Fuzz"} {
		if rest, ok := strings.CutPrefix(name, prefix); ok {
			r, _ := utf8.DecodeRuneInString(rest)
			return rest == "" || !unicode.IsLower(r)
		}
	}
	return false
}

// readGo parses a Go file. Its test declarations are the top-level Test,
// Benchmark, and Fuzz functions and the subtests in them (t.Run or b.Run on a
// *testing.T or *testing.B parameter, however the file imports package
// testing), named as go test names them: a subtest without a literal name is
// "*".
func readGo(file, src string, isTest bool) source {
	var s source
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		line, msg := 1, err.Error()
		if list, ok := err.(scanner.ErrorList); ok && len(list) > 0 {
			// The error's line follows //line directives; its offset does not.
			line, msg = 1+strings.Count(src[:min(list[0].Pos.Offset, len(src))], "\n"), list[0].Msg
		}
		s.problems = append(s.problems, doccheck.Problem{File: file, Line: line, Msg: "cannot parse the file as Go: " + msg})
		return s
	}
	// position locates p in this file. A //line directive, as in generated
	// code, would move it to another file, line, or column.
	position := func(p token.Pos) token.Position { return fset.PositionFor(p, false) }

	for _, g := range f.Comments {
		for _, c := range g.List {
			pos := position(c.Pos())
			if strings.HasPrefix(c.Text, "/*") {
				s.comments = append(s.comments, blockComment(c.Text, pos.Line, "/*", "*/")...)
				continue
			}
			lineStart := pos.Offset - (pos.Column - 1)
			s.comments = append(s.comments, comment{
				line:    pos.Line,
				text:    lineCommentText(c.Text, "//"),
				ownLine: strings.TrimSpace(src[lineStart:pos.Offset]) == "",
			})
		}
	}
	if !isTest {
		return s
	}

	pkgs := testingNames(f)
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !isGoTest(fn.Name.Name) {
			continue
		}
		s.decls = append(s.decls, decl{line: position(fn.Pos()).Line, name: fn.Name.Name})
		if fn.Body == nil {
			continue
		}
		// The names of the *testing.T and *testing.B parameters of the test
		// and of the functions in it, whose Run calls start subtests.
		params := map[string]bool{}
		addTestingParams(fn.Type, pkgs, params)
		isRun := func(call *ast.CallExpr) bool {
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Run" || len(call.Args) != 2 {
				return false
			}
			recv, ok := sel.X.(*ast.Ident)
			return ok && params[recv.Name]
		}
		// For each node being visited, innermost last, whether it is a t.Run
		// call, and the subtests that those calls start.
		type subtest struct {
			name string
			// exact is set when the name is the one go test prints: the
			// subtest's name and those of the subtests it is in are literals.
			exact bool
		}
		var runs []bool
		var subtests []subtest
		seen := map[string]int{} // the uses of each exact name, as go test counts them
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if n == nil {
				if runs[len(runs)-1] {
					subtests = subtests[:len(subtests)-1]
				}
				runs = runs[:len(runs)-1]
				return true
			}
			if lit, ok := n.(*ast.FuncLit); ok {
				addTestingParams(lit.Type, pkgs, params)
			}
			call, ok := n.(*ast.CallExpr)
			run := ok && isRun(call)
			runs = append(runs, run)
			if run {
				parent := subtest{name: fn.Name.Name, exact: true}
				if len(subtests) > 0 {
					parent = subtests[len(subtests)-1]
				}
				st := subtest{name: parent.name + "/*"}
				if name, ok := subtestName(call.Args[0]); ok {
					st.name = parent.name + "/" + name
					if parent.exact {
						st = subtest{name: uniqueSubtest(seen, parent.name, name), exact: true}
					}
				}
				subtests = append(subtests, st)
				s.decls = append(s.decls, decl{line: position(call.Pos()).Line, name: st.name})
			}
			return true
		})
	}
	return s
}

// testingNames returns the names by which a file refers to package testing:
// "testing", and any other name it imports the package as, with "." for a
// dot import.
func testingNames(f *ast.File) map[string]bool {
	names := map[string]bool{"testing": true}
	for _, imp := range f.Imports {
		if path, err := strconv.Unquote(imp.Path.Value); err == nil && path == "testing" && imp.Name != nil {
			names[imp.Name.Name] = true
		}
	}
	return names
}

// addTestingParams adds to names the parameters of a function that are a
// *testing.T or a *testing.B, whose Run method starts a subtest. pkgs holds
// the names by which the file refers to package testing (see testingNames).
// A Run call on anything else, such as app.Run(ctx, args), is not a test.
func addTestingParams(ft *ast.FuncType, pkgs, names map[string]bool) {
	for _, f := range ft.Params.List {
		star, ok := f.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		typ := ""
		switch x := star.X.(type) {
		case *ast.SelectorExpr:
			if pkg, ok := x.X.(*ast.Ident); ok && pkgs[pkg.Name] {
				typ = x.Sel.Name
			}
		case *ast.Ident:
			if pkgs["."] {
				typ = x.Name
			}
		}
		if typ != "T" && typ != "B" {
			continue
		}
		for _, n := range f.Names {
			names[n.Name] = true
		}
	}
}

// uniqueSubtest returns the full name that go test gives a subtest named sub
// in the test or subtest named parent. As go test does, it numbers a name
// used again in the same parent, as "case#01", and an empty name, as "#00".
// seen counts the full names used so far.
func uniqueSubtest(seen map[string]int, parent, sub string) string {
	name := parent + "/" + sub
	empty := sub == ""
	for {
		next, used := seen[name]
		if !empty && !used {
			seen[name] = 1
			return name
		}
		seen[name] = next + 1
		name = fmt.Sprintf("%s#%02d", name, next)
		empty = false
	}
}

// subtestName returns a subtest's name as go test prints it, and false when
// the name is not a literal. Like go test, it writes each space character,
// such as a tab or a line break, as "_", and each character that is not
// printable as its Go escape.
func subtestName(arg ast.Expr) (string, bool) {
	lit, ok := arg.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	name, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsSpace(r):
			b.WriteByte('_')
		case !strconv.IsPrint(r):
			q := strconv.QuoteRune(r)
			b.WriteString(q[1 : len(q)-1])
		default:
			b.WriteRune(r)
		}
	}
	return b.String(), true
}
