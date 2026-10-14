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
// Benchmark, and Fuzz functions and the subtests in them (t.Run or b.Run on a
// *testing.T or *testing.B parameter), named as go test names them: a
// subtest without a literal name is "*".
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
		// The names of the *testing.T and *testing.B parameters of the test
		// and of the functions in it, whose Run calls start subtests.
		params := map[string]bool{}
		addTestingParams(fn.Type, params)
		isRun := func(call *ast.CallExpr) bool {
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Run" || len(call.Args) != 2 {
				return false
			}
			recv, ok := sel.X.(*ast.Ident)
			return ok && params[recv.Name]
		}
		// For each node being visited, innermost last, whether it is a t.Run
		// call, and the names of those calls.
		var runs []bool
		var names []string
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if n == nil {
				if runs[len(runs)-1] {
					names = names[:len(names)-1]
				}
				runs = runs[:len(runs)-1]
				return true
			}
			if lit, ok := n.(*ast.FuncLit); ok {
				addTestingParams(lit.Type, params)
			}
			call, ok := n.(*ast.CallExpr)
			run := ok && isRun(call)
			runs = append(runs, run)
			if run {
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

// addTestingParams adds to names the parameters of a function that are a
// *testing.T or a *testing.B, whose Run method starts a subtest. A Run call
// on anything else, such as app.Run(ctx, args), is not a test.
func addTestingParams(ft *ast.FuncType, names map[string]bool) {
	for _, f := range ft.Params.List {
		star, ok := f.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		sel, ok := star.X.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "T" && sel.Sel.Name != "B") {
			continue
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "testing" {
			continue
		}
		for _, n := range f.Names {
			names[n.Name] = true
		}
	}
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
