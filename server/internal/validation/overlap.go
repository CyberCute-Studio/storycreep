package validation

import (
	"math"

	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// maxSteps bounds the work of a prover, the nodes that its proofs compare,
// so that the choices of one element cost a bounded time, however many of
// them share a label and however large their conditions are; a proof that
// needs more than the steps left proves nothing, and the conditions may then
// overlap.
const maxSteps = 10_000

// prover proves conditions exclusive within its steps, which its proofs
// share: the rule of duplicate choices proves the pairs of one element's
// choices with one prover.
type prover struct {
	steps int
}

// newProver returns a prover of maxSteps.
func newProver() *prover {
	return &prover{steps: maxSteps}
}

// exclusive reports whether the conditions a and b never hold together, as
// far as a few patterns prove it within p's steps (FR-DEP-03, plan note
// N55), each a condition's AST, or nil for none, which always holds. They are
// exclusive when one is the literal false; when one is the negation of the
// other, as c and not c; when they compare the same operand with literals of
// values that no value meets both comparisons of, as x == 1 and x == 2,
// x < 3 and x >= 3, or x > 5 and x < 2, with numbers compared as real
// numbers; when they compare the same two operands with complementary
// operators, as x < y and x >= y; when one is a conjunction one of whose
// terms is exclusive with the other; and when one is a disjunction both of
// whose terms are. Two conditions that the patterns do not prove exclusive
// may overlap. The conditions are pure, so that an operand is the same value
// in both, and an operand that a runtime error makes its type's zero value is
// a value all the same.
func (p *prover) exclusive(a, b expr.Node) bool {
	if a == nil || b == nil {
		return a != nil && never(a) || b != nil && never(b)
	}
	return p.prove(a, b)
}

// spend takes a step, and reports whether there was one left.
func (p *prover) spend() bool {
	p.steps--
	return p.steps >= 0
}

// prove reports whether the conditions a and b, neither of them none, never
// hold together (exclusive), within p's steps. Each pattern is tried of the
// conditions whole, a disjunction's among them, so that a disjunction whose
// terms are not each exclusive with the other condition may still be its
// negation, or a term of its conjunction.
func (p *prover) prove(a, b expr.Node) bool {
	if !p.spend() {
		return false
	}
	if never(a) || never(b) {
		return true
	}
	if x, ok := a.(*expr.Binary); ok {
		switch x.Op {
		case expr.OpOr:
			if p.prove(x.Left, b) && p.prove(x.Right, b) {
				return true
			}
		case expr.OpAnd:
			if p.prove(x.Left, b) || p.prove(x.Right, b) {
				return true
			}
		}
	}
	if y, ok := b.(*expr.Binary); ok {
		switch y.Op {
		case expr.OpOr:
			if p.prove(a, y.Left) && p.prove(a, y.Right) {
				return true
			}
		case expr.OpAnd:
			if p.prove(a, y.Left) || p.prove(a, y.Right) {
				return true
			}
		}
	}
	return p.negation(a, b) || p.negation(b, a) || p.comparisons(a, b)
}

// never reports whether the condition n never holds: the literal false.
func never(n expr.Node) bool {
	l, ok := n.(*expr.Lit)
	return ok && l.Type == value.Bool && !l.Bool
}

// negation reports whether a is the negation of b.
func (p *prover) negation(a, b expr.Node) bool {
	u, ok := a.(*expr.Unary)
	return ok && u.Op == expr.OpNot && p.same(u.Operand, b)
}

// complements are the comparisons that hold exactly when their complement
// does not, which no two values meet both of.
var complements = map[expr.BinaryOp]expr.BinaryOp{
	expr.OpEq: expr.OpNe, expr.OpNe: expr.OpEq, expr.OpLt: expr.OpGte, expr.OpGte: expr.OpLt,
	expr.OpLte: expr.OpGt, expr.OpGt: expr.OpLte,
}

// flipped are the comparisons as they read with their operands swapped, as
// x < 3 is 3 > x.
var flipped = map[expr.BinaryOp]expr.BinaryOp{
	expr.OpEq: expr.OpEq, expr.OpNe: expr.OpNe, expr.OpLt: expr.OpGt, expr.OpGt: expr.OpLt,
	expr.OpLte: expr.OpGte, expr.OpGte: expr.OpLte,
}

// comparisons reports whether a and b are comparisons that no value meets
// both of: of the same two operands, by complementary operators, or of the
// same operand with literals, whose sets of values do not meet.
func (p *prover) comparisons(a, b expr.Node) bool {
	x, ok := a.(*expr.Binary)
	if !ok {
		return false
	}
	y, ok := b.(*expr.Binary)
	if !ok {
		return false
	}
	if _, ok := complements[x.Op]; !ok {
		return false
	}
	if _, ok := complements[y.Op]; !ok {
		return false
	}
	switch {
	case p.same(x.Left, y.Left) && p.same(x.Right, y.Right):
		if complements[x.Op] == y.Op {
			return true
		}
	case p.same(x.Left, y.Right) && p.same(x.Right, y.Left):
		if complements[x.Op] == flipped[y.Op] {
			return true
		}
	}
	xo, xop, xl := literalSide(x)
	yo, yop, yl := literalSide(y)
	if xl == nil || yl == nil || !p.same(xo, yo) {
		return false
	}
	return disjoint(xop, xl, yop, yl)
}

// literalSide returns a comparison c as its other operand, its operator as
// it reads with the operand first, and its literal, if one of its operands
// is a literal, or a nil literal.
func literalSide(c *expr.Binary) (expr.Node, expr.BinaryOp, *expr.Lit) {
	if l, ok := c.Right.(*expr.Lit); ok {
		return c.Left, c.Op, l
	}
	if l, ok := c.Left.(*expr.Lit); ok {
		return c.Right, flipped[c.Op], l
	}
	return nil, "", nil
}

// disjoint reports whether no value v meets both v op1 l1 and v op2 l2: of
// literals of the same value, by complementary operators; of different
// values, by == both; and of numbers, by comparisons whose intervals of the
// real numbers do not meet.
func disjoint(op1 expr.BinaryOp, l1 *expr.Lit, op2 expr.BinaryOp, l2 *expr.Lit) bool {
	n1, num1 := number(l1)
	n2, num2 := number(l2)
	if num1 && num2 {
		if op1 == expr.OpNe || op2 == expr.OpNe {
			return n1 == n2 && (op1 == expr.OpEq || op2 == expr.OpEq)
		}
		return interval(op1, n1).disjoint(interval(op2, n2))
	}
	if l1.Type != l2.Type || l1.Type != value.String && l1.Type != value.Bool {
		return false
	}
	equal := l1.Str == l2.Str && l1.Bool == l2.Bool
	switch {
	case op1 == expr.OpEq && op2 == expr.OpEq:
		return !equal
	case op1 == expr.OpEq && op2 == expr.OpNe, op1 == expr.OpNe && op2 == expr.OpEq:
		return equal
	}
	return false
}

// number returns the literal l's value as a real number, and whether it is a
// number. An int is a safe integer (§5.8.9), which a float64 holds exactly.
func number(l *expr.Lit) (float64, bool) {
	switch l.Type {
	case value.Int:
		return float64(l.Int), true
	case value.Float:
		return l.Float, true
	}
	return 0, false
}

// span is an interval of the real numbers, each bound open or closed.
type span struct {
	lo, hi         float64
	loOpen, hiOpen bool
}

// interval returns the interval of the values v that meet v op n.
func interval(op expr.BinaryOp, n float64) span {
	inf := math.Inf(1)
	switch op {
	case expr.OpLt:
		return span{lo: -inf, hi: n, loOpen: true, hiOpen: true}
	case expr.OpLte:
		return span{lo: -inf, hi: n, loOpen: true}
	case expr.OpGt:
		return span{lo: n, hi: inf, loOpen: true, hiOpen: true}
	case expr.OpGte:
		return span{lo: n, hi: inf, hiOpen: true}
	}
	return span{lo: n, hi: n}
}

// disjoint reports whether s and t have no number in common.
func (s span) disjoint(t span) bool {
	lo, loOpen := s.lo, s.loOpen
	if t.lo > lo || t.lo == lo && t.loOpen {
		lo, loOpen = t.lo, t.loOpen
	}
	hi, hiOpen := s.hi, s.hiOpen
	if t.hi < hi || t.hi == hi && t.hiOpen {
		hi, hiOpen = t.hi, t.hiOpen
	}
	return lo > hi || lo == hi && (loOpen || hiOpen)
}

// same reports whether a and b are the same expression: of the same kinds,
// operators, functions, literals, and the IDs of what they name, which hold
// the same value in one state, as the conditions are pure.
func (p *prover) same(a, b expr.Node) bool {
	if !p.spend() {
		return false
	}
	switch x := a.(type) {
	case *expr.Lit:
		y, ok := b.(*expr.Lit)
		return ok && x.Type == y.Type && x.Bool == y.Bool && x.Int == y.Int && x.Float == y.Float && x.Str == y.Str
	case *expr.Var:
		y, ok := b.(*expr.Var)
		return ok && x.Ref != "" && x.Ref == y.Ref
	case *expr.Attr:
		y, ok := b.(*expr.Attr)
		return ok && x.Ref != "" && x.Ref == y.Ref && x.Attribute == y.Attribute
	case *expr.NodeRef:
		y, ok := b.(*expr.NodeRef)
		return ok && x.Ref == y.Ref
	case *expr.Unary:
		y, ok := b.(*expr.Unary)
		return ok && x.Op == y.Op && p.same(x.Operand, y.Operand)
	case *expr.Binary:
		y, ok := b.(*expr.Binary)
		return ok && x.Op == y.Op && p.same(x.Left, y.Left) && p.same(x.Right, y.Right)
	case *expr.Call:
		y, ok := b.(*expr.Call)
		if !ok || x.Function != y.Function || len(x.Args) != len(y.Args) {
			return false
		}
		for i := range x.Args {
			if !p.same(x.Args[i], y.Args[i]) {
				return false
			}
		}
		return true
	}
	return false
}
