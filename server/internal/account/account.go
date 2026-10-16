// Package account creates accounts and checks their passwords (PRD
// FR-AUTH-01, NFR-SEC-03).
//
// An account is registered with an email address, a display name, and a
// password, when the instance's signup policy (STORYCREEP_SIGNUP, the Signup
// capability) lets anyone register: with closed, nobody can, and with invite,
// only an invitation creates an account (FR-TEN-02). Registering an address
// that has an account already looks the same to the client as registering a
// new one, and takes as long, and leaves the account as it was, so that it
// does not tell whether the address has one (FR-AUTH-01 AC1).
//
// The password policy: a password has at least 12 characters, counted as
// Unicode code points once it is in NFKC, and is not one of the 10,000 most
// common passwords, compared without regard to case (CheckPassword).
//
// Passwords are hashed with argon2id, with a random 16-byte salt per hash and
// a 32-byte key, and these parameters (RFC 9106 §4, the second recommended
// setting): memory 64 MiB (m=65536), 3 passes (t=3), and parallelism 4
// (p=4). A hash records its parameters, in the PHC string format, so that
// when a release changes them, Authenticate replaces a hash made with the old
// ones at the account's next sign-in, once the password has matched it.
package account

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// Limits of the fields of a registration.
const (
	// MaxEmailLength is the longest email address, in bytes, that SMTP can
	// deliver to (RFC 5321 §4.5.3.1.3, a path of 256 octets with its angle
	// brackets).
	MaxEmailLength = 254
	// MaxDisplayNameLength is the most characters that a display name may
	// have.
	MaxDisplayNameLength = 100
)

// The errors of the signup policy, which Register returns before it looks at
// the registration.
var (
	// ErrSignupClosed is the error of the closed policy, under which nobody
	// can register.
	ErrSignupClosed = errors.New("account: signup is closed")
	// ErrInviteRequired is the error of the invite policy, under which only
	// an invitation creates an account.
	ErrInviteRequired = errors.New("account: signup needs an invitation")
)

// ErrInvalidCredentials is returned by Authenticate when the address belongs
// to no active account, or the password is not the account's.
var ErrInvalidCredentials = errors.New("account: the email address or the password is wrong")

// DB runs a transaction of the application login, as *database.DB does.
type DB interface {
	InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
}

// Service creates accounts and checks their passwords.
type Service struct {
	db     DB
	signup config.SignupPolicy
	hasher *Hasher
	// decoy returns a hash with the current parameters of a password that
	// nobody has, which Authenticate checks a password against when the
	// address has no account, so that it takes as long as when it has one.
	decoy func() (string, error)
}

// New returns a Service that keeps accounts in db, under the signup policy.
func New(db DB, signup config.SignupPolicy) *Service {
	return newService(db, signup, NewHasher())
}

func newService(db DB, signup config.SignupPolicy, h *Hasher) *Service {
	s := &Service{db: db, signup: signup, hasher: h}
	s.decoy = sync.OnceValues(func() (string, error) {
		return h.Hash(context.Background(), secret.New(ids.New().String()))
	})
	return s
}

// CheckSignup returns ErrSignupClosed or ErrInviteRequired unless the signup
// policy lets anyone register. The API checks it before it reads a
// registration, so that it refuses one whatever the registration holds
// (FR-AUTH-01 AC3).
func (s *Service) CheckSignup(context.Context) error {
	switch s.signup {
	case config.SignupOpen:
		return nil
	case config.SignupInvite:
		return ErrInviteRequired
	}
	return ErrSignupClosed
}

// Registration is what an account is registered with.
type Registration struct {
	Email       string
	DisplayName string
	Password    secret.Value
}

// FieldError is a field of a registration that is not valid.
type FieldError struct {
	// Field is the field's name in the API, such as display_name.
	Field string
	// Message says what is wrong with it, in English, for programs and
	// logs.
	Message string
}

// FieldErrors are the fields of a registration that are not valid.
type FieldErrors []FieldError

func (e FieldErrors) Error() string {
	var b strings.Builder
	b.WriteString("account: the registration is not valid:")
	for _, f := range e {
		fmt.Fprintf(&b, " %s %s;", f.Field, f.Message)
	}
	return strings.TrimSuffix(b.String(), ";")
}

// Register creates an account, unless the address has one already, which it
// leaves as it was. Either way it returns nil, after it has done the same
// work, hashing the password included, so that the caller answers the same
// and as late (FR-AUTH-01 AC1). It returns the error of CheckSignup unless
// the policy lets anyone register, FieldErrors if the email address or the
// display name is not valid, and a *PasswordError if the password breaks the
// policy, in that order, and adds nothing then.
func (s *Service) Register(ctx context.Context, r Registration) error {
	if err := s.CheckSignup(ctx); err != nil {
		return err
	}
	email, name, err := r.fields()
	if err != nil {
		return err
	}
	if err := CheckPassword(r.Password); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(ctx, r.Password)
	if err != nil {
		return err
	}
	return s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		_, err := queries.New(tx).InsertUser(ctx, queries.InsertUserParams{
			ID:           pgtype.UUID{Bytes: ids.New(), Valid: true},
			Email:        email,
			PasswordHash: pgtype.Text{String: hash, Valid: true},
			DisplayName:  name,
		})
		if err != nil {
			return fmt.Errorf("account: add the account: %w", err)
		}
		return nil
	})
}

// fields returns the registration's email address and display name as an
// account holds them, without the white space around them, or FieldErrors.
func (r Registration) fields() (email, name string, err error) {
	var errs FieldErrors
	email = strings.TrimSpace(r.Email)
	if msg := checkEmail(email); msg != "" {
		errs = append(errs, FieldError{Field: "email", Message: msg})
	}
	name = strings.TrimSpace(r.DisplayName)
	if msg := checkDisplayName(name); msg != "" {
		errs = append(errs, FieldError{Field: "display_name", Message: msg})
	}
	if len(errs) > 0 {
		return "", "", errs
	}
	return email, name, nil
}

// checkEmail says what is wrong with an email address, or returns "". It
// takes an address alone, as in jo@example.com, without a name or angle
// brackets, and without a quoted local part or a comment, which mail
// delivery and people rarely handle.
func checkEmail(email string) string {
	switch {
	case email == "":
		return "is empty"
	case len(email) > MaxEmailLength:
		return fmt.Sprintf("is longer than %d bytes", MaxEmailLength)
	}
	if a, err := mail.ParseAddress(email); err != nil || a.Name != "" || a.Address != email {
		return "is not an email address"
	}
	return ""
}

// checkDisplayName says what is wrong with a display name, or returns "".
func checkDisplayName(name string) string {
	switch n := utf8.RuneCountInString(name); {
	case n == 0:
		return "is empty"
	case n > MaxDisplayNameLength:
		return fmt.Sprintf("is longer than %d characters", MaxDisplayNameLength)
	case strings.IndexFunc(name, unicode.IsControl) >= 0:
		return "contains a control character"
	}
	return ""
}

// Authenticate returns the ID of the active account that the email address
// belongs to, if password is its password, and ErrInvalidCredentials if the
// address belongs to no account, to one that is not active, as a suspended
// one or one whose deletion is pending (FR-GDPR-02), or to one without a
// password, or if the password is another. It takes as long whichever of
// these it is, because it checks the password against a decoy hash when it
// has no hash to check it against.
//
// When the password matches a hash made with other parameters than the
// current ones, Authenticate hashes the password again and replaces the
// account's hash (NFR-SEC-03), unless the account's password changed in the
// meantime. The sign-in of plan item 14 calls it.
func (s *Service) Authenticate(ctx context.Context, email string, password secret.Value) (ids.UUID, error) {
	var found bool
	var row queries.UserCredentialsRow
	err := s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		var err error
		row, err = queries.New(tx).UserCredentials(ctx, strings.TrimSpace(email))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		found = err == nil
		return err
	})
	if err != nil {
		return ids.UUID{}, fmt.Errorf("account: read the account: %w", err)
	}
	if !found || !row.PasswordHash.Valid || row.Status != "active" {
		decoy, err := s.decoy()
		if err != nil {
			return ids.UUID{}, err
		}
		if _, _, err := s.hasher.Verify(ctx, decoy, password); err != nil {
			return ids.UUID{}, err
		}
		return ids.UUID{}, ErrInvalidCredentials
	}
	match, rehash, err := s.hasher.Verify(ctx, row.PasswordHash.String, password)
	switch {
	case err != nil:
		return ids.UUID{}, err
	case !match:
		return ids.UUID{}, ErrInvalidCredentials
	case rehash:
		if err := s.rehash(ctx, row.ID, row.PasswordHash.String, password); err != nil {
			return ids.UUID{}, err
		}
	}
	return row.ID.Bytes, nil
}

// rehash replaces old, the hash of the account id's password, with a hash of
// password with the current parameters, unless the hash is no longer old.
func (s *Service) rehash(ctx context.Context, id pgtype.UUID, old string, password secret.Value) error {
	hash, err := s.hasher.Hash(ctx, password)
	if err != nil {
		return err
	}
	return s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		_, err := queries.New(tx).UpdatePasswordHash(ctx, queries.UpdatePasswordHashParams{
			NewHash: pgtype.Text{String: hash, Valid: true},
			ID:      id,
			OldHash: pgtype.Text{String: old, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("account: replace the password's hash: %w", err)
		}
		return nil
	})
}
