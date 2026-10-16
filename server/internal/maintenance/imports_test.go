package maintenance

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

// packageDir is this package's directory, relative to the module root.
const packageDir = "internal/maintenance"

// allowedImporters are the package directories, relative to the module root,
// that may import this package besides its own: those of the closed list of
// callers that PRD §5.4 names, once one of them needs to call into this
// package rather than being written in it. Adding one adds a caller of the
// maintenance path, which the PRD must name first.
var allowedImporters []string

// guardedNames are the names, besides this package, through which code could
// reach the maintenance path without importing it, by the package
// directories, relative to the module root, that may use them: the
// configuration's field that holds the maintenance login's URL, which the
// configuration, the server's startup, and storycreep migrate read to
// provision the login, and package database's helpers, which migrating uses
// to check the login and this package to act as storycreep_maintenance. A
// package that used them could open a pool that bypasses row-level security
// of its own.
var guardedNames = map[string][]string{
	"DatabaseMaintenanceURL": {"internal/config", "internal/database", "internal/cli"},
	"CheckMaintenanceLogin":  {"internal/database"},
	"SetMaintenanceRole":     {"internal/database"},
}

// lintResult is what lintAccess found.
type lintResult struct {
	violations []string
	files      int
}

// lintAccess reads every Go file of the module at root, its tests included,
// and reports each import of the package in pkg, a directory relative to
// root, or of a package below it, from a file outside them whose directory is
// not in allowed, and each identifier of guarded, a name of a field, a
// function, or anything else that a file declares or uses, in a file outside
// them whose directory is not among those that guarded lists for the name.
func lintAccess(root, pkg string, allowed []string, guarded map[string][]string) (lintResult, error) {
	var r lintResult
	module, err := modulePath(root)
	if err != nil {
		return r, err
	}
	target := module + "/" + pkg
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
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
		if !strings.HasSuffix(name, ".go") || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
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
		if dir == pkg || strings.HasPrefix(dir, pkg+"/") {
			return nil
		}
		if !slices.Contains(allowed, dir) {
			for _, imp := range f.Imports {
				ip, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return err
				}
				if ip == target || strings.HasPrefix(ip, target+"/") {
					r.violations = append(r.violations, fmt.Sprintf("%s:%d: imports %s", rel, fset.Position(imp.Pos()).Line, ip))
				}
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				if dirs, ok := guarded[id.Name]; ok && !slices.Contains(dirs, dir) {
					r.violations = append(r.violations, fmt.Sprintf("%s:%d: uses %s", rel, fset.Position(id.Pos()).Line,
						id.Name))
				}
			}
			return true
		})
		return nil
	})
	slices.Sort(r.violations)
	return r, err
}

// modulePath returns the module path that root's go.mod declares.
func modulePath(root string) (string, error) {
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

// TestOnlyMaintenanceImportsIt checks that no package but this one, its
// tests included, imports this package, so that only it can obtain the pool
// that bypasses row-level security (PRD §5.4), and that no package uses the
// names through which it could open such a pool of its own (guardedNames).
func TestOnlyMaintenanceImportsIt(t *testing.T) {
	r, err := lintAccess(moduleRoot(t), packageDir, allowedImporters, guardedNames)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range r.violations {
		t.Errorf("%s; only %s may use the maintenance path (PRD §5.4)", v, packageDir)
	}
	// Guard against a walk that silently finds nothing.
	if r.files < 10 {
		t.Errorf("the lint read only %d files", r.files)
	}
}

// TestImportLintFindsImporters runs the lint over a fixture module, so that
// TestOnlyMaintenanceImportsIt cannot pass because the lint stopped working.
func TestImportLintFindsImporters(t *testing.T) {
	root := t.TempDir()
	files := map[string][]string{
		"go.mod":                                    {"module example.com/m"},
		"internal/maintenance/maintenance.go":       {"package maintenance", "var _ = cfg.Guarded"},
		"internal/maintenance/sub/sub.go":           {"package sub", `import _ "example.com/m/internal/maintenance"`},
		"internal/maintenance/maintenance_test.go":  {"package maintenance_test", `import _ "example.com/m/internal/maintenance"`},
		"internal/api/api.go":                       {"package api", `import _ "example.com/m/internal/maintenance"`},
		"internal/api/api_test.go":                  {"package api", "", `import m "example.com/m/internal/maintenance/sub"`},
		"internal/maintenancefake/fake.go":          {"package maintenancefake", `import _ "example.com/m/internal/maintenancefake"`},
		"internal/other/other.go":                   {"package other", `import _ "example.com/m/internal/maintenancex"`},
		"internal/other/testdata/x.go":              {"package x", `import _ "example.com/m/internal/maintenance"`},
		"internal/other/_skipped.go":                {"package other", `import _ "example.com/m/internal/maintenance"`},
		"nested/go.mod":                             {"module example.com/nested"},
		"nested/n.go":                               {"package nested", `import _ "example.com/m/internal/maintenance"`},
		"internal/database/dbtest/dbtest.go":        {"package dbtest", `import _ "example.com/m/internal/maintenance"`},
		"internal/maintenance/sub/deeper/deeper.go": {"package deeper"},
		"internal/config/config.go":                 {"package config", "type Config struct{ Guarded string }"},
		"internal/jobs/jobs.go":                     {"package jobs", "", "", "var _ = Config{Guarded: GuardedToo}"},
		"internal/importer/importer.go": {"package importer", `import _ "example.com/m/internal/maintenance"`,
			"var _ = cfg.Guarded"},
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

	r, err := lintAccess(root, packageDir, []string{"internal/importer"},
		map[string][]string{"Guarded": {"internal/config"}, "GuardedToo": nil})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"internal/api/api.go:2: imports example.com/m/internal/maintenance",
		"internal/api/api_test.go:3: imports example.com/m/internal/maintenance/sub",
		"internal/database/dbtest/dbtest.go:2: imports example.com/m/internal/maintenance",
		"internal/importer/importer.go:3: uses Guarded",
		"internal/jobs/jobs.go:4: uses Guarded",
		"internal/jobs/jobs.go:4: uses GuardedToo",
	}
	if !slices.Equal(r.violations, want) {
		t.Errorf("violations =\n%s\nwant\n%s", strings.Join(r.violations, "\n"), strings.Join(want, "\n"))
	}
}
