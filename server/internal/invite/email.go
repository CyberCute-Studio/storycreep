package invite

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/text/language"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
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

// linkPath is the path of the web app's page that accepts an invitation,
// which its link opens.
const linkPath = "/app/invite"

// linkURL returns the URL of an invitation's link with the secret value, on
// the instance at u, with the secret in its fragment, as account's links
// are made (account.LinkURL).
func linkURL(u config.PublicURL, value secret.Value) string {
	return account.LinkURL(u, linkPath, value)
}

// emailInvite is the job that emails the link of an invitation (send).
type emailInvite struct {
	// Workspace is the ID of the invitation's workspace, and Invite the
	// invitation's.
	Workspace string `json:"workspace"`
	Invite    string `json:"invite"`
	// Request is the ID of the request that asked for the email, which the
	// invitation records until a later request replaces it, and which grants
	// nothing.
	Request string `json:"request"`
}

func (emailInvite) Kind() string { return "email_invite" }

// emailInviteAttempts is how many times the job tries to email an
// invitation: with the queue's backoff, which waits an hour at most between
// attempts, over about a day, as the job that emails an account's link does.
// An attempt sends nothing once the invitation has been accepted, revoked,
// or sent anew, or has expired.
const emailInviteAttempts = 36

// enqueue enqueues the job of the request request that emails the link of
// the invitation inviteID of the workspace id to the address address, in tx,
// the transaction that records the request in the invitation. The job is due
// once ResendInterval has passed since the workspace last emailed an
// invitation to the address, or at once if it has passed (InviteEmailDue),
// so that sending an invitation anew again and again, or revoking it and
// inviting the address again, cannot flood the address's inbox. Each request
// enqueues its own job, even while the job of an earlier one still waits, is
// retried, or runs, but only the job of the latest request sends anything
// (send): the earlier ones find the invitation sent anew, or closed, as the
// job of a revoked invitation does. A job that ran before the request was
// made recorded when it emailed the address, which the request's job waits
// ResendInterval after, and one that runs while the request is made locks
// the invitation, so that it either sends before the request, which then
// sees when, or finds itself replaced.
func (s *Service) enqueue(ctx context.Context, tx pgx.Tx, id, inviteID, request ids.UUID, address string) error {
	switch {
	case s.queue == nil:
		return errors.New("invite: the Service has no job queue to email an invitation with")
	case s.limits == nil:
		// The job would fail every attempt, after the request had been
		// answered.
		return errNoLimits
	}
	opts := &jobs.EnqueueOptions{MaxAttempts: emailInviteAttempts}
	due, err := queries.New(tx).InviteEmailDue(ctx, queries.InviteEmailDueParams{
		ResendInterval: database.Interval(ResendInterval), WorkspaceID: nullUUID(id),
		Email: pgtype.Text{String: address, Valid: true}})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return fmt.Errorf("invite: read when the address was last emailed: %w", err)
	default:
		opts.At = due.Time
	}
	_, err = s.queue.Enqueue(ctx, tx, emailInvite{Workspace: id.String(), Invite: inviteID.String(),
		Request: request.String()}, opts)
	return err
}

// RegisterJobs registers the job that emails an invitation's link in r.
func (s *Service) RegisterJobs(r *jobs.Registry) {
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[emailInvite]) error {
		id, err := ids.ParseUUID(job.Args.Workspace)
		if err != nil {
			// The job's arguments are the server's own, so this is a bug,
			// which no attempt would mend.
			return fmt.Errorf("invite: the job's workspace %q is not a UUID: %w", job.Args.Workspace, err)
		}
		inviteID, err := ids.ParseUUID(job.Args.Invite)
		if err != nil {
			return fmt.Errorf("invite: the job's invitation %q is not a UUID: %w", job.Args.Invite, err)
		}
		request, err := ids.ParseUUID(job.Args.Request)
		if err != nil {
			return fmt.Errorf("invite: the job's request %q is not a UUID: %w", job.Args.Request, err)
		}
		return s.send(ctx, job.DB, id, inviteID, request)
	})
}

// send is the work of the job of the request request that emails the link of
// the invitation inviteID of the workspace id: it makes the link's secret,
// holds its hash in the invitation, which then expires Lifetime from now,
// and sends the email to the invited address, in the preferred locale of the
// account that the address belongs to, if any, and else in the workspace's
// default locale. An invitation that has been accepted or revoked, or has
// expired, as one asked to be emailed longer ago than Lifetime has, or whose
// workspace's deletion is pending, is sent no email, and nor is any on an
// instance that no longer sends email. Nor is one that a later request has
// sent anew, whose own job emails it (enqueue), so that however many of a
// request's attempts fail, as while the SMTP server is down, and however
// often the invitation is sent anew meanwhile, the address is sent one email
// once the server takes email again. An attempt that fails to send the email
// records that the invitation's link was not emailed after all, and is made
// again later, with a new secret, which replaces the one that the failed
// attempt may have sent, so that only the latest email's link works, unless
// the SMTP server refuses the email for good (email.JobError).
//
// An attempt that has an email to send counts it against the limit of
// invitation emails, from all workspaces together, of the mailbox that its
// address goes to (ratelimit.InviteEmail, ratelimit.Mailbox), and sends
// nothing if the mailbox is over it: the job ends, cancelled, with the
// limit as its error, as one whose email the SMTP server refuses for good
// does, so that the job's row and metrics tell an operator why, and the
// invitation stays without a link, which its admins see as not emailed,
// and send anew later. An attempt that finds the invitation with a link
// does not count the email, because an earlier attempt of the job, which
// counted it, gave it that link: so the retries of an email that an SMTP
// server that is down refused, or that the server's stop cut off, do not
// use up the mailbox's limit, though a retry in a later hour than the
// attempt that counted the email sends it past that hour's limit, as when
// a server that was down for hours takes email again. An attempt that
// failed, or was cut off, before it gave the invitation its link, as one
// that failed before its count, leaves the email to be counted by the
// next, whatever its number, so that no email goes uncounted but one sent
// again: a retry sends uncounted an email that the SMTP server took
// although its attempt failed, as when the server's reply to it was lost,
// or the instance stopped before the job was recorded as done, as the work
// of any job may run twice (package jobs). The count is made before the
// invitation is locked to be sent, in a transaction of its own, so that no
// lock of the invitation waits on it; an invitation that is sent anew or
// closed in between counted its email to no use, and so did an attempt cut
// off between its count and its link, whose next attempt counts the email
// again. The job reads the SMTP settings, as it does the invitation, and
// counts the email, on the workers' pool that it carries, db, one
// connection at a time.
func (s *Service) send(ctx context.Context, db DB, id, inviteID, request ids.UUID) error {
	if s.email == nil {
		return nil
	}
	mailer := s.email.WithDB(db)
	settings, configured, err := mailer.Settings(ctx)
	if err != nil || !configured {
		return err
	}
	if s.limits == nil {
		return errNoLimits
	}
	scope := database.Scope{WorkspaceID: id}
	params := queries.LockInviteToSendParams{WorkspaceID: nullUUID(id), ID: nullUUID(inviteID),
		EmailRequest: nullUUID(request)}
	pending, found, err := lockToSend(ctx, db, scope, params, nil)
	switch {
	case err != nil:
		return fmt.Errorf("invite: read the invitation to email: %w", err)
	case !found:
		// Nothing to send, so nothing to count.
		return nil
	}
	if !pending.Linked {
		var over *ratelimit.LimitError
		_, err = s.limits.TakeOn(ctx, db, ratelimit.InviteEmail, ratelimit.Mailbox(pending.Email))
		switch {
		case errors.As(err, &over):
			// No attempt sends it: its admins send the invitation anew.
			return jobs.Cancel(fmt.Errorf("invite: the invitation's email is over its mailbox's limit: %w", err))
		case err != nil:
			return fmt.Errorf("invite: count the invitation's email: %w", err)
		}
	}
	value, hash := account.NewSecret()
	row, found, err := lockToSend(ctx, db, scope, params, func(q *queries.Queries) error {
		return q.SetInviteHash(ctx, queries.SetInviteHashParams{TokenHash: hash,
			Lifetime: database.Interval(Lifetime), WorkspaceID: nullUUID(id), ID: nullUUID(inviteID)})
	})
	if err != nil {
		return fmt.Errorf("invite: prepare the invitation's email: %w", err)
	}
	if !found {
		return nil
	}
	msg := inviteEmail(s.publicURL, row.Locale.String, row.Role, value, row.Email)
	if err := mailer.Deliver(ctx, settings, msg); err != nil {
		err = fmt.Errorf("invite: email the invitation: %w", err)
		// No email holds the link, so the invitation's admins see that it
		// is still to be emailed, or never was, and the email of a request
		// to send it anew does not wait for this one (InviteEmailDue). That
		// holds even when the job's context has ended, as when the server
		// stops.
		uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), unmarkTimeout)
		defer cancel()
		if unmark := db.InTx(uctx, scope, func(tx pgx.Tx) error {
			return queries.New(tx).UnmarkInviteSent(uctx, queries.UnmarkInviteSentParams{WorkspaceID: nullUUID(id),
				ID: nullUUID(inviteID), TokenHash: hash})
		}); unmark != nil {
			// The invitation still looks emailed, by this attempt. The error
			// does not wrap the email's, so that a refusal for good does not
			// cancel the job: its next attempt, unless a later request has
			// sent the invitation anew by then, sends the email again and
			// records how that went. It still carries the context's error,
			// so that an attempt that the server's stop cut off does not
			// count.
			return email.JobError(ctx, fmt.Errorf("%v; invite: record that the invitation was not emailed: %v",
				err, unmark))
		}
		return email.JobError(ctx, err)
	}
	return nil
}

// lockToSend reads what the email of the invitation of params needs, if it
// has an email to send (LockInviteToSend), in a transaction of scope, on db,
// and runs then, unless it is nil, in the same transaction, with the
// invitation locked. It reports whether the invitation has an email to
// send. The lock makes even a read that changes nothing wait for a change
// of the invitation that is under way, as its being sent anew, and see what
// it changed, so that the job counts no email of an invitation that the
// change replaces.
func lockToSend(ctx context.Context, db DB, scope database.Scope, params queries.LockInviteToSendParams,
	then func(q *queries.Queries) error) (row queries.LockInviteToSendRow, found bool, err error) {
	err = db.InTx(ctx, scope, func(tx pgx.Tx) error {
		q := queries.New(tx)
		var err error
		row, err = q.LockInviteToSend(ctx, params)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return err
		}
		found = true
		if then == nil {
			return nil
		}
		return then(q)
	})
	if err != nil {
		return queries.LockInviteToSendRow{}, false, err
	}
	return row, found, nil
}

// unmarkTimeout is how long a job whose email failed has to record that,
// once its own context has ended.
const unmarkTimeout = 10 * time.Second

// errNoLimits is the error of a Service that emails invitations without
// Limits to count them with.
var errNoLimits = errors.New("invite: the Service has no Limits to count the invitations that it emails")

// inviteEmail returns the email of an invitation with the role r, with the
// secret value, to the address address, in locale, a BCP 47 tag, or the
// source locale if the server has no catalog for it. It names neither the
// workspace nor whoever made the invitation, whose names their users chose,
// so that no account can put text of its own in email that the instance
// sends; the page that the link opens names the workspace (Lookup).
func inviteEmail(u config.PublicURL, locale, r string, value secret.Value, address string) email.Message {
	tag, err := language.Parse(locale)
	if err != nil {
		tag = i18n.Source
	}
	l := i18n.Default().Localizer(tag)
	args := i18n.Args{"site": u.URL().Host, "url": linkURL(u, value), "role": r,
		"days": int(Lifetime / (24 * time.Hour))}
	return email.Message{To: mail.Address{Address: address},
		Subject: l.Format(i18n.Message{Key: i18n.K("email.invite.subject")}),
		Body:    l.Format(i18n.Message{Key: i18n.K("email.invite.body"), Args: args})}
}
