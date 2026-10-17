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
}

func (emailInvite) Kind() string { return "email_invite" }

// emailInviteAttempts is how many times the job tries to email an
// invitation: with the queue's backoff, which waits an hour at most between
// attempts, over about a day, as the job that emails an account's link does.
// An attempt sends nothing once the invitation has been accepted or revoked,
// or has expired.
const emailInviteAttempts = 36

// enqueue enqueues the job that emails the link of the invitation inviteID
// of the workspace id to the address address, in tx. The job is due once
// ResendInterval has passed since the workspace last emailed an invitation
// to the address, or at once if it has passed (InviteEmailDue), so that
// sending an invitation anew again and again, or revoking it and inviting
// the address again, cannot flood the address's inbox. Each request
// enqueues its own job, even while an earlier one still waits or runs, whose
// email may hold a link that the request has replaced: whichever job runs
// last sends the link that works, and a job that finds the invitation's link
// emailed within ResendInterval, by another of its jobs that ran just
// before, sends none (send).
func (s *Service) enqueue(ctx context.Context, tx pgx.Tx, id, inviteID ids.UUID, address string) error {
	if s.queue == nil {
		return errors.New("invite: the Service has no job queue to email an invitation with")
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
	_, err = s.queue.Enqueue(ctx, tx, emailInvite{Workspace: id.String(), Invite: inviteID.String()}, opts)
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
		return s.send(ctx, job.DB, id, inviteID, job.Attempt <= 1)
	})
}

// send is the work of the job that emails the link of the invitation
// inviteID of the workspace id: it makes the link's secret, holds its hash
// in the invitation, which then expires Lifetime from now, and sends the
// email to the invited address, in the preferred locale of the account that
// the address belongs to, if any, and else in the workspace's default
// locale. An invitation that has been accepted or revoked, or has expired,
// as one asked to be emailed longer ago than Lifetime has, or whose
// workspace's deletion is pending, is sent no email, and nor is any on an
// instance that no longer sends email. Nor is the invitation on the job's
// first attempt, firstAttempt, if the link that it has was emailed within
// ResendInterval, as by another of its jobs, which waited for the same
// moment (enqueue). An attempt that fails to send the email records that the
// invitation's link was not emailed after all, and is made again later, with
// a new secret, which replaces the one that the failed attempt may have
// sent, so that only the latest email's link works, unless the SMTP server
// refuses the email for good (email.JobError). An attempt that cannot record
// that counts as failed, however it ended, so that the next attempt sends
// the email. The job reads the SMTP
// settings, as it does the invitation, on the workers' pool that it
// carries, db.
func (s *Service) send(ctx context.Context, db DB, id, inviteID ids.UUID, firstAttempt bool) error {
	if s.email == nil {
		return nil
	}
	mailer := s.email.WithDB(db)
	settings, configured, err := mailer.Settings(ctx)
	if err != nil || !configured {
		return err
	}
	value, hash := account.NewSecret()
	var row queries.LockInviteToSendRow
	found := false
	scope := database.Scope{WorkspaceID: id}
	err = db.InTx(ctx, scope, func(tx pgx.Tx) error {
		q := queries.New(tx)
		var err error
		row, err = q.LockInviteToSend(ctx, queries.LockInviteToSendParams{WorkspaceID: nullUUID(id),
			ID: nullUUID(inviteID), FirstAttempt: firstAttempt, ResendInterval: database.Interval(ResendInterval)})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
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
		// is still to be emailed, or never was, and the next attempt is not
		// taken for another job's. That holds even when the job's context
		// has ended, as when the server stops.
		uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), unmarkTimeout)
		defer cancel()
		if unmark := db.InTx(uctx, scope, func(tx pgx.Tx) error {
			return queries.New(tx).UnmarkInviteSent(uctx, queries.UnmarkInviteSentParams{WorkspaceID: nullUUID(id),
				ID: nullUUID(inviteID), TokenHash: hash})
		}); unmark != nil {
			// The invitation still looks emailed, by this attempt. The error
			// wraps neither the context's error nor a refusal for good, so
			// the attempt counts, even when the server is stopping, and the
			// next attempt, which is not the job's first, sends the email
			// rather than take this attempt's for another job's
			// (LockInviteToSend).
			return fmt.Errorf("%v; invite: record that the invitation was not emailed: %v", err, unmark)
		}
		return email.JobError(ctx, err)
	}
	return nil
}

// unmarkTimeout is how long a job whose email failed has to record that,
// once its own context has ended.
const unmarkTimeout = 10 * time.Second

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
