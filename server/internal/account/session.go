package account

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// Sessions (PRD FR-AUTH-02, NFR-SEC-04). Signing in starts a session, which
// a secret identifies: 32 bytes from the operating system's
// cryptographically secure random source, which only the client holds, in
// unpadded base64url. The database holds the secret's SHA-256 hash only
// (AC3), so that whoever reads the database cannot use its sessions.
//
// Every request made with a session finds it by its secret in the database
// (Service.Session), so a session that is revoked or ends is refused by every
// server from the next request on (AC1). A session ends when its user signs
// out, which deletes it (AC2), when it is revoked, when it has not been used
// for the idle timeout, or when the lifetime has passed since its user signed
// in, however often it is used. Changing the account's password revokes its
// other sessions (AC4), and a sign-in that checked the old password starts
// none afterwards (startSession). The change also gives the session that
// made it a new secret, as a change of what a session may do should
// (NFR-SEC-04).
//
// Each session records the client that started it, its IP address and user
// agent, so that its user can tell their sessions apart, and when it was last
// used, at most once a minute, so that a request does not write to the
// database each time.

// sessionSecretBytes is the length of a session's secret: 256 bits, more
// than the 128 that FR-AUTH-02 AC3 asks for.
const sessionSecretBytes = 32

// cleanupBatch is how many ended sessions the session cleanup job deletes in
// a transaction.
const cleanupBatch = 1000

// cleanupInterval is how often the session cleanup job runs.
const cleanupInterval = time.Hour

// The reasons that an audit entry of a session's revocation records, as its
// metadata's reason.
const (
	revokedBySignOut        = "sign_out"
	revokedByUser           = "revoked"
	revokedByPasswordChange = "password_changed"
	revokedByPasswordReset  = "password_reset"
)

// The reasons that an audit entry of a failed sign-in records, as its
// metadata's reason.
const (
	// failedCredentials is a sign-in whose address belongs to no active
	// account, or whose password is not the account's.
	failedCredentials = "invalid_credentials"
	// failedLocked is a sign-in that the lock of its address refused, after
	// too many failures in a row (FR-AUTH-05), whatever its password.
	failedLocked = "locked"
)

var (
	// ErrNoSession is returned for a secret that is not an active session's.
	ErrNoSession = errors.New("account: no active session has that secret")
	// ErrSessionNotFound is returned for a session ID that is not an
	// active session of the account.
	ErrSessionNotFound = errors.New("account: the account has no active session with that ID")
	// ErrWrongPassword is returned by ChangePassword when the current
	// password given is not the account's.
	ErrWrongPassword = errors.New("account: the current password is wrong")
)

// Client is what a session and the audit log record of the client that
// makes a request.
type Client = audit.Client

// Session is an active session.
type Session struct {
	ID, UserID ids.UUID
	// CreatedAt is when its user signed in, and LastSeenAt when it was last
	// used, to the minute.
	CreatedAt, LastSeenAt time.Time
	// EndsAt is when it ends at the latest, however often it is used, and
	// IdleEndsAt when it ends unless it is used before then.
	EndsAt, IdleEndsAt time.Time
	// IP and UserAgent are those of the client that started it, either zero
	// if unknown.
	IP        netip.Addr
	UserAgent string
}

// Issued is a session with its secret, which Login and the rotation of a
// session return, and which only the client then holds.
type Issued struct {
	Session
	Secret secret.Value
}

// NewSecret returns a new secret, of a session, a link, or an invitation's
// link (package invite), and the SHA-256 hash that the database holds of it.
func NewSecret() (secret.Value, []byte) {
	b := make([]byte, sessionSecretBytes)
	// crypto/rand.Read never fails; the program crashes if the operating
	// system's source does.
	_, _ = rand.Read(b)
	sum := sha256.Sum256(b)
	return secret.New(base64.RawURLEncoding.EncodeToString(b)), sum[:]
}

// SecretHash returns the hash that the database holds of s, and false if s
// is not a secret that NewSecret could have made.
func SecretHash(s secret.Value) ([]byte, bool) {
	b, err := base64.RawURLEncoding.Strict().DecodeString(s.Reveal())
	if err != nil || len(b) != sessionSecretBytes {
		return nil, false
	}
	sum := sha256.Sum256(b)
	return sum[:], true
}

// Login signs an account in, as authenticate checks its address and
// password, calls Options.Authenticated, and starts a session for it, with
// client, and the audit entry of the login, in one transaction
// (startSession). A failed sign-in returns ErrInvalidCredentials, and is
// recorded in the audit log, in a transaction of its own, with the account
// that the address belongs to, if any, and the address's keyed hash
// otherwise (FR-TEN-07).
//
// Each sign-in counts as a failure of its address until it succeeds
// (ratelimit.Limiter.BeginLogin), and one with an address that is locked,
// after too many failures in a row, returns the *ratelimit.LockedError,
// without checking the password, even a right one (FR-AUTH-05 AC1). It is
// recorded in the audit log as a failure too. A sign-in that succeeds
// forgets the address's failures, in the transaction that starts its session
// (AC3).
//
// If the account's password changes, or the account stops being active,
// between the check of the password and the start of the session, Login
// checks the password again, against what the account holds then, at most
// passwordAttempts times in all, so that a sign-in with the old password does
// not outlive the change, and one with the password that the account holds
// still signs in, as after another sign-in hashed it again (NFR-SEC-03).
func (s *Service) Login(ctx context.Context, email string, password secret.Value, client Client) (Issued, error) {
	counted, err := s.limits.BeginLogin(ctx, CanonicalEmail(email))
	var locked *ratelimit.LockedError
	if errors.As(err, &locked) {
		return Issued{}, s.loginLocked(ctx, email, client, locked)
	}
	if err != nil {
		return Issued{}, err
	}
	for attempt := 1; ; attempt++ {
		id, account, hash, err := s.authenticate(ctx, email, password)
		if errors.Is(err, ErrInvalidCredentials) {
			return Issued{}, s.loginFailed(ctx, email, account, client)
		}
		if err != nil {
			return Issued{}, err
		}
		if s.authenticated != nil {
			if err := s.authenticated(ctx, id, client); err != nil {
				return Issued{}, err
			}
		}
		issued, err := s.startSession(ctx, id, hash, client, counted)
		switch {
		case !errors.Is(err, errCredentialsChanged):
			return issued, err
		case attempt == passwordAttempts:
			return Issued{}, s.loginFailed(ctx, email, id, client)
		}
	}
}

// passwordAttempts is how many times Login and ChangePassword check a
// password at most, when the account's password's hash changes after each
// check (startSession, changePassword).
const passwordAttempts = 2

// errCredentialsChanged is returned by startSession when the account's
// password, or whether it can sign in, changed since the sign-in checked it,
// and by changePassword when the password's hash changed since it checked the
// current password.
var errCredentialsChanged = errors.New("account: the account changed since its password was checked")

// loginFailed records a failed sign-in with email, whose address belongs to
// the account account, or to none if it is zero, and returns
// ErrInvalidCredentials, or the error that recording it failed with.
func (s *Service) loginFailed(ctx context.Context, email string, account ids.UUID, client Client) error {
	if err := s.recordFailedLogin(ctx, email, account, failedCredentials, client); err != nil {
		return err
	}
	return ErrInvalidCredentials
}

// loginLocked records a sign-in with email that the lock of its address
// refused, with the account that the address belongs to, if any, and
// returns locked, or the error that recording it failed with. It looks the
// account up as a sign-in does, but checks no password, whether or not the
// address has an account, so that its answer takes as long either way.
func (s *Service) loginLocked(ctx context.Context, email string, client Client, locked *ratelimit.LockedError) error {
	row, found, err := s.credentials(ctx, email)
	if err != nil {
		return err
	}
	var account ids.UUID
	if found {
		account = row.ID.Bytes
	}
	if err := s.recordFailedLogin(ctx, email, account, failedLocked, client); err != nil {
		return err
	}
	return locked
}

// recordFailedLogin records, in a transaction of its own, a failed sign-in
// with email, whose address belongs to the account account, or to none if it
// is zero, for reason.
func (s *Service) recordFailedLogin(ctx context.Context, email string, account ids.UUID, reason string,
	client Client) error {
	entry := audit.Entry{Action: audit.ActionLogin, SubjectID: account, Result: audit.ResultFailure,
		IP: client.IP, UserAgent: client.UserAgent, Metadata: map[string]any{"reason": reason}}
	if account == (ids.UUID{}) {
		entry.AttemptedEmail = CanonicalEmail(email)
	}
	return s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		return audit.Record(ctx, tx, entry)
	})
}

// startSession starts a session of the account id, whose password a sign-in
// checked against hash, with client, forgets the failed sign-ins that
// counted counts (FR-AUTH-05 AC3), and records the login, in one
// transaction. That transaction first locks the account's row
// (LockPasswordHash), and returns errCredentialsChanged, starting nothing,
// unless the account is still active and holds hash. A change of the
// password that committed after the check, and so revoked the account's
// other sessions without this one, is seen then; one that comes later waits
// for the lock, and then revokes this session with the others (FR-AUTH-02
// AC4).
func (s *Service) startSession(ctx context.Context, id ids.UUID, hash string, client Client,
	counted ratelimit.Attempt) (Issued, error) {
	var issued Issued
	err := s.db.InTx(ctx, database.Scope{UserID: id}, func(tx pgx.Tx) error {
		held, err := queries.New(tx).LockPasswordHash(ctx, nullUUID(id))
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return errCredentialsChanged
		case err != nil:
			return fmt.Errorf("account: lock the account: %w", err)
		case !held.Valid || held.String != hash:
			return errCredentialsChanged
		}
		if issued, err = s.insertSession(ctx, tx, id, client); err != nil {
			return err
		}
		if err := counted.Succeeded(ctx, tx); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: audit.ActionLogin, ActorUserID: id, SubjectID: id,
			Result: audit.ResultSuccess, IP: client.IP, UserAgent: client.UserAgent,
			Metadata: map[string]any{"session_id": issued.ID.String()}})
	})
	if err != nil {
		return Issued{}, err
	}
	return issued, nil
}

// insertSession starts a session of the account id in tx.
func (s *Service) insertSession(ctx context.Context, tx pgx.Tx, id ids.UUID, client Client) (Issued, error) {
	value, hash := NewSecret()
	// The session keeps as much of the user agent as the audit log does, cut
	// at a character's boundary.
	session := Session{ID: ids.New(), UserID: id, IP: client.IP.WithZone(""),
		UserAgent: audit.ClientText(client.UserAgent, audit.MaxUserAgentBytes)}
	var ip *netip.Addr
	if session.IP.IsValid() {
		ip = &session.IP
	}
	row, err := queries.New(tx).InsertSession(ctx, queries.InsertSessionParams{
		ID:        nullUUID(session.ID),
		UserID:    nullUUID(id),
		TokenHash: hash,
		UserAgent: pgtype.Text{String: session.UserAgent, Valid: session.UserAgent != ""},
		Ip:        ip,
		Lifetime:  database.Interval(s.lifetime),
	})
	if err != nil {
		return Issued{}, fmt.Errorf("account: add the session: %w", err)
	}
	session.CreatedAt, session.LastSeenAt, session.EndsAt = row.CreatedAt.Time, row.LastSeenAt.Time, row.ExpiresAt.Time
	session.IdleEndsAt = s.idleEnd(session)
	return Issued{Session: session, Secret: value}, nil
}

// Session returns the active session whose secret is value, of an active
// account, and ErrNoSession if there is none. It reads the session from the
// database each time, so that a session that is revoked or ends is refused at
// once, by every server (FR-AUTH-02 AC1), and records that the session is
// used, if it last did over a minute ago.
func (s *Service) Session(ctx context.Context, value secret.Value) (Session, error) {
	hash, ok := SecretHash(value)
	if !ok {
		return Session{}, ErrNoSession
	}
	var session Session
	err := s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		row, err := q.ActiveSession(ctx, queries.ActiveSessionParams{TokenHash: hash,
			Lifetime: database.Interval(s.lifetime), Idle: database.Interval(s.idle)})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoSession
		}
		if err != nil {
			return fmt.Errorf("account: read the session: %w", err)
		}
		session = Session{ID: row.ID.Bytes, UserID: row.UserID.Bytes, CreatedAt: row.CreatedAt.Time,
			LastSeenAt: row.LastSeenAt.Time, EndsAt: row.EndsAt.Time, IP: addr(row.Ip),
			UserAgent: row.UserAgent.String}
		if !row.Stale {
			return nil
		}
		seen, err := q.TouchSession(ctx, row.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			// It was revoked since it was read.
			return ErrNoSession
		}
		if err != nil {
			return fmt.Errorf("account: record the session's use: %w", err)
		}
		session.LastSeenAt = seen.Time
		return nil
	})
	if err != nil {
		return Session{}, err
	}
	session.IdleEndsAt = s.idleEnd(session)
	return session, nil
}

// idleEnd returns when session ends unless it is used before.
func (s *Service) idleEnd(session Session) time.Time {
	end := session.LastSeenAt.Add(s.idle)
	if session.EndsAt.Before(end) {
		return session.EndsAt
	}
	return end
}

// Sessions returns at most limit of the active sessions of the account
// userID, newest first: the newest, if before is zero, or else those that
// started before the session before.
func (s *Service) Sessions(ctx context.Context, userID, before ids.UUID, limit int) ([]Session, error) {
	var sessions []Session
	err := s.db.InTx(ctx, database.Scope{UserID: userID}, func(tx pgx.Tx) error {
		rows, err := queries.New(tx).UserSessions(ctx, queries.UserSessionsParams{
			UserID:   nullUUID(userID),
			Before:   nullUUID(before),
			Lifetime: database.Interval(s.lifetime),
			Idle:     database.Interval(s.idle),
			MaxRows:  int32(min(limit, 1<<20)),
		})
		if err != nil {
			return fmt.Errorf("account: list the sessions: %w", err)
		}
		for _, r := range rows {
			session := Session{ID: r.ID.Bytes, UserID: userID, CreatedAt: r.CreatedAt.Time,
				LastSeenAt: r.LastSeenAt.Time, EndsAt: r.EndsAt.Time, IP: addr(r.Ip), UserAgent: r.UserAgent.String}
			session.IdleEndsAt = s.idleEnd(session)
			sessions = append(sessions, session)
		}
		return nil
	})
	return sessions, err
}

// RevokeSession revokes the active session id of the account userID, which
// client asks for, and records the revocation in the audit log. It returns
// ErrSessionNotFound if the account has no such session.
func (s *Service) RevokeSession(ctx context.Context, userID, id ids.UUID, client Client) error {
	return s.db.InTx(ctx, database.Scope{UserID: userID}, func(tx pgx.Tx) error {
		n, err := queries.New(tx).RevokeSession(ctx, queries.RevokeSessionParams{ID: nullUUID(id),
			UserID: nullUUID(userID), Lifetime: database.Interval(s.lifetime), Idle: database.Interval(s.idle)})
		if err != nil {
			return fmt.Errorf("account: revoke the session: %w", err)
		}
		if n == 0 {
			return ErrSessionNotFound
		}
		return recordRevocation(ctx, tx, userID, id, revokedByUser, client)
	})
}

// RevokeOtherSessions revokes every active session of the account of
// current but current itself, which client asks for, records each
// revocation in the audit log, and returns how many it revoked. It returns
// ErrNoSession, and revokes nothing, if current has no ID, which would keep
// none of the sessions (revokeOthers).
//
// It first locks the account's row (LockUser), so that it and a change of
// the password, which updates the row, run one after the other, in the order
// that they lock it. Each revokes sessions that the other revokes too, and
// the change then gives its own session a new secret, so that run together
// each could hold a session's row that the other waits for: a deadlock,
// which PostgreSQL would end by failing one of them.
func (s *Service) RevokeOtherSessions(ctx context.Context, current Session, client Client) (int, error) {
	if current.ID == (ids.UUID{}) {
		return 0, ErrNoSession
	}
	var n int
	err := s.db.InTx(ctx, database.Scope{UserID: current.UserID}, func(tx pgx.Tx) error {
		if err := queries.New(tx).LockUser(ctx, nullUUID(current.UserID)); err != nil {
			return fmt.Errorf("account: lock the account: %w", err)
		}
		var err error
		n, err = s.revokeOthers(ctx, tx, current, revokedByUser, client)
		return err
	})
	return n, err
}

// revokeOthers revokes the account's other sessions in tx, for reason, and
// every one of them if current has no ID, as resetting the password does.
func (s *Service) revokeOthers(ctx context.Context, tx pgx.Tx, current Session, reason string, client Client) (int,
	error) {
	revoked, err := queries.New(tx).RevokeOtherSessions(ctx, queries.RevokeOtherSessionsParams{
		UserID: nullUUID(current.UserID), Keep: nullUUID(current.ID), Lifetime: database.Interval(s.lifetime),
		Idle: database.Interval(s.idle)})
	if err != nil {
		return 0, fmt.Errorf("account: revoke the other sessions: %w", err)
	}
	for _, id := range revoked {
		if err := recordRevocation(ctx, tx, current.UserID, id.Bytes, reason, client); err != nil {
			return 0, err
		}
	}
	return len(revoked), nil
}

// Logout ends the session current, which client signs out of: it deletes
// the session (FR-AUTH-02 AC2), and records that in the audit log.
func (s *Service) Logout(ctx context.Context, current Session, client Client) error {
	return s.db.InTx(ctx, database.Scope{UserID: current.UserID}, func(tx pgx.Tx) error {
		n, err := queries.New(tx).DeleteSession(ctx, nullUUID(current.ID))
		if err != nil {
			return fmt.Errorf("account: delete the session: %w", err)
		}
		if n == 0 {
			// It ended since the request found it, as when the cleanup job
			// deleted it.
			return nil
		}
		return recordRevocation(ctx, tx, current.UserID, current.ID, revokedBySignOut, client)
	})
}

// recordRevocation records that the user userID's session id ended early,
// for reason.
func recordRevocation(ctx context.Context, tx pgx.Tx, userID, id ids.UUID, reason string, client Client) error {
	return audit.Record(ctx, tx, audit.Entry{Action: audit.ActionSessionRevoke, ActorUserID: userID, SubjectID: id,
		Result: audit.ResultSuccess, IP: client.IP, UserAgent: client.UserAgent,
		Metadata: map[string]any{"reason": reason}})
}

// Rotate gives the active session current a new secret, which it returns,
// so that the old secret no longer finds it, as a change of what the session
// may do calls for (NFR-SEC-04). The session keeps its ID, its start, and
// its end. It returns ErrNoSession if the session has ended.
func (s *Service) Rotate(ctx context.Context, current Session) (Issued, error) {
	var issued Issued
	err := s.db.InTx(ctx, database.Scope{UserID: current.UserID}, func(tx pgx.Tx) error {
		var err error
		issued, err = s.rotate(ctx, tx, current)
		return err
	})
	return issued, err
}

// rotate is Rotate in tx.
func (s *Service) rotate(ctx context.Context, tx pgx.Tx, current Session) (Issued, error) {
	value, hash := NewSecret()
	n, err := queries.New(tx).RotateSession(ctx, queries.RotateSessionParams{TokenHash: hash,
		ID: nullUUID(current.ID), UserID: nullUUID(current.UserID), Lifetime: database.Interval(s.lifetime),
		Idle: database.Interval(s.idle)})
	if err != nil {
		return Issued{}, fmt.Errorf("account: rotate the session: %w", err)
	}
	if n == 0 {
		return Issued{}, ErrNoSession
	}
	return Issued{Session: current, Secret: value}, nil
}

// ChangePassword changes the password of the account of the session
// current, which client asks for, from password to replacement. It returns
// a *PasswordError if replacement breaks the password policy, and
// ErrWrongPassword if password is not the account's, which it records in
// the audit log. Otherwise, in one transaction, it replaces the password's
// hash, revokes every other session of the account (FR-AUTH-02 AC4),
// deletes the account's reset links that have not been used (FR-AUTH-03
// AC4), gives current a new secret (Rotate), and records all of that in the
// audit log, and it returns current with its new secret. It returns
// ErrNoSession if current has ended meanwhile, and changes nothing then.
//
// If the password's hash changes between the check of password and the
// change, ChangePassword checks password again, against the hash that the
// account holds then, at most passwordAttempts times in all, so that a
// password that another sign-in hashed again meanwhile (NFR-SEC-03) is still
// the account's, and one that another change replaced is not.
func (s *Service) ChangePassword(ctx context.Context, current Session, password, replacement secret.Value,
	client Client) (Issued, error) {
	if err := CheckPassword(replacement); err != nil {
		return Issued{}, err
	}
	for attempt := 1; ; attempt++ {
		issued, err := s.changePassword(ctx, current, password, replacement, client)
		switch {
		case !errors.Is(err, errCredentialsChanged):
			return issued, err
		case attempt == passwordAttempts:
			return Issued{}, s.passwordChangeFailed(ctx, current.UserID, client)
		}
	}
}

// changePassword is one attempt of ChangePassword, which returns
// errCredentialsChanged, and changes nothing, if the password's hash changed
// since it checked password against it.
func (s *Service) changePassword(ctx context.Context, current Session, password, replacement secret.Value,
	client Client) (Issued, error) {
	var old pgtype.Text
	err := s.db.InTx(ctx, database.Scope{UserID: current.UserID}, func(tx pgx.Tx) error {
		var err error
		old, err = queries.New(tx).UserPasswordHash(ctx, nullUUID(current.UserID))
		if errors.Is(err, pgx.ErrNoRows) {
			// The account is no longer active.
			return ErrNoSession
		}
		return err
	})
	if err != nil {
		return Issued{}, fmt.Errorf("account: read the password's hash: %w", err)
	}
	hash := s.decoy
	if old.Valid {
		hash = old.String
	}
	match, _, err := s.hasher.Verify(ctx, hash, password)
	if err != nil {
		return Issued{}, err
	}
	if !match || !old.Valid {
		return Issued{}, s.passwordChangeFailed(ctx, current.UserID, client)
	}
	replaced, err := s.hasher.Hash(ctx, replacement)
	if err != nil {
		return Issued{}, err
	}
	var issued Issued
	err = s.db.InTx(ctx, database.Scope{UserID: current.UserID}, func(tx pgx.Tx) error {
		n, err := queries.New(tx).UpdatePasswordHash(ctx, queries.UpdatePasswordHashParams{
			NewHash: pgtype.Text{String: replaced, Valid: true}, ID: nullUUID(current.UserID), OldHash: old})
		if err != nil {
			return fmt.Errorf("account: replace the password's hash: %w", err)
		}
		if n == 0 {
			// The hash changed since password was checked against it: another
			// sign-in hashed the same password again, or another request
			// changed it.
			return errCredentialsChanged
		}
		if _, err := s.revokeOthers(ctx, tx, current, revokedByPasswordChange, client); err != nil {
			return err
		}
		if err := queries.New(tx).DeleteResetTokens(ctx, nullUUID(current.UserID)); err != nil {
			return fmt.Errorf("account: delete the reset links: %w", err)
		}
		if issued, err = s.rotate(ctx, tx, current); err != nil {
			return err
		}
		return recordPasswordChange(ctx, tx, current.UserID, audit.ResultSuccess, client)
	})
	if err != nil {
		return Issued{}, err
	}
	return issued, nil
}

// passwordChangeFailed records that the user userID failed to change the
// account's password, because the current password given is not the
// account's, and returns ErrWrongPassword, or the error that recording it
// failed with.
func (s *Service) passwordChangeFailed(ctx context.Context, userID ids.UUID, client Client) error {
	if err := s.db.InTx(ctx, database.Scope{UserID: userID}, func(tx pgx.Tx) error {
		return recordPasswordChange(ctx, tx, userID, audit.ResultFailure, client)
	}); err != nil {
		return err
	}
	return ErrWrongPassword
}

// recordPasswordChange records an attempt of the user userID to change the
// account's password.
func recordPasswordChange(ctx context.Context, tx pgx.Tx, userID ids.UUID, result audit.Result, client Client) error {
	return audit.Record(ctx, tx, audit.Entry{Action: audit.ActionPasswordChange, ActorUserID: userID,
		SubjectID: userID, Result: result, IP: client.IP, UserAgent: client.UserAgent})
}

// sessionCleanup is the job that deletes the sessions that have ended
// (FR-JOBS-01), which no request finds any more.
type sessionCleanup struct{}

func (sessionCleanup) Kind() string { return "session_cleanup" }

// RegisterJobs registers the jobs of accounts in r: the session cleanup and
// the cleanup of the links' tokens, which run every hour, and the job that
// emails a link (email.go).
func (s *Service) RegisterJobs(r *jobs.Registry) {
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[sessionCleanup]) error {
		_, err := s.cleanUpSessions(ctx, job.DB)
		return err
	})
	jobs.Periodic(r, cleanupInterval, sessionCleanup{})
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[emailTokenCleanup]) error {
		_, err := cleanUpEmailTokens(ctx, job.DB)
		return err
	})
	jobs.Periodic(r, cleanupInterval, emailTokenCleanup{})
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[emailLink]) error {
		id, err := ids.ParseUUID(job.Args.Token)
		if err != nil {
			// The job's arguments are the server's own, so this is a bug,
			// which no attempt would mend.
			return fmt.Errorf("account: the job's token %q is not a UUID: %w", job.Args.Token, err)
		}
		return s.sendLink(ctx, job.DB, id)
	})
}

// cleanUpSessions deletes the sessions that have ended, in batches, each in
// a transaction of its own, so that no transaction holds many rows, and
// returns how many it deleted. Deleting a session that has ended changes
// nothing that a request sees, so it is safe to repeat.
func (s *Service) cleanUpSessions(ctx context.Context, db DB) (int64, error) {
	var total int64
	for {
		var n int64
		err := db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
			var err error
			n, err = queries.New(tx).DeleteEndedSessions(ctx, queries.DeleteEndedSessionsParams{
				Lifetime: database.Interval(s.lifetime), Idle: database.Interval(s.idle), MaxRows: cleanupBatch})
			return err
		})
		if err != nil {
			return total, fmt.Errorf("account: delete the ended sessions: %w", err)
		}
		total += n
		if n < cleanupBatch {
			return total, nil
		}
	}
}

// nullUUID returns id as a uuid column, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}

// addr returns the address of an inet column, zero if it is null.
func addr(ip *netip.Addr) netip.Addr {
	if ip == nil {
		return netip.Addr{}
	}
	return *ip
}
