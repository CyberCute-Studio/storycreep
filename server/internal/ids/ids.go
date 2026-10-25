// Package ids generates entity identifiers and converts them to and from their
// public form (PRD §5.2, ADR-021).
//
// Every entity's primary key is a UUIDv7. Everywhere an identifier leaves the
// server, it is written as a type prefix, an underscore, and the UUID in 26
// characters of Crockford base32: "nd_01HQ8Z3XK4F7NAWR5TCQ9M2BVD". Decoding
// checks the prefix against the expected entity type, so a board ID passed
// where a node ID is expected fails with a [*DecodeError].
//
// The rules are pinned by the vectors in schema/ids, which the TypeScript
// implementation in web/shared runs too.
package ids

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// Kind is an entity type that has a public identifier.
type Kind uint8

// The entity types of PRD §5.2. The zero Kind is not a type.
const (
	Workspace Kind = iota + 1
	User
	Membership
	Invite
	APIToken
	Session
	Project
	Folder
	Board
	Node
	BranchOutput
	Connection
	Variable
	ComponentType
	AttributeDef
	Component
	Comment
	Snapshot
	Operation
	Asset
	Job
	PlayState
)

var kinds = [...]struct{ name, prefix string }{
	Workspace:     {"workspace", "ws"},
	User:          {"user", "usr"},
	Membership:    {"membership", "mem"},
	Invite:        {"invite", "inv"},
	APIToken:      {"api_token", "tok"},
	Session:       {"session", "ses"},
	Project:       {"project", "prj"},
	Folder:        {"folder", "fld"},
	Board:         {"board", "bd"},
	Node:          {"node", "nd"},
	BranchOutput:  {"branch_output", "bo"},
	Connection:    {"connection", "cn"},
	Variable:      {"variable", "var"},
	ComponentType: {"component_type", "ct"},
	AttributeDef:  {"attribute_def", "atr"},
	Component:     {"component", "cmp"},
	Comment:       {"comment", "cmt"},
	Snapshot:      {"snapshot", "snp"},
	Operation:     {"operation", "op"},
	Asset:         {"asset", "ast"},
	Job:           {"job", "job"},
	PlayState:     {"play_state", "pst"},
}

// Kinds returns every entity type, in declaration order.
func Kinds() []Kind {
	ks := make([]Kind, 0, len(kinds)-1)
	for k := Workspace; int(k) < len(kinds); k++ {
		ks = append(ks, k)
	}
	return ks
}

func (k Kind) valid() bool { return k > 0 && int(k) < len(kinds) }

// String returns the entity type's name in snake case, such as "branch_output".
func (k Kind) String() string {
	if !k.valid() {
		return fmt.Sprintf("Kind(%d)", uint8(k))
	}
	return kinds[k].name
}

// Prefix returns the prefix of the entity type's identifiers, without the
// underscore, such as "nd". It panics if k is not an entity type.
func (k Kind) Prefix() string {
	if !k.valid() {
		panic(fmt.Sprintf("ids: %v has no prefix", k))
	}
	return kinds[k].prefix
}

// kindOf returns the entity type with the given prefix. The prefix is matched
// with ASCII case folding only, so that no other character can stand in for a
// letter of a prefix.
func kindOf(prefix string) (Kind, bool) {
	for k := Workspace; int(k) < len(kinds); k++ {
		if equalFoldASCII(prefix, kinds[k].prefix) {
			return k, true
		}
	}
	return 0, false
}

// equalFoldASCII reports whether s equals the lowercase ASCII string lower
// when ASCII uppercase letters in s are lowercased.
func equalFoldASCII(s, lower string) bool {
	if len(s) != len(lower) {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != lower[i] {
			return false
		}
	}
	return true
}

// alphabet is Crockford's base32 alphabet. It is in ASCII order, so encoded
// bodies sort in the same order as the UUIDs.
const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// BodyLength is the length of an identifier's body, the part after its
// prefix: an encoded UUID, 128 bits in 5-bit characters, padded with two zero
// bits in front.
const BodyLength = 26

// Pattern returns the regular expression of the identifiers of the entity
// type that Decode accepts, as a JSON schema writes one, which Go's regular
// expressions and ECMAScript's read alike: the prefix and the body in either
// case, and a first character of the body that keeps the value within 128
// bits. It panics if k is not an entity type.
func (k Kind) Pattern() string {
	var b strings.Builder
	b.WriteByte('^')
	for _, c := range k.Prefix() {
		b.WriteString("[" + strings.ToUpper(string(c)) + string(c) + "]")
	}
	b.WriteString("_")
	class := func(chars string) string {
		var c strings.Builder
		c.WriteByte('[')
		for _, r := range chars {
			c.WriteRune(r)
			if 'A' <= r && r <= 'Z' {
				c.WriteRune(r + 'a' - 'A')
			}
		}
		c.WriteByte(']')
		return c.String()
	}
	// The body holds 130 bits, of which the two highest are zero, so its
	// first character is one of the first eight of the alphabet.
	fmt.Fprintf(&b, "%s%s{%d}$", class(alphabet[:8]), class(alphabet), BodyLength-1)
	return b.String()
}

// CanonicalPattern returns the regular expression of the identifiers of the
// entity type k in their canonical spelling, as Encode writes them: the
// lowercase prefix, an underscore, and the uppercase body, whose first
// character keeps the value within 128 bits, as Pattern's does. Go's regular
// expressions and ECMAScript's read it alike. It panics if k is not an
// entity type.
func (k Kind) CanonicalPattern() string {
	return fmt.Sprintf("^%s_[%s][%s]{%d}$", k.Prefix(), alphabet[:8], alphabet, BodyLength-1)
}

// decodeTable maps an ASCII byte to its value in the alphabet, accepting
// lowercase letters, or to invalid.
var decodeTable = func() (t [256]byte) {
	for i := range t {
		t[i] = invalid
	}
	for v, c := range []byte(alphabet) {
		t[c] = byte(v)
		if 'A' <= c && c <= 'Z' {
			t[c+'a'-'A'] = byte(v)
		}
	}
	return t
}()

const invalid = 0xFF

// Encode returns the public identifier of the entity of type k with the given
// UUID: the lowercase prefix, an underscore, and the uppercase body. It panics
// if k is not an entity type.
func Encode(k Kind, u UUID) string {
	prefix := k.Prefix()
	b := make([]byte, len(prefix)+1+BodyLength)
	copy(b, prefix)
	b[len(prefix)] = '_'
	encodeBody(b[len(prefix)+1:], u)
	return string(b)
}

// encodeBody writes the 26 characters of u to dst, least significant first,
// shifting the 128 bits right by 5 each time.
func encodeBody(dst []byte, u UUID) {
	hi := binary.BigEndian.Uint64(u[:8])
	lo := binary.BigEndian.Uint64(u[8:])
	for i := BodyLength - 1; i >= 0; i-- {
		dst[i] = alphabet[lo&0x1F]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
}

// Decode returns the UUID of the public identifier s, which must be the
// identifier of an entity of type k. Prefix and body are case-insensitive. The
// error is always a [*DecodeError].
func Decode(k Kind, s string) (UUID, error) {
	got, u, err := decode(k, s)
	if err != nil {
		return UUID{}, err
	}
	if got != k {
		return UUID{}, &DecodeError{Expected: k, Got: got, Reason: WrongPrefix}
	}
	return u, nil
}

// IsCanonical reports whether s is the identifier of an entity of type k in
// its canonical spelling, as Encode writes it, which CanonicalPattern
// matches: the published format holds identifiers so, so that a consumer
// compares them as strings.
func IsCanonical(k Kind, s string) bool {
	u, err := Decode(k, s)
	return err == nil && Encode(k, u) == s
}

// DecodeAny returns the entity type and the UUID of the public identifier s,
// whatever its type. The error is always a [*DecodeError].
func DecodeAny(s string) (Kind, UUID, error) {
	return decode(0, s)
}

// decode parses s into its type and UUID. The checks run in the order of the
// table in schema/ids/README.md, which decides the error when several apply.
// A type mismatch with expected is left to the caller, so that it is reported
// before any problem with the body.
func decode(expected Kind, s string) (Kind, UUID, error) {
	fail := func(got Kind, r Reason) (Kind, UUID, error) {
		return 0, UUID{}, &DecodeError{Expected: expected, Got: got, Reason: r}
	}
	sep := strings.IndexByte(s, '_')
	if sep < 0 {
		return fail(0, MissingPrefix)
	}
	k, ok := kindOf(s[:sep])
	if !ok {
		return fail(0, UnknownPrefix)
	}
	if expected != 0 && k != expected {
		return fail(k, WrongPrefix)
	}
	body := s[sep+1:]
	for i := range len(body) {
		if decodeTable[body[i]] == invalid {
			return fail(k, BadCharacter)
		}
	}
	if len(body) != BodyLength {
		return fail(k, BadLength)
	}
	if decodeTable[body[0]] > 7 {
		return fail(k, Overflow)
	}
	var hi, lo uint64
	for i := range BodyLength {
		hi = hi<<5 | lo>>59
		lo = lo<<5 | uint64(decodeTable[body[i]])
	}
	var u UUID
	binary.BigEndian.PutUint64(u[:8], hi)
	binary.BigEndian.PutUint64(u[8:], lo)
	return k, u, nil
}

// Reason says why an identifier could not be decoded. The values are the error
// names used in schema/ids/vectors.json.
type Reason string

// The reasons, in the order in which decoding checks for them.
const (
	MissingPrefix Reason = "missing_prefix" // no underscore
	UnknownPrefix Reason = "unknown_prefix" // the prefix is not in the table
	WrongPrefix   Reason = "wrong_prefix"   // the prefix is another entity type's
	BadCharacter  Reason = "character"      // the body has a character outside the alphabet
	BadLength     Reason = "length"         // the body is not 26 characters long
	Overflow      Reason = "overflow"       // the body's value does not fit in 128 bits
)

// DecodeError reports an identifier that could not be decoded. It does not
// hold the input, which comes from a client and may be long.
type DecodeError struct {
	// Expected is the entity type the identifier had to be, or zero when any
	// type was accepted.
	Expected Kind
	// Got is the entity type named by the identifier's prefix, or zero when the
	// prefix is missing or unknown.
	Got    Kind
	Reason Reason
}

func (e *DecodeError) Error() string {
	what := "identifier"
	if e.Expected != 0 {
		what = "identifier for type " + e.Expected.String()
	}
	var why string
	switch e.Reason {
	case MissingPrefix:
		why = "it has no type prefix"
	case UnknownPrefix:
		why = "its type prefix is unknown"
	case WrongPrefix:
		why = "its prefix is for another type"
		if e.Got.valid() {
			why = "its prefix " + e.Got.Prefix() + "_ is for type " + e.Got.String()
		}
	case BadCharacter:
		why = "it contains a character outside Crockford base32"
	case BadLength:
		why = fmt.Sprintf("the part after the prefix is not %d characters long", BodyLength)
	case Overflow:
		why = "the part after the prefix does not fit in 128 bits"
	default:
		why = string(e.Reason)
	}
	return "ids: invalid " + what + ": " + why
}
