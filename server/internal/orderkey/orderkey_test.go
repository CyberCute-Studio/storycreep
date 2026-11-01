package orderkey

import (
	"strings"
	"testing"
)

// TestValid checks the keys that Valid takes: base-62 digits that do not end
// in 0, of at most MaxLength digits.
func TestValid(t *testing.T) {
	t.Parallel()
	for _, k := range []string{"1", "V", "z", "0V", "Az", "a1", strings.Repeat("z", MaxLength)} {
		if !Valid(k) {
			t.Errorf("%q is not taken", k)
		}
	}
	for _, k := range []string{"", "0", "V0", "a-b", "é", " V", strings.Repeat("z", MaxLength+1)} {
		if Valid(k) {
			t.Errorf("%q is taken", k)
		}
	}
}

// TestAfter checks that After gives a key that sorts after the one given, as
// short as it can be, that a run of keys each after the last grows by a digit
// once every 61 keys at most, and that a key with no room after it within
// MaxLength digits has none.
func TestAfter(t *testing.T) {
	t.Parallel()
	for k, want := range map[string]string{"": First, "V": "W", "9": "A", "Z": "a", "Vz": "W", "z": "z1",
		"zz": "zz1", "z1": "z2", "0V": "0W"} {
		if got, ok := After(k); !ok || got != want {
			t.Errorf("After(%q) = %q, %v; want %q", k, got, ok, want)
		}
	}
	k := ""
	for i := range 1000 {
		next, ok := After(k)
		if !ok || !Valid(next) || next <= k {
			t.Fatalf("key %d after %q is %q, %v", i, k, next, ok)
		}
		k = next
	}
	if len(k) > 1+1000/61 {
		t.Errorf("the 1000th key is %q, of %d digits", k, len(k))
	}
	for _, k := range []string{strings.Repeat("z", MaxLength), "V0", "-"} {
		if got, ok := After(k); ok {
			t.Errorf("After(%q) = %q", k, got)
		}
	}
}

// TestSpread checks that Spread gives keys in order, each a key, of one
// length, the fewest digits that so many take.
func TestSpread(t *testing.T) {
	t.Parallel()
	for n, width := range map[int]int{0: 1, 1: 1, 62: 1, 63: 2, 3844: 2, 3845: 3, 10_000: 3} {
		keys := Spread(n)
		if len(keys) != n {
			t.Fatalf("Spread(%d) gives %d keys", n, len(keys))
		}
		for i, k := range keys {
			if !Valid(k) || len(k) != width+1 || i > 0 && keys[i-1] >= k {
				t.Fatalf("Spread(%d)[%d] = %q, after %q, want a key of %d digits after it", n, i, k,
					keys[max(i-1, 0)], width+1)
			}
		}
	}
	if got := Spread(2); got[0] != "0V" || got[1] != "1V" {
		t.Errorf("Spread(2) = %v, want 0V and 1V", got)
	}
}
