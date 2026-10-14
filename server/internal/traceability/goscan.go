package traceability

import (
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"regexp"
	"strconv"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/doccheck"
)

var goLang = &language{
	comment: "//",
	isTest:  func(name string) bool { return strings.HasSuffix(name, "_test.go") },
	read:    readGo,
}

var goTestNameRE = regexp.MustCompile(`^(?:Test|Benchmark|Fuzz)(?:[A-Z0-9_]\w*)?$`)

// isGoTest reports whether a function name declares a Go test, benchmark,
// or fuzz test.
func isGoTest(name string) bool {
	return goTestNameRE.MatchString(name) && name != "TestMain"
}

// readGo parses a Go file. Its test declarations are the top-level Test,
// Benchmark, and Fuzz functions and the t.Run subtests in them, named as go
// test names them: a subtest without a literal name is "*".
func readGo(file, src string, isTest bool) source {
	var s source
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		line, msg := 1, err.Error()
		if list, ok := err.(scanner.ErrorList); ok && len(list) > 0 {
			line, msg = list[0].Pos.Line, list[0].Msg
		}
		s.problems = append(s.problems, doccheck.Problem{File: file, Line: line, Msg: "cannot parse the file as Go: " + msg})
		return s
	}

	for _, g := range f.Comments {
		for _, c := range g.List {
			pos := fset.Position(c.Pos())
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

	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !isGoTest(fn.Name.Name) {
			continue
		}
		s.decls = append(s.decls, decl{line: fset.Position(fn.Pos()).Line, name: fn.Name.Name})
		if fn.Body == nil {
			continue
		}
		// The t.Run calls the node being visited is in, innermost last.
		var stack []ast.Node
		var names []string
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if n == nil {
				if call, ok := stack[len(stack)-1].(*ast.CallExpr); ok && isRun(call) {
					names = names[:len(names)-1]
				}
				stack = stack[:len(stack)-1]
				return true
			}
			stack = append(stack, n)
			if call, ok := n.(*ast.CallExpr); ok && isRun(call) {
				names = append(names, subtestName(call.Args[0]))
				s.decls = append(s.decls, decl{
					line: fset.Position(call.Pos()).Line,
					name: fn.Name.Name + "/" + strings.Join(names, "/"),
				})
			}
			return true
		})
	}
	return s
}

// isRun reports whether a call starts a subtest: x.Run(name, f).
func isRun(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Run" && len(call.Args) == 2
}

// subtestName returns a subtest's name as go test prints it, which writes
// spaces as underscores, or "*" when the name is not a literal.
func subtestName(arg ast.Expr) string {
	lit, ok := arg.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "*"
	}
	name, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "*"
	}
	return strings.ReplaceAll(name, " ", "_")
}
