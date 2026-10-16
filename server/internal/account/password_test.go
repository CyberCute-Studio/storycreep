package account

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// cheapParams make hashes that the tests can afford many of. Their memory and
// passes are the least that argon2id takes.
var cheapParams = params{memory: 32, time: 1, threads: 4, saltLen: 16, keyLen: 32}

// TestHash checks that passwords are hashed with argon2id, with the
// documented parameters, in the PHC string format, and with a salt of each
// hash's own, so that two accounts with one password have different hashes.
//
// Verifies: NFR-SEC-03
func TestHash(t *testing.T) {
	t.Parallel()
	h := NewHasher()
	password := secret.New("correct horse battery staple")
	a, err := h.Hash(context.Background(), password)
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.Hash(context.Background(), password)
	if err != nil {
		t.Fatal(err)
	}
	const prefix = "$argon2id$v=19$m=65536,t=3,p=4$"
	if !strings.HasPrefix(a, prefix) {
		t.Fatalf("the hash %q does not start with %q", a, prefix)
	}
	p, salt, key, err := parseHash(a)
	if err != nil {
		t.Fatal(err)
	}
	if p != currentParams || len(salt) != 16 || len(key) != 32 {
		t.Errorf("parameters %+v, a salt of %d bytes, and a key of %d", p, len(salt), len(key))
	}
	_, saltB, _, _ := parseHash(b)
	if a == b || string(salt) == string(saltB) {
		t.Errorf("two hashes of one password share their salt: %q and %q", a, b)
	}
	for _, encoded := range []string{a, b} {
		match, rehash, err := h.Verify(context.Background(), encoded, password)
		if err != nil || !match || rehash {
			t.Errorf("Verify of the right password: match %t, rehash %t, error %v", match, rehash, err)
		}
	}
	if match, _, err := h.Verify(context.Background(), a, secret.New("correct horse battery stapler")); err != nil ||
		match {
		t.Errorf("Verify of a wrong password: match %t, error %v", match, err)
	}
}

// TestVerifyNormalizes checks that a password matches its hash however its
// characters are composed or which width they have, as NFKC makes them the
// same.
func TestVerifyNormalizes(t *testing.T) {
	t.Parallel()
	h := newHasher(cheapParams, 1)
	for _, c := range []struct{ name, stored, typed string }{
		{"composed and decomposed", "café au lait 1984", "café au lait 1984"},
		{"full width", "Password 2026!", "Ｐａｓｓｗｏｒｄ ２０２６！"},
	} {
		encoded, err := h.Hash(context.Background(), secret.New(c.stored))
		if err != nil {
			t.Fatal(err)
		}
		if match, _, err := h.Verify(context.Background(), encoded, secret.New(c.typed)); err != nil || !match {
			t.Errorf("%s: match %t, error %v", c.name, match, err)
		}
	}
}

// TestVerifyRehash checks that Verify asks for a hash made with other
// parameters than the current ones to be made again, once the password
// matches it, and only then.
//
// Verifies: NFR-SEC-03
func TestVerifyRehash(t *testing.T) {
	t.Parallel()
	password := secret.New("correct horse battery staple")
	old, err := newHasher(cheapParams, 1).Hash(context.Background(), password)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name   string
		params params
		rehash bool
	}{
		{"same parameters", cheapParams, false},
		{"more memory", params{memory: 64, time: 1, threads: 4, saltLen: 16, keyLen: 32}, true},
		{"more passes", params{memory: 32, time: 2, threads: 4, saltLen: 16, keyLen: 32}, true},
		{"other parallelism", params{memory: 32, time: 1, threads: 1, saltLen: 16, keyLen: 32}, true},
		{"longer salt", params{memory: 32, time: 1, threads: 4, saltLen: 32, keyLen: 32}, true},
		{"longer key", params{memory: 32, time: 1, threads: 4, saltLen: 16, keyLen: 64}, true},
	} {
		h := newHasher(c.params, 1)
		match, rehash, err := h.Verify(context.Background(), old, password)
		if err != nil || !match || rehash != c.rehash {
			t.Errorf("%s: match %t, rehash %t, error %v; want a match, and rehash %t", c.name, match, rehash, err,
				c.rehash)
		}
		if match, rehash, _ := h.Verify(context.Background(), old, secret.New("another password")); match || rehash {
			t.Errorf("%s, a wrong password: match %t, rehash %t", c.name, match, rehash)
		}
	}
}

// TestParseHashRejects checks that a stored hash that is not an argon2id
// hash as Hash writes it, or whose parameters are out of bounds, is refused
// rather than computed.
func TestParseHashRejects(t *testing.T) {
	t.Parallel()
	const salt, key = "c2FsdHNhbHRzYWx0c2FsdA", "a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U"
	valid := "$argon2id$v=19$m=65536,t=3,p=4$" + salt + "$" + key
	if _, _, _, err := parseHash(valid); err != nil {
		t.Fatalf("a valid hash: %v", err)
	}
	for _, encoded := range []string{
		"",
		"plain text",
		"$argon2i$v=19$m=65536,t=3,p=4$" + salt + "$" + key,
		"$argon2id$v=16$m=65536,t=3,p=4$" + salt + "$" + key,
		"$argon2id$m=65536,t=3,p=4$" + salt + "$" + key,
		"$argon2id$v=19$m=65536,t=3,p=4$" + salt,
		"$argon2id$v=19$m=65536,t=3,p=4$" + salt + "$" + key + "$",
		"x$argon2id$v=19$m=65536,t=3,p=4$" + salt + "$" + key,
		"$argon2id$v=19$t=3,m=65536,p=4$" + salt + "$" + key,
		"$argon2id$v=19$m=65536,t=3$" + salt + "$" + key,
		"$argon2id$v=19$m=65536,t=3,p=4,k=1$" + salt + "$" + key,
		"$argon2id$v=19$m=0,t=3,p=4$" + salt + "$" + key,
		"$argon2id$v=19$m=65536,t=0,p=4$" + salt + "$" + key,
		"$argon2id$v=19$m=65536,t=3,p=0$" + salt + "$" + key,
		"$argon2id$v=19$m=65536,t=3,p=256$" + salt + "$" + key,
		"$argon2id$v=19$m=65536,t=101,p=4$" + salt + "$" + key,
		"$argon2id$v=19$m=4194305,t=3,p=4$" + salt + "$" + key,
		"$argon2id$v=19$m=31,t=3,p=4$" + salt + "$" + key,
		"$argon2id$v=19$m=065536,t=3,p=4$" + salt + "$" + key,
		"$argon2id$v=19$m=+65536,t=3,p=4$" + salt + "$" + key,
		"$argon2id$v=19$m=65536,t=3,p=4$" + salt + "==$" + key,
		"$argon2id$v=19$m=65536,t=3,p=4$" + salt + "$" + key + "=",
		"$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$" + key,
		"$argon2id$v=19$m=65536,t=3,p=4$" + salt + "$a2V5",
		"$argon2id$v=19$m=65536,t=3,p=4$" + salt + "$" + key + "!",
	} {
		if _, _, _, err := parseHash(encoded); !errors.Is(err, errMalformedHash) {
			t.Errorf("parseHash(%q) = %v, want errMalformedHash", encoded, err)
		}
		if _, _, err := newHasher(cheapParams, 1).Verify(context.Background(), encoded, secret.New("x")); err == nil {
			t.Errorf("Verify against %q: no error", encoded)
		}
	}
}

// TestHasherWaitsForASlot checks that a Hasher runs at most as many hashes
// at a time as it has slots, and that a hash waiting for a slot stops when
// its context ends.
func TestHasherWaitsForASlot(t *testing.T) {
	t.Parallel()
	h := newHasher(cheapParams, 1)
	encoded, err := h.Hash(context.Background(), secret.New("x"))
	if err != nil {
		t.Fatal(err)
	}
	h.slots <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.Hash(ctx, secret.New("x")); !errors.Is(err, context.Canceled) {
		t.Errorf("Hash while every slot is taken: %v, want context.Canceled", err)
	}
	if _, _, err := h.Verify(ctx, encoded, secret.New("x")); !errors.Is(err, context.Canceled) {
		t.Errorf("Verify while every slot is taken: %v, want context.Canceled", err)
	}
	<-h.slots
	if match, _, err := h.Verify(context.Background(), encoded, secret.New("x")); err != nil || !match {
		t.Errorf("Verify once the slot is free: match %t, error %v", match, err)
	}
	if n := cap(NewHasher().slots); n < 1 {
		t.Errorf("NewHasher has %d slots", n)
	}
}
