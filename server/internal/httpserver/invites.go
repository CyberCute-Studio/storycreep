package httpserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/invite"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
	"github.com/CyberCute-Studio/storycreep/server/internal/workspace"
)

// Members and invitations on the wire (PRD FR-TEN-02, FR-AUTH-03): a
// workspace's member list and the removal of a member, its invitations,
// which its owners and admins make, send anew, and revoke, and the routes
// that the holder of an invitation's link reads it, accepts it, and
// registers an account with. A link opens the web app's page /app/invite,
// with its token in the URL's fragment, which the page sends to the API.

// memberBody is a member of a workspace, as its member list shows them.
type memberBody struct {
	ID          string    `json:"id" doc:"The ID of the membership"`
	UserID      string    `json:"user_id" doc:"The ID of the member's account"`
	DisplayName string    `json:"display_name" doc:"The member's display name"`
	Email       string    `json:"email,omitempty" doc:"The member's email address, which only an owner or an admin of the workspace sees"`
	Role        string    `json:"role" enum:"owner,admin,editor,commenter,viewer,guest" doc:"The membership's role. A guest acts only on the projects granted to them."`
	JoinedAt    time.Time `json:"joined_at" doc:"When the member joined the workspace"`
}

func newMemberBody(m workspace.Member) memberBody {
	return memberBody{ID: ids.Encode(ids.Membership, m.MembershipID), UserID: ids.Encode(ids.User, m.UserID),
		DisplayName: m.DisplayName, Email: m.Email, Role: string(m.Role), JoinedAt: m.JoinedAt}
}

type membersInput struct {
	WorkspaceID string `path:"workspace_id" doc:"The workspace's ID"`
	api.PageParams
}

type membersOutput struct {
	Body api.Page[memberBody]
}

type removeMemberInput struct {
	CSRFInput
	WorkspaceID  string `path:"workspace_id" doc:"The workspace's ID"`
	MembershipID string `path:"membership_id" doc:"The ID of the membership to remove"`
}

// inviteBody is an invitation, as its workspace's owners and admins see it.
type inviteBody struct {
	ID        string     `json:"id" doc:"The invitation's ID"`
	Kind      string     `json:"kind" enum:"email,link" doc:"Whom it invites: email, the address of email, which the account that accepts it must have, or link, whoever holds its link"`
	Email     string     `json:"email,omitempty" doc:"The invited address, of an invitation of the kind email"`
	Role      string     `json:"role" enum:"owner,admin,editor,commenter,viewer,guest" doc:"The role that accepting it gives in the workspace"`
	Status    string     `json:"status" enum:"pending,expired" doc:"pending until it expires, and expired from then until it is sent anew. A pending invitation is accepted with its link once it has one: one that the instance emails has none until its email is sent, as sent_at says."`
	ExpiresAt time.Time  `json:"expires_at" doc:"When it expires: 14 days after it was made, sent anew, or emailed"`
	SentAt    *time.Time `json:"sent_at,omitempty" doc:"When the instance emailed its link, which then lasts its 14 days from then. Without it, no email holds its current link: the instance sends no email, the invitation is a link invitation, or its email is still to be sent, or could not be sent, as to an address that the SMTP server refuses."`
	CreatedBy string     `json:"created_by,omitempty" doc:"The ID of the account that made it, unless that account was deleted"`
	CreatedAt time.Time  `json:"created_at" doc:"When it was made"`
	URL       string     `json:"url,omitempty" doc:"Its link, which opens the web app's page that accepts it, with its token in the URL's fragment. It is here only in the answer that made the invitation or sent it anew, and only when the instance does not email it: for a link invitation, and for every invitation on an instance that sends no email. Whoever holds it can accept the invitation, so give it only to the person invited. The database holds no copy, and sending the invitation anew gives a new link, in place of this one."`
}

func newInviteBody(i invite.Invite) inviteBody {
	b := inviteBody{ID: ids.Encode(ids.Invite, i.ID), Kind: string(i.Kind()), Email: i.Email, Role: string(i.Role),
		Status: "pending", ExpiresAt: i.ExpiresAt, CreatedAt: i.CreatedAt}
	if i.Expired {
		b.Status = "expired"
	}
	if !i.SentAt.IsZero() {
		b.SentAt = &i.SentAt
	}
	if i.CreatedBy != (ids.UUID{}) {
		b.CreatedBy = ids.Encode(ids.User, i.CreatedBy)
	}
	if i.URL.IsSet() {
		b.URL = i.URL.Reveal()
	}
	return b
}

// createInviteBody is the body of POST
// /api/v1/workspaces/{workspace_id}/invites.
type createInviteBody struct {
	Email string `json:"email,omitempty" format:"idn-email" maxLength:"254" doc:"The email address to invite, which the account that accepts the invitation must have, in any case. Without it, the invitation is a link invitation, which whoever holds its link may accept, with any account."`
	Role  string `json:"role" enum:"owner,admin,editor,commenter,viewer,guest" doc:"The role that accepting the invitation gives. Only an owner invites someone as owner, and an admin is answered with 403 owner_required."`
}

type createInviteInput struct {
	CSRFInput
	WorkspaceID string `path:"workspace_id" doc:"The workspace's ID"`
	Body        createInviteBody
}

type inviteOutput struct {
	RateLimitOutput
	Body inviteBody
}

type invitesInput struct {
	WorkspaceID string `path:"workspace_id" doc:"The workspace's ID"`
	api.PageParams
}

type invitesOutput struct {
	Body api.Page[inviteBody]
}

type inviteActionInput struct {
	CSRFInput
	WorkspaceID string `path:"workspace_id" doc:"The workspace's ID"`
	InviteID    string `path:"invite_id" doc:"The invitation's ID"`
}

// inviteTokenBody names an invitation by its link's token.
type inviteTokenBody struct {
	Token string `json:"token" maxLength:"64" doc:"The token of the invitation's link: what follows token= in the fragment of the URL that the link opens"`
}

type lookupInviteInput struct {
	Body inviteTokenBody
}

// invitePreviewBody is what the holder of an invitation's link sees of it.
type invitePreviewBody struct {
	WorkspaceName string    `json:"workspace_name" doc:"The name of the workspace that it invites to"`
	Kind          string    `json:"kind" enum:"email,link" doc:"Whom it invites: email, the address of email, or link, whoever holds its link"`
	Email         string    `json:"email,omitempty" doc:"The invited address, which the account that accepts it must have, of an invitation of the kind email"`
	Role          string    `json:"role" enum:"owner,admin,editor,commenter,viewer,guest" doc:"The role that accepting it gives in the workspace"`
	ExpiresAt     time.Time `json:"expires_at" doc:"When it expires"`
}

type invitePreviewOutput struct {
	RateLimitOutput
	Body invitePreviewBody
}

type acceptInviteInput struct {
	CSRFInput
	Body inviteTokenBody
}

type acceptedInviteOutput struct {
	RateLimitOutput
	Body membershipBody
}

// registerWithInviteBody is the body of POST /api/v1/invites/register.
type registerWithInviteBody struct {
	Token       string `json:"token" maxLength:"64" doc:"The token of the invitation's link: what follows token= in the fragment of the URL that the link opens"`
	Email       string `json:"email,omitempty" format:"idn-email" maxLength:"254" doc:"The account's email address, as for POST /api/v1/auth/register. An invitation to an address takes only that address, as citext compares them, and the address is the invitation's when this is left out; a link invitation needs one."`
	DisplayName string `json:"display_name" maxLength:"100" doc:"The name that other people see, as for POST /api/v1/auth/register"`
	Password    string `json:"password" maxLength:"256" doc:"The password, as for POST /api/v1/auth/register"`
}

type registerWithInviteInput struct {
	Body registerWithInviteBody
}

// inviteEmailLimitDoc says, in the description of an operation that emails
// an invitation, how the pace of invitation emails to an address, from all
// workspaces together, puts the email off (ratelimit.InviteEmail).
const inviteEmailLimitDoc = " The instance also paces the invitations that it emails an address, from all " +
	"workspaces together: 10 at once, and then one every 6 minutes. The email of an invitation over that pace " +
	"takes a turn after those that wait already, and is sent then, in the order of the turns, and the " +
	"invitation's sent_at stays empty until then. Sending the invitation anew keeps its turn, and gives it 14 " +
	"days more; one that expires before its turn is not emailed. A workspace whose invitation is revoked while " +
	"its email waits emails the address no other invitation until 10 minutes after that turn."

// manageMembersDoc says, in the description of an operation that manages a
// workspace's members or invitations, who may.
const manageMembersDoc = " Only an owner or an admin of the workspace may, and anyone else with a role in it is " +
	"answered with 403 insufficient_role." + notFoundDoc

// inviteStateDoc says, in the description of an operation that uses an
// invitation's link, what a link that cannot be used is answered with.
const inviteStateDoc = " A token that belongs to no invitation, as one that sending the invitation anew replaced, " +
	"or whose workspace no longer exists, is answered with 400 token_invalid, an invitation that has been revoked " +
	"with 410 invite_revoked, one that has been accepted with 410 invite_accepted, and one that has expired with " +
	"410 invite_expired (FR-TEN-02 AC1)."

// registerInvites registers the routes of a workspace's members and
// invitations (PRD FR-TEN-02, FR-AUTH-03).
func registerInvites(m *api.Mux, workspaces *workspace.Service, invites *invite.Service, accounts *account.Service,
	limits *ratelimit.Limiter) {
	api.Register(m, api.ReadWorkspace, huma.Operation{
		OperationID: "listMembers",
		Method:      http.MethodGet,
		Path:        "/api/v1/workspaces/{workspace_id}/members",
		Summary:     "List a workspace's members",
		Description: "Lists the members of a workspace, guests included, in the order of their memberships' IDs, " +
			"oldest first, with their display names and roles, to every member with a role in it. Only its owners " +
			"and admins also see their email addresses." + notFoundDoc + sessionAuthDoc,
		Tags: []string{"Members"},
	}, func(ctx context.Context, in *membersInput) (*membersOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		id, err := ids.Decode(ids.Workspace, in.WorkspaceID)
		if err != nil {
			return nil, err
		}
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		members, err := workspaces.Members(ctx, cur.session.UserID, id, after, in.Fetch())
		if err != nil {
			return nil, memberError(err)
		}
		page, err := api.NewPage(in.PageParams, members, func(m workspace.Member) idKey {
			return idKey{ID: m.MembershipID.String()}
		})
		if err != nil {
			return nil, err
		}
		out := &membersOutput{Body: api.Page[memberBody]{Items: make([]memberBody, len(page.Items)),
			NextCursor: page.NextCursor}}
		for i, m := range page.Items {
			out.Body.Items[i] = newMemberBody(m)
		}
		return out, nil
	})

	api.Register(m, api.ManageMembers, huma.Operation{
		OperationID: "removeMember",
		Method:      http.MethodDelete,
		Path:        "/api/v1/workspaces/{workspace_id}/members/{membership_id}",
		Summary:     "Remove a member from a workspace",
		Description: "Removes a membership of a workspace, and with it the member's project grants there; the " +
			"account stays as it was. It also revokes the workspace's open invitations, expired or not, that the " +
			"member made, those whose link the member was last given as text, as by sending one anew, and those to " +
			"the member's address, so that no link that they hold lets them back in. " +
			"Only an owner removes an owner, and an admin is answered with 403 " +
			"owner_required. A workspace keeps an owner whose account is active, so removing its last one is " +
			"answered with 409 last_owner (FR-TEN-02 AC2). An owner or an admin may remove themselves, as long as " +
			"the workspace keeps an owner." + manageMembersDoc + " So is a membership that the workspace does not " +
			"have." + csrfDoc,
		Tags:          []string{"Members"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *removeMemberInput) (*struct{}, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		id, err := ids.Decode(ids.Workspace, in.WorkspaceID)
		if err != nil {
			return nil, err
		}
		membershipID, err := ids.Decode(ids.Membership, in.MembershipID)
		if err != nil {
			return nil, err
		}
		if err := workspaces.RemoveMember(ctx, cur.session.UserID, id, membershipID, clientFrom(ctx)); err != nil {
			return nil, memberError(err)
		}
		return &struct{}{}, nil
	})

	api.Register(m, api.ManageMembers, huma.Operation{
		OperationID: "createInvite",
		Method:      http.MethodPost,
		Path:        "/api/v1/workspaces/{workspace_id}/invites",
		Summary:     "Invite someone to a workspace",
		Description: "Invites an email address, or, without one, whoever holds the invitation's link, to join a " +
			"workspace with a role, for 14 days. Only an owner invites someone as owner, and an admin is answered " +
			"with 403 owner_required." + manageMembersDoc + " An address that belongs to a member of the workspace " +
			"is answered with 409 already_member, and one that has an open invitation of the workspace already, " +
			"expired or not, with 409 invite_exists. On an instance that sends email, as GET /api/v1/instance " +
			"says, an invitation to an address is emailed, with a link that lasts its 14 days from when the email " +
			"is sent, and the answer holds no link. A workspace emails an address one invitation in 10 minutes at " +
			"most, so the email of an invitation made sooner after the workspace last emailed the address, as " +
			"after revoking an invitation that was emailed and inviting the address again, is sent once those 10 " +
			"minutes have passed." + inviteEmailLimitDoc + " On an instance that sends no email, and for a link " +
			"invitation on every instance, the answer's url is the link, which the signed-in user gives the " +
			"person invited; the server keeps no copy of it. The email names the instance and the role, but " +
			"neither the workspace nor who invites, which the page that the link opens shows instead (POST " +
			"/api/v1/invites/lookup)." + csrfDoc + authLimitDoc,
		Tags:          []string{"Members"},
		DefaultStatus: http.StatusCreated,
		Middlewares:   huma.Middlewares{rateLimit(limits, ratelimit.Auth)},
	}, func(ctx context.Context, in *createInviteInput) (*inviteOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		id, err := ids.Decode(ids.Workspace, in.WorkspaceID)
		if err != nil {
			return nil, err
		}
		inv, err := invites.Create(ctx, cur.session.UserID, id, invite.Request{Email: in.Body.Email,
			Role: role.Role(in.Body.Role)}, clientFrom(ctx))
		if err != nil {
			return nil, memberError(err)
		}
		return &inviteOutput{Body: newInviteBody(inv)}, nil
	})

	api.Register(m, api.ManageMembers, huma.Operation{
		OperationID: "listInvites",
		Method:      http.MethodGet,
		Path:        "/api/v1/workspaces/{workspace_id}/invites",
		Summary:     "List a workspace's invitations",
		Description: "Lists the open invitations of a workspace, those that have been neither accepted nor revoked, " +
			"expired or not, in the order of their IDs, oldest first. Their links are not among them: the server " +
			"keeps none, and sending an invitation anew gives a new one." + manageMembersDoc + sessionAuthDoc,
		Tags: []string{"Members"},
	}, func(ctx context.Context, in *invitesInput) (*invitesOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		id, err := ids.Decode(ids.Workspace, in.WorkspaceID)
		if err != nil {
			return nil, err
		}
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := invites.List(ctx, cur.session.UserID, id, after, in.Fetch())
		if err != nil {
			return nil, memberError(err)
		}
		page, err := api.NewPage(in.PageParams, list, func(i invite.Invite) idKey {
			return idKey{ID: i.ID.String()}
		})
		if err != nil {
			return nil, err
		}
		out := &invitesOutput{Body: api.Page[inviteBody]{Items: make([]inviteBody, len(page.Items)),
			NextCursor: page.NextCursor}}
		for i, inv := range page.Items {
			out.Body.Items[i] = newInviteBody(inv)
		}
		return out, nil
	})

	api.Register(m, api.ManageMembers, huma.Operation{
		OperationID: "resendInvite",
		Method:      http.MethodPost,
		Path:        "/api/v1/workspaces/{workspace_id}/invites/{invite_id}/resend",
		Summary:     "Send an invitation anew",
		Description: "Gives an open invitation, expired or not, a new link and 14 days more. The old link stops " +
			"working at once. As when it was made, the new link is emailed, or is the answer's url, and an email " +
			"waits until 10 minutes have passed since the workspace last emailed the address, so sending an " +
			"invitation anew again and again emails it once in 10 minutes at most, with the latest link." +
			inviteEmailLimitDoc + " Only an owner sends an invitation as owner anew, and an admin is answered with " +
			"403 owner_required." +
			manageMembersDoc + " So is an invitation that the workspace does not have. One that has been accepted " +
			"is answered with 410 invite_accepted, one that has been revoked with 410 invite_revoked, and one to " +
			"an address that belongs to a member of the workspace by now with 409 already_member." + csrfDoc +
			authLimitDoc,
		Tags:        []string{"Members"},
		Middlewares: huma.Middlewares{rateLimit(limits, ratelimit.Auth)},
	}, func(ctx context.Context, in *inviteActionInput) (*inviteOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		id, inviteID, err := inviteIDs(in)
		if err != nil {
			return nil, err
		}
		inv, err := invites.Resend(ctx, cur.session.UserID, id, inviteID, clientFrom(ctx))
		if err != nil {
			return nil, memberError(err)
		}
		return &inviteOutput{Body: newInviteBody(inv)}, nil
	})

	api.Register(m, api.ManageMembers, huma.Operation{
		OperationID: "revokeInvite",
		Method:      http.MethodDelete,
		Path:        "/api/v1/workspaces/{workspace_id}/invites/{invite_id}",
		Summary:     "Revoke an invitation",
		Description: "Revokes an open invitation, whose link is then answered with 410 invite_revoked (FR-TEN-02 " +
			"AC1). Revoking it again changes nothing. An admin may revoke an invitation as owner too." +
			manageMembersDoc + " So is an invitation that the workspace does not have. One that has been accepted " +
			"is answered with 410 invite_accepted." + csrfDoc,
		Tags:          []string{"Members"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *inviteActionInput) (*struct{}, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		id, inviteID, err := inviteIDs(in)
		if err != nil {
			return nil, err
		}
		if err := invites.Revoke(ctx, cur.session.UserID, id, inviteID, clientFrom(ctx)); err != nil {
			return nil, memberError(err)
		}
		return &struct{}{}, nil
	})

	api.Register(m, api.Public, huma.Operation{
		OperationID: "lookupInvite",
		Method:      http.MethodPost,
		Path:        "/api/v1/invites/lookup",
		Summary:     "Read an invitation with its link",
		Description: "Returns what the holder of an invitation's link sees of it, so that the page that accepts " +
			"it can show it: the workspace's name, the role, and the invited address, if it has one. It is a POST, " +
			"so that the token travels in the body, never in a URL that a log could hold." + inviteStateDoc +
			authLimitDoc,
		Tags:        []string{"Members"},
		Middlewares: huma.Middlewares{rateLimit(limits, ratelimit.Auth)},
	}, func(ctx context.Context, in *lookupInviteInput) (*invitePreviewOutput, error) {
		p, err := invites.Lookup(ctx, secret.New(in.Body.Token))
		if err != nil {
			return nil, memberError(err)
		}
		return &invitePreviewOutput{Body: invitePreviewBody{WorkspaceName: p.WorkspaceName, Kind: string(p.Kind()),
			Email: p.Email, Role: string(p.Role), ExpiresAt: p.ExpiresAt}}, nil
	})

	api.Register(m, api.OwnAccount, huma.Operation{
		OperationID: "acceptInvite",
		Method:      http.MethodPost,
		Path:        "/api/v1/invites/accept",
		Summary:     "Accept an invitation",
		Description: "Accepts an invitation with its link as the signed-in user, who joins its workspace with its " +
			"role, and returns the membership. An invitation to an address that is not the account's is answered " +
			"with 403 invite_email_mismatch (FR-TEN-02 AC3), while a link invitation may be accepted by any " +
			"account. A user who is a member of the workspace already is answered with 409 already_member, and the " +
			"invitation stays open. Accepting again an invitation that the user accepted, as registering with it " +
			"does, returns the membership again, while the user is still a member." + inviteStateDoc + csrfDoc +
			authLimitDoc,
		Tags:        []string{"Members"},
		Middlewares: huma.Middlewares{rateLimit(limits, ratelimit.Auth)},
	}, func(ctx context.Context, in *acceptInviteInput) (*acceptedInviteOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		ms, err := invites.Accept(ctx, cur.session.UserID, secret.New(in.Body.Token), clientFrom(ctx))
		if err != nil {
			return nil, memberError(err)
		}
		return &acceptedInviteOutput{Body: membershipBody{WorkspaceFields: newWorkspaceFields(ms.Workspace),
			MembershipID: ids.Encode(ids.Membership, ms.ID), Role: string(ms.Role)}}, nil
	})

	api.Register(m, api.Public, huma.Operation{
		OperationID: "registerWithInvite",
		Method:      http.MethodPost,
		Path:        "/api/v1/invites/register",
		Summary:     "Register an account with an invitation",
		Description: "Creates an account, as POST /api/v1/auth/register does, and accepts an invitation with its " +
			"link as the new account, which joins the invitation's workspace with its role, under the invite and " +
			"the open signup policies. Until first-run setup has completed, the answer is 403 setup_required, " +
			"whatever the policy. Under closed, the answer is 403 signup_closed, whatever the request holds, " +
			"and only an account that exists already accepts an invitation, with POST /api/v1/invites/accept. An " +
			"invitation to an address registers that address, and another is answered with 403 " +
			"invite_email_mismatch (FR-TEN-02 AC3)." + inviteStateDoc + " The fields are checked as POST " +
			"/api/v1/auth/register checks them, with its errors. Otherwise the answer is 202 without a body, " +
			"whether or not the address has an account already, so that it does not tell whether an address has " +
			"one: an account that exists stays as it was, and the invitation stays open for it. The client then " +
			"signs in, and accepts the invitation with POST /api/v1/invites/accept, which returns the membership " +
			"either way." + authLimitDoc,
		Tags:          []string{"Members"},
		DefaultStatus: http.StatusAccepted,
		// The policy is checked before the body is read, so that a
		// registration that it refuses is refused whatever it holds, and
		// before the rate limit, as for POST /api/v1/auth/register.
		Middlewares: huma.Middlewares{api.Guard(func(ctx context.Context) error {
			return accountError(accounts.CheckInvitedSignup(ctx))
		}), rateLimit(limits, ratelimit.Auth)},
	}, func(ctx context.Context, in *registerWithInviteInput) (*acceptedOutput, error) {
		err := invites.Register(ctx, secret.New(in.Body.Token), account.Registration{Email: in.Body.Email,
			DisplayName: in.Body.DisplayName, Password: secret.New(in.Body.Password)}, clientFrom(ctx))
		if err != nil {
			return nil, memberError(err)
		}
		return &acceptedOutput{}, nil
	})
}

// inviteIDs returns the IDs of the workspace and the invitation that in
// names.
func inviteIDs(in *inviteActionInput) (id, inviteID ids.UUID, err error) {
	if id, err = ids.Decode(ids.Workspace, in.WorkspaceID); err != nil {
		return ids.UUID{}, ids.UUID{}, err
	}
	if inviteID, err = ids.Decode(ids.Invite, in.InviteID); err != nil {
		return ids.UUID{}, ids.UUID{}, err
	}
	return id, inviteID, nil
}

// memberError returns the error that the API answers err with, an error of
// a workspace's members or invitations, of packages workspace, invite, and
// account, or err itself if it is none of them.
func memberError(err error) error {
	switch {
	case errors.Is(err, workspace.ErrOwnerRequired):
		return api.NewError(api.CodeOwnerRequired)
	case errors.Is(err, workspace.ErrLastOwner):
		return api.NewError(api.CodeLastOwner)
	case errors.Is(err, workspace.ErrAlreadyMember):
		return api.NewError(api.CodeAlreadyMember)
	case errors.Is(err, invite.ErrNotFound):
		return api.NewError(api.CodeNotFound)
	case errors.Is(err, invite.ErrExists):
		return api.NewError(api.CodeInviteExists)
	case errors.Is(err, invite.ErrTokenInvalid):
		return api.NewError(api.CodeTokenInvalid)
	case errors.Is(err, invite.ErrExpired):
		return api.NewError(api.CodeInviteExpired)
	case errors.Is(err, invite.ErrRevoked):
		return api.NewError(api.CodeInviteRevoked)
	case errors.Is(err, invite.ErrAccepted):
		return api.NewError(api.CodeInviteAccepted)
	case errors.Is(err, invite.ErrEmailMismatch):
		return api.NewError(api.CodeInviteEmailMismatch)
	case errors.Is(err, account.ErrNoSession):
		return api.NewError(api.CodeUnauthenticated)
	case errors.Is(err, workspace.ErrNotFound), errors.Is(err, workspace.ErrInsufficientRole):
		return workspaceError(err)
	}
	return accountError(err)
}
