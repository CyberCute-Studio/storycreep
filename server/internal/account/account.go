// Package account creates accounts, checks their passwords, and signs them
// in with sessions (PRD FR-AUTH-01, FR-AUTH-02, NFR-SEC-03, NFR-SEC-04).
//
// An account is registered with an email address, a display name, and a
// password, once first-run setup has completed (FR-ADMIN-02), when the
// instance's signup policy lets anyone register: with closed, nobody can, and
// with invite, only an invitation creates an account (FR-TEN-02). The policy
// is the one that STORYCREEP_SIGNUP sets (the Signup capability), or else the
// one that setup chose. Registering an address
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
// when a release changes them, signing in replaces a hash made with the old
// ones at the account's next sign-in, once the password has matched it.
//
// The operator, and first-run setup, add accounts without the signup policy
// (Prepare and Insert).
//
// Signing in starts a session (session.go), which the server finds by its
// secret, on every request made with it.
//
// Links verify an account's email address and reset its password
// (email.go): an instance that sends email emails them, and one that sends
// none gives them as text to its instance admins and its operator only.
//
// Registering and signing in are limited (package ratelimit, PRD FR-AUTH-05):
// each email address may be registered only so often, and an address with
// which signing in failed too often in a row is locked for a while, whether
// or not it belongs to an account, so that neither tells whether it does.
package account

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/text/unicode/norm"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/email"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
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
	// ErrSetupRequired is the error of an instance whose first-run setup has
	// not completed, on which nobody can register, whatever the policy, so
	// that the first account is always the one that setup creates (PRD
	// FR-ADMIN-02 AC4).
	ErrSetupRequired = errors.New("account: first-run setup has not completed")
	// ErrSignupClosed is the error of the closed policy, under which nobody
	// can register.
	ErrSignupClosed = errors.New("account: signup is closed")
	// ErrInviteRequired is the error of the invite policy, under which only
	// an invitation creates an account.
	ErrInviteRequired = errors.New("account: signup needs an invitation")
)

// ErrInvalidCredentials is returned by Login when the address belongs to no
// active account, or the password is not the account's.
var ErrInvalidCredentials = errors.New("account: the email address or the password is wrong")

// ErrEmailTaken is returned by Insert for an email address that has an
// account already.
var ErrEmailTaken = errors.New("account: the email address has an account already")

// DB runs a transaction of the application login, as *database.DB does.
type DB interface {
	InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
}

// Options configure a Service.
type Options struct {
	// Signup is the Signup capability: a policy, or config.SignupSetup for
	// the one that first-run setup chose.
	Signup config.SignupPolicy
	// IdleTimeout is how long a session lasts unused, and Lifetime how long
	// it lasts at most. Both must be positive.
	IdleTimeout, Lifetime time.Duration
	// Limits limit how often an email address is registered, and lock an
	// address out of signing in after too many failures in a row. It must
	// be set.
	Limits *ratelimit.Limiter
	// Authenticated, if set, is called by Login once the password has matched
	// the account userID's, which client signs in to, before the session
	// starts, in no transaction. An error fails the sign-in, which then
	// starts no session. The server gives the account its personal workspace
	// there (FR-TEN-01), so it must be safe to call at every sign-in.
	Authenticated func(ctx context.Context, userID ids.UUID, client Client) error
	// Email sends the instance's email, or is nil for an instance that sends
	// none, as in tests.
	Email *email.Service
	// Queue enqueues the jobs that email links. It must be set where Email
	// is.
	Queue Enqueuer
	// PublicURL is the instance's URL, which links open.
	PublicURL config.PublicURL
	// Verified, if set, is called by VerifyEmail once it has verified the
	// email address of the account userID, which client asks for, in no
	// transaction. The server gives the account its personal workspace
	// there, where a workspace needs a verified address (FR-AUTH-03).
	// VerifyEmail returns its error, although the address stays verified and
	// the link used, so a callback whose failure the account's next sign-in
	// mends, as the server's does, logs the failure rather than return it.
	Verified func(ctx context.Context, userID ids.UUID, client Client) error
}

// Service creates accounts, checks their passwords, and keeps their
// sessions.
type Service struct {
	db     DB
	signup config.SignupPolicy
	// idle and lifetime are the sessions' idle timeout and lifetime.
	idle, lifetime time.Duration
	limits         *ratelimit.Limiter
	// authenticated is Options.Authenticated, and verified Options.Verified.
	authenticated, verified func(ctx context.Context, userID ids.UUID, client Client) error
	// email, queue, and publicURL are Options.Email, Options.Queue, and
	// Options.PublicURL.
	email     *email.Service
	queue     Enqueuer
	publicURL config.PublicURL
	hasher    *Hasher
	// decoy is a hash with the current parameters that no password matches
	// (Hasher.decoy), which signing in checks a password against when the
	// address has no account, so that it takes as long as when it has one.
	decoy string
}

// New returns a Service that keeps accounts in db, with opts. It panics if
// a session's idle timeout or lifetime is not positive, or opts has no
// Limits, which are mistakes in the code that calls it.
func New(db DB, opts Options) *Service {
	return newService(db, opts, NewHasher())
}

func newService(db DB, opts Options, h *Hasher) *Service {
	if opts.IdleTimeout <= 0 || opts.Lifetime <= 0 {
		panic(fmt.Sprintf("account: the sessions' idle timeout %v and lifetime %v must be positive", opts.IdleTimeout,
			opts.Lifetime))
	}
	if opts.Limits == nil {
		panic("account: the Service has no Limits")
	}
	return &Service{db: db, signup: opts.Signup, idle: opts.IdleTimeout, lifetime: opts.Lifetime,
		limits: opts.Limits, authenticated: opts.Authenticated, verified: opts.Verified, email: opts.Email,
		queue: opts.Queue, publicURL: opts.PublicURL, hasher: h, decoy: h.decoy()}
}

// Hasher returns the Hasher that the Service hashes passwords with, which
// first-run setup shares, so that the hashes of the process are bounded
// together.
func (s *Service) Hasher() *Hasher {
	return s.hasher
}

// CheckSignup returns ErrSetupRequired until first-run setup has completed,
// and then ErrSignupClosed or ErrInviteRequired unless the signup policy lets
// anyone register: the policy of the Signup capability, or the one that
// setup chose (config.SignupPolicy.Resolve). The API checks it before it
// reads a registration, so that it refuses one whatever the registration
// holds (FR-AUTH-01 AC3, FR-ADMIN-02 AC4).
func (s *Service) CheckSignup(ctx context.Context) error {
	var row queries.InstanceSetupRow
	err := s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		var err error
		row, err = queries.New(tx).InstanceSetup(ctx)
		return err
	})
	switch {
	case err != nil:
		return fmt.Errorf("account: read the instance's setup: %w", err)
	case !row.SetupCompleted:
		return ErrSetupRequired
	}
	switch s.signup.Resolve(config.SignupPolicy(row.SignupPolicy.String)) {
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

// Register creates an account, unless the address has one already, which it
// leaves as it was. Either way it returns nil, after it has done the same
// work, hashing the password included, so that the caller answers the same
// and as late (FR-AUTH-01 AC1). On an instance that sends email, a new
// account is emailed a link that verifies its address (FR-AUTH-03), by a
// job that the transaction that adds the account enqueues, so that the
// answer does not wait for the email; an address that has an account gets
// the token and the job too, without the account, which the job deletes, as
// RequestPasswordReset does. It returns the error of CheckSignup unless the
// policy lets anyone register, input.FieldErrors if the email address or the
// display name is not valid, a *ratelimit.LimitError if the address has been
// registered too often lately (FR-AUTH-05), whether or not it has an
// account, and a *PasswordError if the password breaks the policy, in that
// order, and adds nothing then.
func (s *Service) Register(ctx context.Context, r Registration) error {
	if err := s.CheckSignup(ctx); err != nil {
		return err
	}
	email, name, err := r.fields()
	if err != nil {
		return err
	}
	if _, err := s.limits.Take(ctx, ratelimit.Auth, ratelimit.EmailAddress(email)); err != nil {
		return err
	}
	if err := CheckPassword(r.Password); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(ctx, r.Password)
	if err != nil {
		return err
	}
	verify, err := s.EmailConfigured(ctx)
	if err != nil {
		return err
	}
	return s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		id := ids.New()
		n, err := queries.New(tx).InsertUser(ctx, queries.InsertUserParams{
			ID:           pgtype.UUID{Bytes: id, Valid: true},
			Email:        email,
			PasswordHash: pgtype.Text{String: hash, Valid: true},
			DisplayName:  name,
		})
		switch {
		case err != nil:
			return fmt.Errorf("account: add the account: %w", err)
		case !verify:
			return nil
		case n == 0:
			// The address has an account, which is emailed nothing, but the
			// token and the job are added all the same, without the account,
			// and the job deletes the token, so that the work does not tell
			// whether the address has one.
			id = ids.UUID{}
		}
		return s.enqueueLink(ctx, tx, LinkVerification, id, email)
	})
}

// Account is an account to add with Insert, whose fields Prepare has
// checked, and whose password it has hashed.
type Account struct {
	// ID is the account's new ID.
	ID ids.UUID
	// Email and DisplayName are as the account holds them.
	Email, DisplayName string
	// InstanceAdmin makes the account an instance admin.
	InstanceAdmin bool
	// PersonalWorkspace marks the account's personal workspace as given, as
	// first-run setup does with the workspace that it creates, so that its
	// first sign-in gives it none (FR-TEN-01).
	PersonalWorkspace bool
	hash              string
}

// CheckRegistration checks the registration r as Register does, but for the
// signup policy and the rate limit, which apply only to people who register
// themselves: it returns input.FieldErrors if the email address or the
// display name is not valid, and then a *PasswordError if the password
// breaks the policy.
func CheckRegistration(r Registration) error {
	if _, _, err := r.fields(); err != nil {
		return err
	}
	return CheckPassword(r.Password)
}

// Prepare checks the registration r as CheckRegistration does, and returns
// the account to add, with a new ID and its password hashed with h, for the
// operator's storycreep create-user (FR-ADMIN-06) and first-run setup
// (FR-ADMIN-02), which add accounts whatever the signup policy.
func Prepare(ctx context.Context, h *Hasher, r Registration) (Account, error) {
	email, name, err := r.fields()
	if err != nil {
		return Account{}, err
	}
	if err := CheckPassword(r.Password); err != nil {
		return Account{}, err
	}
	hash, err := h.Hash(ctx, r.Password)
	if err != nil {
		return Account{}, err
	}
	return Account{ID: ids.New(), Email: email, DisplayName: name, hash: hash}, nil
}

// Insert adds the account a in tx, or returns ErrEmailTaken if its address
// has an account already, as citext compares addresses, and adds nothing
// then.
func Insert(ctx context.Context, tx pgx.Tx, a Account) error {
	n, err := queries.New(tx).InsertUser(ctx, queries.InsertUserParams{
		ID:                pgtype.UUID{Bytes: a.ID, Valid: true},
		Email:             a.Email,
		PasswordHash:      pgtype.Text{String: a.hash, Valid: true},
		DisplayName:       a.DisplayName,
		IsInstanceAdmin:   a.InstanceAdmin,
		PersonalWorkspace: a.PersonalWorkspace,
	})
	switch {
	case err != nil:
		return fmt.Errorf("account: add the account: %w", err)
	case n == 0:
		return ErrEmailTaken
	}
	return nil
}

// fields returns the registration's email address and display name as an
// account holds them, without the white space around them, and the address
// in its canonical form (canonicalEmail), or input.FieldErrors.
func (r Registration) fields() (email, name string, err error) {
	var errs input.FieldErrors
	email = canonicalEmail(r.Email)
	if msg := checkEmail(email); msg != "" {
		errs = append(errs, input.FieldError{Field: "email", Message: msg})
	}
	name = strings.TrimSpace(r.DisplayName)
	if msg := checkDisplayName(name); msg != "" {
		errs = append(errs, input.FieldError{Field: "display_name", Message: msg})
	}
	if len(errs) > 0 {
		return "", "", errs
	}
	return email, name, nil
}

// canonicalEmail returns an email address as accounts hold it and look it
// up: without the white space around it, and in Unicode normalization form
// NFC, so that an address whose characters another keyboard or system
// composes differently, such as ž typed as z and a combining caron, is the
// same address, and is not a second account that looks like the first.
// citext compares addresses without regard to case, but not to how their
// characters are composed. NFC changes how characters are composed, and
// replaces the few that Unicode defines as another character written twice,
// such as U+212A KELVIN SIGN, which becomes K, but it keeps the characters
// that the NFKC of a password changes, such as full-width letters, so the
// address stays the one that mail is delivered to: NFC is the form that RFC
// 6532 §3.1 recommends for mail.
func canonicalEmail(email string) string {
	return norm.NFC.String(strings.TrimSpace(email))
}

// checkEmail says what is wrong with an email address, or returns "". It
// takes an address alone, as in jo@example.com, without a name or angle
// brackets, and without a quoted local part or a comment, which mail
// delivery and people rarely handle. Nor does it take a space or a character
// that is not printed, which net/mail accepts outside ASCII, such as U+200B
// ZERO WIDTH SPACE or U+202E, which reverses the text after it, so that no
// address looks like another account's.
func checkEmail(email string) string {
	switch {
	case email == "":
		return "is empty"
	case len(email) > MaxEmailLength:
		return fmt.Sprintf("is longer than %d bytes", MaxEmailLength)
	case strings.IndexFunc(email, func(r rune) bool { return !input.Printed(r) }) >= 0:
		return "contains a space or a character that is not printed"
	}
	if a, err := mail.ParseAddress(email); err != nil || a.Name != "" || a.Address != email {
		return "is not an email address"
	}
	return ""
}

// checkDisplayName says what is wrong with a display name, or returns "", as
// input.CheckName does of a name of at most MaxDisplayNameLength characters.
func checkDisplayName(name string) string {
	return input.CheckName(name, MaxDisplayNameLength)
}

// authenticate checks a sign-in, for Login. It returns as id the ID of the
// active account that the email address belongs to, if password is its
// password, and ErrInvalidCredentials if the address belongs to no account,
// to one that is not active, as a suspended one or one whose deletion is
// pending (FR-GDPR-02), or to one without a password, or if the password is
// another. It takes as long whichever of these it is, because it checks the
// password against a decoy hash, with the current parameters, when it has no
// hash to check it against. The exception is an account whose hash was made
// with other parameters, against which a wrong password takes as long as
// those parameters take: after a release changes them, the time of a failed
// sign-in tells such an account from an address without one, until the
// account's next sign-in replaces its hash. The address is looked up in its
// canonical form (canonicalEmail), and one that registration would refuse
// (checkEmail), which no account has, such as one with a NUL character, is
// not looked up at all.
//
// When the password matches a hash made with other parameters than the
// current ones, authenticate hashes the password again and replaces the
// account's hash (NFR-SEC-03), unless the account's password changed in the
// meantime.
//
// It also returns as account the ID of the account that the address belongs
// to, if one does, whether or not it signs in, which the audit log names as
// the subject of a failed sign-in, and, when it signs in, the hash that the
// account holds now, as far as it knows: the one that the password matched,
// or the one that replaced it. Login starts a session only while the account
// still holds that hash (startSession).
func (s *Service) authenticate(ctx context.Context, email string, password secret.Value) (id, account ids.UUID,
	hash string, err error) {
	row, found, err := s.credentials(ctx, email)
	if err != nil {
		return ids.UUID{}, ids.UUID{}, "", err
	}
	if found {
		account = row.ID.Bytes
	}
	if !found || !row.PasswordHash.Valid || row.Status != "active" {
		if _, _, err := s.hasher.Verify(ctx, s.decoy, password); err != nil {
			return ids.UUID{}, ids.UUID{}, "", err
		}
		return ids.UUID{}, account, "", ErrInvalidCredentials
	}
	hash = row.PasswordHash.String
	match, rehash, err := s.hasher.Verify(ctx, hash, password)
	switch {
	case err != nil:
		return ids.UUID{}, ids.UUID{}, "", err
	case !match:
		return ids.UUID{}, account, "", ErrInvalidCredentials
	case rehash:
		if hash, err = s.rehash(ctx, row.ID, hash, password); err != nil {
			return ids.UUID{}, ids.UUID{}, "", err
		}
	}
	return account, account, hash, nil
}

// credentials returns the credentials of the account that the email address
// belongs to, in its canonical form (canonicalEmail), whatever the account's
// status, and false if it belongs to none. An address that registration
// would refuse (checkEmail), which no account has, such as one with a NUL
// character, is not looked up at all.
func (s *Service) credentials(ctx context.Context, email string) (queries.UserCredentialsRow, bool, error) {
	var row queries.UserCredentialsRow
	var found bool
	if email = canonicalEmail(email); checkEmail(email) != "" {
		return row, false, nil
	}
	err := s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		var err error
		row, err = queries.New(tx).UserCredentials(ctx, email)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		found = err == nil
		return err
	})
	if err != nil {
		return row, false, fmt.Errorf("account: read the account: %w", err)
	}
	return row, found, nil
}

// rehash replaces old, the hash of the account id's password, with a hash of
// password with the current parameters, unless the hash is no longer old. It
// returns the hash that it stored, or old if it stored none.
func (s *Service) rehash(ctx context.Context, id pgtype.UUID, old string, password secret.Value) (string, error) {
	hash, err := s.hasher.Hash(ctx, password)
	if err != nil {
		return "", err
	}
	var n int64
	err = s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		var err error
		n, err = queries.New(tx).UpdatePasswordHash(ctx, queries.UpdatePasswordHashParams{
			NewHash: pgtype.Text{String: hash, Valid: true},
			ID:      id,
			OldHash: pgtype.Text{String: old, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("account: replace the password's hash: %w", err)
		}
		return nil
	})
	switch {
	case err != nil:
		return "", err
	case n == 0:
		return old, nil
	}
	return hash, nil
}
