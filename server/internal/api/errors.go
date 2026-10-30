package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/text/language"

	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/logging"
)

// Code is a stable error code of the error envelope (PRD FR-API-04, §10.5).
// Clients branch on the code, never on the message, so a code, once released,
// keeps its meaning and its status.
type Code string

// The error codes. Each is registered in codes with its status, and
// docs/error-codes.md is generated from the registry.
const (
	CodeInvalidRequest        Code = "invalid_request"
	CodeInvalidID             Code = "invalid_id"
	CodeInvalidCursor         Code = "invalid_cursor"
	CodeUnauthenticated       Code = "unauthenticated"
	CodeInvalidCredentials    Code = "invalid_credentials"
	CodeCSRFFailed            Code = "csrf_failed"
	CodeWrongPassword         Code = "wrong_password"
	CodeInsufficientRole      Code = "insufficient_role"
	CodeInsufficientScope     Code = "insufficient_scope"
	CodeSessionRequired       Code = "session_required"
	CodeOwnerRequired         Code = "owner_required"
	CodeOwnerNotGrantable     Code = "owner_not_grantable"
	CodeSignupClosed          Code = "signup_closed"
	CodeInviteRequired        Code = "invite_required"
	CodeSetupRequired         Code = "setup_required"
	CodeInvalidSetupCode      Code = "invalid_setup_code"
	CodeInstanceAdminRequired Code = "instance_admin_required"
	CodeEmailNotConfigured    Code = "email_not_configured"
	CodeEmailConfigured       Code = "email_configured"
	CodeEmailUnverified       Code = "email_unverified"
	CodeTokenInvalid          Code = "token_invalid"
	CodeTokenUsed             Code = "token_used"
	CodeTokenExpired          Code = "token_expired"
	CodeInviteEmailMismatch   Code = "invite_email_mismatch"
	CodeNotFound              Code = "not_found"
	CodeSlugTaken             Code = "slug_taken"
	CodeFolderNotEmpty        Code = "folder_not_empty"
	CodeKeyTaken              Code = "key_taken"
	CodeComponentTypeInUse    Code = "component_type_in_use"
	CodeBoardReferenced       Code = "board_referenced"
	CodeOutputConnected       Code = "branch_output_connected"
	CodeVariableInUse         Code = "variable_in_use"
	CodeComponentInUse        Code = "component_in_use"
	CodeLastOwner             Code = "last_owner"
	CodeAlreadyMember         Code = "already_member"
	CodeInviteExists          Code = "invite_exists"
	CodeLimitExceeded         Code = "limit_exceeded"
	CodeStorageLimitExceeded  Code = "storage_limit_exceeded"
	CodeRevisionConflict      Code = "revision_conflict"
	CodeProjectArchived       Code = "project_archived"
	CodeIdempotencyMismatch   Code = "idempotency_mismatch"
	CodeInviteExpired         Code = "invite_expired"
	CodeInviteRevoked         Code = "invite_revoked"
	CodeInviteAccepted        Code = "invite_accepted"
	CodeMethodNotAllowed      Code = "method_not_allowed"
	CodeRequestTimeout        Code = "request_timeout"
	CodeRequestTooLarge       Code = "request_too_large"
	CodeUnsupportedMediaType  Code = "unsupported_media_type"
	CodeValidationFailed      Code = "validation_failed"
	CodeWeakPassword          Code = "weak_password"
	CodeImageInvalid          Code = "image_invalid"
	CodeSlugInvalid           Code = "slug_invalid"
	CodeKeyInvalid            Code = "key_invalid"
	CodeRateLimited           Code = "rate_limited"
	CodeInternal              Code = "internal_error"
	CodeNotReady              Code = "not_ready"
)

// codeSpec registers a code.
type codeSpec struct {
	code   Code
	status int
	// message is the envelope's message for the code, in the catalog
	// (package i18n).
	message i18n.Key
	// doc says when the code is returned, in Markdown, for the reference.
	doc string
}

// codes is the registry of error codes, in the order of the reference.
var codes = []codeSpec{
	{CodeInvalidRequest, http.StatusBadRequest, i18n.K("error.invalid_request"),
		"The request body is not valid JSON, or not one JSON value; it has a key twice in one object, " +
			"text that is not UTF-8, or values nested more than " + fmt.Sprint(maxJSONDepth) + " deep; or a " +
			"required body is missing. `details.errors` lists each problem with its `location`, such as `body`."},
	{CodeInvalidID, http.StatusBadRequest, i18n.K("error.invalid_id"),
		"An identifier does not decode, or is the identifier of another type of entity, such as a board's " +
			"identifier where a node's is expected. `details.reason` says why: `missing_prefix`, `unknown_prefix`, " +
			"`wrong_prefix`, `character`, `length`, or `overflow`, as in `schema/ids/README.md`."},
	{CodeInvalidCursor, http.StatusBadRequest, i18n.K("error.invalid_cursor"),
		"The `cursor` query parameter is not a cursor that the same list returned as `next_cursor`."},
	{CodeUnauthenticated, http.StatusUnauthorized, i18n.K("error.unauthenticated"),
		"The request has no credentials, or credentials that are wrong. A route that needs a signed-in user " +
			"answers with it to a request without the session cookie that `POST /api/v1/auth/login` sets, or " +
			"with the cookie of a session that has ended: signed out, revoked, unused for " +
			"`STORYCREEP_SESSION_IDLE_TIMEOUT`, past `STORYCREEP_SESSION_LIFETIME`, or of an account that can no " +
			"longer sign in. The answer then clears the cookie. A route that takes an API token answers with it, " +
			"and a `WWW-Authenticate` header of the `Bearer` scheme with `error=\"invalid_token\"` (RFC 6750), to a " +
			"request whose `Authorization: Bearer` token is not one, or is revoked or expired, or whose owner's " +
			"account is not active or is no longer a member of the token's workspace (PRD FR-AUTH-04 AC4); a " +
			"session-only route answers so too, as it would answer `session_required` to a valid token. A route " +
			"that takes an API token also sends the `Bearer` scheme's challenge, without an `error`, to a request " +
			"without credentials or with the cookie of a session that has ended. A " +
			"`Bearer` credential that does not start with `scp_` is not taken for a token, and the request is " +
			"authenticated by its session cookie instead. `/metrics` answers with it to a request without " +
			"`STORYCREEP_METRICS_TOKEN` as its bearer token, and its `WWW-Authenticate` header names the scheme."},
	{CodeInvalidCredentials, http.StatusUnauthorized, i18n.K("error.invalid_credentials"),
		"`POST /api/v1/auth/login` was given an email address that belongs to no account, a password that is " +
			"not the account's, or the address of an account that cannot sign in, such as a suspended one. The " +
			"answer is the same for each, so that it does not tell whether an address has an account " +
			"(PRD FR-AUTH-01)."},
	{CodeCSRFFailed, http.StatusForbidden, i18n.K("error.csrf_failed"),
		"The request changes something, with a method other than `GET`, `HEAD`, or `OPTIONS` (PRD FR-AUTH-02 " +
			"AC5). Either it was made to a route that needs a signed-in user with the session cookie but without " +
			"the session's CSRF token, which `POST /api/v1/auth/login` and `GET /api/v1/auth/session` return as " +
			"`csrf_token`, in its `X-CSRF-Token` header, or a browser sent it, to any route, signing in included, " +
			"from another site or from another origin of the same site, as its `Sec-Fetch-Site` header says, " +
			"whatever its credentials. Browsers send that header only to `https` URLs and to loopback addresses, " +
			"such as `localhost`, so with a plain-HTTP `STORYCREEP_PUBLIC_URL` of another host only the CSRF " +
			"token is checked, on the routes that need a signed-in user."},
	{CodeWrongPassword, http.StatusForbidden, i18n.K("error.wrong_password"),
		"`POST /api/v1/auth/password` was given a current password that is not the account's, so the " +
			"password stays as it was."},
	{CodeInsufficientRole, http.StatusForbidden, i18n.K("error.insufficient_role"),
		"The signed-in user's role does not allow the operation (PRD §5.6). An operation of a workspace is " +
			"allowed by the user's role in the workspace, as only an `owner` or an `admin` changes its settings, " +
			"invites, removes members, or changes their roles, and creates, deletes, or restores its projects; an " +
			"operation of a project by their role on the project, their grant on it or else their role in its " +
			"workspace (§5.5), as only an `owner` or an `admin` of the project changes its name, description, or " +
			"settings, or archives it. A project grant never authorises an operation of the workspace, as a " +
			"`viewer`'s `admin` grant on a project lets them neither delete it nor manage its grants (FR-TEN-04 " +
			"AC4). A user who has no role in the workspace or on the project that the route " +
			"names, as one who is not a member of the workspace, or a `guest` without a grant on the project, is " +
			"answered with `not_found` instead, so that the answer does not tell that it exists (§5.5)."},
	{CodeInsufficientScope, http.StatusForbidden, i18n.K("error.insufficient_scope"),
		"The request was made with an API token that does not hold the scope that the route needs, nor a scope " +
			"that implies it (PRD FR-AUTH-04 AC2): `snapshot:read` to list the token's workspace and its projects " +
			"and to read a project, which every scope but `export:read` implies, `project:read` to read a " +
			"project's assets, which `project:write` implies, and `project:write` to change a project's settings " +
			"and to upload an asset. `details.scope` names the scope that the route needs, and " +
			"so does the `WWW-Authenticate` header, with the `Bearer` scheme's `error=\"insufficient_scope\"` and " +
			"`scope` (RFC 6750 §3.1). A token never does more than its owner's role allows either, which is " +
			"answered with `insufficient_role` when the token holds the scope."},
	{CodeSessionRequired, http.StatusForbidden, i18n.K("error.session_required"),
		"The route takes no API token, whatever its scopes, and needs the session cookie of a signed-in user " +
			"instead, which `POST /api/v1/auth/login` sets (PRD FR-AUTH-04): every route of an account, its " +
			"session, sessions, password, and email address, of a workspace's members, invitations, project " +
			"grants, settings, and usage, of creating a workspace, of creating, deleting, and restoring projects " +
			"and the trash, of API tokens, and of the instance admin, so that a token cannot make another, change " +
			"a credential, change who has access, or administer a workspace. Every other route that needs a " +
			"signed-in user takes a token that holds its scope, as its description says."},
	{CodeOwnerRequired, http.StatusForbidden, i18n.K("error.owner_required"),
		"Only an `owner` of the workspace may do this, and the signed-in user is an `admin`: grant the role " +
			"`owner`, by a role change or an invitation, send an invitation as `owner` anew, demote or remove an " +
			"owner, or set or remove an owner's grant on a project, which would replace their role there (PRD §5.6, " +
			"FR-TEN-03 AC4, FR-TEN-04)."},
	{CodeOwnerNotGrantable, http.StatusBadRequest, i18n.K("error.owner_not_grantable"),
		"A project grant was asked for with the role `owner`, which is a role of the workspace alone (PRD " +
			"FR-TEN-04 AC5): a grant is `admin`, `editor`, `commenter`, or `viewer`. Nothing changes. " +
			"`details.errors` names the field, with its `location` and a `message`, as for `validation_failed`."},
	{CodeSignupClosed, http.StatusForbidden, i18n.K("error.signup_closed"),
		"The instance's signup policy is `closed`, so `POST /api/v1/auth/register` creates no account, " +
			"whatever the request holds (PRD FR-AUTH-01 AC3), and nor does `POST /api/v1/invites/register`, with an " +
			"invitation or without: only an account that exists already accepts an invitation then (FR-TEN-02). The " +
			"policy is the one that `STORYCREEP_SIGNUP` sets, or else the one that first-run setup chose, or else " +
			"`closed`."},
	{CodeInviteRequired, http.StatusForbidden, i18n.K("error.invite_required"),
		"The instance's signup policy is `invite`, so only an invitation creates an account, and " +
			"`POST /api/v1/auth/register` creates none, whatever the request holds. `POST /api/v1/invites/register` " +
			"registers an account with an invitation's link."},
	{CodeSetupRequired, http.StatusForbidden, i18n.K("error.setup_required"),
		"The instance's first-run setup has not completed, so `POST /api/v1/auth/register` creates no " +
			"account, and nor does `POST /api/v1/invites/register`, whatever the signup policy and whatever the " +
			"request holds, so that the first account is always the owner that setup creates (PRD FR-ADMIN-02 " +
			"AC4). An upload of an asset is refused too, unless `STORYCREEP_ASSET_STORAGE` names where the files " +
			"are stored, as setup may yet choose a bucket for them (FR-ASSET-01), which an account that " +
			"`storycreep create-user` made before setup would otherwise have stored files outside of. " +
			"`POST /api/v1/setup` completes it."},
	{CodeInvalidSetupCode, http.StatusForbidden, i18n.K("error.invalid_setup_code"),
		"`POST /api/v1/setup` was given a setup code that is not the instance's, so setup stays pending. The " +
			"server writes the code to its log while setup is pending (PRD FR-ADMIN-02)."},
	{CodeInstanceAdminRequired, http.StatusForbidden, i18n.K("error.instance_admin_required"),
		"The route belongs to the instance-admin area, which only a signed-in instance admin may use (PRD " +
			"FR-ADMIN-03 AC1), and the signed-in user is not one. A workspace's `owner` or `admin` is not an " +
			"instance admin."},
	{CodeEmailNotConfigured, http.StatusForbidden, i18n.K("error.email_not_configured"),
		"The instance sends no email, because neither `STORYCREEP_SMTP_URL` nor first-run setup gave it an SMTP " +
			"server, so it cannot email the link that the request asks for (PRD FR-AUTH-03). The answer is the same " +
			"for every email address. An instance admin gives out the links as text instead, which " +
			"`POST /api/v1/admin/password-reset-links` and `POST /api/v1/admin/verification-links` issue, and " +
			"`GET /api/v1/instance` says whether the instance sends email."},
	{CodeEmailConfigured, http.StatusForbidden, i18n.K("error.email_configured"),
		"The instance sends email, so it emails its links to the account's address, and an instance admin " +
			"takes none as text (PRD FR-AUTH-03). `POST /api/v1/auth/password-reset` and " +
			"`POST /api/v1/auth/email-verification` email them."},
	{CodeEmailUnverified, http.StatusForbidden, i18n.K("error.email_unverified"),
		"The signed-in user has not verified their email address, which the instance requires before they " +
			"create a workspace, where it always sends email (the `EmailRequired` capability, as in hosted mode; " +
			"PRD FR-AUTH-03). The link that `POST /api/v1/auth/email-verification` emails verifies it."},
	{CodeTokenInvalid, http.StatusBadRequest, i18n.K("error.token_invalid"),
		"The link's token does not exist: it was mistyped, or it was deleted, as a change of the password deletes " +
			"the account's reset links that have not been used (PRD FR-AUTH-03 AC4), or as an expired one is " +
			"deleted after a while, or it was replaced, as sending an invitation anew replaces its link. Or it is " +
			"the token of another kind of link, or its account can no longer use it, as one that is suspended, or " +
			"one whose address is no longer the one that it verifies, or its invitation's workspace no longer " +
			"exists."},
	{CodeTokenUsed, http.StatusBadRequest, i18n.K("error.token_used"),
		"The link has been used already, and a link is used once (PRD FR-AUTH-03 AC2)."},
	{CodeTokenExpired, http.StatusBadRequest, i18n.K("error.token_expired"),
		"The link has expired: a link that verifies an email address lasts 24 hours, and one that resets a " +
			"password an hour (PRD FR-AUTH-03)."},
	{CodeInviteEmailMismatch, http.StatusForbidden, i18n.K("error.invite_email_mismatch"),
		"The invitation is to another email address than the account's, or than the one that the registration " +
			"names, and only an account with that address may accept it (PRD FR-TEN-02 AC3). A link invitation, " +
			"which is to no address, may be accepted by whoever holds its link."},
	{CodeNotFound, http.StatusNotFound, i18n.K("error.not_found"),
		"No route matches the path, or the resource it names does not exist, or is in its workspace's trash, as " +
			"a deleted project is to every route but the one that restores it (PRD FR-PROJ-01 AC3), or the " +
			"signed-in user's role in it, or in its workspace, is none, as it is for a user who is not a member of " +
			"the workspace, or the request's API token does not cover it, as a token of another workspace, or one " +
			"narrowed to another project, does not, so that the answer does not tell that it exists (PRD §5.5, " +
			"FR-TEN-04 AC6, FR-AUTH-04 AC3)."},
	{CodeSlugTaken, http.StatusConflict, i18n.K("error.slug_taken"),
		"Another workspace has the slug, which is unique on the instance (PRD FR-TEN-01 AC3), or another board of " +
			"the project has it, in the project's trash or not, which a board's is unique within (FR-PROJ-02 AC1). " +
			"`details.errors` names the field, with its `location` and a `message`, as for `validation_failed`."},
	{CodeFolderNotEmpty, http.StatusConflict, i18n.K("error.folder_not_empty"),
		"A `folder.delete` command names a folder that still holds folders, boards, variables, or components that " +
			"are not deleted, so nothing of its batch is applied (PRD FR-PROJ-02). Move them out of it, or delete " +
			"them, first; what is in the trash does not count, a variable of a board in the trash among it. " +
			"`details.errors` names the field, with its `location` and a `message`, as for `validation_failed`."},
	{CodeKeyTaken, http.StatusConflict, i18n.K("error.key_taken"),
		"Another entity of the scope that a key is unique within has the key, of those that are not in the " +
			"project's trash: another variable of the variable's scope, among the project's global variables for a " +
			"global, or among its board's for a board's (PRD FR-VAR-01), another component type of the project, " +
			"another attribute definition of the definition's type (FR-COMP-01 AC3), or another component of the " +
			"project (FR-COMP-02 AC1). A command creates the entity with the key, or changes its key to it, moves a " +
			"variable to a scope where another has its key, or restores an entity from the trash, or a component's " +
			"type with it, once another has taken its key. A deleted entity's key is free. `details.errors` names " +
			"the field, with its `location` and a `message`, as for `validation_failed`."},
	{CodeComponentTypeInUse, http.StatusConflict, i18n.K("error.component_type_in_use"),
		"A `component_type.delete` command names a type of which a component that is not in the project's trash " +
			"is, so nothing of its batch is applied (PRD FR-COMP-01). Delete the type's components first; a type " +
			"goes to the trash only once each of its components is there, and restoring one of them restores it. " +
			"`details.errors` names the field, with its `location` and a `message`, as for `validation_failed`."},
	{CodeBoardReferenced, http.StatusConflict, i18n.K("error.board_referenced"),
		"A `board.delete` command names a board that jumpers on the project's other boards, which are not in its " +
			"trash, target the nodes of, and does not confirm the deletion, which would leave them dangling, so " +
			"nothing of its batch is applied (PRD FR-PROJ-02 AC2). `details.jumpers` lists the first 100 of them, in " +
			"the order of their IDs, each with its `id`, its `board_id`, and its `title`, and `details.jumper_count` " +
			"says how many there are, which `GET /api/v1/projects/{project_id}/boards/{board_id}/referenced-by` " +
			"lists in full. Warn of them, and send the command again with `confirm` to delete the board all the " +
			"same: the jumpers then keep their targets, which the validation reports. `details.errors` names the " +
			"field, with its `location` and a `message`, as for `validation_failed`."},
	{CodeOutputConnected, http.StatusConflict, i18n.K("error.branch_output_connected"),
		"A `branch_output.delete` command names an output of a branch that connections, which are not in the " +
			"project's trash, leave, and does not confirm the deletion, which would delete them with it, so nothing " +
			"of its batch is applied (PRD FR-EDIT-04 AC3). `details.connections` lists the first 100 of them, in " +
			"the order of their IDs, each with its `id` and its `to_node_id`, and `details.connection_count` says " +
			"how many there are. Warn of them, and send the command again with `confirm` to delete the output all " +
			"the same: its connections then go to the trash with it, and come back with it. `details.errors` names " +
			"the field, with its `location` and a `message`, as for `validation_failed`."},
	{CodeVariableInUse, http.StatusConflict, i18n.K("error.variable_in_use"),
		"A `variable.delete` command names a variable that sites of the project that are not in its trash use, as " +
			"the project stands once the batch's commands before it have applied, and does not force the deletion, " +
			"which would leave them naming what does not resolve, so nothing of its batch is applied (PRD FR-VAR-04 " +
			"AC1, FR-TEXT-02 AC2): the expressions that read or write the variable, and the texts whose value " +
			"references show it. `details.usage_count` says how many usages there are, as the variable's usage lists " +
			"count them, `details.counts` how many of each use, `read` and `write`, and `details.usages` lists the " +
			"first 100 of them, reads first, then in the order of the usage lists, each with its `use` and the fields " +
			"of a usage of `GET /api/v1/projects/{project_id}/variables/{variable_id}/usages`, which lists them in " +
			"full. Show them, and send the command again with `force` to delete the variable all the same, which " +
			"only a role that may publish the project's snapshots may: the expressions are then invalid, and the " +
			"texts show nothing. `details.errors` names the field, with its `location` and a `message`, as for " +
			"`validation_failed`."},
	{CodeComponentInUse, http.StatusConflict, i18n.K("error.component_in_use"),
		"A `component.delete` command names a component that sites of the project that are not in its trash use, " +
			"as the project stands once the batch's commands before it have applied, and does not force the " +
			"deletion, so nothing of its batch is applied (PRD FR-COMP-04 AC1, FR-EDIT-06 AC2): the nodes whose " +
			"speaker it is, the texts that mention it, and the sites that read or write its attributes. " +
			"`details.usage_count` says how many usages there are, as the component's usage lists count them, a " +
			"site once for each attribute that it reads or writes, `details.counts` how many of each use, " +
			"`speaker`, `mention`, `read`, and `write`, and `details.usages` lists the first 100 of them, in that " +
			"order of their uses, then in the order of the usage lists, each with its `use` and the fields of a usage " +
			"of `GET /api/v1/projects/{project_id}/components/{component_id}/usages`, which lists them in full. Show " +
			"them, and send the command again with `force` to delete the component all the same, which only a role " +
			"that may publish the project's snapshots may. `details.errors` names the field, with its `location` and " +
			"a `message`, as for `validation_failed`."},
	{CodeLastOwner, http.StatusConflict, i18n.K("error.last_owner"),
		"The change would leave the workspace without an owner whose account is active, and a workspace keeps one " +
			"(PRD FR-TEN-02 AC2): its last owner cannot be removed or demoted. An owner whose account's deletion is " +
			"pending does not count (FR-GDPR-02). Make another member an owner first."},
	{CodeAlreadyMember, http.StatusConflict, i18n.K("error.already_member"),
		"The account is a member of the workspace already: the one that an invitation's email address belongs " +
			"to, or the signed-in user, who accepts an invitation of a workspace that they belong to, which then " +
			"stays open."},
	{CodeInviteExists, http.StatusConflict, i18n.K("error.invite_exists"),
		"The email address has an open invitation of the workspace already, one that has been neither accepted " +
			"nor revoked, whether or not it has expired. Send that one anew, or revoke it first."},
	{CodeLimitExceeded, http.StatusConflict, i18n.K("error.limit_exceeded"),
		"The change would take the workspace past one of its limits, so it is not made (PRD FR-ENT-03): a limit " +
			"of its plan, or a cap that the instance puts on every workspace (`STORYCREEP_WORKSPACE_LIMITS`). " +
			"`details.key` names the limit, as the plan file does, such as `members`, `projects`, or " +
			"`storage_bytes`; `details.usage` is how much of it the workspace uses, and `details.limit` the most " +
			"that it may use (FR-ENT-03 AC1). The storage limit, `storage_bytes`, which an upload passes, is " +
			"answered with `storage_limit_exceeded` instead. `details.upgrade_url` is the public site's page that " +
			"offers the plans, `/pricing`, where the instance both bills for plans and serves the public site, as " +
			"hosted mode does unless `STORYCREEP_PUBLIC_SITE` is `off`, the limit is the plan's rather than a cap " +
			"of the instance, and another plan allows more. A self-hosted instance never sends it (FR-ENT-03 AC2). A " +
			"workspace past a limit already, as after its plan changed, keeps what it has, but cannot add more."},
	{CodeRevisionConflict, http.StatusConflict, i18n.K("error.revision_conflict"),
		"Commands of the batch expect revisions of entities that the entities no longer have, because they changed " +
			"after the client read them, so nothing of the batch is applied (PRD FR-API-03 AC3, ADR-006). Every " +
			"`expected_rev` of a batch is checked against the entities as they are before the batch, not as an " +
			"earlier command of it would leave them, so an entity that the batch creates has no revision to expect. " +
			"`details.conflicts` lists each command's entities whose revisions are not the ones that its " +
			"`expected_rev` names: the command's index in the batch as `command`, its `command_id`, and its `type`, " +
			"and the entity's `id`, its `expected_rev`, and `exists`, which is `true`, with the entity's " +
			"`current_rev`, or `false`, without it, for an entity that no longer exists, as one that was purged " +
			"from the trash or never created (FR-OFF-04 AC5). `details.states` holds the state of each of those " +
			"entities that still exists, the entity as it is now, by its `id`, once, however many commands expect " +
			"a revision of it. An entity in the trash exists, and its state says that it is deleted. To make the " +
			"change anyway, make it again on the current state, as a new command with an ID of its own and the " +
			"current revisions."},
	{CodeProjectArchived, http.StatusConflict, i18n.K("error.project_archived"),
		"The project is archived, and an archived project is read-only, so nothing of the change is made (PRD " +
			"FR-PROJ-01 AC2). Unarchiving it, deleting it, and restoring it from the trash are the changes that it " +
			"takes. An owner or admin of the project unarchives it with `POST /api/v1/projects/{project_id}/unarchive`."},
	{CodeInviteExpired, http.StatusGone, i18n.K("error.invite_expired"),
		"The invitation has expired: it lasts 14 days from when it was made, sent anew, or emailed (PRD " +
			"FR-TEN-02 AC1). An owner or admin of the workspace sends it anew."},
	{CodeInviteRevoked, http.StatusGone, i18n.K("error.invite_revoked"),
		"An owner or admin of the workspace revoked the invitation, or removed the member who made it, who was " +
			"last given its link as text, as by sending it anew, or whom it invites, which revokes it, and it no " +
			"longer works (PRD FR-TEN-02 AC1)."},
	{CodeInviteAccepted, http.StatusGone, i18n.K("error.invite_accepted"),
		"The invitation has been accepted already, and an invitation is accepted once. Reading it with its link, " +
			"or registering with it, answers so, and so does accepting it, but for the account that accepted it, " +
			"which `POST /api/v1/invites/accept` answers with its membership again while it is still a member. An " +
			"owner or admin of the workspace can neither revoke it nor send it anew."},
	{CodeMethodNotAllowed, http.StatusMethodNotAllowed, i18n.K("error.method_not_allowed"),
		"A route matches the path, but not the method. The `Allow` header lists the methods it takes."},
	{CodeRequestTimeout, http.StatusRequestTimeout, i18n.K("error.request_timeout"),
		"The client did not send the whole request body within the route's time limit."},
	{CodeRequestTooLarge, http.StatusRequestEntityTooLarge, i18n.K("error.request_too_large"),
		"The request body is larger than the route accepts, 5 MB unless the route says otherwise " +
			"(PRD NFR-SEC-07), or, for an upload of an SVG image, the file that the server would store once it has " +
			"removed what it does not allow is larger than the upload's limit (FR-ASSET-02). `details.max_bytes` is " +
			"the limit."},
	{CodeStorageLimitExceeded, http.StatusRequestEntityTooLarge, i18n.K("error.storage_limit_exceeded"),
		"The upload would take the workspace past its storage limit, `storage_bytes`, so it stores nothing (PRD " +
			"FR-ASSET-02 AC4, FR-ENT-03): its plan's, or the cap that the instance puts on every workspace " +
			"(`STORYCREEP_WORKSPACE_LIMITS`). A file that the workspace stores already, in this project or another, " +
			"takes up no more, and is never refused so. Uploads that run at once are each checked against the " +
			"usage before any of them adds to it, so together they may take the workspace past its limit by as " +
			"much as their own files (FR-ENT-02). `details` are those of `limit_exceeded`: `details.key` is " +
			"`storage_bytes`, `details.usage` how many bytes the workspace's files take up, `details.limit` the " +
			"most that they may, and `details.upgrade_url` the page that offers the plans, where " +
			"`limit_exceeded` would have it."},
	{CodeUnsupportedMediaType, http.StatusUnsupportedMediaType, i18n.K("error.unsupported_media_type"),
		"The `Content-Type` of the request body is neither `application/json` nor a type ending in `+json`, " +
			"such as `application/merge-patch+json`, and the route takes JSON, as every route but an upload of an " +
			"asset does, whose body is the file, whatever its `Content-Type` says. A body sent without a " +
			"`Content-Type` is read as JSON."},
	{CodeValidationFailed, http.StatusUnprocessableEntity, i18n.K("error.validation_failed"),
		"A parameter or the body does not match the route's schema: a field that the schema does not have, " +
			"which is never ignored (PRD FR-API-01 AC2), a missing field, a value of the wrong type, or one out of " +
			"range; or a field's value is not one that the route takes, such as a registration's email address " +
			"that is not one, or, for a batch of commands, one that the project's state does not take, or one " +
			"that would take the batch's undo, which a client sends back as a batch to undo it, past what a " +
			"request can hold (`POST /api/v1/projects/{project_id}/commands`), whose `details.reason` is then " +
			"`undo_too_large`, the one reason that it gives, so that a client sends an undo refused so in smaller " +
			"batches. `details.errors` lists each problem with its `location`, such as `body.name` or " +
			"`query.limit`, and a `message`."},
	{CodeIdempotencyMismatch, http.StatusUnprocessableEntity, i18n.K("error.idempotency_mismatch"),
		"A batch of commands holds commands that were applied already, in another batch, and commands that were " +
			"not, so nothing of it is applied (PRD FR-API-03). A batch applies whole or not at all, so only a client " +
			"that grouped its commands anew sends one. `details.applied` lists the `command_id`s of the batch that " +
			"were applied, in the order of the batch. Send the commands that were not applied in a batch of their " +
			"own; a batch of commands that were all applied is answered as already applied."},
	{CodeWeakPassword, http.StatusUnprocessableEntity, i18n.K("error.weak_password"),
		"The password breaks the password policy (PRD FR-AUTH-01 AC2). `details.reason` says how: `too_short`, " +
			"with fewer than 12 characters, counted as Unicode code points once the password is in NFKC, or " +
			"`common`, one of the 10,000 most common passwords, compared without regard to case. " +
			"`details.errors` names the field, with its `location` and a `message`, as for `validation_failed`."},
	{CodeImageInvalid, http.StatusUnprocessableEntity, i18n.K("error.image_invalid"),
		"The uploaded file is not an image that an upload takes (PRD FR-ASSET-02): a PNG, JPEG, GIF, WebP, or " +
			"SVG image, which the server tells from the file's content, never from its name or the request's " +
			"`Content-Type`. Nothing is stored. `details.reason` says why: `type`, for a file that is none of " +
			"them, as text whose name ends in `.png` (FR-ASSET-02 AC1), or an SVG file whose root is not an `svg` " +
			"element of SVG's namespace; `malformed`, for one that starts as an image does but whose header does " +
			"not decode, or an SVG file that is not well-formed XML, as one of more than one document type, or " +
			"one that names no encoding other than UTF-8 but whose bytes are not UTF-8; `unsupported`, for an " +
			"SVG file that is well-formed, but that is in UTF-16, or declares an encoding other than UTF-8, " +
			"whatever its bytes, or a version of XML other than 1.0, nests its elements more than 256 deep, has " +
			"an element of more than 1,024 attributes, or elements that enclose one another and declare more " +
			"than 1,024 namespaces in all, or whose document type declares an entity that is not text of at most " +
			"1,024 bytes, more than 64 entities, or entities whose references expand to more than 4 MiB in all; " +
			"or `dimensions`, for an image of more than 25,000,000 pixels, or of more than 16,384 on a side. " +
			"`details.errors` names the body, with its `location` and a `message` that says what is wrong, as for " +
			"`validation_failed`. An SVG file that holds a script or refers to something outside itself is not " +
			"refused: the server stores it without them."},
	{CodeSlugInvalid, http.StatusUnprocessableEntity, i18n.K("error.slug_invalid"),
		"A slug is not one that the route or the command takes (PRD FR-TEN-01 AC3, FR-PROJ-02 AC4). A slug is " +
			"groups of lowercase ASCII letters and digits joined by single hyphens, as `harbour-team`, of at most 64 " +
			"characters, and not a reserved word: a name that Windows reserves for a device, such as `con` or " +
			"`com1`, which no slug may be, and, for a workspace's, a word that the product keeps for itself, such as " +
			"`admin` or `api`. `details.reason` says which rule it breaks: `format` or `reserved`. `details.errors` " +
			"names the field, with its `location` and a `message`, as for `validation_failed`."},
	{CodeKeyInvalid, http.StatusUnprocessableEntity, i18n.K("error.key_invalid"),
		"The key of a variable, a component type, an attribute definition, or a component is not an identifier of " +
			"the expression language (PRD FR-VAR-01 AC1, FR-COMP-01 AC3, FR-COMP-02 AC1, §5.8.1): an ASCII " +
			"letter or an underscore, then ASCII letters, digits, and underscores, as `met_elara`, of at most 64 " +
			"characters, compared case by case, and not one of the language's reserved words, `true`, `false`, " +
			"`and`, `or`, `not`, `global`, `board`, `null`, `if`, `else`, and `elseif`. `details.reason` says which " +
			"rule it breaks: `format` or `reserved`. `details.errors` names the field, with its `location` and a " +
			"`message`, as for `validation_failed`."},
	{CodeRateLimited, http.StatusTooManyRequests, i18n.K("error.rate_limited"),
		"The client made too many requests (PRD FR-API-04 AC3, NFR-SEC-06), or signing in with an email address " +
			"failed too often in a row (FR-AUTH-05). The `Retry-After` header says how many seconds to wait. " +
			"`details.reason` says which: `rate`, when the requests of a kind, its `details.bucket`, used up their " +
			"limit for the client's IP address, its account, the API token that they are made with, or the email " +
			"address that they name, until the " +
			"`RateLimit-Reset` header's seconds have passed; or `locked`, when 10 sign-ins in a row with the email " +
			"address failed, whether or not it belongs to an account, which locks it for a minute, and each further " +
			"failure, once a lock ends, for twice as long as the lock before, up to 15 minutes. While the address " +
			"is locked, `POST /api/v1/auth/login` refuses every sign-in with it, even with the right password. Once " +
			"the lock ends, the next sign-in is checked: one that succeeds forgets the failures, and one that fails " +
			"locks the address again. The failures are forgotten a day after the last one. " +
			"`details.bucket` is `auth` for signing in and the other requests about credentials, among them " +
			"making, listing, and revoking an API token, which the description of each operation that counts in it " +
			"lists, and in which no request made with an API token counts, so that a token's requests cannot keep " +
			"its owner from revoking it; `read` for every other request that reads; and `write` for every other " +
			"that changes something. Every operation's description states its limits."},
	{CodeInternal, http.StatusInternalServerError, i18n.K("error.internal_error"),
		"The server failed. Its log has the cause, under the envelope's `request_id`."},
	{CodeNotReady, http.StatusServiceUnavailable, i18n.K("error.not_ready"),
		"The server is starting, its database has pending migrations, or it is stopping. `/readyz` answers " +
			"with it then too, and also while the server cannot reach its database, which the API's routes " +
			"report as `internal_error` instead. `details.reason` says which: `starting`, `migrations_pending`, " +
			"`stopping`, or, from `/readyz` only, `database_unreachable`. A `Retry-After` header suggests when " +
			"to try again."},
}

// spec returns the registration of c. It panics on a code that is not
// registered, which a test rules out for the constants.
func spec(c Code) codeSpec {
	for _, s := range codes {
		if s.code == c {
			return s
		}
	}
	panic(fmt.Sprintf("api: unregistered error code %q", c))
}

// Status returns the HTTP status of the code.
func (c Code) Status() int {
	return spec(c).status
}

// Known reports whether c is a registered code.
func (c Code) Known() bool {
	return slices.ContainsFunc(codes, func(s codeSpec) bool { return s.code == c })
}

// Error is an error that the API reports in its error envelope. A handler
// returns one to answer with a 4xx or 5xx status. Any other error a handler
// returns is answered with internal_error, and logged; an *ids.DecodeError is
// answered with invalid_id.
type Error struct {
	Code Code
	// Message replaces the code's message, if it is set: a message of the
	// catalog, which the envelope shows in the request's locale (PRD
	// FR-I18N-01).
	Message i18n.Message
	// Details are the envelope's details, which must encode as a JSON
	// object. They are for programs, so their keys are stable.
	Details map[string]any
}

// NewError returns an error with the code's message and no details.
func NewError(c Code) *Error {
	return &Error{Code: c}
}

func (e *Error) Error() string {
	return string(e.Code) + ": " + english(e.Code, e.Message)
}

// english returns the message m, or c's message if m is not set, in the
// source locale, as the log shows it.
func english(c Code, m i18n.Message) string {
	if m.IsZero() {
		m = i18n.Message{Key: spec(c).message}
	}
	return i18n.Default().Localizer(i18n.Source).Format(m)
}

// ErrorEnvelope is the body of every 4xx and 5xx response:
// {"error": {"code", "message", "details", "request_id"}} (PRD FR-API-04).
// It is what the API writes for an error, so it is huma's error type too.
type ErrorEnvelope struct {
	Body ErrorBody `json:"error"`

	status int
	// message is the catalog's message that Body.Message shows. It is
	// formatted when the envelope is written, in the request's locale,
	// because huma builds some envelopes without the request.
	message i18n.Message
}

// ErrorBody is the envelope's object.
type ErrorBody struct {
	Code    Code           `json:"code" doc:"A stable error code; docs/error-codes.md lists them"`
	Message string         `json:"message" doc:"What went wrong, for people, in the language that the Content-Language header names"`
	Details map[string]any `json:"details,omitempty" doc:"More about the error, for programs; the code decides the keys"`
	// RequestID is filled in when the envelope is written.
	RequestID string `json:"request_id" doc:"The request's ID, which the server's log records it under"`
}

func (e *ErrorEnvelope) Error() string {
	if e.message.IsZero() {
		return string(e.Body.Code) + ": " + e.Body.Message
	}
	return string(e.Body.Code) + ": " + english(e.Body.Code, e.message)
}

// GetStatus returns the HTTP status, for huma.
func (e *ErrorEnvelope) GetStatus() int {
	return e.status
}

// ContentType makes huma write the envelope as plain JSON, instead of the
// application/problem+json of its own error type.
func (e *ErrorEnvelope) ContentType(string) string {
	return "application/json"
}

// forRequest returns a copy of e as it answers the request whose context is
// ctx: with the request's ID, and its message in the request's locale, or in
// English if the locale's catalog lacks it, and the locale that the message
// is in. The envelope a handler returns is never changed, because it may be
// one that the handler returns for every request, such as a package-level
// error.
func (e *ErrorEnvelope) forRequest(ctx context.Context) (*ErrorEnvelope, language.Tag) {
	c := *e
	c.Body.RequestID = logging.RequestID(ctx)
	if e.message.IsZero() {
		return &c, i18n.Source
	}
	var locale language.Tag
	c.Body.Message, locale = i18n.FromContext(ctx).Localize(e.message)
	return &c, locale
}

func envelopeFor(c Code, message i18n.Message, details map[string]any) *ErrorEnvelope {
	s := spec(c)
	if message.IsZero() {
		message = i18n.Message{Key: s.message}
	}
	return &ErrorEnvelope{Body: ErrorBody{Code: c, Details: details}, status: s.status, message: message}
}

// toEnvelope returns the envelope that answers err, which a handler
// returned. An error that is not one the API reports is logged with the
// request, and answered with internal_error, so that its text never reaches
// the client.
func toEnvelope(ctx context.Context, err error) *ErrorEnvelope {
	var env *ErrorEnvelope
	var apiErr *Error
	var idErr *ids.DecodeError
	switch {
	case errors.As(err, &env):
		return env
	case errors.As(err, &apiErr):
		return envelopeFor(apiErr.Code, apiErr.Message, apiErr.Details)
	case errors.As(err, &idErr):
		return envelopeFor(CodeInvalidID, idMessage(idErr), map[string]any{"reason": idErr.Reason})
	}
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		// The client went away, and the handler stopped. Nothing failed, and
		// nothing will read the answer.
		loggerFrom(ctx).DebugContext(ctx, "the request ended early", "error", err)
	} else {
		loggerFrom(ctx).ErrorContext(ctx, "the request failed", "error", err)
	}
	return envelopeFor(CodeInternal, i18n.Message{}, nil)
}

// ErrorBodyOf returns the envelope's object that answers err for the request
// whose context is ctx, as the error that a handler returns is answered, with
// its message in the request's locale, which it returns too, and the
// request's ID: for an answer of 200 that reports the errors of its parts,
// as the results of a request's batches of commands (PRD FR-API-03). An error
// that is not one the API reports is logged with the request, and answered
// with internal_error, as toEnvelope does.
func ErrorBodyOf(ctx context.Context, err error) (ErrorBody, language.Tag) {
	env, locale := toEnvelope(ctx, err).forRequest(ctx)
	return env.Body, locale
}

// idMessage returns the message that says why an identifier could not be
// decoded, or none, for the code's message.
func idMessage(e *ids.DecodeError) i18n.Message {
	switch e.Reason {
	case ids.MissingPrefix:
		return i18n.Message{Key: i18n.K("error.invalid_id.missing_prefix")}
	case ids.UnknownPrefix:
		return i18n.Message{Key: i18n.K("error.invalid_id.unknown_prefix")}
	case ids.WrongPrefix:
		if slices.Contains(ids.Kinds(), e.Got) && slices.Contains(ids.Kinds(), e.Expected) {
			return i18n.Message{Key: i18n.K("error.invalid_id.wrong_prefix"),
				Args: i18n.Args{"prefix": e.Got.Prefix(), "got": e.Got.String(), "expected": e.Expected.String()}}
		}
	case ids.BadCharacter:
		return i18n.Message{Key: i18n.K("error.invalid_id.character")}
	case ids.BadLength:
		return i18n.Message{Key: i18n.K("error.invalid_id.length"), Args: i18n.Args{"length": ids.BodyLength}}
	case ids.Overflow:
		return i18n.Message{Key: i18n.K("error.invalid_id.overflow")}
	}
	return i18n.Message{}
}

// statusCodes maps the statuses that huma answers with itself to codes.
var statusCodes = map[int]Code{
	http.StatusBadRequest:            CodeInvalidRequest,
	http.StatusUnauthorized:          CodeUnauthenticated,
	http.StatusNotFound:              CodeNotFound,
	http.StatusMethodNotAllowed:      CodeMethodNotAllowed,
	http.StatusRequestTimeout:        CodeRequestTimeout,
	http.StatusRequestEntityTooLarge: CodeRequestTooLarge,
	http.StatusUnsupportedMediaType:  CodeUnsupportedMediaType,
	http.StatusUnprocessableEntity:   CodeValidationFailed,
	http.StatusInternalServerError:   CodeInternal,
}

// fromHuma is huma.NewErrorWithContext: it turns an error that huma reports
// itself, such as a body that fails validation, into the envelope. op is the
// operation, or nil when huma gives none.
func fromHuma(ctx context.Context, op *huma.Operation, status int, msg string, errs []error) *ErrorEnvelope {
	if status == http.StatusNotModified {
		// huma writes no body for it, so it is not an error response.
		return &ErrorEnvelope{status: status}
	}
	c, ok := statusCodes[status]
	if !ok {
		// Registration asks for an example with status 0.
		if status != 0 {
			loggerFrom(ctx).ErrorContext(ctx, "huma answered with a status that has no error code",
				"status", status, "message", msg)
		}
		c = CodeInternal
	}
	var details map[string]any
	switch c {
	case CodeInvalidRequest:
		// huma answers 400 itself only for the body, as when a required body
		// is missing, which it reports without a location.
		details = map[string]any{"errors": errorDetails(msg, "body", errs)}
	case CodeValidationFailed:
		details = map[string]any{"errors": errorDetails(msg, "", errs)}
	case CodeRequestTooLarge:
		if op != nil && op.MaxBodyBytes > 0 {
			// Register adds the byte that huma's limit takes away.
			details = map[string]any{"max_bytes": op.MaxBodyBytes - 1}
		}
	case CodeInternal:
		err := errors.Join(append([]error{errors.New(msg)}, errs...)...)
		if ctx.Err() != nil {
			// The client went away, as while it sent the body, which net/http
			// reports by ending the request's context. Nothing failed, as in
			// toEnvelope.
			loggerFrom(ctx).DebugContext(ctx, "the request ended early", "error", err)
		} else {
			loggerFrom(ctx).ErrorContext(ctx, "the request failed", "error", err)
		}
	}
	return envelopeFor(c, i18n.Message{}, details)
}

// Problem is one entry of details.errors.
type Problem struct {
	// Location is where the problem is, such as body.name or query.limit.
	Location string `json:"location,omitempty"`
	Message  string `json:"message"`
}

// errorDetails lists huma's problems, or, if huma gave none, msg as the one
// problem, at location. huma also records the offending value, which is left
// out: it may be a password, and it may be large.
func errorDetails(msg, location string, errs []error) []Problem {
	var out []Problem
	for _, err := range errs {
		if err == nil {
			continue
		}
		var d huma.ErrorDetailer
		if errors.As(err, &d) {
			detail := d.ErrorDetail()
			out = append(out, Problem{Location: detail.Location, Message: detail.Message})
			continue
		}
		out = append(out, Problem{Message: err.Error()})
	}
	if len(out) == 0 {
		out = append(out, Problem{Location: location, Message: msg})
	}
	return out
}

// WriteError writes the envelope that answers err, as toEnvelope builds it,
// for a handler that is not a huma operation. The headers that err carries,
// which huma.ErrorWithHeaders adds, as for Retry-After or NotModified's ETag,
// are sent with it, as Register sends them, and replace any of the same name
// already set, such as httpserver's Cache-Control: no-store.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var he huma.HeadersError
	if errors.As(err, &he) {
		for name, values := range he.GetHeaders() {
			w.Header()[http.CanonicalHeaderKey(name)] = slices.Clone(values)
		}
	}
	writeEnvelope(w, r, toEnvelope(r.Context(), err))
}

// writeEnvelope writes env as forRequest fills it in. It fills in a copy, as
// fillEnvelope does, because a handler may return one envelope for many
// requests.
func writeEnvelope(w http.ResponseWriter, r *http.Request, env *ErrorEnvelope) {
	if env.status == http.StatusNotModified {
		// A 304 has no body (RFC 9110 §15.4.5), as huma writes it.
		w.WriteHeader(env.status)
		return
	}
	env, locale := env.forRequest(r.Context())
	b, err := json.Marshal(env)
	if err != nil {
		// Details that do not encode are a bug in the code that set them.
		loggerFrom(r.Context()).ErrorContext(r.Context(),
			"an error envelope does not encode, so the request is answered with internal_error", "error", err)
		env, locale = envelopeFor(CodeInternal, i18n.Message{}, nil).forRequest(r.Context())
		b, _ = json.Marshal(env)
	}
	w.Header().Set("Content-Type", "application/json")
	setLanguage(w.Header(), locale)
	w.WriteHeader(env.status)
	_, _ = w.Write(append(b, '\n'))
}

// setLanguage sets the headers of an envelope whose message is in locale:
// Content-Language names the locale, and Vary says that the message depends
// on the request's Accept-Language (RFC 9110 §8.5, §12.5.5).
func setLanguage(h http.Header, locale language.Tag) {
	h.Set("Content-Language", locale.String())
	h.Add("Vary", "Accept-Language")
}

type loggerKey struct{}

// withLogger returns a context whose errors are logged to log.
func withLogger(ctx context.Context, log *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, log)
}

// loggerFrom returns the logger of the context's Mux, or one that discards.
func loggerFrom(ctx context.Context) *slog.Logger {
	if log, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return log
	}
	return slog.New(slog.DiscardHandler)
}
