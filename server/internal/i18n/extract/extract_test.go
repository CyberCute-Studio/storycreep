package extract

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeTree writes files, by path relative to root with slashes, into root.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readFile(t *testing.T, root, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// fixture is a repository with a Go server and a web app that use keys, and
// catalogs that are out of date: the source catalogs lack keys that the code
// uses, and hold keys that it no longer uses.
var fixture = map[string]string{
	"server/cmd/x/main.go": `package main

import "github.com/CyberCute-Studio/storycreep/server/internal/i18n"

var (
	b = i18n.K("b.key")
	a = i18n.K("a.key")
)
`,
	"server/internal/y/y.go": `package y

import msg "github.com/CyberCute-Studio/storycreep/server/internal/i18n"

var c = msg.K(` + "`c.key`" + `)
`,
	"server/internal/y/y_test.go": `package y

import "github.com/CyberCute-Studio/storycreep/server/internal/i18n"

var test = i18n.K("only.in.a.test")
`,
	"server/internal/y/testdata/z.go": `package z

import "github.com/CyberCute-Studio/storycreep/server/internal/i18n"

var z = i18n.K("only.in.test.data")
`,
	"server/internal/i18n/catalog/en.json": `{"unused": "Unused.", "a.key": "A & <b>"}`,
	"server/internal/i18n/catalog/cs.json": "{\r\n  \"a.key\": \"Á\",\r\n  \"unused\": \"Nepoužito.\",\r\n  \"b.key\": \"B\"\r\n}\r\n",
	"web/app/src/board.tsx": `export function Board() {
  const { t } = useTranslation();
  return (
    <section>
      <h1>{t("board.title")}</h1>
      <p>{i18n.t('board.count', { count: 2 })}</p>
      <Trans i18nKey="board.hint" />
      <Trans i18nKey={"board.help"} />
    </section>
  );
}
`,
	"web/app/src/board.test.tsx":   `test("renders", () => t("only.in.a.test"));`,
	"web/shared/src/i18n/index.ts": `export function translate(key: string) { return i18n.t(key); }`,
	"web/shared/i18n/en.json":      `{"board.title": "Board"}`,
}

// TestExtract checks that extraction keeps each catalog to exactly the keys
// that the code uses, sorted, and that running it again changes nothing.
//
// Verifies: FR-I18N-01/AC3
func TestExtract(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, fixture)
	catalogs := slices.Clone(Catalogs)

	// A check reports the catalogs to rewrite, and rewrites nothing.
	res, err := Run(root, catalogs, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"server/internal/i18n/catalog/cs.json", "server/internal/i18n/catalog/en.json", "web/shared/i18n/en.json"}
	if !slices.Equal(res.Changed, want) {
		t.Errorf("Changed = %v, want %v", res.Changed, want)
	}
	if got := readFile(t, root, "web/shared/i18n/en.json"); got != fixture["web/shared/i18n/en.json"] {
		t.Errorf("a check rewrote web/shared/i18n/en.json: %s", got)
	}

	if _, err := Run(root, catalogs, true); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		// The source catalog gains an empty message for each key it lacks,
		// and keeps its messages as they are written.
		"server/internal/i18n/catalog/en.json": "{\n  \"a.key\": \"A & <b>\",\n  \"b.key\": \"\",\n  \"c.key\": \"\"\n}\n",
		// Another catalog keeps only the keys the code uses, and gains none.
		"server/internal/i18n/catalog/cs.json": "{\n  \"a.key\": \"Á\",\n  \"b.key\": \"B\"\n}\n",
		"web/shared/i18n/en.json": "{\n  \"board.count\": \"\",\n  \"board.help\": \"\",\n  \"board.hint\": \"\",\n" +
			"  \"board.title\": \"Board\"\n}\n",
	} {
		if got := readFile(t, root, name); got != want {
			t.Errorf("%s =\n%s\nwant\n%s", name, got, want)
		}
	}

	// Nothing changed, so running it again rewrites nothing, and reports
	// the same problems: the keys whose source message is empty.
	again, err := Run(root, catalogs, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Changed) != 0 {
		t.Errorf("a second run would rewrite %v", again.Changed)
	}
	var problems []string
	for _, p := range again.Problems {
		problems = append(problems, p.String())
	}
	wantProblems := []string{
		"server/cmd/x/main.go:6: the key b.key has no message in server/internal/i18n/catalog/en.json; write it there",
		"server/internal/y/y.go:5: the key c.key has no message in server/internal/i18n/catalog/en.json; write it there",
		"server/internal/i18n/catalog/en.json:1: b.key: the message is empty",
		"server/internal/i18n/catalog/en.json:1: c.key: the message is empty",
		"web/app/src/board.tsx:6: the key board.count has no message in web/shared/i18n/en.json; write it there",
		"web/app/src/board.tsx:8: the key board.help has no message in web/shared/i18n/en.json; write it there",
		"web/app/src/board.tsx:7: the key board.hint has no message in web/shared/i18n/en.json; write it there",
	}
	if !slices.Equal(problems, wantProblems) {
		t.Errorf("problems:\n%s\nwant:\n%s", strings.Join(problems, "\n"), strings.Join(wantProblems, "\n"))
	}
}

func TestReadGo(t *testing.T) {
	src := `package x

import (
	"fmt"

	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
)

var name = "x"

var (
	ok       = i18n.K("ok.key")
	raw      = i18n.K(` + "`raw.key`" + `)
	variable = i18n.K(name)
	joined   = i18n.K("a." + "b")
	two      = i18n.K("a", "b")
	value    = i18n.K
	other    = fmt.Sprint("not.a.key")
)
`
	uses, keys, problems := readGo("x.go", []byte(src))
	if want := []string{"ok.key", "raw.key"}; !slices.Equal(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
	if len(uses) != 2 || uses[0] != (use{"x.go", 12}) {
		t.Errorf("uses = %v", uses)
	}
	var lines []int
	for _, p := range problems {
		lines = append(lines, p.Line)
	}
	if want := []int{14, 15, 16, 17}; !slices.Equal(lines, want) {
		t.Errorf("problems on lines %v, want %v: %v", lines, want, problems)
	}

	if _, _, problems := readGo("dot.go", []byte(`package x

import . "github.com/CyberCute-Studio/storycreep/server/internal/i18n"

var k = K("a")
`)); len(problems) != 1 || !strings.Contains(problems[0].Msg, "dot-imported") {
		t.Errorf("a dot import: %v", problems)
	}
	if _, _, problems := readGo("bad.go", []byte("package x\nfunc (")); len(problems) != 1 {
		t.Errorf("a file that does not parse: %v", problems)
	}
	if _, keys, problems := readGo("none.go", []byte("package x\n\nvar K = 1\n")); keys != nil || problems != nil {
		t.Errorf("a file without package i18n: %v, %v", keys, problems)
	}
}

func TestReadJS(t *testing.T) {
	src := "const a = t(\"a.key\");\n" + // 1
		"const b = i18next.t('b.key', { count: 2 });\n" + // 2
		"const c = t(`c.key`);\n" + // 3
		"const d = t(key);\n" + // 4
		"const e = t(`e.${kind}`);\n" + // 5
		"const f = t(\"f.\" + kind);\n" + // 6
		"const g = t();\n" + // 7
		"function t(key) { return key; }\n" + // 8: a declaration
		"const h = { t: (key) => key };\n" + // 9: a property, not a call
		"const i = <Trans i18nKey=\"i.key\" />;\n" + // 10
		"const j = <Trans i18nKey={\"j.key\"} />;\n" + // 11
		"const k = <Trans i18nKey={key} />;\n" + // 12
		"// t(\"in.a.comment\")\n" + // 13
		"const l = \"t('in.a.string')\";\n" // 14
	uses, keys, problems := readJS("x.tsx", []byte(src))
	if want := []string{"a.key", "b.key", "c.key", "i.key", "j.key"}; !slices.Equal(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
	if want := []use{{"x.tsx", 1}, {"x.tsx", 2}, {"x.tsx", 3}, {"x.tsx", 10}, {"x.tsx", 11}}; !slices.Equal(uses, want) {
		t.Errorf("uses = %v, want %v", uses, want)
	}
	var lines []int
	for _, p := range problems {
		lines = append(lines, p.Line)
	}
	if want := []int{4, 5, 6, 7, 12}; !slices.Equal(lines, want) {
		t.Errorf("problems on lines %v, want %v: %v", lines, want, problems)
	}
}

func TestKeyForm(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"web/app/src/x.ts":        `t("not a key!"); t("fine.key-2_b");`,
		"web/shared/i18n/en.json": `{"fine.key-2_b": "Fine."}`,
	})
	res, err := Run(root, Catalogs[1:], false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) != 1 || !strings.Contains(res.Problems[0].Msg, `"not a key!"`) {
		t.Errorf("problems = %v, want one about the key", res.Problems)
	}
	if len(res.Changed) != 1 {
		t.Errorf("Changed = %v, want the catalog reformatted", res.Changed)
	}
}

func TestInvalidCatalog(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"web/shared/i18n/en.json": `{"a": "x", "a": "y"}`,
		"web/shared/i18n/cs.json": `not json`,
	})
	res, err := Run(root, Catalogs[1:], true)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) != 2 || len(res.Changed) != 0 {
		t.Errorf("Run = %+v, want a problem with each catalog, and neither rewritten", res)
	}
	if got := readFile(t, root, "web/shared/i18n/cs.json"); got != "not json" {
		t.Errorf("an invalid catalog was rewritten: %q", got)
	}
}

// TestCatalogs checks that the repository's catalogs are as extraction
// writes them, and that the server's source catalog has a message for every
// key that the code uses.
//
// Verifies: FR-I18N-01/AC3
func TestCatalogs(t *testing.T) {
	res, err := Run(filepath.Join("..", "..", "..", ".."), Catalogs, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range res.Problems {
		t.Error(p)
	}
	for _, path := range res.Changed {
		t.Errorf("%s is out of date; run pnpm i18n:extract", path)
	}
}
