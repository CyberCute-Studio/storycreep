package personaldata

import (
	"context"
	"encoding/json"
	"net/netip"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// Reader reads a user's rows of every workspace, whether or not the user is
// still a member of it, for the personal-data export (PRD FR-GDPR-01, §5.4):
// package maintenance implements it, through the maintenance path, which only
// the export's build calls it on. Each page is read in a transaction of its
// own, so that the build holds none open while it writes the archive.
type Reader interface {
	// Holdings returns the user's memberships, with their grants, API tokens,
	// invitations, and imported identities, of every workspace, as of one
	// moment, and the subjects of the audit log's entries about the user.
	// email is the user's address, which the invitations are to, or "" for an
	// account that has not verified its address, which anyone may have
	// registered, so that it learns of no invitation to the address but those
	// that it accepted.
	Holdings(ctx context.Context, user ids.UUID, email string) (Holdings, error)
	// Acted returns at most limit of the audit entries whose actor is the
	// user, made by until, in the order in which they were made, after the
	// entry after, or from the first for the zero AuditCursor.
	Acted(ctx context.Context, user ids.UUID, until time.Time, after AuditCursor, limit int) ([]AuditEntry, error)
	// ActedOn returns at most limit of the audit entries about the user, or
	// about one of subjects, whose actor is someone else, or nobody, made by
	// until, in the order in which they were made, after the entry after.
	ActedOn(ctx context.Context, user ids.UUID, subjects []ids.UUID, until time.Time, after AuditCursor,
		limit int) ([]AuditEntry, error)
	// Comments returns at most limit of the comments that the user wrote, in
	// the trash too, in the order of their IDs, after the comment after, or
	// from the first for the zero UUID.
	Comments(ctx context.Context, user, after ids.UUID, limit int) ([]Comment, error)
	// Notifications returns at most limit of the user's notifications, in the
	// order of their IDs, after the notification after.
	Notifications(ctx context.Context, user, after ids.UUID, limit int) ([]Notification, error)
	// Attribution returns at most limit of the rows of a's table whose column
	// is the user, in the order of their IDs, after the row after. a is one
	// of Attributions().
	Attribution(ctx context.Context, user ids.UUID, a Attribution, after ids.UUID, limit int) ([]Attributed, error)
}

// Holdings are the user's rows of every workspace that the export reads
// whole, as each is small.
type Holdings struct {
	// Memberships are the user's memberships, in the order of their IDs.
	Memberships []Membership
	// Tokens are the user's API tokens, revoked ones too, in the order of
	// their IDs.
	Tokens []Token
	// Invites are the invitations to the user's address, if it is verified,
	// and those that the user accepted, in the order of their IDs.
	Invites []Invite
	// Identities are the imported identities of other workspaces that stand
	// for the user, as an archive that named the user made them (FR-DATA-01),
	// in the order of their workspaces' IDs and their display names.
	Identities []Identity
	// Subjects are the IDs of what an audit entry about the user may be of,
	// beside the account: its memberships, those that are gone too, as the
	// log's entries of their removal or their creation name the user, and its
	// API tokens, those of deleted projects too, as the log's entries of their
	// creation and their revocation name the user.
	Subjects []ids.UUID
}

// Membership is a membership of the user's.
type Membership struct {
	ID, WorkspaceID ids.UUID
	WorkspaceName   string
	Role            string
	JoinedAt        time.Time
	Grants          []Grant
}

// Grant is a grant of a membership's on a project.
type Grant struct {
	ProjectID ids.UUID
	Role      string
}

// Token is an API token of the user's, without its hash.
type Token struct {
	ID, WorkspaceID ids.UUID
	// ProjectID is the project that the token is narrowed to, or zero.
	ProjectID  ids.UUID
	Name       string
	Scopes     []string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
}

// Invite is an invitation to the user's address, or one that the user
// accepted, without its hash, nor who made it.
type Invite struct {
	ID, WorkspaceID ids.UUID
	WorkspaceName   string
	// Email says whether it is to an address, rather than whoever holds its
	// link.
	Email     bool
	Role      string
	CreatedAt time.Time
	ExpiresAt time.Time
	// Expired says whether it had expired as of the moment that it was read.
	Expired    bool
	AcceptedAt *time.Time
	RevokedAt  *time.Time
}

// Identity is an imported identity of another workspace that stands for the
// user.
type Identity struct {
	WorkspaceID ids.UUID
	DisplayName string
	CreatedAt   time.Time
}

// AuditCursor is where a page of audit entries starts after: the entry made
// at At with the ID ID. The zero AuditCursor starts at the first.
type AuditCursor struct {
	At time.Time
	ID ids.UUID
}

// AuditEntry is an entry of the audit log.
type AuditEntry struct {
	ID ids.UUID
	// WorkspaceID is the entry's workspace, or zero for an entry of none.
	WorkspaceID ids.UUID
	// ActorUserID and ActorTokenID are zero where there is none.
	ActorUserID, ActorTokenID ids.UUID
	Action                    string
	SubjectKind               string
	// SubjectID is zero where there is none.
	SubjectID ids.UUID
	Result    string
	// IP is the zero Addr where it is unknown.
	IP        netip.Addr
	UserAgent string
	RequestID string
	Metadata  json.RawMessage
	CreatedAt time.Time
}

// Cursor returns where the page after e starts.
func (e AuditEntry) Cursor() AuditCursor { return AuditCursor{At: e.CreatedAt, ID: e.ID} }

// Comment is a comment that the user wrote.
type Comment struct {
	ID, WorkspaceID, ProjectID ids.UUID
	// ParentID is a reply's root, or zero for a root.
	ParentID ids.UUID
	ThreadID ids.UUID
	// TargetKind and TargetID are the thread's target: node, connection,
	// board, or variable.
	TargetKind string
	TargetID   ids.UUID
	// Body is the comment's rich-text document, as it is stored.
	Body       json.RawMessage
	CreatedAt  time.Time
	EditedAt   *time.Time
	DeletedAt  *time.Time
	ResolvedAt *time.Time
}

// Notification is a notification of the user's, without who made it.
type Notification struct {
	ID, WorkspaceID, ProjectID, CommentID ids.UUID
	Kind                                  string
	CreatedAt                             time.Time
	ReadAt                                *time.Time
}

// Attributed is a row whose Attribution's column names the user.
type Attributed struct {
	ID, WorkspaceID ids.UUID
	// ProjectID is the row's project, or zero for a workspace's own asset.
	ProjectID ids.UUID
	At        time.Time
}
