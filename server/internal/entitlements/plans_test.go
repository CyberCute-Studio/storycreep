package entitlements

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestPlanFile checks that plans.json loads, so that Default never panics,
// and that it is the file that the server reads: the plans that the server
// embeds are those of the file on disk, which the tests read too.
//
// Verifies: FR-ENT-01/AC3
func TestPlanFile(t *testing.T) {
	onDisk, err := os.ReadFile("plans.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(onDisk, planFile) {
		t.Fatalf("the embedded plan file differs from plans.json")
	}
	f, err := Load(onDisk)
	if err != nil {
		t.Fatalf("Load(plans.json): %v", err)
	}
	for _, p := range f.Plans() {
		d, ok := Default().Plan(p.Key)
		if !ok {
			t.Fatalf("Default has no plan %s", p.Key)
		}
		for _, l := range f.Limits() {
			if d.Quota(l.Key) != p.Quota(l.Key) {
				t.Errorf("%s: Default's %s is %s, the file's %s", p.Key, l.Key, d.Quota(l.Key), p.Quota(l.Key))
			}
		}
	}
}

// TestOnePlanFile checks that no code but the loader reads a plan file, so
// that plans.json is the one place where the plans live: no other Go file of
// the server embeds or opens a file named like it, outside the tests.
//
// Verifies: FR-ENT-01/AC3
func TestOnePlanFile(t *testing.T) {
	root := filepath.Join("..", "..")
	// The loader embeds the file, and names it when it cannot load it.
	loader := filepath.Join("internal", "entitlements", "plans.go")
	fset := token.NewFileSet()
	var files int
	var sawLoader bool
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// The root's name, "..", starts with a dot too, but it is no hidden
		// directory.
		if d.IsDir() && path != root && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == loader {
			sawLoader = true
			return nil
		}
		files++
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		mentions := func(s string) bool { return strings.Contains(s, "plans.json") }
		ast.Inspect(file, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && mentions(lit.Value) {
				t.Errorf("%s: names plans.json", fset.Position(lit.Pos()))
			}
			return true
		})
		for _, group := range file.Comments {
			for _, c := range group.List {
				if strings.HasPrefix(c.Text, "//go:embed") && mentions(c.Text) {
					t.Errorf("%s: embeds plans.json", fset.Position(c.Pos()))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A walk that skipped the server's code would pass without a word.
	if !sawLoader || files == 0 {
		t.Errorf("the walk found the loader %v, and %d other Go files; want the loader and the server's code",
			sawLoader, files)
	}
}

// TestPlanFileShape checks the settled shape of ADR-010: the plans
// unlimited, free, team, and studio; members, which hosted mode bills by,
// projects, storage_bytes, and snapshot_retention as limits, with
// snapshot_retention inert until FR-SNAP-06, which enforces it; and
// collaboration, sso, and audit_export as feature flags. It names no number
// of a plan, which are placeholders.
//
// Verifies: FR-ENT-01/AC4
func TestPlanFileShape(t *testing.T) {
	f := Default()
	var plans []string
	for _, p := range f.Plans() {
		plans = append(plans, p.Key)
	}
	if want := []string{"unlimited", "free", "team", "studio"}; !slices.Equal(plans, want) {
		t.Errorf("plans = %v, want %v", plans, want)
	}
	want := []Limit{
		{Key: "members", Billable: true},
		{Key: "projects"},
		{Key: "storage_bytes"},
		{Key: "snapshot_retention", InertUntil: "FR-SNAP-06"},
	}
	if got := f.Limits(); !slices.Equal(got, want) {
		t.Errorf("limits = %+v, want %+v", got, want)
	}
	if got, want := f.Features(), []Feature{"collaboration", "sso", "audit_export"}; !slices.Equal(got, want) {
		t.Errorf("features = %v, want %v", got, want)
	}
	// The unlimited plan is self-hosted mode's: it limits nothing, and has
	// every feature.
	unlimited, _ := f.Plan("unlimited")
	for _, l := range f.Limits() {
		if unlimited.Quota(l.Key) != NoLimit {
			t.Errorf("the unlimited plan limits %s", l.Key)
		}
	}
	for _, feature := range f.Features() {
		if !unlimited.Has(feature) {
			t.Errorf("the unlimited plan lacks %s", feature)
		}
	}
	// The hosted plans are per-seat with a free tier: the free plan limits
	// the members, which the paid plans bill for instead.
	for _, key := range []string{"free", "team", "studio"} {
		p, _ := f.Plan(key)
		_, limited := p.Quota(Members).Max()
		if limited != (key == "free") {
			t.Errorf("the %s plan limits members: %v", key, limited)
		}
	}
}

// TestPlanValuesComeFromTheFile checks that the plans' numbers are read from
// the plan file and from nowhere else: in a copy of plans.json with every
// number changed, each plan has the copy's numbers, so they can be set in
// Phase 3 without a change of code.
//
// Verifies: FR-ENT-01/AC4
func TestPlanValuesComeFromTheFile(t *testing.T) {
	var doc map[string]any
	d := json.NewDecoder(bytes.NewReader(planFile))
	d.UseNumber()
	if err := d.Decode(&doc); err != nil {
		t.Fatal(err)
	}
	want := map[string]map[LimitKey]Quota{}
	for _, p := range doc["plans"].([]any) {
		plan := p.(map[string]any)
		key := plan["key"].(string)
		want[key] = map[LimitKey]Quota{}
		limits := plan["limits"].(map[string]any)
		for l, v := range limits {
			n, ok := v.(json.Number)
			if !ok {
				want[key][LimitKey(l)] = NoLimit
				continue
			}
			i, err := n.Int64()
			if err != nil {
				t.Fatal(err)
			}
			limits[l] = json.Number(strconv.FormatInt(i*7+3, 10))
			want[key][LimitKey(l)] = AtMost(i*7 + 3)
		}
	}
	changed, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Load(changed)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for key, limits := range want {
		p, ok := f.Plan(key)
		if !ok {
			t.Fatalf("no plan %s", key)
		}
		for l, q := range limits {
			if p.Quota(l) != q {
				t.Errorf("%s: %s is %s, want the file's %s", key, l, p.Quota(l), q)
			}
		}
	}
}

// TestLoadRefuses checks that Load refuses a plan file that the server could
// not use, and says why.
func TestLoadRefuses(t *testing.T) {
	// edit returns plans.json with edit applied to it as a JSON document.
	edit := func(edit func(doc map[string]any)) []byte {
		var doc map[string]any
		d := json.NewDecoder(bytes.NewReader(planFile))
		d.UseNumber()
		if err := d.Decode(&doc); err != nil {
			t.Fatal(err)
		}
		edit(doc)
		b, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	plan := func(doc map[string]any, key string) map[string]any {
		for _, p := range doc["plans"].([]any) {
			if p.(map[string]any)["key"] == key {
				return p.(map[string]any)
			}
		}
		t.Fatalf("no plan %s", key)
		return nil
	}
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"not JSON", []byte(`{"limits": [`), "unexpected EOF"},
		{"two values", append(slices.Clone(planFile), []byte(`{}`)...), "more than one JSON value"},
		{"an unknown field", edit(func(doc map[string]any) { doc["prices"] = map[string]any{} }), `unknown field "prices"`},
		{"a key twice", []byte(`{"limits": [], "limits": []}`), `the key "limits" appears more than once`},
		{"a limit key twice in a plan", bytes.Replace(planFile, []byte(`"members": 1,`),
			[]byte(`"members": 1, "members": 2,`), 1), `the key "members" appears more than once`},
		{"a negative limit", edit(func(doc map[string]any) {
			plan(doc, "free")["limits"].(map[string]any)["projects"] = -1
		}), "a limit is a whole number from 0 to 9007199254740991"},
		{"a fraction", edit(func(doc map[string]any) {
			plan(doc, "free")["limits"].(map[string]any)["projects"] = 2.5
		}), "a limit is a whole number from 0 to 9007199254740991"},
		// A JavaScript client would read a larger number inexactly.
		{"a limit past 2^53 - 1", edit(func(doc map[string]any) {
			plan(doc, "free")["limits"].(map[string]any)["storage_bytes"] = json.Number("9007199254740992")
		}), "a limit is a whole number from 0 to 9007199254740991, or \"unlimited\", not 9007199254740992"},
		{"null", edit(func(doc map[string]any) {
			plan(doc, "free")["limits"].(map[string]any)["projects"] = nil
		}), `the value of the key "projects" is null`},
		// encoding/json would read a null feature as one that the plan lacks.
		{"a null feature", edit(func(doc map[string]any) {
			plan(doc, "studio")["features"].(map[string]any)["sso"] = nil
		}), `the value of the key "sso" is null`},
		{"a null element", edit(func(doc map[string]any) {
			doc["features"] = append(doc["features"].([]any), nil)
		}), "a value is null"},
		// encoding/json would match these keys to the fields of another case,
		// keeping the last of "features" and "Features".
		{"a key of another case", bytes.Replace(planFile, []byte(`"plans": [`),
			[]byte(`"Features": ["collaboration", "sso", "audit_export"], "plans": [`), 1),
			`the key "Features" is not lowercase`},
		{"a field of another case", bytes.Replace(planFile, []byte(`"billable": true`), []byte(`"Billable": true`), 1),
			`the key "Billable" is not lowercase`},
		{"another word", edit(func(doc map[string]any) {
			plan(doc, "free")["limits"].(map[string]any)["projects"] = "infinite"
		}), "a limit is a whole number from 0 to 9007199254740991"},
		{"a missing limit", edit(func(doc map[string]any) {
			delete(plan(doc, "team")["limits"].(map[string]any), "storage_bytes")
		}), "plans[2] (team): the limits are members, projects, snapshot_retention, but the file declares"},
		{"an undeclared limit", edit(func(doc map[string]any) {
			plan(doc, "team")["limits"].(map[string]any)["boards"] = 5
		}), "plans[2] (team): the limits are boards,"},
		// Every workspace has its owner, so a plan without a member could
		// create none.
		{"no member", edit(func(doc map[string]any) {
			plan(doc, "team")["limits"].(map[string]any)["members"] = 0
		}), "plans[2] (team): members is 0, but every workspace has its owner"},
		{"a missing feature", edit(func(doc map[string]any) {
			delete(plan(doc, "studio")["features"].(map[string]any), "sso")
		}), "plans[3] (studio): the features are audit_export, collaboration, but the file declares"},
		{"a limit that the server does not check", edit(func(doc map[string]any) {
			doc["limits"] = append(doc["limits"].([]any), map[string]any{"key": "boards"})
		}), "the limits that are not inert are boards, members, projects, storage_bytes, but the server checks " +
			"members, projects, storage_bytes"},
		{"an inert limit that the server checks", edit(func(doc map[string]any) {
			doc["limits"].([]any)[1].(map[string]any)["inert_until"] = "FR-PROJ-01"
		}), "the limits that are not inert are members, storage_bytes"},
		{"a limit declared twice", edit(func(doc map[string]any) {
			doc["limits"] = append(doc["limits"].([]any), map[string]any{"key": "members"})
		}), "limits[4]: members is declared twice"},
		{"a malformed limit", edit(func(doc map[string]any) {
			doc["limits"].([]any)[0].(map[string]any)["key"] = "Members"
		}), `limits[0]: the key "Members" is not lowercase letters`},
		{"an unknown feature", edit(func(doc map[string]any) {
			doc["features"] = append(doc["features"].([]any), "themes")
		}), "the features are audit_export, collaboration, sso, themes, but the server knows"},
		{"a feature declared twice", edit(func(doc map[string]any) {
			doc["features"] = append(doc["features"].([]any), "sso")
		}), "features[3]: sso is declared twice"},
		{"a plan declared twice", edit(func(doc map[string]any) {
			doc["plans"] = append(doc["plans"].([]any), plan(doc, "team"))
		}), "plans[4]: team is declared twice"},
		{"a malformed plan", edit(func(doc map[string]any) {
			plan(doc, "studio")["key"] = "studio plan"
		}), `plans[3]: the key "studio plan" is not lowercase letters`},
		{"no unlimited plan", edit(func(doc map[string]any) {
			plan(doc, "unlimited")["key"] = "everything"
		}), "there is no unlimited plan"},
		{"a limited unlimited plan", edit(func(doc map[string]any) {
			plan(doc, "unlimited")["limits"].(map[string]any)["members"] = 100
		}), "the unlimited plan limits members to 100"},
		{"an unlimited plan without a feature", edit(func(doc map[string]any) {
			plan(doc, "unlimited")["features"].(map[string]any)["sso"] = false
		}), "the unlimited plan lacks sso"},
		{"no free plan", edit(func(doc map[string]any) {
			plan(doc, "free")["key"] = "starter"
		}), "there is no free plan"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := Load(tt.data)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Load = %v, %v; want an error containing %q", f, err, tt.want)
			}
		})
	}
}

// TestQuota checks what a quota allows, and when it warns: from 80% of it,
// rounded up.
func TestQuota(t *testing.T) {
	tests := []struct {
		q             Quota
		used, adding  int64
		allows, warns bool
		max           int64
		limited       bool
		shown         string
	}{
		{q: NoLimit, used: 1 << 62, adding: 1 << 62, allows: true, warns: false, shown: "unlimited"},
		{q: AtMost(3), used: 2, adding: 1, allows: true, warns: false, max: 3, limited: true, shown: "3"},
		{q: AtMost(3), used: 3, adding: 1, allows: false, warns: true, max: 3, limited: true, shown: "3"},
		{q: AtMost(3), used: 1, adding: 3, allows: false, warns: false, max: 3, limited: true, shown: "3"},
		// Adding nothing is allowed, even past the quota.
		{q: AtMost(3), used: 5, adding: 0, allows: true, warns: true, max: 3, limited: true, shown: "3"},
		{q: AtMost(10), used: 8, adding: 2, allows: true, warns: true, max: 10, limited: true, shown: "10"},
		{q: AtMost(10), used: 7, adding: 4, allows: false, warns: false, max: 10, limited: true, shown: "10"},
		{q: AtMost(0), used: 0, adding: 1, allows: false, warns: true, max: 0, limited: true, shown: "0"},
		// Negative usage, which a meter should never read, counts as none.
		{q: AtMost(1 << 62), used: -(1 << 62), adding: 1 << 62, allows: true, warns: false, max: 1 << 62,
			limited: true, shown: "4611686018427387904"},
		// No overflow near the largest numbers.
		{q: AtMost(1<<63 - 1), used: 1<<63 - 2, adding: 1<<63 - 1, allows: false, warns: true, max: 1<<63 - 1,
			limited: true, shown: "9223372036854775807"},
	}
	for _, tt := range tests {
		if got := tt.q.Allows(tt.used, tt.adding); got != tt.allows {
			t.Errorf("%s.Allows(%d, %d) = %v", tt.q, tt.used, tt.adding, got)
		}
		if got := tt.q.Warns(tt.used); got != tt.warns {
			t.Errorf("%s.Warns(%d) = %v", tt.q, tt.used, got)
		}
		if max, limited := tt.q.Max(); max != tt.max || limited != tt.limited {
			t.Errorf("%s.Max() = %d, %v", tt.q, max, limited)
		}
		if tt.q.String() != tt.shown {
			t.Errorf("String() = %q, want %q", tt.q.String(), tt.shown)
		}
	}
	// 80% of each quota from 1 to 100, rounded up, is where the warning
	// starts.
	for n := int64(1); n <= 100; n++ {
		threshold := (n*4 + 4) / 5
		if AtMost(n).Warns(threshold-1) || !AtMost(n).Warns(threshold) {
			t.Errorf("AtMost(%d) warns from %d, want %d", n, firstWarning(AtMost(n)), threshold)
		}
	}
	if AtMost(-1) != AtMost(0) {
		t.Errorf("AtMost(-1) = %v, want AtMost(0)", AtMost(-1))
	}
	// The plan file's largest limit is 2^53 - 1, which a JavaScript client
	// reads exactly.
	var q Quota
	if err := q.UnmarshalJSON([]byte("9007199254740991")); err != nil || q != AtMost(1<<53-1) {
		t.Errorf("UnmarshalJSON(2^53 - 1) = %v, %v", q, err)
	}
	if NoLimit.min(AtMost(5)) != AtMost(5) || AtMost(5).min(NoLimit) != AtMost(5) ||
		AtMost(5).min(AtMost(3)) != AtMost(3) || NoLimit.min(NoLimit) != NoLimit {
		t.Errorf("min does not return the smaller quota")
	}
}

func firstWarning(q Quota) int64 {
	for used := int64(0); ; used++ {
		if q.Warns(used) {
			return used
		}
	}
}

// TestParseCaps checks that caps are read, whatever the white space around
// their entries, up to 2^53 - 1, which a JavaScript client reads exactly, and
// written in the order of the limits that the server checks.
func TestParseCaps(t *testing.T) {
	caps, err := ParseCaps(" storage_bytes=1073741824 ,projects = 2,members=9007199254740991")
	if err != nil {
		t.Fatal(err)
	}
	want := Caps{StorageBytes: 1073741824, Projects: 2, Members: 1<<53 - 1}
	if !maps.Equal(caps, want) {
		t.Errorf("ParseCaps = %v, want %v", caps, want)
	}
	if got := caps.String(); got != "members=9007199254740991, projects=2, storage_bytes=1073741824" {
		t.Errorf("String() = %q", got)
	}
	if again, err := ParseCaps(caps.String()); err != nil || !maps.Equal(again, caps) {
		t.Errorf("ParseCaps(String()) = %v, %v", again, err)
	}
}
