package config

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

// configDir is the one package, relative to the module root, that may read
// the environment and know the mode.
const configDir = "internal/config"

// envReaders are the functions that read the process environment, by the
// import path of their package.
var envReaders = map[string][]string{
	"os":                       {"Getenv", "LookupEnv", "Environ", "ExpandEnv"},
	"syscall":                  {"Getenv", "Environ"},
	"golang.org/x/sys/unix":    {"Getenv", "Environ"},
	"golang.org/x/sys/windows": {"Getenv", "Environ"},
}

type lintResult struct {
	// modeReaders are the package directories whose code names
	// STORYCREEP_MODE.
	modeReaders []string
	violations  []string
	files       int
}

// lintModeIsolation checks the non-test Go files of the module at root for
// code outside configDir that names STORYCREEP_MODE, compares against a mode
// value, or reads the environment, which could read the mode under another
// spelling. Test files are exempt: they set the mode up but are not part of
// the binary.
func lintModeIsolation(root string) (lintResult, error) {
	var r lintResult
	readers := map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if p == root {
				return nil
			}
			if name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
				return filepath.SkipDir // another module
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
		rel = filepath.ToSlash(rel)
		dir := path.Dir(rel)
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		r.files++
		report := func(pos token.Pos, format string, args ...any) {
			if dir != configDir {
				r.violations = append(r.violations,
					fmt.Sprintf("%s:%d: %s", rel, fset.Position(pos).Line, fmt.Sprintf(format, args...)))
			}
		}

		imported := map[string]string{} // local name → import path
		for _, imp := range f.Imports {
			ip, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if _, ok := envReaders[ip]; !ok {
				continue
			}
			local := path.Base(ip)
			if imp.Name != nil {
				local = imp.Name.Name
			}
			switch local {
			case ".":
				report(imp.Pos(), "dot-imports %s, which would hide reads of the environment", ip)
			case "_":
			default:
				imported[local] = ip
			}
		}

		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.BasicLit:
				if n.Kind != token.STRING {
					break
				}
				s, err := strconv.Unquote(n.Value)
				if err != nil {
					break
				}
				if strings.Contains(s, "STORYCREEP_MODE") {
					readers[dir] = true
					report(n.Pos(), "names STORYCREEP_MODE, which only %s reads", configDir)
				}
				if s == string(modeHosted) || s == string(modeSelfHosted) {
					report(n.Pos(), "uses the mode value %q; branch on config.Capabilities instead", s)
				}
			case *ast.SelectorExpr:
				x, ok := n.X.(*ast.Ident)
				if !ok {
					break
				}
				if ip, ok := imported[x.Name]; ok && slices.Contains(envReaders[ip], n.Sel.Name) {
					report(n.Pos(), "reads the environment with %s.%s; only %s may", x.Name, n.Sel.Name, configDir)
				}
			}
			return true
		})
		return nil
	})
	for dir := range readers {
		r.modeReaders = append(r.modeReaders, dir)
	}
	slices.Sort(r.modeReaders)
	return r, err
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's directory")
		}
		dir = parent
	}
}

// TestModeIsReadInOnePackage covers FR-ADMIN-01/AC3: STORYCREEP_MODE is read
// in exactly one package, and no code outside it branches on the mode or
// reads the environment.
func TestModeIsReadInOnePackage(t *testing.T) {
	r, err := lintModeIsolation(moduleRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range r.violations {
		t.Error(v)
	}
	if !slices.Equal(r.modeReaders, []string{configDir}) {
		t.Errorf("STORYCREEP_MODE is named in %v, want exactly [%s]", r.modeReaders, configDir)
	}
	// Guard against a walk that silently finds nothing.
	if r.files < 10 {
		t.Errorf("the lint read only %d files", r.files)
	}
}

// TestModeLintFindsViolations runs the lint over a fixture module, so that
// TestModeIsReadInOnePackage cannot pass because the lint stopped working.
func TestModeLintFindsViolations(t *testing.T) {
	root := t.TempDir()
	files := map[string][]string{
		"go.mod": {"module example.com/m"},
		"internal/config/config.go": {
			"package config",
			`import "os"`,
			`var Mode = os.Getenv("STORYCREEP_MODE") == "hosted"`,
		},
		"internal/other/other.go": {
			"package other",
			"import (",
			`	"os"`,
			`	env "syscall"`,
			")",
			`var a = os.Getenv("HOME")`,
			"var b = env.Environ",
			`var c = "STORYCREEP_MODE"`,
			"var d = `prefix STORYCREEP_MODE suffix`",
			`var e = "self-hosted"`,
			"var f = os.Getpid()",
			`var g = "hosted mode"`,
		},
		"internal/other/dot.go": {
			"package other",
			`import . "os"`,
			"var h = Getpid()",
		},
		"internal/other/other_test.go": {
			"package other",
			`import "os"`,
			`var _ = os.Getenv("STORYCREEP_MODE") == "hosted"`,
		},
		"testdata/x.go":                 {"package x", `var _ = "hosted"`},
		"internal/other/_skipped.go":    {"package other", `var _ = "hosted"`},
		"nested/go.mod":                 {"module example.com/nested"},
		"nested/n.go":                   {"package nested", `var _ = "hosted"`},
		"internal/config/sub/notcfg.go": {"package sub", `var _ = "STORYCREEP_MODE"`},
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

	r, err := lintModeIsolation(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"internal/config/sub/notcfg.go:2: names STORYCREEP_MODE, which only internal/config reads",
		"internal/other/dot.go:2: dot-imports os, which would hide reads of the environment",
		"internal/other/other.go:6: reads the environment with os.Getenv; only internal/config may",
		"internal/other/other.go:7: reads the environment with env.Environ; only internal/config may",
		"internal/other/other.go:8: names STORYCREEP_MODE, which only internal/config reads",
		"internal/other/other.go:9: names STORYCREEP_MODE, which only internal/config reads",
		`internal/other/other.go:10: uses the mode value "self-hosted"; branch on config.Capabilities instead`,
	}
	slices.Sort(r.violations)
	slices.Sort(want)
	if !slices.Equal(r.violations, want) {
		t.Errorf("violations =\n%s\nwant\n%s", strings.Join(r.violations, "\n"), strings.Join(want, "\n"))
	}
	if wantReaders := []string{"internal/config", "internal/config/sub", "internal/other"}; !slices.Equal(r.modeReaders, wantReaders) {
		t.Errorf("modeReaders = %v, want %v", r.modeReaders, wantReaders)
	}
}
