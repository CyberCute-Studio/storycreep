package expr

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// testID returns the ID of the kind k whose UUID ends in the byte n, and whose
// body has letters, which its canonical spelling writes in upper case.
func testID(k ids.Kind, n byte) string {
	u := ids.UUID{0x01, 0x8f, 0xa3, 0xb2, 0xc4, 0xd5, 0x7e, 0xf6, 0x8a, 0x9b, 0xac, 0xbd, 0xce, 0xdf, 0xe0, n}
	return ids.Encode(k, u)
}

// The entities of testProject.
var (
	boardHarbour = testID(ids.Board, 1)
	boardElara   = testID(ids.Board, 2)
	varTrust     = testID(ids.Variable, 1) // global
	varMet       = testID(ids.Variable, 2) // global
	varName      = testID(ids.Variable, 3) // global
	varRate      = testID(ids.Variable, 4) // global
	varBoardTrst = testID(ids.Variable, 5) // the harbour's trust
	varScratch   = testID(ids.Variable, 6) // elara's
	cmpElara     = testID(ids.Component, 1)
	atrTrust     = testID(ids.AttributeDef, 1)
	atrBio       = testID(ids.AttributeDef, 2)
	atrAge       = testID(ids.AttributeDef, 3)
	atrMood      = testID(ids.AttributeDef, 4)
	nodeEntry    = testID(ids.Node, 1)
	nodeJumper   = testID(ids.Node, 2)
)

// testProject returns a project of two boards: global variables trust, met,
// name, and rate; the harbour's trust, which shadows the global one; elara's
// scratch; the component elara, with the runtime attributes trust and mood,
// an enum, and the attributes bio, a text, and age, which are not runtime;
// and a node and a jumper.
func testProject(t testing.TB) *Project {
	t.Helper()
	p, err := NewProject([]Variable{
		{ID: varTrust, Key: "trust", Type: value.Int},
		{ID: varMet, Key: "met", Type: value.Bool},
		{ID: varName, Key: "name", Type: value.String},
		{ID: varRate, Key: "rate", Type: value.Float},
		{ID: varBoardTrst, Key: "trust", Type: value.Int, Board: boardHarbour},
		{ID: varScratch, Key: "scratch", Type: value.Int, Board: boardElara},
	}, []Component{{ID: cmpElara, Key: "elara", Attributes: []Attribute{
		{ID: atrTrust, Key: "trust", Type: value.Int, Runtime: true},
		{ID: atrBio, Key: "bio", Type: value.Text},
		{ID: atrAge, Key: "age", Type: value.Int},
		{ID: atrMood, Key: "mood", Type: value.Enum, Runtime: true, Options: []string{"calm", "angry"}},
	}}}, []BoardNode{{ID: nodeEntry}, {ID: nodeJumper, Jumper: true}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// covered returns the characters of source that the error err covers, or
// what err is if it is no *Error.
func covered(source string, err error) string {
	var e *Error
	if !errors.As(err, &e) {
		return fmt.Sprint("no *Error: ", err)
	}
	r := []rune(source)
	if e.Start < 0 || e.Start > e.End || e.End > len(r) {
		return fmt.Sprintf("beyond the source: %d to %d", e.Start, e.End)
	}
	return string(r[e.Start:e.End])
}

// codeOf returns the code of err, an *Error, or "" for any other.
func codeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// TestBoardFirst checks that a bare key that a board's variable and a global
// one have names the board's on its board, and the global one elsewhere, and
// that global. names the global one on the board too.
//
// Verifies: FR-VAR-02/AC1
func TestBoardFirst(t *testing.T) {
	t.Parallel()
	p := testProject(t)
	for _, c := range []struct {
		source, board, want string
	}{
		{"trust > 1", boardHarbour, varBoardTrst},
		{"board.trust > 1", boardHarbour, varBoardTrst},
		{"global.trust > 1", boardHarbour, varTrust},
		{"trust > 1", boardElara, varTrust},
		{"trust > 1", "", varTrust},
	} {
		n, err := Compile(c.source, ContextCondition, p, c.board)
		if err != nil {
			t.Errorf("Compile(%q) on %q: %v", c.source, c.board, err)
			continue
		}
		v := n.(*Binary).Left.(*Var)
		if v.Ref != c.want || v.Name != "trust" {
			t.Errorf("Compile(%q) on %q names %s, %s; want %s, trust", c.source, c.board, v.Ref, v.Name, c.want)
		}
	}
}

// TestBoardVariableOutOfScope checks that a variable of another board, read or
// assigned, by its bare key or as board., is board_variable_out_of_scope over
// its name, as it is on no board, while its own board reads it.
//
// Verifies: FR-VAR-02/AC2
func TestBoardVariableOutOfScope(t *testing.T) {
	t.Parallel()
	p := testProject(t)
	for _, c := range []struct {
		ctx           Context
		source, board string
		name          string
	}{
		{ContextCondition, "scratch > 1", boardHarbour, "scratch"},
		{ContextCondition, "board.scratch > 1", boardHarbour, "board.scratch"},
		{ContextCondition, "met and scratch == 2", boardHarbour, "scratch"},
		{ContextScript, "scratch = 1", boardHarbour, "scratch"},
		{ContextScript, "trust += board.scratch", boardHarbour, "board.scratch"},
		{ContextCondition, "scratch > 1", "", "scratch"},
	} {
		_, err := Compile(c.source, c.ctx, p, c.board)
		if codeOf(err) != BoardVariableOutOfScope || covered(c.source, err) != c.name {
			t.Errorf("Compile(%q) on %q = %v; want board_variable_out_of_scope over %q", c.source, c.board, err,
				c.name)
		}
	}
	if _, err := Compile("scratch > 1", ContextCondition, p, boardElara); err != nil {
		t.Errorf("Compile of a board's variable on its board: %v", err)
	}
}

// TestAttributeNotRuntime checks that an attribute that is not runtime, a
// text or any other, read or assigned, in either of an attribute's forms, is
// attribute_not_runtime over its name, while a runtime one resolves to its
// component and its definition.
//
// Verifies: FR-COMP-03/AC2
func TestAttributeNotRuntime(t *testing.T) {
	t.Parallel()
	p := testProject(t)
	for _, c := range []struct {
		ctx          Context
		source, name string
	}{
		{ContextCondition, `elara.bio == "x"`, "elara.bio"},
		{ContextCondition, "elara.age > 30", "elara.age"},
		{ContextCondition, "cmp.elara.age > 30", "cmp.elara.age"},
		{ContextScript, "elara.age = 31", "elara.age"},
		{ContextScript, `elara.bio = "x"`, "elara.bio"},
		{ContextScript, "trust = elara.age", "elara.age"},
	} {
		_, err := Compile(c.source, c.ctx, p, boardElara)
		if codeOf(err) != AttributeNotRuntime || covered(c.source, err) != c.name {
			t.Errorf("Compile(%q) = %v; want attribute_not_runtime over %q", c.source, err, c.name)
		}
	}
	n, err := Compile("elara.trust += 1", ContextScript, p, boardElara)
	if err != nil {
		t.Fatal(err)
	}
	a := n.(*Script).Statements[0].(*Assign).Target.(*Attr)
	if a.Ref != cmpElara || a.Attribute != atrTrust || a.Name != "elara.trust" {
		t.Errorf("elara.trust resolves to %s, %s, %s; want %s, %s, elara.trust", a.Ref, a.Attribute, a.Name,
			cmpElara, atrTrust)
	}
}

// TestSandboxLimits checks that a source beyond each limit of the sandbox
// (PRD §5.8.6) is refused with the limit's own code, whether the parser
// checks it of the source or Check of the AST, and that a source at each limit
// compiles. No AST that compiles exceeds the evaluation step budget, which
// the evaluator checks (plan item 42): an AST of at most MaxNodes nodes,
// without loops, evaluates each node once.
//
// Verifies: FR-EXPR-02/AC2
func TestSandboxLimits(t *testing.T) {
	t.Parallel()
	p := testProject(t)
	for _, c := range []struct {
		name   string
		ctx    Context
		source string
		want   Code
	}{
		{"an identifier of 64 characters", ContextCondition, strings.Repeat("a", 64) + " > 1", UnknownIdentifier},
		{"an identifier of 65 characters", ContextCondition, strings.Repeat("a", 65) + " > 1", IdentifierTooLong},
		{"a string of 4,096 characters", ContextCondition, `"` + strings.Repeat("é", 4096) + `" == name`, ""},
		{"a string of 4,097 characters", ContextCondition, `"` + strings.Repeat("é", 4097) + `" == name`,
			StringTooLong},
		{"parentheses 64 deep", ContextCondition, strings.Repeat("(", 64) + "met" + strings.Repeat(")", 64), ""},
		{"parentheses 65 deep", ContextCondition, strings.Repeat("(", 65) + "met" + strings.Repeat(")", 65),
			ASTTooDeep},
		{"an AST 64 deep", ContextCondition, "1" + strings.Repeat(" + 1", 62) + " > 0", ""},
		{"an AST 65 deep", ContextCondition, "1" + strings.Repeat(" + 1", 63) + " > 0", ASTTooDeep},
		// <, -, and a 1, and 255 ones summed in a balanced tree of 254 +.
		{"an AST of 512 nodes", ContextCondition, "-" + balanced(255) + " < 1", ""},
		{"an AST of 513 nodes", ContextCondition, "-(-" + balanced(255) + ") < 1", ASTTooLarge},
		{"64 statements", ContextScript, strings.Repeat("trust = 1\n", 64), ""},
		{"65 statements", ContextScript, strings.Repeat("trust = 1\n", 65), TooManyStatements},
		{"8 arguments", ContextCondition, "min(1, 1, 1, 1, 1, 1, 1, 1) > 0", WrongArgumentCount},
		{"9 arguments", ContextCondition, "min(1, 1, 1, 1, 1, 1, 1, 1, 1) > 0", TooManyArguments},
		{"8 signals", ContextScript, strings.Repeat(`signal("s")`+"\n", 8), ""},
		{"9 signals", ContextScript, strings.Repeat(`signal("s")`+"\n", 9), TooManySignals},
	} {
		_, err := Compile(c.source, c.ctx, p, boardElara)
		if codeOf(err) != c.want || (c.want == "") != (err == nil) {
			t.Errorf("%s: Compile = %v; want the code %q", c.name, err, c.want)
		}
	}
}

// balanced returns a sum of n ones in parentheses, its + in a balanced tree,
// so that its AST, of 2n - 1 nodes, is as shallow as such a sum can be.
func balanced(n int) string {
	if n == 1 {
		return "1"
	}
	return "(" + balanced(n/2) + " + " + balanced(n-n/2) + ")"
}

// TestNodeLimit checks that Check refuses an AST of more than MaxNodes nodes,
// which no source that Parse reads gives it, at the 513th node, the nodes
// counted each before its children, and the children in order.
func TestNodeLimit(t *testing.T) {
	t.Parallel()
	// A call of eight arguments, each a sum of 63 ones in a chain of 62 +,
	// each + the left operand of the next, has 1 + 8 × 125 nodes, none deeper
	// than 64. Counted root first, the call is the 1st node, the fifth
	// argument's root the 502nd, and the + below it, down the chain, the
	// 503rd and on, so that the 513th is the fifth argument's 12th +, from
	// its root. Each + has its argument and its place in the chain as its
	// range, to tell it by.
	args := make([]Node, 8)
	for i := range args {
		var a Node = &Lit{Type: value.Int, Int: 1}
		for j := 61; j >= 0; j-- {
			a = &Binary{Op: OpAdd, Left: a, Right: &Lit{Type: value.Int, Int: 1}, Src: Range{i, j}}
		}
		args[i] = a
	}
	err := Check(&Call{Function: "min", Args: args}, ContextCondition, nil, "")
	want := &Error{Code: ASTTooLarge, Range: Range{4, 11}}
	if fmt.Sprint(err) != fmt.Sprint(want) {
		t.Errorf("Check of an AST of 1,001 nodes = %v; want %v", err, want)
	}
	// A script of more statements than it may have is too_many_statements,
	// before its statements, and so its nodes beyond its own, are counted.
	calls := make([]Node, 300)
	for i := range calls {
		calls[i] = &Call{Function: "signal", Args: []Node{&Lit{Type: value.String, Str: "s"}}, Src: Range{i, i}}
	}
	err = Check(&Script{Statements: calls}, ContextScript, nil, "")
	want = &Error{Code: TooManyStatements, Range: Range{64, 64}}
	if fmt.Sprint(err) != fmt.Sprint(want) {
		t.Errorf("Check of a script of 300 statements = %v; want %v", err, want)
	}
}

// TestTypeMismatch checks that a type error names its operand, by its
// characters, and the operand's type, and the types that its place takes.
//
// Verifies: FR-EXPR-02/AC4
func TestTypeMismatch(t *testing.T) {
	t.Parallel()
	p := testProject(t)
	for _, c := range []struct {
		ctx             Context
		source, operand string
		typ             string
		expected        []string
	}{
		{ContextCondition, "trust", "trust", "int", []string{"bool"}},
		{ContextCondition, `name + 1 == "x"`, "1", "int", []string{"string"}},
		{ContextCondition, `trust == "3"`, `"3"`, "string", []string{"int", "float"}},
		{ContextCondition, "not rate", "rate", "float", []string{"bool"}},
		{ContextCondition, "rate % 2 == 0", "rate", "float", []string{"int"}},
		{ContextCondition, "met and (trust + rate)", "trust + rate", "float", []string{"bool"}},
		{ContextCondition, "len(trust) > 1", "trust", "int", []string{"string"}},
		{ContextCondition, "visited(trust)", "trust", "int", []string{"node"}},
		{ContextCondition, `to_string(node("` + nodeEntry + `")) == ""`, `node("` + nodeEntry + `")`, "node",
			[]string{"bool", "int", "float", "string"}},
		{ContextCondition, `elara.mood == 1`, "1", "int", []string{"string"}},
		{ContextScript, "trust = 1.5", "1.5", "float", []string{"int"}},
		{ContextScript, "met += true", "met", "bool", []string{"int", "float", "string"}},
		{ContextScript, `rate = "1"`, `"1"`, "string", []string{"int", "float"}},
	} {
		_, err := Compile(c.source, c.ctx, p, boardElara)
		var e *Error
		if !errors.As(err, &e) || e.Code != TypeMismatch || covered(c.source, err) != c.operand || e.Type != c.typ ||
			!slices.Equal(e.Expected, c.expected) {
			t.Errorf("Compile(%q) = %v; want type_mismatch over %q, a %s, where %v is expected", c.source, err,
				c.operand, c.typ, c.expected)
		}
	}
}

// TestUnknownFunction checks that a call of a function that is not a
// built-in is unknown_function over its name, listing the near built-ins that
// its place can call, or none.
//
// Verifies: FR-EXPR-04/AC2
func TestUnknownFunction(t *testing.T) {
	t.Parallel()
	p := testProject(t)
	for _, c := range []struct {
		ctx          Context
		source, name string
		near         []string
	}{
		{ContextCondition, `uppr(name) == "X"`, "uppr", []string{"upper"}},
		{ContextCondition, "mx(1, 2) > 0", "mx", []string{"max"}},
		{ContextCondition, "Clamp(trust, 0, 3) > 1", "Clamp", []string{"clamp"}},
		{ContextCondition, "ceiling(rate) > 1", "ceiling", nil},
		{ContextCondition, `starts(name, "a")`, "starts", nil},
		{ContextScript, `sigal("door")`, "sigal", []string{"signal"}},
		{ContextScript, "trust = to_nt(rate)", "to_nt", []string{"to_int"}},
	} {
		_, err := Compile(c.source, c.ctx, p, boardElara)
		var e *Error
		if !errors.As(err, &e) || e.Code != UnknownFunction || covered(c.source, err) != c.name ||
			!slices.Equal(e.Suggestions, c.near) {
			t.Errorf("Compile(%q) = %v; want unknown_function over %q, near %v", c.source, err, c.name, c.near)
		}
	}
}

// TestSignalInCondition checks that signal in a condition, wherever it is, is
// condition_must_be_pure over the call, as it is a script's statement.
//
// Verifies: FR-EXPR-04/AC3
func TestSignalInCondition(t *testing.T) {
	t.Parallel()
	p := testProject(t)
	for _, source := range []string{`signal("door")`, `met and signal("door")`, `signal("door", 1) or met`,
		`to_string(signal("door")) == ""`, "signal()"} {
		_, err := Compile(source, ContextCondition, p, boardElara)
		if codeOf(err) != ConditionMustBePure || !strings.HasPrefix(covered(source, err), "signal(") {
			t.Errorf("Compile(%q) as a condition = %v; want condition_must_be_pure over the call", source, err)
		}
	}
	if _, err := Compile(`signal("door")`, ContextScript, p, boardElara); err != nil {
		t.Errorf(`Compile(signal("door")) as a script: %v`, err)
	}
}

// TestCheckResolves checks that Check sets the names of an AST only once it
// meets no error, so that an AST that it refuses is as Parse returned it, and
// that checking an AST that it has resolved again changes nothing.
func TestCheckResolves(t *testing.T) {
	t.Parallel()
	p := testProject(t)
	n, err := Parse("global.trust > 1 and elara.trust > 2 and nope", ContextCondition)
	if err != nil {
		t.Fatal(err)
	}
	before := encoded(n)
	if err := Check(n, ContextCondition, p, boardHarbour); codeOf(err) != UnknownIdentifier {
		t.Fatalf("Check = %v; want unknown_identifier", err)
	}
	if after := encoded(n); after != before {
		t.Errorf("Check, refusing the AST, changed it from %s to %s", before, after)
	}
	n, err = Parse("global.trust += elara.trust", ContextScript)
	if err != nil {
		t.Fatal(err)
	}
	if err := Check(n, ContextScript, p, boardHarbour); err != nil {
		t.Fatal(err)
	}
	want := `{"kind":"script","statements":[{"kind":"assign","op":"+=","target":{"kind":"var","name":"trust","ref":"` +
		varTrust + `"},"value":{"attribute":"` + atrTrust + `","kind":"attr","name":"elara.trust","ref":"` +
		cmpElara + `"}}]}`
	if got := encoded(n); got != want {
		t.Errorf("Check resolves the AST as %s; want %s", got, want)
	}
	if err := Check(n, ContextScript, p, boardHarbour); err != nil || encoded(n) != want {
		t.Errorf("Check of the AST again = %v, %s; want nil, %s", err, encoded(n), want)
	}
}

// TestCheckRefuses checks Check's errors for what is its caller's mistake
// rather than the source's: a context that is none, a board that is not a
// board's ID, and what is not an AST that Parse returns of the context, each
// an error that is no *Error. A nil project names nothing.
func TestCheckRefuses(t *testing.T) {
	t.Parallel()
	one := func() *Lit { return &Lit{Type: value.Int, Int: 1} }
	shared := one()
	cycle := &Unary{Op: OpNot}
	cycle.Operand = cycle
	nine := make([]Node, 9)
	for i := range 8 {
		nine[i] = one()
	}
	for _, c := range []struct {
		name  string
		n     Node
		ctx   Context
		board string
		want  error
	}{
		{"no context", one(), "text", "", ErrContext},
		{"a node's ID as the board", one(), ContextCondition, nodeEntry, ErrBoard},
		{"a board's ID in lower case", one(), ContextCondition, strings.ToLower(boardElara), ErrBoard},
		{"a nil AST", nil, ContextCondition, "", ErrAST},
		{"a nil *Binary", (*Binary)(nil), ContextCondition, "", ErrAST},
		{"a nil operand", &Binary{Op: OpAnd, Left: &Lit{Type: value.Bool}}, ContextCondition, "", ErrAST},
		{"a node twice", &Binary{Op: OpEq, Left: shared, Right: shared}, ContextCondition, "", ErrAST},
		{"a cycle", cycle, ContextCondition, "", ErrAST},
		{"a script as a condition", &Script{}, ContextCondition, "", ErrAST},
		{"an expression as a script", one(), ContextScript, "", ErrAST},
		{"an assignment as a condition", &Assign{Op: OpAssign, Target: &Var{Key: "x"}, Value: one()},
			ContextCondition, "", ErrAST},
		{"a variable as a statement", &Script{Statements: []Node{&Var{Key: "x"}}}, ContextScript, "", ErrAST},
		{"an unknown operator", &Binary{Op: "xor", Left: one(), Right: one()}, ContextCondition, "", ErrAST},
		{"an unknown literal type", &Lit{Type: "text"}, ContextCondition, "", ErrAST},
		{"a nil 9th argument", &Call{Function: "min", Args: nine}, ContextCondition, "", ErrAST},
	} {
		err := Check(c.n, c.ctx, nil, c.board)
		var e *Error
		if !errors.Is(err, c.want) || errors.As(err, &e) {
			t.Errorf("%s: Check = %v; want an error that wraps %v, and no *Error", c.name, err, c.want)
		}
	}
	if _, err := Compile("1 > 0", ContextCondition, nil, ""); err != nil {
		t.Errorf("Compile of a source that names nothing against no project: %v", err)
	}
	if _, err := Compile("x > 0", ContextCondition, nil, boardElara); codeOf(err) != UnknownIdentifier {
		t.Errorf("Compile of a name against no project = %v; want unknown_identifier", err)
	}
}

// TestNewProject checks that NewProject refuses what no project has, and
// keeps nothing of what it is given that a change afterwards could change.
func TestNewProject(t *testing.T) {
	t.Parallel()
	v := func(id, key, typ, board string) Variable { return Variable{ID: id, Key: key, Type: typ, Board: board} }
	attr := Attribute{ID: atrTrust, Key: "trust", Type: value.Int, Runtime: true}
	for _, c := range []struct {
		name       string
		variables  []Variable
		components []Component
		nodes      []BoardNode
	}{
		{"a node's ID for a variable", []Variable{v(nodeEntry, "x", value.Int, "")}, nil, nil},
		{"an ID in lower case", []Variable{v(strings.ToLower(varTrust), "x", value.Int, "")}, nil, nil},
		{"a reserved word as a key", []Variable{v(varTrust, "global", value.Int, "")}, nil, nil},
		{"a key that is no identifier", []Variable{v(varTrust, "9x", value.Int, "")}, nil, nil},
		{"a variable of the type text", []Variable{v(varTrust, "x", value.Text, "")}, nil, nil},
		{"a board that is a node", []Variable{v(varTrust, "x", value.Int, nodeEntry)}, nil, nil},
		{"two globals of a key", []Variable{v(varTrust, "x", value.Int, ""), v(varMet, "x", value.Bool, "")}, nil,
			nil},
		{"two of a board of a key", []Variable{v(varTrust, "x", value.Int, boardElara),
			v(varMet, "x", value.Bool, boardElara)}, nil, nil},
		{"one ID twice", []Variable{v(varTrust, "x", value.Int, ""), v(varTrust, "y", value.Int, "")}, nil, nil},
		{"two components of a key", nil, []Component{{ID: cmpElara, Key: "c"}, {ID: testID(ids.Component, 9),
			Key: "c"}}, nil},
		{"two attributes of a key", nil, []Component{{ID: cmpElara, Key: "c", Attributes: []Attribute{attr,
			{ID: atrAge, Key: "trust", Type: value.Int}}}}, nil},
		{"an enum without options", nil, []Component{{ID: cmpElara, Key: "c", Attributes: []Attribute{
			{ID: atrMood, Key: "mood", Type: value.Enum, Runtime: true}}}}, nil},
		{"options of an int", nil, []Component{{ID: cmpElara, Key: "c", Attributes: []Attribute{
			{ID: atrAge, Key: "age", Type: value.Int, Options: []string{"a"}}}}}, nil},
		{"an attribute of no type", nil, []Component{{ID: cmpElara, Key: "c", Attributes: []Attribute{
			{ID: atrAge, Key: "age", Type: "date"}}}}, nil},
		{"a variable's ID for a node", nil, nil, []BoardNode{{ID: varTrust}}},
	} {
		if _, err := NewProject(c.variables, c.components, c.nodes); !errors.Is(err, ErrProject) {
			t.Errorf("%s: NewProject = %v; want an error that wraps ErrProject", c.name, err)
		}
	}
	variables := []Variable{v(varTrust, "trust", value.Int, "")}
	components := []Component{{ID: cmpElara, Key: "elara", Attributes: []Attribute{
		{ID: atrMood, Key: "mood", Type: value.Enum, Runtime: true, Options: []string{"calm"}}}}}
	p, err := NewProject(variables, components, nil)
	if err != nil {
		t.Fatal(err)
	}
	variables[0].Key = "changed"
	components[0].Attributes[0].Options[0] = "changed"
	components[0].Attributes[0].Key = "changed"
	for _, source := range []string{"trust = 1", `elara.mood = "calm"`} {
		if _, err := Compile(source, ContextScript, p, ""); err != nil {
			t.Errorf("Compile(%q) once what NewProject read has changed: %v", source, err)
		}
	}
}

// TestBuiltins checks that the built-in table holds each function of PRD
// §5.8.4, each of an ast_version of the language, and that ASTVersion is the
// highest of every construct's.
func TestBuiltins(t *testing.T) {
	t.Parallel()
	want := []string{"abs", "ceil", "clamp", "contains", "ends_with", "floor", "len", "lower", "max", "min", "round",
		"signal", "starts_with", "to_float", "to_int", "to_string", "trim", "upper", "visited", "visits"}
	var got []string
	highest := 0
	for name, b := range builtins {
		got = append(got, name)
		if b.version < 1 || len(b.params) == 0 || b.optional >= len(b.params) {
			t.Errorf("the built-in %s is of the version %d, %d parameters, %d of them optional", name, b.version,
				len(b.params), b.optional)
		}
		highest = max(highest, b.version)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("the built-ins are %v; want %v", got, want)
	}
	for _, vs := range []map[string]int{toNames(kindVersions), toNames(unaryVersions), toNames(binaryVersions),
		toNames(assignVersions)} {
		for _, v := range vs {
			highest = max(highest, v)
		}
	}
	if highest != ASTVersion {
		t.Errorf("the highest version of a construct is %d; want ASTVersion, %d", highest, ASTVersion)
	}
	if len(kindVersions) != 9 || len(unaryVersions) != len(unaryOps) || len(binaryVersions) != len(binaryOps) ||
		len(assignVersions) != len(assignOpSet) {
		t.Error("a node kind or an operator has no version")
	}
}

// toNames returns the map m by the names of its keys.
func toNames[K ~string](m map[K]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[string(k)] = v
	}
	return out
}

// TestVersionOf checks that VersionOf is the highest ast_version of the
// constructs that ASTs use, so that a snapshot that calls a built-in of a
// newer version declares it, and the same snapshot without that call the
// older one. It changes a built-in's version for a while, so it does not run
// in parallel with the tests that read the table.
func TestVersionOf(t *testing.T) {
	p := testProject(t)
	compile := func(source string) Node {
		n, err := Compile(source, ContextCondition, p, "")
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	with, without := compile(`trim(name) == ""`), compile(`name == ""`)
	if v := VersionOf(); v != 1 {
		t.Errorf("VersionOf() = %d; want 1", v)
	}
	if v := VersionOf(with, without); v != 1 {
		t.Errorf("VersionOf of the language's first constructs = %d; want 1", v)
	}
	trim := builtins["trim"]
	t.Cleanup(func() { builtins["trim"] = trim })
	newer := trim
	newer.version = 2
	builtins["trim"] = newer
	if v := VersionOf(without, with); v != 2 {
		t.Errorf("VersionOf of an AST that calls a built-in of the version 2 = %d; want 2", v)
	}
	if v := VersionOf(without); v != 1 {
		t.Errorf("VersionOf of an AST that does not call it = %d; want 1", v)
	}
	shared := &Lit{Type: value.Bool}
	cycle := &Unary{Op: OpNot}
	cycle.Operand = cycle
	if v := VersionOf(&Binary{Op: OpAnd, Left: shared, Right: shared}, cycle, nil, (*Call)(nil)); v != 1 {
		t.Errorf("VersionOf of shared, cyclic, and nil nodes = %d; want 1", v)
	}
}

// TestNear checks the near matches that an Error suggests, as the corpus's
// README describes them.
func TestNear(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name       string
		candidates []string
		want       []string
	}{
		{"trsut", []string{"trust", "rust", "trusty"}, []string{"trust"}},
		{"Trust", []string{"trust"}, []string{"trust"}},
		{"x", []string{"X", "y", "xx"}, []string{"X"}},
		{"ab", []string{"ba", "abc", "a", "xy"}, []string{"a", "abc", "ba"}},
		{"gold", []string{"gold_", "golden", "old", "bold", "cold"}, []string{"bold", "cold", "gold_"}},
		{"reputation", []string{"repuation", "reputations", "rep"}, []string{"repuation", "reputations"}},
		{"abcdef", []string{"badcfe"}, nil},
		{"abcdefghi", []string{"badcfeghi", "badcfehgi"}, []string{"badcfeghi"}},
		{"ca", []string{"abc"}, nil},
		{"same", []string{"sane", "sane", "same_"}, []string{"same_", "sane"}},
		{"q", nil, nil},
	} {
		if got := near(c.name, c.candidates); !slices.Equal(got, c.want) {
			t.Errorf("near(%q, %v) = %v; want %v", c.name, c.candidates, got, c.want)
		}
	}
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"", "", 0}, {"a", "", 1}, {"", "abc", 3}, {"kitten", "sitting", 3}, {"ab", "ba", 1}, {"ca", "abc", 3},
		{"abcd", "badc", 2}, {"trust", "trsut", 1},
	} {
		// The distance, or one beyond the limit when it is beyond it.
		for limit := range 4 {
			got := distance(c.a, c.b, limit)
			if c.want <= limit && got != c.want || c.want > limit && got <= limit {
				t.Errorf("distance(%q, %q, %d) = %d; want %d, or more than %d if it is", c.a, c.b, limit, got,
					c.want, limit)
			}
		}
	}
}
