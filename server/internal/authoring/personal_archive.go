package authoring

import (
	"archive/zip"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/personaldata"
	"github.com/CyberCute-Studio/storycreep/server/internal/serializer"
)

// How many rows a personal-data archive reads at a time, each page in a short
// transaction of its own, so that it holds neither all of a user's rows in
// memory nor a transaction open while it writes: comments, whose bodies may
// be long, a few hundred, and the references of attribution, which are small,
// thousands. The audit log is read auditPage entries at a time.
const (
	commentPage   = 500
	pageItems     = 1000
	referencePage = 5000
)

// errAccountGone is the error of a personal-data export whose account is no
// longer active by the time that the job reads it, as one suspended or
// pending its deletion: its user can no longer sign in to read the export,
// and another attempt would find the account so again.
var errAccountGone = errors.New("authoring: the account is no longer active")

// personalContent is what a personal-data build read of the account at the
// start: the account's own rows, which belong to no workspace, and its rows of
// the workspaces that it reads whole, beside those that it reads as it writes
// them.
type personalContent struct {
	manifest   PersonalManifest
	account    PersonalAccount
	emailLinks []PersonalEmailLink
	sessions   []PersonalSession
	holdings   personaldata.Holdings
	fileName   string
}

// readPersonal reads what the archive of the personal-data export row holds
// of the account itself, in one transaction that reads as of one moment, and
// the user's memberships, API tokens, invitations, and imported identities of
// every workspace, through the maintenance path.
func (s *Service) readPersonal(ctx context.Context, row queries.PersonalDataExport) (*personalContent, error) {
	c := &personalContent{}
	user := ids.UUID(row.UserID.Bytes)
	err := s.db.InSnapshot(ctx, database.Scope{UserID: user}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		a, err := q.PersonalExportAccount(ctx, row.UserID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errAccountGone
		}
		if err != nil {
			return fmt.Errorf("authoring: read the account: %w", err)
		}
		// When the transaction began, by the database's clock, which the
		// audit log's entries are of, so that the archive holds every entry
		// made by the time that the manifest names, as a workspace's does.
		exportedAt := a.ReadAt.Time.UTC()
		c.account = PersonalAccount{ID: ids.Encode(ids.User, a.ID.Bytes), Email: a.Email, DisplayName: a.DisplayName,
			Locale: a.Locale.String, Status: a.Status, EmailVerifiedAt: timeOf(a.EmailVerifiedAt),
			InstanceAdmin: a.IsInstanceAdmin, CreatedAt: a.CreatedAt.Time.UTC(),
			Credentials: PersonalCredentials{PasswordSet: a.PasswordSet, PasswordChangedAt: timeOf(a.PasswordChangedAt)}}
		sessions, err := q.PersonalExportSessions(ctx, row.UserID)
		if err != nil {
			return fmt.Errorf("authoring: read the account's sessions: %w", err)
		}
		c.sessions = make([]PersonalSession, len(sessions))
		for i, ss := range sessions {
			c.sessions[i] = PersonalSession{ID: ids.Encode(ids.Session, ss.ID.Bytes), UserAgent: ss.UserAgent.String,
				CreatedAt: ss.CreatedAt.Time.UTC(), LastSeenAt: ss.LastSeenAt.Time.UTC(),
				ExpiresAt: ss.ExpiresAt.Time.UTC(), RevokedAt: timeOf(ss.RevokedAt)}
			if ss.Ip != nil {
				c.sessions[i].IP = ss.Ip.String()
			}
		}
		links, err := q.PersonalExportEmailLinks(ctx, row.UserID)
		if err != nil {
			return fmt.Errorf("authoring: read the account's email links: %w", err)
		}
		c.emailLinks = make([]PersonalEmailLink, len(links))
		for i, l := range links {
			c.emailLinks[i] = PersonalEmailLink{Kind: l.Kind, CreatedAt: l.CreatedAt.Time.UTC(), SentAt: timeOf(l.SentAt),
				ExpiresAt: l.ExpiresAt.Time.UTC(), UsedAt: timeOf(l.UsedAt)}
		}
		c.manifest = PersonalManifest{Format: PersonalFormat, FormatVersion: PersonalVersion,
			Generator: serializer.Generator(), ExportedAt: exportedAt, UserID: c.account.ID, PersonalData: true}
		c.fileName = "personal-data-" + exportedAt.Format(time.DateOnly) + ".zip"
		return nil
	})
	if err != nil {
		return nil, err
	}
	if c.holdings, err = s.personal.Holdings(ctx, user, c.account.Email); err != nil {
		return nil, fmt.Errorf("authoring: read the user's memberships, tokens, and invitations: %w", err)
	}
	return c, nil
}

// writePersonal writes the archive of c, of the user, to w: the account's
// files, then those of its rows of the workspaces, the audit log's, the
// comments', the notifications', and the attribution's a page at a time, the
// README, and last the manifest, as the format says, since it names what was
// written before it.
func (s *Service) writePersonal(ctx context.Context, w io.Writer, user ids.UUID, c *personalContent) error {
	zw := zip.NewWriter(w)
	compressed, err := flate.NewWriter(io.Discard, flate.DefaultCompression)
	if err != nil {
		return fmt.Errorf("authoring: start the compressor: %w", err)
	}
	zw.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		compressed.Reset(out)
		return compressed, nil
	})
	file := func(name string) (io.Writer, error) {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipTime})
		if err != nil {
			return nil, fmt.Errorf("authoring: add %s to the archive: %w", name, err)
		}
		return f, nil
	}
	// Not nil, as the format requires an array.
	c.manifest.Sections = []ManifestSection{}
	whole := func(name, at string, items int, v any) error {
		data, err := marshal(v)
		if err != nil {
			return fmt.Errorf("authoring: write %s: %w", at, err)
		}
		f, err := file(at)
		if err != nil {
			return err
		}
		if _, err := f.Write(data); err != nil {
			return fmt.Errorf("authoring: write %s to the archive: %w", at, err)
		}
		sum := sha256.Sum256(data)
		c.manifest.Sections = append(c.manifest.Sections, ManifestSection{Name: name, Path: at, Items: items,
			SHA256: hex.EncodeToString(sum[:])})
		return nil
	}
	paged := func(name, at string, fill func(add func(any) error) error) error {
		f, err := file(at)
		if err != nil {
			return err
		}
		h := sha256.New()
		iw, err := newItemsWriter(io.MultiWriter(f, h))
		if err != nil {
			return fmt.Errorf("authoring: write %s: %w", at, err)
		}
		if err := fill(iw.add); err != nil {
			return err
		}
		if err := iw.close(); err != nil {
			return fmt.Errorf("authoring: write %s: %w", at, err)
		}
		c.manifest.Sections = append(c.manifest.Sections, ManifestSection{Name: name, Path: at, Items: iw.n,
			SHA256: hex.EncodeToString(h.Sum(nil))})
		return nil
	}
	h := c.holdings
	at := c.manifest.ExportedAt
	if err := whole("account", personaldata.AccountPath, 1, c.account); err != nil {
		return err
	}
	if err := whole("email links", personaldata.EmailLinksPath, len(c.emailLinks),
		Items[PersonalEmailLink]{Items: c.emailLinks}); err != nil {
		return err
	}
	if err := whole("sessions", personaldata.SessionsPath, len(c.sessions),
		Items[PersonalSession]{Items: c.sessions}); err != nil {
		return err
	}
	if err := whole("API tokens", personaldata.TokensPath, len(h.Tokens),
		Items[PersonalToken]{Items: personalTokens(h.Tokens)}); err != nil {
		return err
	}
	if err := whole("memberships", personaldata.MembershipsPath, len(h.Memberships),
		Items[PersonalMembership]{Items: personalMemberships(h.Memberships)}); err != nil {
		return err
	}
	if err := whole("invitations", personaldata.InvitesPath, len(h.Invites),
		Items[PersonalInvite]{Items: personalInvites(h.Invites, at)}); err != nil {
		return err
	}
	if err := whole("imported identities", personaldata.IdentitiesPath, len(h.Identities),
		Items[PersonalIdentity]{Items: personalIdentities(h.Identities)}); err != nil {
		return err
	}
	if err := paged("audit entries of what you did", personaldata.ActedPath, func(add func(any) error) error {
		return pageAudit(func(after personaldata.AuditCursor) ([]personaldata.AuditEntry, error) {
			return s.personal.Acted(ctx, user, at, after, auditPage)
		}, func(e personaldata.AuditEntry) error { return add(actedEntry(e)) })
	}); err != nil {
		return err
	}
	if err := paged("audit entries of what others did to you", personaldata.ActedOnPath,
		func(add func(any) error) error {
			return pageAudit(func(after personaldata.AuditCursor) ([]personaldata.AuditEntry, error) {
				return s.personal.ActedOn(ctx, user, h.Subjects, at, after, auditPage)
			}, func(e personaldata.AuditEntry) error {
				return add(PersonalActedOnEntry{Action: e.Action, Result: e.Result,
					Actor: personaldata.ActorLabel(e.Action, e.ActorUserID != (ids.UUID{})), CreatedAt: e.CreatedAt.UTC()})
			})
		}); err != nil {
		return err
	}
	if err := paged("comments", personaldata.CommentsPath, func(add func(any) error) error {
		return pageByID(func(after ids.UUID) ([]personaldata.Comment, error) {
			return s.personal.Comments(ctx, user, after, commentPage)
		}, func(c personaldata.Comment) ids.UUID { return c.ID }, commentPage,
			func(c personaldata.Comment) error { return add(personalComment(c)) })
	}); err != nil {
		return err
	}
	if err := paged("notifications", personaldata.NotificationsPath, func(add func(any) error) error {
		return pageByID(func(after ids.UUID) ([]personaldata.Notification, error) {
			return s.personal.Notifications(ctx, user, after, pageItems)
		}, func(n personaldata.Notification) ids.UUID { return n.ID }, pageItems,
			func(n personaldata.Notification) error { return add(personalNotification(n)) })
	}); err != nil {
		return err
	}
	if err := paged("attribution", personaldata.AttributionPath, func(add func(any) error) error {
		for _, a := range personaldata.Attributions {
			kind, kinded := kindNamed(a.Table)
			err := pageByID(func(after ids.UUID) ([]personaldata.Attributed, error) {
				return s.personal.Attribution(ctx, user, a, after, referencePage)
			}, func(r personaldata.Attributed) ids.UUID { return r.ID }, referencePage,
				func(r personaldata.Attributed) error {
					ref := PersonalReference{Entity: a.Table, ID: r.ID.String(),
						WorkspaceID: ids.Encode(ids.Workspace, r.WorkspaceID), ProjectID: encodeID(ids.Project, r.ProjectID),
						Relation: a.Relation, At: r.At.UTC()}
					if kinded {
						ref.ID = ids.Encode(kind, r.ID)
					}
					return add(ref)
				})
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	f, err := file(personaldata.ReadmePath)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(f, personalReadme(c.manifest)); err != nil {
		return fmt.Errorf("authoring: write the README to the archive: %w", err)
	}
	manifest, err := marshal(c.manifest)
	if err != nil {
		return fmt.Errorf("authoring: write the manifest: %w", err)
	}
	if f, err = file(personaldata.ManifestPath); err != nil {
		return err
	}
	if _, err := f.Write(manifest); err != nil {
		return fmt.Errorf("authoring: write the manifest to the archive: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("authoring: finish the archive: %w", err)
	}
	return nil
}

// pageAudit reads the audit entries that read gives a page at a time, from
// the first, and passes each to each, until a page is short of auditPage.
func pageAudit(read func(after personaldata.AuditCursor) ([]personaldata.AuditEntry, error),
	each func(personaldata.AuditEntry) error) error {
	var after personaldata.AuditCursor
	for {
		page, err := read(after)
		if err != nil {
			return err
		}
		for _, e := range page {
			if err := each(e); err != nil {
				return err
			}
		}
		if len(page) < auditPage {
			return nil
		}
		after = page[len(page)-1].Cursor()
	}
}

// pageByID reads the rows that read gives a page of size at a time, in the
// order of their IDs, from the first, and passes each to each, until a page is
// short of size.
func pageByID[T any](read func(after ids.UUID) ([]T, error), id func(T) ids.UUID, size int,
	each func(T) error) error {
	var after ids.UUID
	for {
		page, err := read(after)
		if err != nil {
			return err
		}
		for _, r := range page {
			if err := each(r); err != nil {
				return err
			}
		}
		if len(page) < size {
			return nil
		}
		after = id(page[len(page)-1])
	}
}

// itemsWriter writes the file of a section whose items are read a page at a
// time, one item to a line, as the audit log of a workspace's archive is
// written.
type itemsWriter struct {
	w io.Writer
	// n is how many items it wrote.
	n int
}

// newItemsWriter starts the file of a section in w.
func newItemsWriter(w io.Writer) (*itemsWriter, error) {
	if _, err := io.WriteString(w, "{\n  \"items\": ["); err != nil {
		return nil, err
	}
	return &itemsWriter{w: w}, nil
}

// add writes the item v.
func (iw *itemsWriter) add(v any) error {
	data, err := marshalLine(v)
	if err != nil {
		return err
	}
	sep := ",\n    "
	if iw.n == 0 {
		sep = "\n    "
	}
	if _, err := io.WriteString(iw.w, sep); err != nil {
		return err
	}
	if _, err := iw.w.Write(data); err != nil {
		return err
	}
	iw.n++
	return nil
}

// close ends the file.
func (iw *itemsWriter) close() error {
	end := "]\n}\n"
	if iw.n > 0 {
		end = "\n  ]\n}\n"
	}
	_, err := io.WriteString(iw.w, end)
	return err
}

// encodeID returns u as an ID of the kind k, or "" for the zero UUID.
func encodeID(k ids.Kind, u ids.UUID) string {
	if u == (ids.UUID{}) {
		return ""
	}
	return ids.Encode(k, u)
}

// kindNamed returns the kind of entity named name, as a table of its rows is.
func kindNamed(name string) (ids.Kind, bool) {
	for _, k := range ids.Kinds() {
		if k.String() == name {
			return k, true
		}
	}
	return 0, false
}

// timeAt returns t in UTC, or nil for nil.
func timeAt(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// actedEntry returns the audit entry e, whose actor is the user, as the
// archive holds it: whole.
func actedEntry(e personaldata.AuditEntry) PersonalActedEntry {
	out := PersonalActedEntry{ID: e.ID.String(), WorkspaceID: encodeID(ids.Workspace, e.WorkspaceID),
		Action: e.Action, ActorUserID: ids.Encode(ids.User, e.ActorUserID),
		ActorTokenID: encodeID(ids.APIToken, e.ActorTokenID), Result: e.Result, UserAgent: e.UserAgent,
		RequestID: e.RequestID, Metadata: Value(e.Metadata), CreatedAt: e.CreatedAt.UTC()}
	if e.SubjectID != (ids.UUID{}) {
		out.SubjectKind, out.SubjectID = e.SubjectKind, e.SubjectID.String()
		if k, ok := kindNamed(e.SubjectKind); ok {
			out.SubjectID = ids.Encode(k, e.SubjectID)
		}
	}
	if e.IP.IsValid() {
		out.IP = e.IP.String()
	}
	return out
}

func personalTokens(in []personaldata.Token) []PersonalToken {
	out := make([]PersonalToken, len(in))
	for i, t := range in {
		scopes := t.Scopes
		if scopes == nil {
			scopes = []string{}
		}
		out[i] = PersonalToken{ID: ids.Encode(ids.APIToken, t.ID), Name: t.Name,
			WorkspaceID: ids.Encode(ids.Workspace, t.WorkspaceID), ProjectID: encodeID(ids.Project, t.ProjectID),
			Scopes: scopes, CreatedAt: t.CreatedAt.UTC(), LastUsedAt: timeAt(t.LastUsedAt),
			ExpiresAt: timeAt(t.ExpiresAt), RevokedAt: timeAt(t.RevokedAt)}
	}
	return out
}

func personalMemberships(in []personaldata.Membership) []PersonalMembership {
	out := make([]PersonalMembership, len(in))
	for i, m := range in {
		grants := make([]MemberGrant, len(m.Grants))
		for j, g := range m.Grants {
			grants[j] = MemberGrant{ProjectID: ids.Encode(ids.Project, g.ProjectID), Role: g.Role}
		}
		out[i] = PersonalMembership{MembershipID: ids.Encode(ids.Membership, m.ID),
			WorkspaceID: ids.Encode(ids.Workspace, m.WorkspaceID), WorkspaceName: m.WorkspaceName, Role: m.Role,
			JoinedAt: m.JoinedAt.UTC(), Grants: grants}
	}
	return out
}

// personalInvites returns the invitations in, each with where it was at the
// time at.
func personalInvites(in []personaldata.Invite, at time.Time) []PersonalInvite {
	out := make([]PersonalInvite, len(in))
	for i, inv := range in {
		kind, status := "link", "open"
		if inv.Email {
			kind = "email"
		}
		switch {
		case inv.AcceptedAt != nil:
			status = "accepted"
		case inv.RevokedAt != nil:
			status = "revoked"
		case !inv.ExpiresAt.After(at):
			status = "expired"
		}
		out[i] = PersonalInvite{ID: ids.Encode(ids.Invite, inv.ID), WorkspaceID: ids.Encode(ids.Workspace, inv.WorkspaceID),
			WorkspaceName: inv.WorkspaceName, Kind: kind, Role: inv.Role, Status: status, CreatedAt: inv.CreatedAt.UTC(),
			ExpiresAt: inv.ExpiresAt.UTC(), AcceptedAt: timeAt(inv.AcceptedAt), RevokedAt: timeAt(inv.RevokedAt)}
	}
	return out
}

func personalIdentities(in []personaldata.Identity) []PersonalIdentity {
	out := make([]PersonalIdentity, len(in))
	for i, id := range in {
		out[i] = PersonalIdentity{WorkspaceID: ids.Encode(ids.Workspace, id.WorkspaceID), DisplayName: id.DisplayName,
			CreatedAt: id.CreatedAt.UTC()}
	}
	return out
}

func personalComment(c personaldata.Comment) PersonalComment {
	return PersonalComment{ID: ids.Encode(ids.Comment, c.ID), WorkspaceID: ids.Encode(ids.Workspace, c.WorkspaceID),
		ProjectID: ids.Encode(ids.Project, c.ProjectID), ThreadID: ids.Encode(ids.Comment, c.ThreadID),
		ParentID: encodeID(ids.Comment, c.ParentID),
		Target:   PersonalTarget{Kind: c.TargetKind, ID: ids.Encode(targetKinds[c.TargetKind], c.TargetID)},
		Body:     Text(c.Body), CreatedAt: c.CreatedAt.UTC(), EditedAt: timeAt(c.EditedAt),
		DeletedAt: timeAt(c.DeletedAt), ResolvedAt: timeAt(c.ResolvedAt)}
}

func personalNotification(n personaldata.Notification) PersonalNotification {
	return PersonalNotification{ID: ids.Encode(ids.Notification, n.ID),
		WorkspaceID: ids.Encode(ids.Workspace, n.WorkspaceID), ProjectID: ids.Encode(ids.Project, n.ProjectID),
		CommentID: ids.Encode(ids.Comment, n.CommentID), Kind: n.Kind, CreatedAt: n.CreatedAt.UTC(),
		ReadAt: timeAt(n.ReadAt)}
}
