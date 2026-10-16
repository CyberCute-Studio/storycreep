package account

import (
	_ "embed"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// MinPasswordLength is the fewest characters that a password may have (PRD
// FR-AUTH-01), counted as Unicode code points of the password as it is
// hashed, in NFKC (normalize).
const MinPasswordLength = 12

// MaxPasswordLength is the most characters that a password may have, as the
// client sends it. It is far above what a person types or a password manager
// generates, and bounds the work of normalizing and hashing a password.
const MaxPasswordLength = 256

// PasswordReason says why a password breaks the policy.
type PasswordReason string

const (
	// PasswordTooShort is a password of fewer than MinPasswordLength
	// characters.
	PasswordTooShort PasswordReason = "too_short"
	// PasswordCommon is one of the most common passwords (commonPasswords).
	PasswordCommon PasswordReason = "common"
)

// PasswordError reports a password that breaks the policy.
type PasswordError struct {
	Reason PasswordReason
}

func (e *PasswordError) Error() string {
	return "account: the password " + e.Problem()
}

// Problem says what is wrong with the password, after its subject, as "has
// fewer than 12 characters", for the API, the operator's commands, and
// unattended first-run setup to say alike.
func (e *PasswordError) Problem() string {
	switch e.Reason {
	case PasswordTooShort:
		return "has fewer than " + strconv.Itoa(MinPasswordLength) + " characters"
	case PasswordCommon:
		return "is one of the most common passwords"
	}
	return "breaks the policy"
}

// CheckPassword returns a *PasswordError if password breaks the policy
// (FR-AUTH-01): if it has fewer than MinPasswordLength characters, or is one
// of the most common passwords, compared without regard to case. It does not
// check MaxPasswordLength, which the API's schema does.
func CheckPassword(password secret.Value) error {
	p := string(normalize(password))
	if utf8.RuneCountInString(p) < MinPasswordLength {
		return &PasswordError{Reason: PasswordTooShort}
	}
	if commonPasswords()[strings.ToLower(p)] {
		return &PasswordError{Reason: PasswordCommon}
	}
	return nil
}

// commonPasswordList is SecLists' list of the 10,000 most common passwords,
// one per line, as it is at
// https://github.com/danielmiessler/SecLists/blob/2d1efd26a4bc0da0a4627040f531c915bb17d604/Passwords/Common-Credentials/10k-most-common.txt,
// where an entry added in 2026 made it 10,001. It is under the MIT licence in
// seclists/LICENSE. A test pins its contents, so that a change to it is a
// change that a review sees.
//
//go:embed seclists/10k-most-common.txt
var commonPasswordList string

// commonPasswords returns the entries of commonPasswordList, as CheckPassword
// compares them: in NFKC, and in lower case.
var commonPasswords = sync.OnceValue(func() map[string]bool {
	set := make(map[string]bool, 10_001)
	for line := range strings.Lines(commonPasswordList) {
		if p := strings.TrimSuffix(line, "\n"); p != "" {
			set[strings.ToLower(norm.NFKC.String(p))] = true
		}
	}
	return set
})
