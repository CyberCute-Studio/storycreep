package expr

import (
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// parser reads a source's tokens by the grammar of §5.8.2, by recursive
// descent, and stops at the first token that it cannot read, with the error
// that it fails with (fail).
type parser struct {
	ctx Context
	lex *lexer
	tok token // the current token
	// ahead is the token after the current one, once peek has read it.
	ahead    token
	hasAhead bool
	depth    int // how many parentheses are open
	nodes    int // how many nodes the AST has so far (count)
}

// bailout carries the parser's error up from where it fails to parse, which
// recovers it.
type bailout struct{ err *Error }

// fail stops the parser with the error code over r.
func (p *parser) fail(code Code, r Range) {
	panic(bailout{&Error{Code: code, Range: r}})
}

// parse reads the whole source, as a condition's or a script's.
func (p *parser) parse() (n Node, err error) {
	defer func() {
		if r := recover(); r != nil {
			b, ok := r.(bailout)
			if !ok {
				panic(r)
			}
			n, err = nil, b.err
		}
	}()
	p.next()
	if p.ctx == ContextScript {
		return p.script(), nil
	}
	n, _ = p.expression()
	if p.tok.kind != tEOF {
		p.unexpected()
	}
	return n, nil
}

// next steps past the current token, to the one after it, which is a tEOF
// again once the current one is the tEOF.
func (p *parser) next() {
	if p.hasAhead {
		p.tok, p.hasAhead = p.ahead, false
		return
	}
	p.tok = p.lex.next()
}

// peek returns the kind of the token after the current one.
func (p *parser) peek() tokenKind {
	if !p.hasAhead {
		p.ahead, p.hasAhead = p.lex.next(), true
	}
	return p.ahead.kind
}

// unexpected fails at the current token, which nothing that the parser has
// read can be followed by: with its own error, if it is a token that the
// lexer could not read; UnexpectedEnd at the end of the source;
// ConditionMustBePure for an assignment operator in a condition, wherever it
// is, as in a + (x = 1); and otherwise UnexpectedToken.
func (p *parser) unexpected() {
	t := &p.tok
	switch {
	case t.kind == tError:
		panic(bailout{t.err})
	case t.kind == tEOF:
		p.fail(UnexpectedEnd, t.Range)
	case p.ctx == ContextCondition && assignOps[t.kind] != "":
		p.fail(ConditionMustBePure, t.Range)
	}
	p.fail(UnexpectedToken, t.Range)
}

// script reads a script: statements, each ended by a ;, a line break that
// ends it (lexer.next), or the end of the source, with as many ; and line
// breaks as a writer likes before, between, and after them.
func (p *parser) script() *Script {
	// The script's own node, which no token makes, counts before its first
	// token.
	p.nodes = 1
	s := &Script{Statements: []Node{}}
	for {
		for p.tok.kind == tSemicolon || p.tok.kind == tNewline {
			p.next()
		}
		if p.tok.kind == tEOF {
			s.Src = Range{0, p.tok.End}
			return s
		}
		s.Statements = append(s.Statements, p.statement())
	}
}

// assignOps are the assignment operators, by their tokens, and "" for every
// other token.
var assignOps = [tokenKinds]AssignOp{
	tAssign: OpAssign, tAddAssign: OpAddAssign, tSubAssign: OpSubAssign, tMulAssign: OpMulAssign,
	tDivAssign: OpDivAssign, tModAssign: OpModAssign,
}

// statement reads a statement of a script, to the token that ends it: an
// assignment to a variable or an attribute, or a call. It reads an expression
// first, so that an assignment to anything but a variable or an attribute is
// InvalidAssignmentTarget, and a statement that is neither an assignment nor
// a call InvalidStatement, over the whole expression as written, parentheses
// and all, once the token after it ends it; any other token after it is that
// token's error, as in a condition.
func (p *parser) statement() Node {
	e, written := p.expression()
	if op := assignOps[p.tok.kind]; op != "" {
		p.count()
		switch e.(type) {
		case *Var, *Attr:
			// A name in parentheses is written over more than its range.
			if written == e.Range() {
				p.next()
				v, rest := p.expression()
				p.end()
				return &Assign{Op: op, Target: e, Value: v, Src: Range{written.Start, rest.End}}
			}
		}
		p.fail(InvalidAssignmentTarget, written)
	}
	p.end()
	if c, ok := e.(*Call); ok && written == c.Src {
		return c
	}
	p.fail(InvalidStatement, written)
	return nil
}

// count counts the node that the current token makes, which the parser has
// reached and not yet stepped past: a literal, the identifier or the
// qualifier that a name, a call, or a node reference starts with, a unary or
// a binary operator, or a statement's assignment operator. It fails with
// ASTTooLarge at the token if the AST has MaxNodes nodes already, before
// anything else is decided at it, so that a source of any length costs no
// more than an AST of MaxNodes nodes, and no AST that Parse returns is deeper
// than that.
func (p *parser) count() {
	if p.nodes == MaxNodes {
		p.fail(ASTTooLarge, p.tok.Range)
	}
	p.nodes++
}

// end fails unless the current token ends a statement: a ;, a line break that
// ends it, or the end of the source.
func (p *parser) end() {
	if k := p.tok.kind; k != tSemicolon && k != tNewline && k != tEOF {
		p.unexpected()
	}
}

// levels are the binary operators by their tokens, by precedence, from the
// lowest (§5.8.2), each level's "" for a token that is none of its operators.
// Each level's operators are left-associative.
var levels = [...][tokenKinds]BinaryOp{
	{tOr: OpOr},
	{tAnd: OpAnd},
	{tEq: OpEq, tNe: OpNe},
	{tLt: OpLt, tLe: OpLte, tGt: OpGt, tGe: OpGte},
	{tPlus: OpAdd, tMinus: OpSub},
	{tStar: OpMul, tSlash: OpDiv, tPercent: OpMod},
}

// expression reads an expression, and returns its node and the range of the
// expression as written: the node's, and the parentheses around it, if any.
func (p *parser) expression() (Node, Range) {
	return p.binary(0)
}

// binary reads an expression of the operators of levels[level] and those
// above it, its operands joined from the left.
func (p *parser) binary(level int) (Node, Range) {
	if level == len(levels) {
		return p.unary()
	}
	left, written := p.binary(level + 1)
	for {
		op := levels[level][p.tok.kind]
		if op == "" {
			return left, written
		}
		p.count()
		p.next()
		right, rest := p.binary(level + 1)
		written = Range{written.Start, rest.End}
		left = &Binary{Op: op, Left: left, Right: right, Src: written}
	}
}

// unary reads a primary, with a unary operator before it or none. The operator
// applies to a primary, so that --x is UnexpectedToken at its second -, and
// -(-x) is read.
func (p *parser) unary() (Node, Range) {
	var op UnaryOp
	switch p.tok.kind {
	case tNot:
		op = OpNot
	case tMinus:
		op = OpNeg
	default:
		return p.primary()
	}
	start := p.tok.Start
	p.count()
	p.next()
	operand, written := p.primary()
	n := &Unary{Op: op, Operand: operand, Src: Range{start, written.End}}
	return n, n.Src
}

// primary reads a literal, a name, a call, a node reference, or an expression
// in parentheses.
func (p *parser) primary() (Node, Range) {
	t := &p.tok
	var n Node
	switch t.kind {
	case tInt:
		n = &Lit{Type: value.Int, Int: t.i, Src: t.Range}
	case tFloat:
		n = &Lit{Type: value.Float, Float: t.f, Src: t.Range}
	case tString:
		n = &Lit{Type: value.String, Str: t.text, Src: t.Range}
	case tTrue, tFalse:
		n = &Lit{Type: value.Bool, Bool: t.kind == tTrue, Src: t.Range}
	case tIdent:
		return p.nameOrCall()
	case tGlobal, tBoard:
		return p.scoped()
	case tReserved:
		p.fail(ReservedWord, t.Range)
	case tLParen:
		start := t.Start
		p.open()
		e, _ := p.expression()
		end := p.close()
		return e, Range{start, end}
	default:
		p.unexpected()
	}
	// A literal.
	p.count()
	p.next()
	return n, n.Range()
}

// open steps past the opening parenthesis that is the current token, a
// group's, a call's, or a node reference's, which is ASTTooDeep if MaxDepth of
// them are open already. A node reference's is one of them, since any
// argument but one string literal is read as a call's, so that node(node(…
// would otherwise recurse without bound.
func (p *parser) open() {
	if p.depth == MaxDepth {
		p.fail(ASTTooDeep, p.tok.Range)
	}
	p.depth++
	p.next()
}

// close steps past the closing parenthesis that the current token must be,
// and returns where it ends.
func (p *parser) close() int {
	if p.tok.kind != tRParen {
		p.unexpected()
	}
	end := p.tok.End
	p.depth--
	p.next()
	return end
}

// nameOrCall reads what starts with an identifier: a call or a node reference,
// when a parenthesis follows it, and otherwise a variable, by its key, or an
// attribute, as <component>.<attribute> or cmp.<component>.<attribute>.
func (p *parser) nameOrCall() (Node, Range) {
	first := p.tok
	p.count()
	p.next()
	if p.tok.kind == tLParen {
		if first.text == "node" {
			return p.nodeRef(first)
		}
		return p.call(first)
	}
	parts := p.parts(first)
	r := Range{first.Start, parts[len(parts)-1].End}
	var n Node
	switch {
	case len(parts) == 1:
		n = &Var{Name: first.text, Key: first.text, Src: r}
	case len(parts) == 2:
		n = &Attr{Name: first.text + "." + parts[1].text, ComponentKey: first.text, AttributeKey: parts[1].text,
			Src: r}
	case len(parts) == 3 && first.text == "cmp":
		n = &Attr{Name: "cmp." + parts[1].text + "." + parts[2].text, Explicit: true, ComponentKey: parts[1].text,
			AttributeKey: parts[2].text, Src: r}
	default:
		p.fail(InvalidName, r)
	}
	return n, r
}

// scoped reads a variable written after its qualifier: global.<key> or
// board.<key>. The qualifier without a dot after it is ReservedWord, and with
// more than one part after it, InvalidName.
func (p *parser) scoped() (Node, Range) {
	q := p.tok
	p.count()
	p.next()
	if p.tok.kind != tDot {
		p.fail(ReservedWord, q.Range)
	}
	parts := p.parts(q)
	r := Range{q.Start, parts[len(parts)-1].End}
	if len(parts) != 2 {
		p.fail(InvalidName, r)
	}
	scope := ScopeGlobal
	if q.kind == tBoard {
		scope = ScopeBoard
	}
	n := &Var{Name: q.text + "." + parts[1].text, Scope: scope, Key: parts[1].text, Src: r}
	return n, r
}

// parts returns the parts of a qualified name: first, which the parser has
// stepped past, and the identifier after each dot that follows. A reserved
// word after a dot is ReservedWord.
func (p *parser) parts(first token) []token {
	parts := []token{first}
	for p.tok.kind == tDot {
		p.next()
		t := &p.tok
		switch {
		case t.kind == tIdent:
			parts = append(parts, *t)
			p.next()
		case t.keyword:
			p.fail(ReservedWord, t.Range)
		default:
			p.unexpected()
		}
	}
	return parts
}

// call reads a call of the function name, which the parser has stepped past,
// from its opening parenthesis.
func (p *parser) call(name token) (Node, Range) {
	p.open()
	args := p.arguments()
	end := p.close()
	n := &Call{Function: name.text, Args: args, Src: Range{name.Start, end}}
	return n, n.Src
}

// arguments reads a call's arguments, expressions separated by commas, up to
// its closing parenthesis, which it leaves.
func (p *parser) arguments() []Node {
	args := []Node{}
	if p.tok.kind == tRParen {
		return args
	}
	for {
		a, _ := p.expression()
		args = append(args, a)
		if p.tok.kind != tComma {
			return args
		}
		p.next()
	}
}

// nodeRef reads a node reference, node("nd_…"), from its opening parenthesis:
// one string literal that holds a node's ID, in any case, which is part of the
// reference's node, or else InvalidNodeRef, over the string, or over the whole
// node(…) when it holds anything else, once the parser has read it to its
// closing parenthesis, as a call's arguments, each node of which counts.
func (p *parser) nodeRef(name token) (Node, Range) {
	p.open()
	if p.tok.kind == tString && p.peek() == tRParen {
		arg := p.tok
		p.next()
		end := p.close()
		u, err := ids.Decode(ids.Node, arg.text)
		if err != nil {
			p.fail(InvalidNodeRef, arg.Range)
		}
		n := &NodeRef{Ref: ids.Encode(ids.Node, u), Src: Range{name.Start, end}}
		return n, n.Src
	}
	p.arguments()
	end := p.close()
	p.fail(InvalidNodeRef, Range{name.Start, end})
	return nil, Range{}
}
