// Package setup completes the first-run setup of an instance (PRD
// FR-ADMIN-02): on a database whose setup is pending, it creates the first
// owner's account and the first workspace, and records the signup policy
// that setup chose. Until setup has completed, nobody can register an
// account, whatever the policy (package account), so that the first account
// is always the owner that setup creates (AC4).
//
// Setup completes once. The instance's row (the instance table, which has
// exactly one) records when it completed, and Complete locks that row in the
// transaction that completes setup, so that of two setups at once, the
// second finds setup completed and changes nothing (AC3). Once it has
// completed, the setup API answers 404 for everyone (AC1).
//
// Setup also records the SMTP server that the instance sends its email
// through, if it is given one (package email), which STORYCREEP_SMTP_URL
// overrides for as long as it is set, as STORYCREEP_SIGNUP does the signup
// policy.
//
// The owner is an instance admin, and the first workspace is the owner's
// personal workspace, so that the owner's first sign-in gives them no other
// (FR-TEN-01). Its default locale is the instance's, STORYCREEP_DEFAULT_LOCALE,
// and the audit log records its creation, as it does any workspace's.
//
// Completing setup through the API needs the setup code, which only who can
// read the server's log learns: the server writes it there while setup is
// pending (Code), so that whoever reaches a new instance first cannot make
// themselves its instance admin. The code is made of a key that migrating
// generated for the database, so every server of an instance logs the same
// one, and it never changes. The operator can instead complete setup
// unattended, from the STORYCREEP_SETUP_ configuration keys, which makes the
// same changes as the API (Unattended, AC2) and needs no code.
package setup

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/text/language"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/workspace"
)

var (
	// ErrCompleted is returned for a setup of an instance whose setup has
	// completed already.
	ErrCompleted = errors.New("setup: first-run setup has completed already")
	// ErrInvalidCode is returned by Complete for a setup code that is not
	// the instance's.
	ErrInvalidCode = errors.New("setup: the setup code is wrong")
)

// DB runs a transaction of the application login, as *database.DB does.
type DB interface {
	InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
}

// Options configure a Service.
type Options struct {
	// Signup is the Signup capability: a policy that the configuration sets,
	// which overrides the one that setup records, or config.SignupSetup.
	Signup config.SignupPolicy
	// SMTPFixed says that the configuration sets the SMTP server, which
	// overrides the one that setup records.
	SMTPFixed bool
	// DefaultLocale is the instance's default locale, which the first
	// workspace has.
	DefaultLocale language.Tag
	// Workspaces creates the first workspace.
	Workspaces *workspace.Service
	// Hasher hashes the owner's password.
	Hasher *account.Hasher
}

// Service completes first-run setup.
type Service struct {
	db         DB
	signup     config.SignupPolicy
	smtpFixed  bool
	locale     language.Tag
	workspaces *workspace.Service
	hasher     *account.Hasher
}

// New returns a Service that completes the setup of the instance of db, with
// opts.
func New(db DB, opts Options) *Service {
	return &Service{db: db, signup: opts.Signup, smtpFixed: opts.SMTPFixed, locale: opts.DefaultLocale,
		workspaces: opts.Workspaces, hasher: opts.Hasher}
}

// Status is the state of first-run setup.
type Status struct {
	// Completed says whether setup has completed.
	Completed bool
	// SignupFixed says whether the configuration sets the signup policy,
	// which then overrides the one that setup records, for as long as it
	// sets it.
	SignupFixed bool
	// Signup is the policy that the configuration sets, if it sets one, or
	// else the one that setup records when it is given none: closed.
	Signup config.SignupPolicy
	// SMTPFixed says whether the configuration sets the SMTP server, which
	// then overrides the one that setup records, for as long as it sets it.
	SMTPFixed bool
}

// Status returns the state of first-run setup.
func (s *Service) Status(ctx context.Context) (Status, error) {
	var row queries.InstanceSetupRow
	err := s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		var err error
		row, err = queries.New(tx).InstanceSetup(ctx)
		return err
	})
	if err != nil {
		return Status{}, fmt.Errorf("setup: read the instance's setup: %w", err)
	}
	return Status{Completed: row.SetupCompleted, SignupFixed: s.signup != config.SignupSetup,
		Signup: s.signup.Resolve(""), SMTPFixed: s.smtpFixed}, nil
}

// codeAlphabet is the alphabet of the setup code: Crockford's base32, which
// leaves out I, L, O, and U, so that a code read from a log is not misread.
const codeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// codeBytes is how many bytes of the key the setup code is made of: 80
// bits, which nobody guesses, let alone at the rate that the rate limit of
// setup allows.
const codeBytes = 10

// Code returns the setup code, as the server logs it: 16 characters of
// Crockford's base32, in four groups of four joined by hyphens, as
// 7K2M-Q9XD-4RTB-H8VW.
func (s *Service) Code(ctx context.Context) (string, error) {
	var key []byte
	err := s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		var err error
		key, err = queries.New(tx).SetupCodeKey(ctx)
		return err
	})
	if err != nil {
		return "", fmt.Errorf("setup: read the key of the setup code: %w", err)
	}
	return codeOf(key), nil
}

// codeOf returns the setup code made of key, which has 32 bytes: its first
// codeBytes bytes in codeAlphabet, 5 bits a character, in groups of four
// characters joined by hyphens.
func codeOf(key []byte) string {
	var b strings.Builder
	var acc uint64
	bits, n := 0, 0
	for _, c := range key[:codeBytes] {
		acc = acc<<8 | uint64(c)
		for bits += 8; bits >= 5; n++ {
			bits -= 5
			if n > 0 && n%4 == 0 {
				b.WriteByte('-')
			}
			b.WriteByte(codeAlphabet[acc>>bits&31])
		}
	}
	return b.String()
}

// normalizeCode returns a setup code as someone typed it, as codeOf writes
// it but without its hyphens: in upper case, without hyphens and spaces, and
// with O read as 0, and I and L as 1, as Crockford's base32 reads them.
func normalizeCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		switch r {
		case '-', ' ':
			continue
		case 'O':
			r = '0'
		case 'I', 'L':
			r = '1'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Request is what first-run setup completes setup with.
type Request struct {
	// Code is the setup code, which only the API asks for.
	Code string
	// Owner is the first owner's account.
	Owner account.Registration
	// WorkspaceName and WorkspaceSlug are the first workspace's name and
	// slug, or "" to derive the slug from the name, as for any workspace
	// (workspace.Settings).
	WorkspaceName, WorkspaceSlug string
	// Signup is the signup policy that setup records, or "" for closed.
	Signup config.SignupPolicy
	// SMTP is the SMTP server that setup records, or nil for none.
	SMTP *config.SMTP
}

// Result is what first-run setup created.
type Result struct {
	// OwnerID is the ID of the owner's account.
	OwnerID ids.UUID
	// Workspace is the first workspace.
	Workspace workspace.Workspace
}

// Complete completes first-run setup with req, which client asks for, once
// it has checked that req.Code is the setup code. It returns ErrCompleted if
// setup has completed already, then ErrInvalidCode if the code is wrong, and
// then what is wrong with the request (check), or ErrSlugTaken if another
// workspace has the workspace's slug, and changes nothing then.
func (s *Service) Complete(ctx context.Context, req Request, client audit.Client) (Result, error) {
	if err := s.pending(ctx); err != nil {
		return Result{}, err
	}
	if err := s.checkCode(ctx, req.Code); err != nil {
		return Result{}, err
	}
	return s.complete(ctx, req, client)
}

// pending returns ErrCompleted if setup has completed, and nil if it is
// pending.
func (s *Service) pending(ctx context.Context) error {
	st, err := s.Status(ctx)
	switch {
	case err != nil:
		return err
	case st.Completed:
		return ErrCompleted
	}
	return nil
}

// checkCode returns ErrInvalidCode unless code is the setup code, as
// normalizeCode reads it. It compares hashes, so that it takes as long
// whatever the code's length, and as long whichever of its characters is
// wrong.
func (s *Service) checkCode(ctx context.Context, code string) error {
	want, err := s.Code(ctx)
	if err != nil {
		return err
	}
	got, wantSum := sha256.Sum256([]byte(normalizeCode(code))), sha256.Sum256([]byte(normalizeCode(want)))
	if subtle.ConstantTimeCompare(got[:], wantSum[:]) != 1 {
		return ErrInvalidCode
	}
	return nil
}

// check returns the first workspace to create, or what is wrong with req,
// without its code: input.FieldErrors for the fields of the owner's account,
// as account.CheckRegistration names them but under owner., as owner.email,
// for the workspace's name, as workspace.name, for the signup policy, as
// signup_policy, and for the SMTP server's settings, under smtp., as
// smtp.host; then a *workspace.SlugError if the workspace's slug is not
// valid; and then an *account.PasswordError if the owner's password breaks
// the policy. Completing setup adds that the owner's email address must
// belong to no account, as owner.email.
func (s *Service) check(ctx context.Context, req Request) (workspace.Draft, error) {
	var fields input.FieldErrors
	var slug *workspace.SlugError
	owner := account.CheckRegistration(req.Owner)
	fields = append(fields, prefixed("owner.", owner)...)
	d, err := s.workspaces.Check(ctx, workspace.Settings{Name: req.WorkspaceName, Slug: req.WorkspaceSlug,
		DefaultLocale: s.locale.String()})
	fields = append(fields, prefixed("workspace.", err)...)
	switch req.Signup {
	case "", config.SignupClosed, config.SignupInvite, config.SignupOpen:
	default:
		fields = append(fields, input.FieldError{Field: "signup_policy", Message: "is not closed, invite, or open"})
	}
	if req.SMTP != nil {
		for _, p := range req.SMTP.Check() {
			fields = append(fields, input.FieldError{Field: "smtp." + p.Field, Message: p.Msg})
		}
	}
	switch {
	case len(fields) > 0:
		return workspace.Draft{}, fields
	case errors.As(err, &slug):
		return workspace.Draft{}, slug
	case err != nil:
		return workspace.Draft{}, err
	}
	return d, owner
}

// prefixed returns the field errors of err, if it is input.FieldErrors, each
// with its field's name after prefix.
func prefixed(prefix string, err error) input.FieldErrors {
	var fields input.FieldErrors
	if !errors.As(err, &fields) {
		return nil
	}
	out := make(input.FieldErrors, len(fields))
	for i, f := range fields {
		out[i] = input.FieldError{Field: prefix + f.Field, Message: f.Message}
	}
	return out
}

// complete completes setup with req, without its code, as Complete does.
func (s *Service) complete(ctx context.Context, req Request, client audit.Client) (Result, error) {
	d, err := s.check(ctx, req)
	if err != nil {
		return Result{}, err
	}
	owner, err := account.Prepare(ctx, s.hasher, req.Owner)
	if err != nil {
		return Result{}, err
	}
	owner.InstanceAdmin, owner.PersonalWorkspace = true, true
	signup := req.Signup
	if signup == "" {
		signup = config.SignupClosed
	}
	var res Result
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: d.ID(), UserID: owner.ID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		row, err := q.LockInstanceSetup(ctx)
		switch {
		case err != nil:
			return fmt.Errorf("setup: read the instance's setup: %w", err)
		case row.SetupCompleted:
			return ErrCompleted
		}
		if err := account.Insert(ctx, tx, owner); errors.Is(err, account.ErrEmailTaken) {
			return input.FieldErrors{{Field: "owner.email", Message: "belongs to an account already"}}
		} else if err != nil {
			return err
		}
		w, err := s.workspaces.Insert(ctx, tx, d, owner.ID, true, client)
		if err != nil {
			return err
		}
		if err := q.CompleteSetup(ctx, completeParams(signup, req.SMTP)); err != nil {
			return fmt.Errorf("setup: record that setup completed: %w", err)
		}
		res = Result{OwnerID: owner.ID, Workspace: w}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

// completeParams returns what CompleteSetup records: the signup policy, and
// the SMTP server, if setup has one, with its host in lower case, as
// config.ParseSMTPURL reads it, so that the API and unattended setup record
// the same host.
func completeParams(signup config.SignupPolicy, smtp *config.SMTP) queries.CompleteSetupParams {
	p := queries.CompleteSetupParams{SignupPolicy: pgtype.Text{String: string(signup), Valid: true}}
	if smtp == nil {
		return p
	}
	text := func(v string) pgtype.Text { return pgtype.Text{String: v, Valid: v != ""} }
	p.SmtpHost, p.SmtpSecurity = text(strings.ToLower(smtp.Host)), text(string(smtp.Security))
	p.SmtpFrom = text(strings.TrimSpace(smtp.From))
	p.SmtpPort = pgtype.Int4{Int32: int32(smtp.Port), Valid: true}
	p.SmtpUsername, p.SmtpPassword = text(smtp.Username), text(smtp.Password.Reveal())
	return p
}

// The configuration keys of unattended setup, which its errors name.
const (
	keyOwnerEmail    = "STORYCREEP_SETUP_OWNER_EMAIL"
	keyOwnerName     = "STORYCREEP_SETUP_OWNER_NAME"
	keyOwnerPassword = "STORYCREEP_SETUP_OWNER_PASSWORD"
	keyWorkspaceName = "STORYCREEP_SETUP_WORKSPACE_NAME"
	keyWorkspaceSlug = "STORYCREEP_SETUP_WORKSPACE_SLUG"
	keySignup        = "STORYCREEP_SETUP_SIGNUP"
	keySMTPURL       = "STORYCREEP_SETUP_SMTP_URL"
	keySMTPFrom      = "STORYCREEP_SETUP_SMTP_FROM"
)

// fieldKeys maps the fields that Check names to the keys of unattended
// setup that set them.
var fieldKeys = map[string]string{
	"owner.email":        keyOwnerEmail,
	"owner.display_name": keyOwnerName,
	"workspace.name":     keyWorkspaceName,
	"signup_policy":      keySignup,
	"smtp.host":          keySMTPURL,
	"smtp.port":          keySMTPURL,
	"smtp.security":      keySMTPURL,
	"smtp.username":      keySMTPURL,
	"smtp.password":      keySMTPURL,
	"smtp.from":          keySMTPFrom,
}

// ConfigError is a configuration that unattended setup refuses, as the
// setup API refuses a request: each of its problems names the key whose
// value is not valid.
type ConfigError struct {
	Problems []config.Problem
}

func (e *ConfigError) Error() string {
	msgs := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		msgs[i] = p.String()
	}
	return "unattended first-run setup refuses the configuration: " + strings.Join(msgs, "; ")
}

// Unattended completes first-run setup with u, the configuration's
// STORYCREEP_SETUP_ keys, if setup is pending, as Complete would complete
// it, but without a setup code or a client, and reports whether it did. It
// returns false, and no error, if setup has completed already, as when
// another server of the instance completed it first. It returns a
// *ConfigError if Complete would refuse a value of the keys.
func (s *Service) Unattended(ctx context.Context, u *config.UnattendedSetup) (bool, error) {
	if err := s.pending(ctx); errors.Is(err, ErrCompleted) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	req, err := unattendedRequest(u)
	if err != nil {
		return false, err
	}
	_, err = s.complete(ctx, req, audit.Client{})
	if errors.Is(err, ErrCompleted) {
		return false, nil
	}
	if err != nil {
		return false, configError(err)
	}
	return true, nil
}

// CheckUnattended checks the values of u, the configuration's
// STORYCREEP_SETUP_ keys, as Unattended checks them, but without the
// database, for storycreep validate-config, with the instance's default
// locale, locale, which the first workspace would have. It returns a
// *ConfigError for a value that unattended setup would refuse. Only
// Unattended finds that the owner's address has an account already, or that
// another workspace has the slug, which the database holds.
func CheckUnattended(u *config.UnattendedSetup, locale language.Tag) error {
	req, err := unattendedRequest(u)
	if err != nil {
		return err
	}
	// Checking the request reads nothing from the database, and adds no
	// member, whose limit the entitlements would check.
	s := &Service{locale: locale, workspaces: workspace.New(nil,
		workspace.Options{Entitlements: entitlements.New(entitlements.Options{})})}
	if _, err := s.check(context.Background(), req); err != nil {
		return configError(err)
	}
	return nil
}

// unattendedRequest returns the request that unattended setup completes
// setup with u, or a *ConfigError if the owner's password is longer than any
// password may be, as the setup API's schema refuses it.
func unattendedRequest(u *config.UnattendedSetup) (Request, error) {
	if utf8.RuneCountInString(u.OwnerPassword.Reveal()) > account.MaxPasswordLength {
		return Request{}, &ConfigError{Problems: []config.Problem{{Key: keyOwnerPassword,
			Msg: fmt.Sprintf("is longer than %d characters", account.MaxPasswordLength)}}}
	}
	return Request{
		Owner:         account.Registration{Email: u.OwnerEmail, DisplayName: u.OwnerName, Password: u.OwnerPassword},
		WorkspaceName: u.WorkspaceName,
		WorkspaceSlug: u.WorkspaceSlug,
		Signup:        u.Signup,
		SMTP:          u.SMTP,
	}, nil
}

// configError returns err, an error of complete, as a *ConfigError that
// names the key of each problem, or err itself if it names no key's value.
func configError(err error) error {
	var fields input.FieldErrors
	var weak *account.PasswordError
	var slug *workspace.SlugError
	var problems []config.Problem
	switch {
	case errors.As(err, &fields):
		for _, f := range fields {
			key, ok := fieldKeys[f.Field]
			if !ok {
				return err
			}
			problems = append(problems, config.Problem{Key: key, Msg: f.Message})
		}
	case errors.As(err, &weak):
		problems = append(problems, config.Problem{Key: keyOwnerPassword, Msg: weak.Problem()})
	case errors.As(err, &slug):
		problems = append(problems, config.Problem{Key: keyWorkspaceSlug, Msg: strings.TrimPrefix(slug.Error(),
			"workspace: ")})
	case errors.Is(err, workspace.ErrSlugTaken):
		problems = append(problems, config.Problem{Key: keyWorkspaceSlug, Msg: "is another workspace's slug"})
	default:
		return err
	}
	return &ConfigError{Problems: problems}
}
