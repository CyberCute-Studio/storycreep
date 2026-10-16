package account

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// TestCheckPassword checks the password policy: at least 12 characters,
// counted once the password is in NFKC, and none of the most common
// passwords, whatever their case or width.
//
// Verifies: FR-AUTH-01/AC2
func TestCheckPassword(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, password string
		want           PasswordReason
	}{
		{"empty", "", PasswordTooShort},
		{"11 characters", "abcdefghijk", PasswordTooShort},
		{"12 characters", "abcdefghijkl", ""},
		{"11 characters once composed", "abcdefghij" + "é", PasswordTooShort},
		{"12 characters outside ASCII", "žluťoučkýkůň", ""},
		{"11 characters outside ASCII", "žluťoučkýků", PasswordTooShort},
		{"11 spaces and a letter", "           x", ""},
		{"in the list", "unbelievable", PasswordCommon},
		{"in the list, in another case", "UnBelievable", PasswordCommon},
		{"in the list, in full width", "ｕｎｂｅｌｉｅｖａｂｌｅ",
			PasswordCommon},
		{"in the list but short", "password", PasswordTooShort},
		{"not in the list", "correct horse battery staple", ""},
	} {
		err := CheckPassword(secret.New(c.password))
		var pe *PasswordError
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v, want nil", c.name, err)
		case c.want != "" && (!errors.As(err, &pe) || pe.Reason != c.want):
			t.Errorf("%s: %v, want the reason %s", c.name, err, c.want)
		}
	}
}

// TestCommonPasswordList pins the bundled list to SecLists' file, so that a
// change to it is one that a review sees, and checks that every entry is one
// that CheckPassword can find.
func TestCommonPasswordList(t *testing.T) {
	t.Parallel()
	sum := sha256.Sum256([]byte(commonPasswordList))
	if got, want := hex.EncodeToString(sum[:]),
		"68782d6a4a19a4768d5f15dd66bd534e7a33055cc755411e33f16d18c50fdcce"; got != want {
		t.Errorf("the list's SHA-256 is %s, want %s, that of SecLists' file", got, want)
	}
	if strings.Contains(commonPasswordList, "\r") {
		t.Error("the list has a carriage return")
	}
	lines := strings.Split(strings.TrimSuffix(commonPasswordList, "\n"), "\n")
	if len(lines) != 10_001 {
		t.Errorf("the list has %d entries, want 10,001", len(lines))
	}
	if n := len(commonPasswords()); n != len(lines) {
		t.Errorf("the list has %d entries, and %d distinct ones", len(lines), n)
	}
	for _, p := range lines {
		if !commonPasswords()[strings.ToLower(string(normalize(secret.New(p))))] {
			t.Errorf("CheckPassword cannot find the entry %q", p)
		}
	}
}
