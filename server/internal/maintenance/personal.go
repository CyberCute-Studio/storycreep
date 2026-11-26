package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/personaldata"
)

// The personal-data export's reads (PRD FR-GDPR-01, §5.4, plan note N94): a
// user's own rows of every workspace, whether or not the user is still a
// member of it, which only the export's build reads, to write them into that
// user's archive. Each reads the rows that name the user alone, as the
// inventory of §9.6 says, and changes nothing.

var _ personaldata.Reader = (*DB)(nil)

// Holdings returns the user's memberships, grants, API tokens, invitations,
// and imported identities, and the subjects of the audit entries about the
// user, as of one moment.
func (db *DB) Holdings(ctx context.Context, user ids.UUID, email string) (personaldata.Holdings, error) {
	var h personaldata.Holdings
	err := db.inSnapshot(ctx, func(tx pgx.Tx) error {
		var err error
		if h.Memberships, err = memberships(ctx, tx, user); err != nil {
			return err
		}
		if h.Tokens, err = tokens(ctx, tx, user); err != nil {
			return err
		}
		if h.Invites, err = invites(ctx, tx, user, email); err != nil {
			return err
		}
		if h.Identities, err = identities(ctx, tx, user); err != nil {
			return err
		}
		h.Subjects, err = subjects(ctx, tx, user)
		return err
	})
	return h, err
}

// memberships returns the user's memberships, each with its grants.
func memberships(ctx context.Context, tx pgx.Tx, user ids.UUID) ([]personaldata.Membership, error) {
	rows, err := tx.Query(ctx, `SELECT m.id, m.workspace_id, w.name, m.role, m.joined_at FROM membership m
		JOIN workspace w ON w.id = m.workspace_id WHERE m.user_id = $1 ORDER BY m.id`, user)
	var out []personaldata.Membership
	if err == nil {
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (personaldata.Membership, error) {
			m := personaldata.Membership{Grants: []personaldata.Grant{}}
			err := r.Scan(&m.ID, &m.WorkspaceID, &m.WorkspaceName, &m.Role, &m.JoinedAt)
			return m, err
		})
	}
	if err != nil {
		return nil, fmt.Errorf("maintenance: read the user's memberships: %w", err)
	}
	rows, err = tx.Query(ctx, `SELECT g.membership_id, g.project_id, g.role FROM project_grant g
		JOIN membership m ON m.workspace_id = g.workspace_id AND m.id = g.membership_id
		WHERE m.user_id = $1 ORDER BY g.membership_id, g.project_id`, user)
	type grant struct {
		membership ids.UUID
		personaldata.Grant
	}
	var grants []grant
	if err == nil {
		grants, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (grant, error) {
			var g grant
			err := r.Scan(&g.membership, &g.ProjectID, &g.Role)
			return g, err
		})
	}
	if err != nil {
		return nil, fmt.Errorf("maintenance: read the user's grants: %w", err)
	}
	for _, g := range grants {
		i := slices.IndexFunc(out, func(m personaldata.Membership) bool { return m.ID == g.membership })
		if i >= 0 {
			out[i].Grants = append(out[i].Grants, g.Grant)
		}
	}
	return out, nil
}

// tokens returns the user's API tokens, without their hashes.
func tokens(ctx context.Context, tx pgx.Tx, user ids.UUID) ([]personaldata.Token, error) {
	rows, err := tx.Query(ctx, `SELECT id, workspace_id, project_id, name, scopes, created_at, last_used_at, expires_at,
		revoked_at FROM api_token WHERE user_id = $1 ORDER BY id`, user)
	var out []personaldata.Token
	if err == nil {
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (personaldata.Token, error) {
			var t personaldata.Token
			var project pgtype.UUID
			err := r.Scan(&t.ID, &t.WorkspaceID, &project, &t.Name, &t.Scopes, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt,
				&t.RevokedAt)
			t.ProjectID = project.Bytes
			return t, err
		})
	}
	if err != nil {
		return nil, fmt.Errorf("maintenance: read the user's API tokens: %w", err)
	}
	return out, nil
}

// invites returns the invitations to the address email, as addresses compare,
// unless email is "", and those that the user accepted, without their hashes,
// nor who made them, each with whether it had expired as of the snapshot.
func invites(ctx context.Context, tx pgx.Tx, user ids.UUID, email string) ([]personaldata.Invite, error) {
	rows, err := tx.Query(ctx, `SELECT i.id, i.workspace_id, w.name, i.email IS NOT NULL, i.role, i.created_at,
			i.expires_at, i.expires_at <= now(), i.accepted_at, i.revoked_at
		FROM invite i JOIN workspace w ON w.id = i.workspace_id
		WHERE ($2 <> '' AND i.email = $2::citext) OR i.accepted_by = $1 ORDER BY i.id`, user, email)
	var out []personaldata.Invite
	if err == nil {
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (personaldata.Invite, error) {
			var i personaldata.Invite
			err := r.Scan(&i.ID, &i.WorkspaceID, &i.WorkspaceName, &i.Email, &i.Role, &i.CreatedAt, &i.ExpiresAt,
				&i.Expired, &i.AcceptedAt, &i.RevokedAt)
			return i, err
		})
	}
	if err != nil {
		return nil, fmt.Errorf("maintenance: read the user's invitations: %w", err)
	}
	return out, nil
}

// identities returns the imported identities that stand for the user, whose
// ID an archive named.
func identities(ctx context.Context, tx pgx.Tx, user ids.UUID) ([]personaldata.Identity, error) {
	rows, err := tx.Query(ctx, `SELECT workspace_id, display_name, created_at FROM imported_user
		WHERE source_user_id = $1 ORDER BY workspace_id, display_name`, user)
	var out []personaldata.Identity
	if err == nil {
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (personaldata.Identity, error) {
			var i personaldata.Identity
			err := r.Scan(&i.WorkspaceID, &i.DisplayName, &i.CreatedAt)
			return i, err
		})
	}
	if err != nil {
		return nil, fmt.Errorf("maintenance: read the user's imported identities: %w", err)
	}
	return out, nil
}

// subjects returns the IDs of the user's memberships and API tokens, those
// that are gone too, as the subjects of the entries about the user: a
// membership that was removed, or that the operator's grant-role made, and a
// token, whose entries' metadata names the user (user_id); a membership that
// the user made by accepting an invitation, as its creation's actor; and the
// membership of a workspace that the user created, which its creation's
// metadata names (membership_id). Only those entries name a membership's
// member, so that one of another member's, which the user acted on as an
// owner or an admin, is never among them.
func subjects(ctx context.Context, tx pgx.Tx, user ids.UUID) ([]ids.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM membership WHERE user_id = $1
		UNION SELECT id FROM api_token WHERE user_id = $1
		UNION SELECT subject_id FROM audit_entry
			WHERE metadata ? 'user_id' AND metadata ->> 'user_id' = $2 AND subject_id IS NOT NULL
				AND subject_kind IN ('membership', 'api_token')
		UNION SELECT subject_id FROM audit_entry
			WHERE actor_user_id = $1 AND action = 'membership.create' AND subject_id IS NOT NULL
		UNION SELECT (metadata ->> 'membership_id')::uuid FROM audit_entry
			WHERE actor_user_id = $1 AND action = 'workspace.create' AND metadata ? 'membership_id'
		ORDER BY 1`, user, user.String())
	var out []ids.UUID
	if err == nil {
		out, err = pgx.CollectRows(rows, pgx.RowTo[ids.UUID])
	}
	if err != nil {
		return nil, fmt.Errorf("maintenance: read the subjects of the user's audit entries: %w", err)
	}
	return out, nil
}

// auditColumns are the columns of an audit entry that auditEntry scans.
const auditColumns = `id, workspace_id, actor_user_id, actor_token_id, action,
	coalesce(subject_kind, '') AS subject_kind, subject_id, result, ip, coalesce(user_agent, '') AS user_agent,
	coalesce(request_id, '') AS request_id, metadata, created_at`

// Acted returns a page of the audit entries whose actor is the user.
func (db *DB) Acted(ctx context.Context, user ids.UUID, until time.Time, after personaldata.AuditCursor,
	limit int) ([]personaldata.AuditEntry, error) {
	return db.auditPage(ctx, `SELECT `+auditColumns+` FROM audit_entry
		WHERE actor_user_id = $1 AND created_at <= $2 AND (created_at, id) > ($3, $4)
		ORDER BY created_at, id LIMIT $5`, user, until, after.At, after.ID, limit)
}

// ActedOn returns a page of the audit entries about the user, or one of
// subjects, whose actor is someone else, or nobody. Each subject's entries
// are read in their order from the log's index of its subjects, at most a
// page of them, so that a page reads no more of a subject's entries whose
// actor is not the user than it may hold, however many there are after it,
// as of failed sign-ins with the user's address. The index also holds the user's own
// entries about their account, as their sign-ins, which a page passes over
// after its cursor, as many as the user made.
func (db *DB) ActedOn(ctx context.Context, user ids.UUID, subjects []ids.UUID, until time.Time,
	after personaldata.AuditCursor, limit int) ([]personaldata.AuditEntry, error) {
	about := append([]ids.UUID{user}, subjects...)
	return db.auditPage(ctx, `SELECT e.* FROM (SELECT DISTINCT s FROM unnest($2::uuid[]) AS s) AS about
		CROSS JOIN LATERAL (SELECT `+auditColumns+` FROM audit_entry
			WHERE subject_id = about.s AND subject_kind IN ('user', 'membership', 'api_token')
				AND actor_user_id IS DISTINCT FROM $1 AND created_at <= $3 AND (created_at, id) > ($4, $5)
			ORDER BY created_at, id LIMIT $6) AS e
		ORDER BY e.created_at, e.id LIMIT $6`, user, about, until, after.At, after.ID, limit)
}

// auditPage runs the query of a page of audit entries with args.
func (db *DB) auditPage(ctx context.Context, query string, args ...any) ([]personaldata.AuditEntry, error) {
	var out []personaldata.AuditEntry
	err := db.inSnapshot(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, query, args...)
		if err == nil {
			out, err = pgx.CollectRows(rows, auditEntry)
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("maintenance: read the user's audit entries: %w", err)
	}
	return out, nil
}

// auditEntry scans an entry of auditColumns.
func auditEntry(r pgx.CollectableRow) (personaldata.AuditEntry, error) {
	var e personaldata.AuditEntry
	var workspace, actor, token, subject pgtype.UUID
	var ip *netip.Addr
	var metadata []byte
	if err := r.Scan(&e.ID, &workspace, &actor, &token, &e.Action, &e.SubjectKind, &subject, &e.Result, &ip,
		&e.UserAgent, &e.RequestID, &metadata, &e.CreatedAt); err != nil {
		return e, err
	}
	e.WorkspaceID, e.ActorUserID, e.ActorTokenID, e.SubjectID = workspace.Bytes, actor.Bytes, token.Bytes, subject.Bytes
	if ip != nil {
		e.IP = *ip
	}
	e.Metadata = json.RawMessage(metadata)
	return e, nil
}

// Comments returns a page of the comments that the user wrote, each with its
// thread's target.
func (db *DB) Comments(ctx context.Context, user, after ids.UUID, limit int) ([]personaldata.Comment, error) {
	var out []personaldata.Comment
	err := db.inSnapshot(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT c.id, c.workspace_id, c.project_id, c.parent_id, c.thread_id,
				CASE WHEN r.node_id IS NOT NULL THEN 'node' WHEN r.connection_id IS NOT NULL THEN 'connection'
					WHEN r.board_id IS NOT NULL THEN 'board' ELSE 'variable' END,
				coalesce(r.node_id, r.connection_id, r.board_id, r.variable_id),
				c.body, c.created_at, c.edited_at, c.deleted_at, c.resolved_at
			FROM comment c
			JOIN comment r ON r.workspace_id = c.workspace_id AND r.project_id = c.project_id AND r.id = c.thread_id
			WHERE c.created_by = $1 AND c.id > $2 ORDER BY c.id LIMIT $3`, user, after, limit)
		if err == nil {
			out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (personaldata.Comment, error) {
				var c personaldata.Comment
				var parent pgtype.UUID
				var body []byte
				err := r.Scan(&c.ID, &c.WorkspaceID, &c.ProjectID, &parent, &c.ThreadID, &c.TargetKind, &c.TargetID,
					&body, &c.CreatedAt, &c.EditedAt, &c.DeletedAt, &c.ResolvedAt)
				c.ParentID, c.Body = parent.Bytes, json.RawMessage(body)
				return c, err
			})
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("maintenance: read the user's comments: %w", err)
	}
	return out, nil
}

// Notifications returns a page of the user's notifications, without who made
// them.
func (db *DB) Notifications(ctx context.Context, user, after ids.UUID, limit int) ([]personaldata.Notification,
	error) {
	var out []personaldata.Notification
	err := db.inSnapshot(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, workspace_id, project_id, comment_id, kind, created_at, read_at
			FROM notification WHERE user_id = $1 AND id > $2 ORDER BY id LIMIT $3`, user, after, limit)
		if err == nil {
			out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (personaldata.Notification, error) {
				var n personaldata.Notification
				err := r.Scan(&n.ID, &n.WorkspaceID, &n.ProjectID, &n.CommentID, &n.Kind, &n.CreatedAt, &n.ReadAt)
				return n, err
			})
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("maintenance: read the user's notifications: %w", err)
	}
	return out, nil
}

// errNoAttribution is the error of an Attribution that is not one of
// personaldata.Attributions(), whose table and columns the maintenance path
// does not read.
var errNoAttribution = errors.New("maintenance: not one of the attributions of the personal-data export")

// Attribution returns a page of the rows of a's table whose column is the
// user.
func (db *DB) Attribution(ctx context.Context, user ids.UUID, a personaldata.Attribution, after ids.UUID,
	limit int) ([]personaldata.Attributed, error) {
	if !slices.Contains(personaldata.Attributions(), a) {
		return nil, errNoAttribution
	}
	query := fmt.Sprintf("SELECT id, workspace_id, %s, %s FROM %s WHERE %s = $1 AND id > $2 ORDER BY id LIMIT $3",
		pgx.Identifier{a.Project}.Sanitize(), pgx.Identifier{a.At}.Sanitize(), pgx.Identifier{a.Table}.Sanitize(),
		pgx.Identifier{a.Column}.Sanitize())
	var out []personaldata.Attributed
	err := db.inSnapshot(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, query, user, after, limit)
		if err == nil {
			out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (personaldata.Attributed, error) {
				var a personaldata.Attributed
				var project pgtype.UUID
				var at pgtype.Timestamptz
				err := r.Scan(&a.ID, &a.WorkspaceID, &project, &at)
				a.ProjectID, a.At = project.Bytes, at.Time
				return a, err
			})
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("maintenance: read the user's %s.%s: %w", a.Table, a.Column, err)
	}
	return out, nil
}

// inSnapshot runs fn as inTx does, in a transaction that only reads, as of
// one moment.
func (db *DB) inSnapshot(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly},
		func(tx pgx.Tx) error {
			if err := database.SetMaintenanceRole(ctx, tx); err != nil {
				return err
			}
			return fn(tx)
		})
}
