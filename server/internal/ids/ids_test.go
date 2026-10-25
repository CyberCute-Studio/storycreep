package ids

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// vectors is schema/ids/vectors.json, shared with the TypeScript
// implementation. See schema/ids/README.md.
type vectors struct {
	Prefixes map[string]string `json:"prefixes"`
	Encode   []struct {
		Note string `json:"note"`
		Kind string `json:"kind"`
		UUID string `json:"uuid"`
		ID   string `json:"id"`
	} `json:"encode"`
	Decode []struct {
		Note      string `json:"note"`
		Kind      string `json:"kind"`
		ID        string `json:"id"`
		UUID      string `json:"uuid"`
		Canonical string `json:"canonical"`
	} `json:"decode"`
	DecodeErrors []struct {
		Note  string `json:"note"`
		Kind  string `json:"kind"`
		ID    string `json:"id"`
		Error string `json:"error"`
		Got   string `json:"got"`
	} `json:"decode_errors"`
	UUIDv7 []struct {
		Note   string `json:"note"`
		UnixMS int64  `json:"unix_ms"`
		Random string `json:"random"`
		UUID   string `json:"uuid"`
	} `json:"uuidv7"`
}

func readVectors(t testing.TB) vectors {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "..", "schema", "ids", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	// A misspelt key would otherwise leave a field empty and weaken a vector
	// without failing it.
	dec.DisallowUnknownFields()
	var v vectors
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	if len(v.Prefixes) == 0 || len(v.Encode) == 0 || len(v.Decode) == 0 || len(v.DecodeErrors) == 0 || len(v.UUIDv7) == 0 {
		t.Fatal("vectors.json has an empty section")
	}
	return v
}

// kindNamed returns the entity type with the given name, as written in the
// vectors.
func kindNamed(t *testing.T, name string) Kind {
	t.Helper()
	for _, k := range Kinds() {
		if k.String() == name {
			return k
		}
	}
	t.Fatalf("no entity type is named %q", name)
	return 0
}

func mustParseUUID(t *testing.T, s string) UUID {
	t.Helper()
	u, err := ParseUUID(s)
	if err != nil {
		t.Fatalf("ParseUUID(%q): %v", s, err)
	}
	return u
}

func TestPrefixesMatchVectors(t *testing.T) {
	v := readVectors(t)
	seen := map[string]Kind{}
	for _, k := range Kinds() {
		want, ok := v.Prefixes[k.String()]
		if !ok {
			t.Errorf("%v is not in the vectors' prefix table", k)
		} else if k.Prefix() != want {
			t.Errorf("%v has prefix %q, the vectors say %q", k, k.Prefix(), want)
		}
		if other, dup := seen[k.Prefix()]; dup {
			t.Errorf("%v and %v share the prefix %q", other, k, k.Prefix())
		}
		seen[k.Prefix()] = k
		for _, c := range []byte(k.Prefix()) {
			if c < 'a' || c > 'z' {
				t.Errorf("%v's prefix %q is not lowercase ASCII letters", k, k.Prefix())
			}
		}
	}
	if len(Kinds()) != len(v.Prefixes) {
		t.Errorf("there are %d entity types, the vectors list %d", len(Kinds()), len(v.Prefixes))
	}
}

func TestEncodeVectors(t *testing.T) {
	for _, tc := range readVectors(t).Encode {
		k := kindNamed(t, tc.Kind)
		u := mustParseUUID(t, tc.UUID)
		if got := u.String(); got != tc.UUID {
			t.Errorf("%s: String() = %q, want %q", tc.Note, got, tc.UUID)
		}
		if got := Encode(k, u); got != tc.ID {
			t.Errorf("%s: Encode(%v, %v) = %q, want %q", tc.Note, k, u, got, tc.ID)
		}
		if got, err := Decode(k, tc.ID); err != nil || got != u {
			t.Errorf("%s: Decode(%v, %q) = %v, %v; want %v", tc.Note, k, tc.ID, got, err, u)
		}
		if gotK, got, err := DecodeAny(tc.ID); err != nil || gotK != k || got != u {
			t.Errorf("%s: DecodeAny(%q) = %v, %v, %v; want %v, %v", tc.Note, tc.ID, gotK, got, err, k, u)
		}
	}
}

func TestDecodeVectors(t *testing.T) {
	for _, tc := range readVectors(t).Decode {
		k := kindNamed(t, tc.Kind)
		u := mustParseUUID(t, tc.UUID)
		got, err := Decode(k, tc.ID)
		if err != nil || got != u {
			t.Errorf("%s: Decode(%v, %q) = %v, %v; want %v", tc.Note, k, tc.ID, got, err, u)
			continue
		}
		if gotK, got, err := DecodeAny(tc.ID); err != nil || gotK != k || got != u {
			t.Errorf("%s: DecodeAny(%q) = %v, %v, %v; want %v, %v", tc.Note, tc.ID, gotK, got, err, k, u)
		}
		if enc := Encode(k, got); enc != tc.Canonical {
			t.Errorf("%s: re-encoded as %q, want %q", tc.Note, enc, tc.Canonical)
		}
	}
}

// TestDecodeErrorVectors checks that every malformed identifier fails with the
// error the vectors name, and that the error is a *DecodeError, which the API
// maps to 400 (plan item 6).
func TestDecodeErrorVectors(t *testing.T) {
	for _, tc := range readVectors(t).DecodeErrors {
		k := kindNamed(t, tc.Kind)
		var want Kind
		if tc.Got != "" {
			want = kindNamed(t, tc.Got)
		}
		u, err := Decode(k, tc.ID)
		var de *DecodeError
		if !errors.As(err, &de) {
			t.Errorf("%s: Decode(%v, %q) = %v, %v; want a *DecodeError", tc.Note, k, tc.ID, u, err)
			continue
		}
		if string(de.Reason) != tc.Error || de.Expected != k || de.Got != want {
			t.Errorf("%s: Decode(%v, %q) failed with %+v, want reason %s, expected %v, got %v", tc.Note, k, tc.ID, *de, tc.Error, k, want)
		}
		if u != (UUID{}) {
			t.Errorf("%s: Decode returned %v with its error, want the zero UUID", tc.Note, u)
		}

		gotK, u, err := DecodeAny(tc.ID)
		if Reason(tc.Error) == WrongPrefix {
			// Without an expected type, the input is just an identifier of
			// the other type, valid or not.
			wantU, wantErr := Decode(want, tc.ID)
			if err == nil && wantErr == nil {
				if gotK != want || u != wantU {
					t.Errorf("%s: DecodeAny(%q) = %v, %v; want %v, %v", tc.Note, tc.ID, gotK, u, want, wantU)
				}
				continue
			}
			var wantDE *DecodeError
			if !errors.As(err, &de) || !errors.As(wantErr, &wantDE) || de.Reason != wantDE.Reason || de.Got != want {
				t.Errorf("%s: DecodeAny(%q) = %v, %v, %v; Decode(%v) fails with %v", tc.Note, tc.ID, gotK, u, err, want, wantErr)
			}
			continue
		}
		if !errors.As(err, &de) || string(de.Reason) != tc.Error || de.Expected != 0 || de.Got != want {
			t.Errorf("%s: DecodeAny(%q) = %v, %v, %v; want a *DecodeError with reason %s, no expected type, and got %v", tc.Note, tc.ID, gotK, u, err, tc.Error, want)
		}
	}
}

func TestUUIDv7Vectors(t *testing.T) {
	for _, tc := range readVectors(t).UUIDv7 {
		var r [10]byte
		if n, err := hex.Decode(r[:], []byte(tc.Random)); err != nil || n != len(r) {
			t.Fatalf("%s: random %q is not 10 hex bytes", tc.Note, tc.Random)
		}
		if got := newV7(tc.UnixMS, r).String(); got != tc.UUID {
			t.Errorf("%s: newV7(%d, %s) = %s, want %s", tc.Note, tc.UnixMS, tc.Random, got, tc.UUID)
		}
	}
}

func TestNew(t *testing.T) {
	before := time.Now().UnixMilli()
	u := New()
	after := time.Now().UnixMilli()
	if v := u[6] >> 4; v != 7 {
		t.Errorf("version = %d, want 7", v)
	}
	if v := u[8] >> 6; v != 0b10 {
		t.Errorf("variant = %02b, want 10", v)
	}
	var ms int64
	for _, b := range u[:6] {
		ms = ms<<8 | int64(b)
	}
	if ms < before || ms > after {
		t.Errorf("timestamp = %d, want between %d and %d", ms, before, after)
	}

	seen := map[UUID]bool{}
	for range 1000 {
		u := New()
		if seen[u] {
			t.Fatalf("New returned %v twice", u)
		}
		seen[u] = true
	}
}

// TestEncodingSortsLikeUUID checks that encoded identifiers of one type sort in
// the same order as their UUIDs, so UUIDv7 identifiers sort by time as text.
func TestEncodingSortsLikeUUID(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	random := func() UUID {
		var u UUID
		for i := range u {
			// Small values make long shared prefixes, which reach the low bits.
			u[i] = byte(r.IntN(3)) * 0x7F
		}
		return u
	}
	for range 10000 {
		a, b := random(), random()
		if got, want := strings.Compare(Encode(Node, a), Encode(Node, b)), bytes.Compare(a[:], b[:]); got != want {
			t.Fatalf("Encode(%v) vs Encode(%v) compares %d, the UUIDs compare %d", a, b, got, want)
		}
	}
	early := newV7(1_700_000_000_000, [10]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	late := newV7(1_700_000_000_001, [10]byte{})
	if Encode(Node, early) >= Encode(Node, late) {
		t.Errorf("an identifier from a later millisecond sorts first")
	}
}

func TestParseUUIDRejects(t *testing.T) {
	for _, s := range []string{
		"",
		"017f22e279b07cc398c4dc0c0c07398f",
		"{017f22e2-79b0-7cc3-98c4-dc0c0c07398f}",
		"urn:uuid:017f22e2-79b0-7cc3-98c4-dc0c0c07398f",
		"017f22e2-79b0-7cc3-98c4-dc0c0c07398",
		"017f22e2-79b0-7cc3-98c4-dc0c0c07398f0",
		"017f22e2_79b0-7cc3-98c4-dc0c0c07398f",
		"017f22e2-79b07-cc3-98c4-dc0c0c07398f",
		"017f22e2-79b0-7cc3-98c4-dc0c0c07398g",
		"017f22e2-79b0-7cc3-98c4-dc0c0c0739+f",
		"017f22e2-79b0-7cc3-98c4-dc0c0c07398f\n",
	} {
		if u, err := ParseUUID(s); !errors.Is(err, ErrUUIDSyntax) {
			t.Errorf("ParseUUID(%q) = %v, %v; want ErrUUIDSyntax", s, u, err)
		}
	}
	if u, err := ParseUUID("017F22E2-79B0-7CC3-98C4-DC0C0C07398F"); err != nil || u.String() != "017f22e2-79b0-7cc3-98c4-dc0c0c07398f" {
		t.Errorf("ParseUUID of uppercase digits = %v, %v", u, err)
	}
}

func TestDecodeErrorMessage(t *testing.T) {
	_, err := Decode(Board, "nd_01HQ8Z3XK4F7NAWR5TCQ9M2BVD")
	if want := "ids: invalid identifier for type board: its prefix nd_ is for type node"; err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}
	_, _, err = DecodeAny("01HQ8Z3XK4F7NAWR5TCQ9M2BVD")
	if want := "ids: invalid identifier: it has no type prefix"; err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}
}

// TestPattern checks that each entity type's pattern matches exactly the
// identifiers that Decode accepts for the type: those of every vector, and
// identifiers of the type in either case, with each character of the body.
func TestPattern(t *testing.T) {
	v := readVectors(t)
	for _, k := range Kinds() {
		re := regexp.MustCompile(k.Pattern())
		check := func(k Kind, id string) {
			t.Helper()
			_, err := Decode(k, id)
			if matched := re.MatchString(id); matched != (err == nil) {
				t.Errorf("%v's pattern %s matches %q: %v, but Decode says %v", k, k.Pattern(), id, matched, err)
			}
		}
		for _, tc := range v.Encode {
			check(k, tc.ID)
		}
		for _, tc := range v.Decode {
			check(k, tc.ID)
		}
		for _, tc := range v.DecodeErrors {
			check(k, tc.ID)
		}
		id := Encode(k, New())
		check(k, strings.ToUpper(id))
		for c := range 128 {
			for _, at := range []int{len(k.Prefix()) + 1, len(id) - 1} {
				check(k, id[:at]+string(rune(c))+id[at+1:])
			}
		}
	}
}

// TestCanonicalPattern checks that each kind's canonical pattern matches
// exactly the identifiers that Decode accepts and that Encode writes as they
// are, as the published format holds them.
func TestCanonicalPattern(t *testing.T) {
	v := readVectors(t)
	for _, k := range Kinds() {
		re := regexp.MustCompile(k.CanonicalPattern())
		check := func(id string) {
			t.Helper()
			u, err := Decode(k, id)
			canonical := err == nil && Encode(k, u) == id
			if matched := re.MatchString(id); matched != canonical {
				t.Errorf("%v's canonical pattern %s matches %q: %v, but its canonical spelling is %v", k,
					k.CanonicalPattern(), id, matched, canonical)
			}
		}
		for _, tc := range v.Encode {
			check(tc.ID)
		}
		for _, tc := range v.Decode {
			check(tc.ID)
		}
		for _, tc := range v.DecodeErrors {
			check(tc.ID)
		}
		id := Encode(k, New())
		check(id)
		check(strings.ToUpper(id))
		check(strings.ToLower(id))
		for c := range 128 {
			for _, at := range []int{0, len(k.Prefix()) + 1, len(id) - 1} {
				check(id[:at] + string(rune(c)) + id[at+1:])
			}
		}
	}
}

func TestInvalidKind(t *testing.T) {
	if got := Kind(0).String(); got != "Kind(0)" {
		t.Errorf("Kind(0).String() = %q", got)
	}
	defer func() {
		if recover() == nil {
			t.Error("Encode with an invalid Kind did not panic")
		}
	}()
	Encode(Kind(len(kinds)), UUID{})
}

// FuzzDecodeAny checks that whatever decodes re-encodes to the same text up to
// ASCII case, and that every failure is a *DecodeError.
func FuzzDecodeAny(f *testing.F) {
	v := readVectors(f)
	for _, tc := range v.Encode {
		f.Add(tc.ID)
	}
	for _, tc := range v.Decode {
		f.Add(tc.ID)
	}
	for _, tc := range v.DecodeErrors {
		f.Add(tc.ID)
	}
	f.Fuzz(func(t *testing.T, s string) {
		k, u, err := DecodeAny(s)
		if err != nil {
			var de *DecodeError
			if !errors.As(err, &de) {
				t.Fatalf("DecodeAny(%q) failed with %T, want *DecodeError", s, err)
			}
			return
		}
		if enc := Encode(k, u); !strings.EqualFold(enc, s) || len(enc) != len(s) {
			t.Fatalf("DecodeAny(%q) = %v, %v, which encodes as %q", s, k, u, enc)
		}
	})
}
