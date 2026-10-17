package account

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/text/language"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/email"
	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// Email links (PRD FR-AUTH-03). A link verifies an account's email address,
// for 24 hours, or resets its password, for an hour, once (AC2). Its secret
// is made as a session's is, and the database holds only the secret's
// SHA-256 hash, in the email_token table, so that whoever reads the
// database cannot use a link.
//
// An instance that sends email (package email) emails the links: one that
// verifies the address of each account that registers itself, one that
// verifies it again when its user asks, and one that resets the password
// when anyone asks for an address. The email is sent by a job, which makes
// the link's secret when it sends it, so that neither the request nor the
// job's arguments hold the secret, and a request does not wait for the SMTP
// server. The link's lifetime starts then, so that it lasts as long as the
// email says. A request to reset a password does the same work whether or
// not the address belongs to an account (AC3), and so does registering
// (FR-AUTH-01 AC1): each adds a token either way, which the job deletes when
// it has no account. The email names no one but its address: the account's
// display name was chosen by whoever registered the address, who need not be
// its owner.
//
// The job emails an account one link of a kind in ResendInterval at most: it
// sends nothing for a link of an account that was emailed another of that
// kind in that time which still works, so that whoever asks again and again,
// as for an address that is not theirs, cannot flood its inbox, nor spend the
// SMTP server's quota. The request does its work and answers as always, so
// that it still does not tell whether the address has an account, and the
// earlier email's link keeps working. An email that the SMTP server refuses
// for good, as to an address that does not exist, is not attempted again
// (email.Permanent).
//
// An instance that sends no email says so, refuses those requests with
// ErrEmailNotConfigured, the same for every address, and never reaches the
// email's code (AC1). Its links go, as text, only to who may already do what
// they do: an instance admin, who asks the API for one (IssueLink), and the
// operator, who runs storycreep reset-password (IssueResetLink). A
// workspace's owner or admin never gets another user's link, because an
// account spans every workspace that it belongs to (AC5). Where the instance
// sends email, links are emailed only, as the EmailRequired capability
// requires of hosted mode, and an instance admin asks for none
// (ErrEmailConfigured).
//
// Resetting the password signs the account out everywhere, and any change
// of the password, by any route, deletes the account's reset links that have
// not been used (AC4).

// The lifetimes of the links.
const (
	VerificationLifetime  = 24 * time.Hour
	PasswordResetLifetime = time.Hour
)

// ResendInterval is the least time between two emails of one kind of link to
// one account, while the first one's link still works. It is short enough
// that a user whose email did not come can soon ask again, and long enough
// that asking without end sends six emails an hour at most.
const ResendInterval = 10 * time.Minute

// LinkKind is what a link does.
type LinkKind string

// The kinds of link, as the email_token table names them.
const (
	// LinkVerification verifies the account's email address.
	LinkVerification LinkKind = "email_verification"
	// LinkPasswordReset resets the account's password.
	LinkPasswordReset LinkKind = "password_reset"
)

// lifetime returns how long a link of kind k lasts.
func (k LinkKind) lifetime() time.Duration {
	if k == LinkPasswordReset {
		return PasswordResetLifetime
	}
	return VerificationLifetime
}

// path returns the path of the web app's page that a link of kind k opens.
func (k LinkKind) path() string {
	if k == LinkPasswordReset {
		return "/app/reset-password"
	}
	return "/app/verify-email"
}

// The errors of email links.
var (
	// ErrEmailNotConfigured is returned for a request that would email a
	// link, on an instance that sends no email.
	ErrEmailNotConfigured = errors.New("account: the instance sends no email")
	// ErrEmailConfigured is returned by IssueLink on an instance that sends
	// email, whose links go only to the account's address.
	ErrEmailConfigured = errors.New("account: the instance sends email, so its links are emailed")
	// ErrNotInstanceAdmin is returned by IssueLink for a user who is not an
	// active instance admin.
	ErrNotInstanceAdmin = errors.New("account: the user is not an instance admin")
	// ErrTokenInvalid is returned for a link whose token does not exist,
	// or no longer does, as one that a change of the password deleted, or
	// that is another kind of link, or whose account cannot use it, as one
	// that is not active, or whose address is no longer the one that the
	// link verifies.
	ErrTokenInvalid = errors.New("account: the link's token is not valid")
	// ErrTokenUsed is returned for a link that has been used (FR-AUTH-03
	// AC2).
	ErrTokenUsed = errors.New("account: the link has been used")
	// ErrTokenExpired is returned for a link that has expired.
	ErrTokenExpired = errors.New("account: the link has expired")
)

// Enqueuer enqueues a job in the transaction of the change that needs it, as
// *jobs.Queue does.
type Enqueuer interface {
	Enqueue(ctx context.Context, tx pgx.Tx, args jobs.Args, opts *jobs.EnqueueOptions) (jobs.Enqueued, error)
}

// Link is a link that an instance admin or the operator gives as text.
type Link struct {
	Kind LinkKind
	// URL opens the web app's page that uses the link, with the secret in
	// its fragment, which a browser does not send to the server, so that no
	// log of a request holds it. It is a secret.Value, which its caller
	// reveals only where it hands the link out.
	URL secret.Value
	// ExpiresAt is when it expires.
	ExpiresAt time.Time
	// Email is the address of the link's account, as the account holds it.
	Email string
}

// linkURL returns the URL of a link of kind k with the secret value, on the
// instance at u.
func linkURL(u config.PublicURL, k LinkKind, value secret.Value) string {
	l := u.URL()
	l.Path = k.path()
	l.Fragment = "token=" + value.Reveal()
	return l.String()
}

// emailLink is the job that emails the link of a token (sendLink).
type emailLink struct {
	// Token is the token's ID.
	Token string `json:"token"`
}

func (emailLink) Kind() string { return "email_link" }

// emailLinkAttempts is how many times the job tries to email a link: with
// the queue's backoff, which waits an hour at most between attempts, over
// about a day, the life of a verification link. An attempt once the request
// is older than the life of its link sends nothing, and ends the job, and so
// does one whose email the SMTP server refuses for good, which no later
// attempt would send.
const emailLinkAttempts = 36

// emailTokenCleanup is the job that deletes the tokens that have expired.
type emailTokenCleanup struct{}

func (emailTokenCleanup) Kind() string { return "email_token_cleanup" }

// EmailConfigured reports whether the instance sends email.
func (s *Service) EmailConfigured(ctx context.Context) (bool, error) {
	if s.email == nil {
		return false, nil
	}
	return s.email.Configured(ctx)
}

// enqueueLink adds a token of kind for the account userID, or for none if it
// is zero, which the job deletes, for the address address, if there is one,
// with the request's locale, without a secret, and the job that emails its
// link, in tx.
func (s *Service) enqueueLink(ctx context.Context, tx pgx.Tx, kind LinkKind, userID ids.UUID, address string) error {
	id := ids.New()
	_, err := queries.New(tx).InsertEmailToken(ctx, queries.InsertEmailTokenParams{
		ID:       nullUUID(id),
		Kind:     string(kind),
		UserID:   nullUUID(userID),
		Email:    pgtype.Text{String: address, Valid: address != ""},
		Locale:   requestLocale(ctx),
		Lifetime: database.Interval(kind.lifetime()),
	})
	if err != nil {
		return fmt.Errorf("account: add the link's token: %w", err)
	}
	return s.enqueue(ctx, tx, id)
}

// enqueue enqueues the job that emails the link of the token id, in tx.
func (s *Service) enqueue(ctx context.Context, tx pgx.Tx, id ids.UUID) error {
	if s.queue == nil {
		return errors.New("account: the Service has no job queue to email a link with")
	}
	_, err := s.queue.Enqueue(ctx, tx, emailLink{Token: id.String()}, &jobs.EnqueueOptions{MaxAttempts: emailLinkAttempts})
	return err
}

// requestLocale returns the locale of the request of ctx, which a link's
// email is written in.
func requestLocale(ctx context.Context) pgtype.Text {
	return pgtype.Text{String: i18n.FromContext(ctx).Locale().String(), Valid: true}
}

// RequestPasswordReset emails a link that resets the password of the active
// account that the address email belongs to, if any. It does the same work
// whether or not the address belongs to one, and returns nil either way, so
// that its caller answers the same, as soon (FR-AUTH-03 AC3). It returns
// ErrEmailNotConfigured on an instance that sends no email, and a
// *ratelimit.LimitError if the address has been asked for too often lately,
// whether or not it has an account, and does nothing then.
func (s *Service) RequestPasswordReset(ctx context.Context, address string) error {
	configured, err := s.EmailConfigured(ctx)
	switch {
	case err != nil:
		return err
	case !configured:
		return ErrEmailNotConfigured
	}
	address = CanonicalEmail(address)
	if CheckEmail(address) != "" {
		// No account has an address that registration would refuse, and one
		// with a NUL character could not even be looked up.
		address = ""
	}
	if _, err := s.limits.Take(ctx, ratelimit.Auth, ratelimit.EmailAddress(address)); err != nil {
		return err
	}
	return s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		id := ids.New()
		if err := queries.New(tx).InsertPasswordResetRequest(ctx, queries.InsertPasswordResetRequestParams{
			ID: nullUUID(id), Email: address, Locale: requestLocale(ctx),
			Lifetime: database.Interval(PasswordResetLifetime)}); err != nil {
			return fmt.Errorf("account: add the request's token: %w", err)
		}
		return s.enqueue(ctx, tx, id)
	})
}

// RequestVerification emails a link that verifies the email address of the
// active account userID, unless the address is verified already, when it
// sends nothing. It returns ErrEmailNotConfigured on an instance that sends
// no email, and ErrNoSession if the account is not active.
func (s *Service) RequestVerification(ctx context.Context, userID ids.UUID) error {
	configured, err := s.EmailConfigured(ctx)
	switch {
	case err != nil:
		return err
	case !configured:
		return ErrEmailNotConfigured
	}
	return s.db.InTx(ctx, database.Scope{UserID: userID}, func(tx pgx.Tx) error {
		row, err := queries.New(tx).UserEmail(ctx, nullUUID(userID))
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrNoSession
		case err != nil:
			return fmt.Errorf("account: read the email address: %w", err)
		case row.Verified:
			return nil
		}
		return s.enqueueLink(ctx, tx, LinkVerification, userID, row.Email)
	})
}

// sendLink is the work of the job that emails the link of the token id: it
// makes the link's secret, holds its hash in the token, which then expires
// after the link's lifetime, and sends the email to the account's address,
// in the account's preferred locale, or else the request's. A token that has
// been used, or was asked for longer ago than its link's lifetime, or whose
// account is no longer active, or no longer has the address that it
// verifies, or has verified it since, or was emailed another link of the
// kind in the last ResendInterval that still works, is sent no email, and a
// token without an account, of a reset requested for an address of no
// account or of a registration of an address that has one, is deleted. An
// instance that no longer sends email sends none either. An attempt that
// fails to send the email is made again later, with a new secret, which
// replaces the one that the failed attempt may have sent, so that only the
// latest email's link works, unless the SMTP server refuses the email for
// good (email.Permanent), which ends the job. The job reads the SMTP
// settings, as it does the token, on the workers' pool that it carries, db.
func (s *Service) sendLink(ctx context.Context, db DB, id ids.UUID) error {
	if s.email == nil {
		return nil
	}
	mailer := s.email.WithDB(db)
	settings, configured, err := mailer.Settings(ctx)
	if err != nil || !configured {
		return err
	}
	value, hash := NewSecret()
	var row queries.LockEmailTokenToSendRow
	found := false
	err = db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		// The account is locked first, so that the job of another of its
		// links, which waits for this one, finds the email that this one
		// makes, and sends none within ResendInterval.
		if err := q.LockEmailTokenAccount(ctx, nullUUID(id)); err != nil {
			return err
		}
		var err error
		row, err = q.LockEmailTokenToSend(ctx, queries.LockEmailTokenToSendParams{ID: nullUUID(id),
			PasswordResetLifetime: database.Interval(PasswordResetLifetime),
			VerificationLifetime:  database.Interval(VerificationLifetime),
			ResendInterval:        database.Interval(ResendInterval)})
		if errors.Is(err, pgx.ErrNoRows) {
			return q.DeleteUnclaimedToken(ctx, nullUUID(id))
		}
		if err != nil {
			return err
		}
		found = true
		return q.SetEmailTokenHash(ctx, queries.SetEmailTokenHashParams{TokenHash: hash,
			Lifetime: database.Interval(LinkKind(row.Kind).lifetime()), ID: nullUUID(id)})
	})
	if err != nil {
		return fmt.Errorf("account: prepare the link's email: %w", err)
	}
	if !found {
		return nil
	}
	msg := linkEmail(s.publicURL, LinkKind(row.Kind), row.Locale.String, value, row.Email)
	if err := mailer.Deliver(ctx, settings, msg); err != nil {
		return email.JobError(ctx, fmt.Errorf("account: email the link: %w", err))
	}
	return nil
}

// linkEmail returns the email of a link of kind, with the secret value, to
// the address address, in locale, a BCP 47 tag, or the source locale if the
// server has no catalog for it. It names the recipient by their address
// alone, never by the account's display name, which whoever registered the
// address chose, so that someone who registers an address that is not
// theirs cannot put text of their own in the email that its owner receives.
func linkEmail(u config.PublicURL, kind LinkKind, locale string, value secret.Value, address string) email.Message {
	tag, err := language.Parse(locale)
	if err != nil {
		tag = i18n.Source
	}
	l := i18n.Default().Localizer(tag)
	to := mail.Address{Address: address}
	args := i18n.Args{"site": u.URL().Host, "url": linkURL(u, kind, value)}
	if kind == LinkPasswordReset {
		args["minutes"] = int(PasswordResetLifetime / time.Minute)
		return email.Message{To: to,
			Subject: l.Format(i18n.Message{Key: i18n.K("email.password_reset.subject")}),
			Body:    l.Format(i18n.Message{Key: i18n.K("email.password_reset.body"), Args: args})}
	}
	args["hours"] = int(VerificationLifetime / time.Hour)
	return email.Message{To: to,
		Subject: l.Format(i18n.Message{Key: i18n.K("email.email_verification.subject")}),
		Body:    l.Format(i18n.Message{Key: i18n.K("email.email_verification.body"), Args: args})}
}

// tokenState returns the token whose secret is value, locked until tx ends,
// or the error that using it as a link of kind is answered with:
// ErrTokenInvalid, ErrTokenUsed, or ErrTokenExpired, in that order.
func tokenState(ctx context.Context, tx pgx.Tx, value secret.Value, kind LinkKind) (queries.LockEmailTokenRow,
	error) {
	hash, ok := SecretHash(value)
	if !ok {
		return queries.LockEmailTokenRow{}, ErrTokenInvalid
	}
	row, err := queries.New(tx).LockEmailToken(ctx, hash)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return row, ErrTokenInvalid
	case err != nil:
		return row, fmt.Errorf("account: read the link's token: %w", err)
	case LinkKind(row.Kind) != kind || !row.Active || kind == LinkVerification && !row.SameEmail:
		return row, ErrTokenInvalid
	case row.Used:
		return row, ErrTokenUsed
	case row.Expired:
		return row, ErrTokenExpired
	}
	return row, nil
}

// VerifyEmail uses the link whose secret is value to verify the email
// address of its account, which client asks for, and records that in the
// audit log. It returns ErrTokenInvalid, ErrTokenUsed, or ErrTokenExpired
// for a link that cannot verify it (tokenState). It locks the account before
// the token, as the job that emails a link does, so that the account stays
// as tokenState finds it until the address is verified. A link of an address
// that another link verified already is used, but records nothing, because
// nothing changes. Once the address is verified, it calls Options.Verified,
// and returns its error, although the address stays verified, and the link
// used.
func (s *Service) VerifyEmail(ctx context.Context, value secret.Value, client Client) error {
	var userID ids.UUID
	err := s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		if hash, ok := SecretHash(value); ok {
			if err := q.LockEmailTokenAccountByHash(ctx, hash); err != nil {
				return fmt.Errorf("account: lock the link's account: %w", err)
			}
		}
		row, err := tokenState(ctx, tx, value, LinkVerification)
		if err != nil {
			return err
		}
		userID = row.UserID.Bytes
		// The account is active, and still has the address, which the lock
		// keeps it from changing, so no row changes only if the address is
		// verified already.
		n, err := q.VerifyEmail(ctx, queries.VerifyEmailParams{ID: row.UserID, Email: row.Email.String})
		if err != nil {
			return fmt.Errorf("account: verify the email address: %w", err)
		}
		if err := q.UseEmailToken(ctx, row.ID); err != nil {
			return fmt.Errorf("account: use the link's token: %w", err)
		}
		if n == 0 {
			return nil
		}
		return audit.Record(ctx, tx, audit.Entry{Action: audit.ActionEmailVerify, ActorUserID: userID,
			SubjectID: userID, Result: audit.ResultSuccess, IP: client.IP, UserAgent: client.UserAgent})
	})
	if err != nil || s.verified == nil {
		return err
	}
	return s.verified(ctx, userID, client)
}

// ResetPassword uses the link whose secret is value to replace the password
// of its account with password, which client asks for. It returns
// ErrTokenInvalid, ErrTokenUsed, or ErrTokenExpired for a link that cannot
// reset it (tokenState), and then a *PasswordError if password breaks the
// policy, which leaves the link as it was. Otherwise, in one transaction, it
// replaces the password's hash, uses the link, deletes the account's other
// reset links (FR-AUTH-03 AC4), revokes every session of the account,
// which signs it out everywhere, and records all of that in the audit log.
func (s *Service) ResetPassword(ctx context.Context, value, password secret.Value, client Client) error {
	var row queries.LockEmailTokenRow
	err := s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		var err error
		row, err = tokenState(ctx, tx, value, LinkPasswordReset)
		return err
	})
	if err != nil {
		return err
	}
	if err := CheckPassword(password); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(ctx, password)
	if err != nil {
		return err
	}
	userID := ids.UUID(row.UserID.Bytes)
	return s.db.InTx(ctx, database.Scope{UserID: userID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		// The account's row is locked first, as a change of the password
		// locks it before the account's reset links, so that the two wait
		// for each other rather than deadlock.
		n, err := q.SetPasswordHash(ctx, queries.SetPasswordHashParams{
			PasswordHash: pgtype.Text{String: hash, Valid: true}, ID: row.UserID})
		switch {
		case err != nil:
			return fmt.Errorf("account: replace the password's hash: %w", err)
		case n == 0:
			return ErrTokenInvalid
		}
		// Another use of the link, or a change of the password, may have
		// come first.
		if _, err := tokenState(ctx, tx, value, LinkPasswordReset); err != nil {
			return err
		}
		if err := q.UseEmailToken(ctx, row.ID); err != nil {
			return fmt.Errorf("account: use the link's token: %w", err)
		}
		if err := q.DeleteResetTokens(ctx, row.UserID); err != nil {
			return fmt.Errorf("account: delete the other reset links: %w", err)
		}
		// A Session without an ID keeps none of the account's sessions.
		if _, err := s.revokeOthers(ctx, tx, Session{UserID: userID}, revokedByPasswordReset, client); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: audit.ActionPasswordReset, ActorUserID: userID,
			SubjectID: userID, Result: audit.ResultSuccess, IP: client.IP, UserAgent: client.UserAgent})
	})
}

// IssueLink returns a link of kind for the active account that the address
// email belongs to, as text, for the instance admin adminID, which client
// asks for, and records that in the audit log, on an instance that sends no
// email (FR-AUTH-03 AC1). It returns ErrNotInstanceAdmin unless adminID is an
// active instance admin, then ErrEmailConfigured on an instance that sends
// email, and then ErrNoAccount or ErrNotActive.
func (s *Service) IssueLink(ctx context.Context, adminID ids.UUID, kind LinkKind, address string,
	client Client) (Link, error) {
	if err := s.CheckInstanceAdmin(ctx, adminID); err != nil {
		return Link{}, err
	}
	configured, err := s.EmailConfigured(ctx)
	switch {
	case err != nil:
		return Link{}, err
	case configured:
		return Link{}, ErrEmailConfigured
	}
	var link Link
	err = s.db.InTx(ctx, database.Scope{UserID: adminID}, func(tx pgx.Tx) error {
		var err error
		link, err = issueLink(ctx, tx, s.publicURL, kind, address, adminID, client)
		return err
	})
	return link, err
}

// CheckInstanceAdmin returns ErrNotInstanceAdmin unless the user userID is an
// active instance admin.
func (s *Service) CheckInstanceAdmin(ctx context.Context, userID ids.UUID) error {
	var admin bool
	err := s.db.InTx(ctx, database.Scope{UserID: userID}, func(tx pgx.Tx) error {
		var err error
		admin, err = queries.New(tx).InstanceAdmin(ctx, nullUUID(userID))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	switch {
	case err != nil:
		return fmt.Errorf("account: read whether the user is an instance admin: %w", err)
	case !admin:
		return ErrNotInstanceAdmin
	}
	return nil
}

// IssueResetLink returns a link that resets the password of the active
// account that the address email belongs to, as text, for the operator's
// storycreep reset-password (FR-ADMIN-06), on the instance at u, and
// records that in the audit log, without an actor. It does so whether or not
// the instance sends email: the operator, who can change any account in the
// database, acts for themselves. It returns ErrNoAccount or ErrNotActive.
func IssueResetLink(ctx context.Context, db DB, u config.PublicURL, address string) (Link, error) {
	var link Link
	err := db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		var err error
		link, err = issueLink(ctx, tx, u, LinkPasswordReset, address, ids.UUID{}, Client{})
		return err
	})
	return link, err
}

// issueLink adds a token of kind, with a secret, for the active account that
// the address email belongs to, in tx, and records that the user actorID,
// or the operator if it is zero, took its link as text, which client asks
// for.
func issueLink(ctx context.Context, tx pgx.Tx, u config.PublicURL, kind LinkKind, address string, actorID ids.UUID,
	client Client) (Link, error) {
	account, err := find(ctx, tx, address)
	if err != nil {
		return Link{}, err
	}
	// The account's address as it holds it, which a verification verifies,
	// and the link names.
	stored, err := queries.New(tx).UserEmail(ctx, account.ID)
	if err != nil {
		return Link{}, fmt.Errorf("account: read the email address: %w", err)
	}
	var verified pgtype.Text
	if kind == LinkVerification {
		verified = pgtype.Text{String: stored.Email, Valid: true}
	}
	value, hash := NewSecret()
	expires, err := queries.New(tx).InsertEmailToken(ctx, queries.InsertEmailTokenParams{
		ID:        nullUUID(ids.New()),
		Kind:      string(kind),
		UserID:    account.ID,
		Email:     verified,
		TokenHash: hash,
		Lifetime:  database.Interval(kind.lifetime()),
	})
	if err != nil {
		return Link{}, fmt.Errorf("account: add the link's token: %w", err)
	}
	if err := audit.Record(ctx, tx, audit.Entry{Action: audit.ActionLinkIssue, ActorUserID: actorID,
		SubjectID: account.ID.Bytes, Result: audit.ResultSuccess, IP: client.IP, UserAgent: client.UserAgent,
		Metadata: map[string]any{"kind": string(kind)}}); err != nil {
		return Link{}, err
	}
	return Link{Kind: kind, URL: secret.New(linkURL(u, kind, value)), ExpiresAt: expires.Time, Email: stored.Email},
		nil
}

// cleanUpEmailTokens deletes the tokens that have expired, in batches, each
// in a transaction of its own, and returns how many it deleted. A link that
// has expired is refused whether or not its token exists, so this changes
// nothing that a request sees but the error of a used link that has also
// expired, and is safe to repeat.
func cleanUpEmailTokens(ctx context.Context, db DB) (int64, error) {
	var total int64
	for {
		var n int64
		err := db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
			var err error
			n, err = queries.New(tx).DeleteExpiredEmailTokens(ctx, cleanupBatch)
			return err
		})
		if err != nil {
			return total, fmt.Errorf("account: delete the expired links' tokens: %w", err)
		}
		total += n
		if n < cleanupBatch {
			return total, nil
		}
	}
}
