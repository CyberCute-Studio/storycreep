// Package extract is catalog extraction (PRD FR-I18N-01 AC3): it reads the
// message keys that the source code uses, and keeps each message catalog to
// exactly those keys, in a form that depends on nothing else, so that running
// it again changes nothing.
//
// The source locale's catalog, en.json, gains an empty message for a key that
// the code uses and it lacks, which extraction then reports until someone
// writes the message. Every catalog loses the keys that the code no longer
// uses. Keys are sorted, and the file is JSON indented by two spaces, with a
// newline at the end.
//
// Code names a key in a way that extraction can read without running it:
//
//   - Go calls i18n.K("key") with a string literal (package i18n);
//   - TypeScript and JavaScript call t("key") or x.t("key"), as i18next's t
//     is called, or set the i18nKey attribute of react-i18next's Trans, with a
//     string literal, as in i18nKey="key".
//
// Extraction reports any other call of i18n.K or t, such as one with a
// variable, so that no key the code uses can escape it.
package extract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing/fstest"

	"github.com/CyberCute-Studio/storycreep/server/internal/doccheck"
	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/jslex"
)

// Catalog is a set of catalogs, one JSON file per locale in a directory, and
// the source code whose keys they hold.
type Catalog struct {
	// Dir is the catalogs' directory, relative to the repository's root,
	// with slashes.
	Dir string
	// Sources are the directories whose code is read, and Skip those of
	// them that are not, such as the package that implements t itself.
	Sources, Skip []string
	// Go is set for a catalog of Go code, and unset for one of TypeScript
	// and JavaScript.
	Go bool
	// Validate checks the catalogs' messages, given the files as they would
	// be written, and returns the problems, or nil.
	Validate func(fsys fs.FS) error
}

// Catalogs are the repository's catalogs: the server's, and the web app's
// and public site's, which share one.
var Catalogs = []Catalog{
	{
		Dir:     "server/internal/i18n/catalog",
		Sources: []string{"server"},
		Skip:    []string{"server/internal/i18n"},
		Go:      true,
		Validate: func(fsys fs.FS) error {
			_, err := i18n.Load(fsys)
			return err
		},
	},
	{
		// The messages are parsed by a test of web/shared, with the parser
		// that the web app formats them with.
		Dir:     "web/shared/i18n",
		Sources: []string{"web"},
		Skip:    []string{"web/shared/src/i18n"},
	},
}

// Result is what Run found.
type Result struct {
	// Changed are the catalog files that Run rewrote or, when it did not
	// write, would rewrite, relative to the root, with slashes.
	Changed []string
	// Problems are keys that extraction cannot read, and catalogs whose
	// messages are invalid or missing.
	Problems []doccheck.Problem
}

// Run extracts the keys of each catalog from root, and writes the catalogs
// that change if write is set. An error means a file could not be read or
// written.
func Run(root string, catalogs []Catalog, write bool) (Result, error) {
	var res Result
	for _, c := range catalogs {
		if err := c.run(root, write, &res); err != nil {
			return res, err
		}
	}
	return res, nil
}

// sourceFile is the source locale's catalog.
var sourceFile = i18n.Source.String() + ".json"

// use is where a key is used.
type use struct {
	file string
	line int
}

func (c Catalog) run(root string, write bool, res *Result) error {
	uses := map[string][]use{}
	for _, dir := range c.Sources {
		if err := c.scan(root, dir, uses, &res.Problems); err != nil {
			return err
		}
	}
	files, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(c.Dir), "*.json"))
	if err != nil {
		return err
	}
	old := map[string][]byte{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		old[filepath.Base(f)] = b
	}
	if _, ok := old[sourceFile]; !ok {
		old[sourceFile] = nil // written for the first time
	}

	written := fstest.MapFS{}
	for _, name := range slices.Sorted(maps.Keys(old)) {
		path := c.Dir + "/" + name
		messages := map[string]string{}
		if old[name] != nil {
			if messages, err = i18n.ReadCatalog(old[name]); err != nil {
				res.Problems = append(res.Problems, doccheck.Problem{File: path, Line: 1, Msg: err.Error()})
				continue
			}
		}
		kept := map[string]string{}
		for _, key := range slices.Sorted(maps.Keys(uses)) {
			msg, ok := messages[key]
			if name == sourceFile {
				kept[key] = msg
				if msg == "" {
					u := uses[key][0]
					res.Problems = append(res.Problems, doccheck.Problem{File: u.file, Line: u.line,
						Msg: fmt.Sprintf("the key %s has no message in %s; write it there", key, path)})
				}
			} else if ok {
				kept[key] = msg
			}
		}
		b := encode(kept)
		written[name] = &fstest.MapFile{Data: b}
		// A checkout made without .gitattributes may have CRLF.
		if bytes.Equal(bytes.ReplaceAll(old[name], []byte("\r\n"), []byte("\n")), b) {
			continue
		}
		res.Changed = append(res.Changed, path)
		if write {
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(path)), b, 0o644); err != nil {
				return err
			}
		}
	}
	if c.Validate != nil {
		if err := c.Validate(written); err != nil {
			for _, e := range unjoin(err) {
				// A catalog's problem starts with its file's name, and then
				// names the key.
				p := doccheck.Problem{File: c.Dir, Line: 1, Msg: e.Error()}
				if name, msg, ok := strings.Cut(p.Msg, ": "); ok && strings.HasSuffix(name, ".json") {
					p.File, p.Msg = c.Dir+"/"+name, msg
				}
				res.Problems = append(res.Problems, p)
			}
		}
	}
	return nil
}

// unjoin returns the errors that errors.Join joined into err, or err alone.
func unjoin(err error) []error {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		return j.Unwrap()
	}
	return []error{err}
}

// encode returns a catalog's file: its messages by key, sorted, as JSON
// indented by two spaces, with a newline at the end. Characters such as < and
// & are written as they are.
func encode(messages map[string]string) []byte {
	if len(messages) == 0 {
		return []byte("{}\n")
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	// A map's keys are encoded sorted, and a map of strings cannot fail.
	_ = enc.Encode(messages)
	return b.Bytes()
}

// keyRE is the form of a key: names of letters, digits, underscores, and
// hyphens, separated by dots, as error.not_found.
var keyRE = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$`)

// scan reads the keys of the code in dir, relative to root, into uses.
func (c Catalog) scan(root, dir string, uses map[string][]use, problems *[]doccheck.Problem) error {
	return filepath.WalkDir(filepath.Join(root, filepath.FromSlash(dir)), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		name := d.Name()
		if d.IsDir() {
			if rel != dir && (skipDir(name) || slices.Contains(c.Skip, rel)) {
				return filepath.SkipDir
			}
			return nil
		}
		var read func(file string, src []byte) ([]use, []string, []doccheck.Problem)
		switch {
		case c.Go && goSource(name):
			read = readGo
		case !c.Go && jsSource(name):
			read = readJS
		default:
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		found, keys, ps := read(rel, src)
		*problems = append(*problems, ps...)
		for i, key := range keys {
			if !keyRE.MatchString(key) {
				*problems = append(*problems, doccheck.Problem{File: found[i].file, Line: found[i].line,
					Msg: fmt.Sprintf("the key %q is not names of letters, digits, _, and -, separated by dots", key)})
				continue
			}
			uses[key] = append(uses[key], found[i])
		}
		return nil
	})
}

// skipDir reports whether a directory is skipped: one of dependencies, of
// build output, of test data, or a hidden one, or one whose Go files the go
// tool ignores.
func skipDir(name string) bool {
	switch name {
	case "node_modules", "dist", "coverage", "testdata", "vendor":
		return true
	}
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// goSource reports whether a Go file is part of a program: not a test.
func goSource(name string) bool {
	return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") &&
		!strings.HasPrefix(name, "_") && !strings.HasPrefix(name, ".")
}

var (
	jsSourceRE = regexp.MustCompile(`\.[cm]?[jt]sx?$`)
	// jsNotSourceRE are the files that are not the app's code: tests,
	// benchmarks, type declarations, and generated code.
	jsNotSourceRE = regexp.MustCompile(`\.(?:test|spec|bench|benchmark)\.[cm]?[jt]sx?$|\.d\.[cm]?ts$|\.gen\.[cm]?[jt]sx?$`)
)

func jsSource(name string) bool {
	return jsSourceRE.MatchString(name) && !jsNotSourceRE.MatchString(name)
}

// i18nPath is the import path of package i18n, whose K names a key.
const i18nPath = "github.com/CyberCute-Studio/storycreep/server/internal/i18n"

// readGo returns the keys of the calls of i18n.K in a Go file, and where
// each is.
func readGo(file string, src []byte) (uses []use, keys []string, problems []doccheck.Problem) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, []doccheck.Problem{{File: file, Line: 1, Msg: "cannot parse the file: " + err.Error()}}
	}
	pkg := ""
	for _, imp := range f.Imports {
		if path, _ := strconv.Unquote(imp.Path.Value); path != i18nPath {
			continue
		}
		pkg = "i18n"
		if imp.Name != nil {
			pkg = imp.Name.Name
		}
	}
	line := func(n ast.Node) int { return fset.Position(n.Pos()).Line }
	switch pkg {
	case "", "_":
		return nil, nil, nil
	case ".":
		return nil, nil, []doccheck.Problem{{File: file, Line: 1,
			Msg: "package i18n is dot-imported, so extraction cannot tell its K from another"}}
	}
	isK := func(e ast.Expr) bool {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "K" {
			return false
		}
		id, ok := sel.X.(*ast.Ident)
		return ok && id.Name == pkg
	}
	called := map[ast.Expr]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if !isK(n.Fun) {
				return true
			}
			called[n.Fun] = true
			if lit, ok := literal(n); ok {
				uses = append(uses, use{file: file, line: line(n)})
				keys = append(keys, lit)
			} else {
				problems = append(problems, doccheck.Problem{File: file, Line: line(n),
					Msg: pkg + ".K is called with something other than one string literal, which extraction cannot read"})
			}
		case *ast.SelectorExpr:
			if isK(n) && !called[n] {
				problems = append(problems, doccheck.Problem{File: file, Line: line(n),
					Msg: pkg + ".K is used without being called, so extraction cannot read the keys it makes"})
			}
		}
		return true
	})
	return uses, keys, problems
}

// literal returns the value of the only argument of call, if it is a string
// literal.
func literal(call *ast.CallExpr) (string, bool) {
	if len(call.Args) != 1 || call.Ellipsis.IsValid() {
		return "", false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// readJS returns the keys of the calls of t, and of the i18nKey attributes,
// in a TypeScript or JavaScript file, and where each is.
func readJS(file string, src []byte) (uses []use, keys []string, problems []doccheck.Problem) {
	toks, _, unclosed := jslex.Lex(string(src))
	if unclosed > 0 {
		problems = append(problems, doccheck.Problem{File: file, Line: unclosed,
			Msg: "cannot find the end of the template literal or block comment that starts here, so extraction " +
				"cannot read the code after it"})
	}
	at := func(i int) jslex.Token {
		if i < len(toks) {
			return toks[i]
		}
		return jslex.Token{}
	}
	for i, tok := range toks {
		switch {
		case tok.Is(jslex.Ident, "t") && at(i+1).Is(jslex.Punct, "("):
			if i > 0 && toks[i-1].Is(jslex.Ident, "function") {
				continue // a declaration of t, not a call
			}
			if key := at(i + 2); key.Kind == jslex.String && (at(i+3).Is(jslex.Punct, ",") || at(i+3).Is(jslex.Punct, ")")) {
				uses = append(uses, use{file: file, line: key.Line})
				keys = append(keys, key.Text)
				continue
			}
			problems = append(problems, doccheck.Problem{File: file, Line: tok.Line,
				Msg: "t is called with a key that is not a string literal, which extraction cannot read"})
		case tok.Is(jslex.Ident, "i18nKey") && at(i+1).Is(jslex.Punct, "="):
			key, next := at(i+2), at(i+3)
			if at(i+2).Is(jslex.Punct, "{") && at(i+4).Is(jslex.Punct, "}") {
				key, next = at(i+3), jslex.Token{}
			}
			if key.Kind == jslex.String && !next.Is(jslex.Punct, "+") {
				uses = append(uses, use{file: file, line: key.Line})
				keys = append(keys, key.Text)
				continue
			}
			problems = append(problems, doccheck.Problem{File: file, Line: tok.Line,
				Msg: "i18nKey is set to something other than a string literal, which extraction cannot read"})
		}
	}
	return uses, keys, problems
}
