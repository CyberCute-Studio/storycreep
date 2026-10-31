package validation

import (
	"strconv"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// TestExclusive checks which pairs of conditions the rule of duplicate
// choices proves never to hold together, as compiled against a project of
// the int trust, the float mood, the string name, and the bools met and
// left, and which it leaves as conditions that may overlap.
//
// Verifies: FR-DEP-03/AC1
func TestExclusive(t *testing.T) {
	node := ids.Encode(ids.Node, ids.New())
	p, err := expr.NewProject([]expr.Variable{
		{ID: ids.Encode(ids.Variable, ids.New()), Key: "trust", Type: "int"},
		{ID: ids.Encode(ids.Variable, ids.New()), Key: "mood", Type: "float"},
		{ID: ids.Encode(ids.Variable, ids.New()), Key: "name", Type: "string"},
		{ID: ids.Encode(ids.Variable, ids.New()), Key: "met", Type: "bool"},
		{ID: ids.Encode(ids.Variable, ids.New()), Key: "left", Type: "bool"},
	}, nil, []expr.BoardNode{{ID: node}})
	if err != nil {
		t.Fatal(err)
	}
	compile := func(src string) expr.Node {
		if src == "" {
			return nil
		}
		n, err := expr.Compile(strings.ReplaceAll(src, "NODE", node), expr.ContextCondition, p, "")
		if err != nil {
			t.Fatalf("compile %q: %v", src, err)
		}
		return n
	}
	for _, tt := range []struct {
		a, b      string
		exclusive bool
	}{
		// None always holds, and false never does.
		{"", "", false},
		{"", "met", false},
		{"", "false", true},
		{"false", "met", true},
		// A condition and its negation, either way round, however written.
		{"met", "not met", true},
		{"!met", "met", true},
		{"visited(node(\"NODE\"))", "not visited(node(\"NODE\"))", true},
		{"met", "not left", false},
		{"met", "met", false},
		// Complementary comparisons of the same operands.
		{"trust < mood", "trust >= mood", true},
		{"trust < mood", "mood <= trust", true},
		{"trust < mood", "mood < trust", false},
		{"trust == mood", "trust != mood", true},
		// Literals that no value meets both comparisons with.
		{"trust == 1", "trust == 2", true},
		{"trust == 1", "1 == trust", false},
		{"trust == 1", "trust == 1.0", false},
		{"trust == 1", "trust != 1", true},
		{"trust != 1", "trust != 2", false},
		{"trust < 3", "trust >= 3", true},
		{"trust < 3", "3 <= trust", true},
		{"trust > 5", "trust < 2", true},
		{"trust > 2", "trust < 3", false},
		{"trust >= 3", "trust <= 3", false},
		{"trust > 3", "trust <= 3", true},
		{"mood > 0.5", "mood < 0.5", true},
		// A negative number, the unary - of a literal, as the literal of its
		// value, but no other negation.
		{"trust == -1", "trust == -2", true},
		{"trust == -1", "trust == 1", true},
		{"trust < -5", "trust > 0", true},
		{"-5 > trust", "trust >= 0", true},
		{"mood <= -0.5", "mood > 0.5", true},
		{"trust > -5", "trust < -2", false},
		{"trust == -0", "trust == 0", false},
		{"-trust == 1", "-trust == 2", true},
		{"-trust == 1", "trust == 1", false},
		// Strings in the order of their code points, the empty one first.
		{"name == \"Elara\"", "name == \"Mira\"", true},
		{"name == \"Elara\"", "name != \"Elara\"", true},
		{"name < \"b\"", "name > \"c\"", true},
		{"name == \"a\"", "name > \"a\"", true},
		{"name >= \"b\"", "\"a\" >= name", true},
		{"name < \"b\"", "name > \"a\"", false},
		{"name < \"é\"", "name > \"z\"", false},
		{"name <= \"\"", "name != \"\"", true},
		{"name != \"a\"", "name != \"b\"", false},
		// Booleans, of which there are two.
		{"met == true", "met == false", true},
		{"met != true", "met != false", true},
		{"met == true", "met != false", false},
		{"trust == 1", "mood == 2", false},
		// A conjunction one of whose terms is exclusive with the other, and
		// a disjunction both of whose terms are.
		{"met and trust > 3", "trust < 2", true},
		{"met and trust > 3", "left and trust < 2", true},
		{"met and trust > 3", "left and trust > 2", false},
		{"trust < 2 or trust > 8", "trust >= 3 and trust <= 7", true},
		{"trust < 2 or trust > 8", "trust == 5 or trust == 9", false},
		{"(met and left)", "not (met and left)", true},
		// A disjunction is the negation of the other condition, or of a term
		// of its conjunction, though neither of its terms is exclusive with
		// it alone.
		{"met or left", "not (met or left)", true},
		{"met or left", "trust > 3 and not (met or left)", true},
		{"met or left", "trust < 2 or not (met or left)", false},
	} {
		a, b := compile(tt.a), compile(tt.b)
		if got := newProver().exclusive(a, b); got != tt.exclusive {
			t.Errorf("exclusive(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.exclusive)
		}
		if got := newProver().exclusive(b, a); got != tt.exclusive {
			t.Errorf("exclusive(%q, %q) = %v, want %v", tt.b, tt.a, got, tt.exclusive)
		}
	}
}

// TestExclusiveBounded checks that a proof that needs more than its prover's
// steps proves nothing, so that two large conditions cost a bounded time, and
// may then overlap: two disjunctions of 120 terms each, every term of one
// exclusive with every term of the other, are exclusive, but proving it
// compares 14,400 pairs of terms. A prover's proofs share its steps, so that
// once one has spent them, the next proves nothing either, but that a
// condition that is false, which needs no step, is exclusive with any.
func TestExclusiveBounded(t *testing.T) {
	p, err := expr.NewProject([]expr.Variable{{ID: ids.Encode(ids.Variable, ids.New()), Key: "trust",
		Type: "int"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A disjunction of the terms, balanced, so that it is not too deep.
	var balanced func(terms []string) string
	balanced = func(terms []string) string {
		if len(terms) == 1 {
			return terms[0]
		}
		return "(" + balanced(terms[:len(terms)/2]) + ") or (" + balanced(terms[len(terms)/2:]) + ")"
	}
	disjunction := func(from, n int) expr.Node {
		terms := make([]string, n)
		for i := range terms {
			terms[i] = "trust == " + strconv.Itoa(from+i)
		}
		c, err := expr.Compile(balanced(terms), expr.ContextCondition, p, "")
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	one, many, b := disjunction(0, 1), disjunction(0, 120), disjunction(1000, 120)
	spent := newProver()
	if spent.exclusive(many, b) {
		t.Error("a proof of 14,400 pairs of terms proves the disjunctions exclusive")
	}
	if spent.exclusive(one, b) {
		t.Error("a prover whose steps a proof has spent proves one term exclusive with a disjunction of 120 others")
	}
	falsity := &expr.Lit{Type: value.Bool}
	if !spent.exclusive(falsity, b) || !spent.exclusive(b, falsity) {
		t.Error("a prover whose steps a proof has spent does not prove false exclusive with a disjunction")
	}
	if !newProver().exclusive(one, b) {
		t.Error("one term is not proved exclusive with a disjunction of 120 others")
	}
}
