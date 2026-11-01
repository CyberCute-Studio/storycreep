// Package orderkey checks and makes the keys that order a parent's children
// among themselves, as the folders and the boards of a folder (PRD
// FR-PROJ-02), so that moving one child to another place changes that child
// alone: its key, which the client picks between the keys of the children
// that it lands between, rather than every child's position (§5.7, ADR-006).
// Two clients that move different children never conflict, and the inverse of
// a move is the move back to the key that the child had.
//
// A key is a string of base-62 digits, 0-9, A-Z, and a-z, in that order,
// which is the order of their bytes, of at most MaxLength digits, that does
// not end in 0: the digits of a fraction between 0 and 1, as 0.V for V, so
// that there is always a key between two others, and before and after any.
// Keys compare as their bytes do, and children with the same key, as two
// clients may pick for two children dropped at one place, are ordered by
// their IDs: by their UUIDs, which the characters of their IDs after the
// prefix, bd_ or fld_, order alike, so that a client orders a board and a
// folder with the same key as the server does, without their prefixes.
package orderkey

import (
	"regexp"
	"strings"
)

// MaxLength is the most digits that a key may have.
const MaxLength = 128

// First is the key of a parent's first child.
const First = "V"

// Pattern is the pattern of a key, as a JSON schema writes it.
const Pattern = `^[0-9A-Za-z]*[1-9A-Za-z]$`

// digits are the digits of a key, in order.
const digits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

var form = regexp.MustCompile(Pattern)

// Valid reports whether k is a key.
func Valid(k string) bool {
	return len(k) <= MaxLength && form.MatchString(k)
}

// After returns a key after k, a key or "" for none, which is First, as short
// as the digits of k allow: k with its last digit that is not z incremented,
// and the digits after it dropped, as W for Vz, or k with the digit 1 after
// it if every digit is z. So a parent whose children are each added after the
// last has keys one digit longer every 61 children at most. It returns false
// if k is not a key, or if the key after it would have more than MaxLength
// digits.
func After(k string) (string, bool) {
	if k == "" {
		return First, true
	}
	if !Valid(k) {
		return "", false
	}
	for i := len(k) - 1; i >= 0; i-- {
		if d := strings.IndexByte(digits, k[i]); d < len(digits)-1 {
			return k[:i] + string(digits[d+1]), true
		}
	}
	if len(k) == MaxLength {
		return "", false
	}
	return k + "1", true
}

// Spread returns n keys in order, each of the fewest digits that n keys of one
// length take, with a V after them, so that none ends in 0, as 0V, 1V, and on
// to zV for 62 keys, and 00V and on for more: the places of n children of a
// parent that has none, which a command that creates them all gives them,
// rather than place each after the last (After), whose keys grow by a digit
// every 61 children, as a paste places a branch's outputs and a port's
// connections (package clipboard), so that creating each reads none of the
// others.
func Spread(n int) []string {
	width := 1
	for size := len(digits); size < n; size *= len(digits) {
		width++
	}
	out := make([]string, n)
	key := make([]byte, width+1)
	key[width] = First[0]
	for i := range out {
		for j, v := width-1, i; j >= 0; j, v = j-1, v/len(digits) {
			key[j] = digits[v%len(digits)]
		}
		out[i] = string(key)
	}
	return out
}
