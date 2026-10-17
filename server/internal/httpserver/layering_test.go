package httpserver

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The layers of a request (PRD FR-TEN-03 AC2): a handler decodes the request,
// calls a service, and encodes the answer. The service checks the role
// (package authz) and reads and writes the database, in one transaction with
// the audit entry of its change, so that no handler can skip a permission
// check by writing to the database itself.

// handlerDirs are the directories, relative to the module root, of the
// packages that serve the API, and of those below them: its handlers, and
// its toolkit.
var handlerDirs = []string{"internal/httpserver", "internal/api"}

// handlerRules are what a handler may not use.
type handlerRules struct {
	// imports are the packages, by their paths below the module, that a
	// handler may not import, nor a package below them: the repositories,
	// which read and write the database, and the matrix of permissions, which
	// only a service checks.
	imports []string
	// methods are the methods that a handler may not call, on whatever: those
	// that open a transaction of the database, reach its pool, write through
	// one, or enqueue a job in one, and the comparison of roles that a
	// permission check makes (role.Role.AtLeast).
	methods []string
	// functions are the functions of other packages that a handler may not
	// call, by the package's path below the module and the function's name:
	// writing an audit entry, which a service does in the transaction of its
	// change.
	functions map[string][]string
	// compared are the packages, by their paths below the module, whose
	// declarations a handler may not compare a value with, by == or !=, or
	// as a case of a switch on the value: the roles, which a permission check
	// compares (package role).
	compared []string
	// adapters are the declarations, as Type.Method, that may call methods
	// anyway: those that hand the database and the job queue to the services,
	// which they delegate to and no handler calls.
	adapters []string
}

// rules are the handlers' rules.
var rules = handlerRules{
	imports: []string{"internal/database/queries", "internal/authz"},
	methods: []string{"InTx", "Begin", "BeginTx", "BeginFunc", "Pool", "Exec", "SendBatch", "CopyFrom", "Enqueue",
		"AtLeast"},
	functions: map[string][]string{"internal/audit": {"Record"}},
	compared:  []string{"internal/role"},
	adapters:  []string{"readyDB.InTx", "queueRef.Enqueue"},
}

// lintHandlers reads the Go files of the packages in dirs, directories
// relative to the module at root, and below them, but not their tests, and
// reports each use of what rules forbids, and how many files it read.
func lintHandlers(root string, dirs []string, rules handlerRules) ([]string, int, error) {
	module, err := goModule(root)
	if err != nil {
		return nil, 0, err
	}
	var violations []string
	files := 0
	fset := token.NewFileSet()
	for _, dir := range dirs {
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(dir)), func(p string, d fs.DirEntry,
			err error) error {
			if err != nil {
				return err
			}
			name := d.Name()
			if d.IsDir() {
				if name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") ||
				strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			files++
			violations = append(violations, lintFile(fset, filepath.ToSlash(rel), f, module, rules)...)
			return nil
		})
		if err != nil {
			return nil, 0, err
		}
	}
	slices.Sort(violations)
	return violations, files, nil
}

// lintFile returns the uses of what rules forbids in f, the file rel of the
// module.
func lintFile(fset *token.FileSet, rel string, f *ast.File, module string, rules handlerRules) []string {
	var out []string
	report := func(pos token.Pos, format string, args ...any) {
		out = append(out, fmt.Sprintf("%s:%d: %s", rel, fset.Position(pos).Line, fmt.Sprintf(format, args...)))
	}
	// names maps the name of each package that f imports to its path below
	// the module.
	names := map[string]string{}
	for _, imp := range f.Imports {
		ip, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		below, ok := strings.CutPrefix(ip, module+"/")
		if !ok {
			continue
		}
		for _, forbidden := range rules.imports {
			if below == forbidden || strings.HasPrefix(below, forbidden+"/") {
				report(imp.Pos(), "imports %s", ip)
			}
		}
		name := path.Base(below)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		names[name] = below
	}
	// comparedName returns the name of e, as pkg.Name, if e is a declaration
	// of a package of rules.compared, and "" otherwise.
	comparedName := func(e ast.Expr) string {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok {
			return ""
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok || !slices.Contains(rules.compared, names[x.Name]) {
			return ""
		}
		return x.Name + "." + sel.Sel.Name
	}
	check := func(decl string, n ast.Node) {
		ast.Inspect(n, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.BinaryExpr:
				if n.Op == token.EQL || n.Op == token.NEQ {
					for _, operand := range []ast.Expr{n.X, n.Y} {
						if name := comparedName(operand); name != "" {
							report(operand.Pos(), "compares %s", name)
						}
					}
				}
				return true
			case *ast.SwitchStmt:
				if n.Tag == nil {
					return true
				}
				for _, stmt := range n.Body.List {
					clause, ok := stmt.(*ast.CaseClause)
					if !ok {
						continue
					}
					for _, e := range clause.List {
						if name := comparedName(e); name != "" {
							report(e.Pos(), "compares %s", name)
						}
					}
				}
				return true
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok {
				if pkg, ok := names[x.Name]; ok {
					if slices.Contains(rules.functions[pkg], sel.Sel.Name) {
						report(call.Pos(), "calls %s.%s", x.Name, sel.Sel.Name)
					}
					return true
				}
			}
			if slices.Contains(rules.methods, sel.Sel.Name) && !slices.Contains(rules.adapters, decl) {
				report(call.Pos(), "calls %s", sel.Sel.Name)
			}
			return true
		})
	}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			check("", d)
			continue
		}
		check(declName(fn), fn)
	}
	return out
}

// declName returns the name of a function's declaration, as Type.Method for
// a method.
func declName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	t := fn.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if index, ok := t.(*ast.IndexExpr); ok {
		t = index.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// goModule returns the module path that root's go.mod declares.
func goModule(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for line := range strings.Lines(string(b)) {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`), nil
		}
	}
	return "", fmt.Errorf("%s/go.mod declares no module", root)
}

// TestHandlersUseServices checks that no handler writes to a repository
// directly, nor checks a permission itself: the packages that serve the API
// neither import the repositories or the matrix of permissions, nor open a
// transaction, reach the pool, write, enqueue a job, compare roles, or write
// an audit entry, but in the adapters that hand the database and the queue
// to the services (rules), so that every operation is authorised in the
// service that does it.
//
// Verifies: FR-TEN-03/AC2
func TestHandlersUseServices(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	violations, files, err := lintHandlers(root, handlerDirs, rules)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range violations {
		t.Errorf("%s; a handler calls a service, which authorises the operation and writes (PRD FR-TEN-03 AC2)", v)
	}
	// Guard against a walk that silently finds nothing.
	if files < 20 {
		t.Errorf("the lint read only %d files", files)
	}
}

// TestHandlerLintFindsWrites runs the lint over a fixture module, so that
// TestHandlersUseServices cannot pass because the lint stopped working.
func TestHandlerLintFindsWrites(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := map[string][]string{
		"go.mod": {"module example.com/m"},
		"internal/httpserver/adapter.go": {"package httpserver",
			"func (d readyDB) InTx() error { return d.db.InTx() }",
			"func (r *queueRef) Enqueue() { r.q.Enqueue() }"},
		"internal/httpserver/handler.go": {"package httpserver",
			`import (`,
			`	"example.com/m/internal/audit"`,
			`	q "example.com/m/internal/database/queries"`,
			`	"example.com/m/internal/authz"`,
			`	"example.com/m/internal/database/queriesx"`,
			`)`,
			"func handle(db DB, tx Tx, r Role) {",
			"	db.InTx(nil)",
			"	tx.Exec(nil)",
			"	audit.Record(nil)",
			"	r.AtLeast(r)",
			"	_ = q.New(tx)",
			"	audit.Client{}.String()",
			"	readyDB{}.InTx()",
			"}",
			"var _ = func() { pool.Pool() }"},
		"internal/httpserver/roles.go": {"package httpserver",
			`import r "example.com/m/internal/role"`,
			"func check(m r.Role, v any) {",
			"	if m == r.Owner || r.Admin != m || m == \"viewer\" {",
			"	}",
			"	switch m {",
			"	case r.Editor, \"guest\":",
			"	}",
			"	switch v.(type) {",
			"	case r.Role:",
			"	}",
			"	use(r.Owner, r.Role(\"x\"), m.String() == \"x\")",
			"}"},
		"internal/httpserver/handler_test.go": {"package httpserver",
			`import _ "example.com/m/internal/database/queries"`, "func f(tx Tx) { tx.Exec(nil) }"},
		"internal/httpserver/sub/sub.go":       {"package sub", "func f(tx Tx) { tx.Begin() }"},
		"internal/httpserver/testdata/x.go":    {"package x", "func f(tx Tx) { tx.Exec(nil) }"},
		"internal/api/api.go":                  {"package api", "func f[T any](s *S[T]) { s.Enqueue() }"},
		"internal/api/generic.go":              {"package api", "func (s *S[T]) Exec() { s.tx.Exec() }"},
		"internal/workspace/service.go":        {"package workspace", "func f(tx Tx) { tx.Exec(nil) }"},
		"internal/httpserver/_skipped/skip.go": {"package skip", "func f(tx Tx) { tx.Exec(nil) }"},
	}
	for name, lines := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, n, err := lintHandlers(root, handlerDirs, rules)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"internal/api/api.go:2: calls Enqueue",
		"internal/api/generic.go:2: calls Exec",
		"internal/httpserver/handler.go:10: calls Exec",
		"internal/httpserver/handler.go:11: calls audit.Record",
		"internal/httpserver/handler.go:12: calls AtLeast",
		"internal/httpserver/handler.go:15: calls InTx",
		"internal/httpserver/handler.go:17: calls Pool",
		"internal/httpserver/handler.go:4: imports example.com/m/internal/database/queries",
		"internal/httpserver/handler.go:5: imports example.com/m/internal/authz",
		"internal/httpserver/handler.go:9: calls InTx",
		"internal/httpserver/roles.go:4: compares r.Admin",
		"internal/httpserver/roles.go:4: compares r.Owner",
		"internal/httpserver/roles.go:7: compares r.Editor",
		"internal/httpserver/sub/sub.go:2: calls Begin",
	}
	if !slices.Equal(got, want) {
		t.Errorf("violations =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// The adapters, the handlers, the roles, sub, api, and generic; not the
	// tests, testdata, _skipped, or another package's files.
	if n != 6 {
		t.Errorf("the lint read %d files, want 6", n)
	}
}
