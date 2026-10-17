// Generated from schema/api/v1/openapi.json. Do not edit; run
// "go test ./internal/httpserver -run TestOpenAPIDocument -update" in server/
// instead.

export interface CompleteSetupBody {
  /** The first owner's account, which is an instance admin */
  owner: SetupOwnerBody;
  /** The setup code, which the server writes to its log while setup is pending, as 7K2M-Q9XD-4RTB-H8VW. Case, hyphens, and spaces do not matter, and O is read as 0, and I and L as 1. */
  setup_code: string;
  /** Who can register an account once setup has completed: closed (no one), invite (people holding an invitation), or open (anyone). Without it, closed. STORYCREEP_SIGNUP overrides it for as long as it is set, as GET /api/v1/setup says. */
  signup_policy?: "closed" | "invite" | "open";
  /** The SMTP server that the instance sends its email through: the links that verify an email address and reset a password. Without it, the instance sends no email, and an instance admin gives out the links as text. STORYCREEP_SMTP_URL overrides it for as long as it is set, as GET /api/v1/setup says. Setup does not connect to it. */
  smtp?: SetupSMTPBody;
  /** The first workspace, which the owner owns, and which is the owner's personal workspace. Its default locale is STORYCREEP_DEFAULT_LOCALE. */
  workspace: SetupWorkspaceBody;
}

export interface CompletedSetupBody {
  /** The ID of the owner's account, which signs in with POST /api/v1/auth/login */
  owner_id: string;
  /** The first workspace */
  workspace: WorkspaceFields;
}

export interface CreateInviteBody {
  /** The email address to invite, which the account that accepts the invitation must have, in any case. Without it, the invitation is a link invitation, which whoever holds its link may accept, with any account. */
  email?: string;
  /** The role that accepting the invitation gives. Only an owner invites someone as owner, and an admin is answered with 403 owner_required. */
  role: "owner" | "admin" | "editor" | "commenter" | "viewer" | "guest";
}

export interface CreateWorkspaceBody {
  /** The workspace's default locale, as a BCP 47 tag of a locale that the server has a catalog for. Without it, the locale of the request: the one that its Accept-Language prefers among those, or else STORYCREEP_DEFAULT_LOCALE. */
  default_locale?: string;
  /** The workspace's name, of 1 to 100 characters, not all of them blank, and without control characters, bidirectional controls and line separators among them. White space around it is removed. */
  name: string;
  /** The workspace's slug: groups of lowercase ASCII letters and digits joined by single hyphens, of at most 64 characters, and not a reserved word, or the answer is 422 slug_invalid; if another workspace has it, the answer is 409 slug_taken. Without it, the slug is derived from the name: its letters and digits in lowercase ASCII, without accents, each run of other characters a hyphen, cut to 64 characters, or workspace if the name has none. If another workspace has that slug, or it is a reserved word, -2, -3, and so on up to -10 is added, and after that a random suffix of 8 lowercase letters and digits. */
  slug?: string;
}

export interface ErrorBody {
  /** A stable error code; docs/error-codes.md lists them */
  code: string;
  /** More about the error, for programs; the code decides the keys */
  details?: Record<string, unknown>;
  /** What went wrong, for people, in the language that the Content-Language header names */
  message: string;
  /** The request's ID, which the server's log records it under */
  request_id: string;
}

export interface ErrorEnvelope {
  error: ErrorBody;
}

export interface InstanceBody {
  /** Whether the instance sends email, through the SMTP server of STORYCREEP_SMTP_URL or of first-run setup. Without it, a page says that email is not configured: the links that reset a password and verify an email address are not emailed, and an instance admin gives them out as text. */
  email_configured: boolean;
}

export interface InviteBody {
  /** When it was made */
  created_at: string;
  /** The ID of the account that made it, unless that account was deleted */
  created_by?: string;
  /** The invited address, of an invitation of the kind email */
  email?: string;
  /** When it expires: 14 days after it was made, sent anew, or emailed */
  expires_at: string;
  /** The invitation's ID */
  id: string;
  /** Whom it invites: email, the address of email, which the account that accepts it must have, or link, whoever holds its link */
  kind: "email" | "link";
  /** The role that accepting it gives in the workspace */
  role: "owner" | "admin" | "editor" | "commenter" | "viewer" | "guest";
  /** When the instance emailed its link, which then lasts its 14 days from then. Without it, no email holds its current link: the instance sends no email, the invitation is a link invitation, or its email is still to be sent, or could not be sent, as to an address that the SMTP server refuses. */
  sent_at?: string;
  /** pending while it may be accepted, and expired once it has expired, until it is sent anew */
  status: "pending" | "expired";
  /** Its link, which opens the web app's page that accepts it, with its token in the URL's fragment. It is here only in the answer that made the invitation or sent it anew, and only when the instance does not email it: for a link invitation, and for every invitation on an instance that sends no email. Whoever holds it can accept the invitation, so give it only to the person invited. The database holds no copy, and sending the invitation anew gives a new link, in place of this one. */
  url?: string;
}

export interface InvitePreviewBody {
  /** The invited address, which the account that accepts it must have, of an invitation of the kind email */
  email?: string;
  /** When it expires */
  expires_at: string;
  /** Whom it invites: email, the address of email, or link, whoever holds its link */
  kind: "email" | "link";
  /** The role that accepting it gives in the workspace */
  role: "owner" | "admin" | "editor" | "commenter" | "viewer" | "guest";
  /** The name of the workspace that it invites to */
  workspace_name: string;
}

export interface InviteTokenBody {
  /** The token of the invitation's link: what follows token= in the fragment of the URL that the link opens */
  token: string;
}

export interface IssueLinkBody {
  /** The email address of the account, in any case */
  email: string;
}

export interface LinkBody {
  /** When the link expires */
  expires_at: string;
  /** The link, which opens the web app's page that uses it, with its token in the URL's fragment. Whoever holds it can use it, so give it only to the account's user. */
  url: string;
}

export interface LoginBody {
  /** The account's email address, in any case */
  email: string;
  /** The account's password */
  password: string;
}

export interface MemberBody {
  /** The member's display name */
  display_name: string;
  /** The member's email address, which only an owner or an admin of the workspace sees */
  email?: string;
  /** The ID of the membership */
  id: string;
  /** When the member joined the workspace */
  joined_at: string;
  /** The membership's role. A guest acts only on the projects granted to them. */
  role: "owner" | "admin" | "editor" | "commenter" | "viewer" | "guest";
  /** The ID of the member's account */
  user_id: string;
}

export interface MembershipBody {
  /** When the workspace was created */
  created_at: string;
  /** The workspace's default locale, as a BCP 47 tag of a locale that the server has a catalog for */
  default_locale: string;
  /** The workspace's ID */
  id: string;
  /** The ID of the signed-in user's membership of the workspace */
  membership_id: string;
  /** The workspace's name */
  name: string;
  /** The role of the signed-in user's membership. A guest acts only on the projects granted to them, and GET /api/v1/workspaces/{workspace_id} answers them with 404 not_found. */
  role: "owner" | "admin" | "editor" | "commenter" | "viewer" | "guest";
  /** The workspace's slug, which is unique on the instance */
  slug: string;
  /** When its name, slug, or settings last changed */
  updated_at: string;
}

export interface PageInviteBody {
  items: Array<InviteBody>;
  /** The cursor of the next page, or null if this is the last page */
  next_cursor: string | null;
}

export interface PageMemberBody {
  items: Array<MemberBody>;
  /** The cursor of the next page, or null if this is the last page */
  next_cursor: string | null;
}

export interface PageMembershipBody {
  items: Array<MembershipBody>;
  /** The cursor of the next page, or null if this is the last page */
  next_cursor: string | null;
}

export interface PageSessionBody {
  items: Array<SessionBody>;
  /** The cursor of the next page, or null if this is the last page */
  next_cursor: string | null;
}

export interface PasswordBody {
  /** The account's password */
  current_password: string;
  /** The new password: at least 12 characters, and not one of the most common passwords, or the answer is weak_password */
  new_password: string;
}

export interface PasswordResetBody {
  /** The email address of the account whose password to reset, in any case */
  email: string;
}

export interface RegisterBody {
  /** The name that other people see, of 1 to 100 characters, not all of them blank, and without control characters, bidirectional controls and line separators among them. White space around it is removed. */
  display_name: string;
  /** The account's email address, which signs in: an address alone, as jo@example.com, of at most 254 bytes, without spaces or characters that are not printed. Addresses compare without regard to case or to how their characters are composed. */
  email: string;
  /** The password: at least 12 characters, and not one of the most common passwords, or the answer is weak_password. */
  password: string;
}

export interface RegisterWithInviteBody {
  /** The name that other people see, as for POST /api/v1/auth/register */
  display_name: string;
  /** The account's email address, as for POST /api/v1/auth/register. An invitation to an address takes only that address, as citext compares them, and the address is the invitation's when this is left out; a link invitation needs one. */
  email?: string;
  /** The password, as for POST /api/v1/auth/register */
  password: string;
  /** The token of the invitation's link: what follows token= in the fragment of the URL that the link opens */
  token: string;
}

export interface ResetPasswordBody {
  /** The new password: at least 12 characters, and not one of the most common passwords, or the answer is weak_password */
  new_password: string;
  /** The token of the link that resets the password: what follows token= in the fragment of the URL that the link opens */
  token: string;
}

export interface RevokedBody {
  /** How many sessions were revoked */
  revoked: number;
}

export interface SessionBody {
  /** When its user signed in */
  created_at: string;
  /** Whether it is the session of the request */
  current: boolean;
  /** When it ends at the latest, however often it is used: STORYCREEP_SESSION_LIFETIME after its user signed in, or the lifetime in force when its user signed in, if that was shorter */
  expires_at: string;
  /** The session's ID */
  id: string;
  /** When it ends unless it is used before: STORYCREEP_SESSION_IDLE_TIMEOUT after its last use, or at expires_at if that is sooner */
  idle_expires_at: string;
  /** The IP address that its user signed in from: the address that the connection came from, or, if that is one of STORYCREEP_TRUSTED_PROXIES, the client's address that the proxies forwarded */
  ip?: string;
  /** When it was last used, to the minute */
  last_seen_at: string;
  /** The User-Agent header of the client that its user signed in with */
  user_agent?: string;
}

export interface SetupBody {
  /** The signup policy that the configuration sets, if it sets one, or else the one that setup records when the request names none: closed. */
  signup_policy: "closed" | "invite" | "open";
  /** Whether the configuration sets the signup policy, with STORYCREEP_SIGNUP, or because the instance is hosted, which then overrides the policy that setup records, for as long as it sets it. A setup screen offers no choice of policy then. */
  signup_policy_fixed: boolean;
  /** Whether the configuration sets the SMTP server, with STORYCREEP_SMTP_URL, which then overrides the one that setup records, for as long as it sets it. A setup screen asks for no SMTP server then. */
  smtp_fixed: boolean;
}

export interface SetupOwnerBody {
  /** The owner's display name, as for POST /api/v1/auth/register */
  display_name: string;
  /** The owner's email address, which signs in, as for POST /api/v1/auth/register */
  email: string;
  /** The owner's password: at least 12 characters, and not one of the most common passwords, or the answer is weak_password. */
  password: string;
}

export interface SetupSMTPBody {
  /** The address that the instance's email comes from, alone or with a name, as Storycreep <noreply@example.com>, which the server must let it send from */
  from: string;
  /** The SMTP server's host name or IP address */
  host: string;
  /** The password that signs in to the server, with the username */
  password?: string;
  /** The SMTP server's port, as 587 for starttls or 465 for tls */
  port: number;
  /** How the connection is protected: tls, from its start; starttls, which the server must offer, before anything is sent; or none, for a relay on the same host or network, which then cannot sign in unless it is on localhost, 127.0.0.1, or ::1 */
  security: "tls" | "starttls" | "none";
  /** The username that signs in to the server, with PLAIN or LOGIN, with the password; without it, the server is not signed in to */
  username?: string;
}

export interface SetupWorkspaceBody {
  /** The workspace's name, as for POST /api/v1/workspaces */
  name: string;
  /** The workspace's slug, as for POST /api/v1/workspaces, with the same errors. Without it, the slug is derived from the name. */
  slug?: string;
}

export interface SignedInBody {
  /** The session's CSRF token: every request made with the session that changes something, with a method other than GET, HEAD, or OPTIONS, sends it in its X-CSRF-Token header, or is answered with 403 csrf_failed. It stays the same for as long as the session does, until changing the password gives the session a new one. */
  csrf_token: string;
  /** The session */
  session: SessionBody;
  /** The ID of the account that is signed in */
  user_id: string;
}

export interface StatusBody {
  /** ok for /healthz, and ready for /readyz */
  status: "ok" | "ready";
}

export interface UpdateWorkspaceBody {
  /** The workspace's new default locale, a BCP 47 tag of a locale that the server has a catalog for */
  default_locale?: string;
  /** The workspace's new name, as for POST /api/v1/workspaces */
  name?: string;
  /** The workspace's new slug, as for POST /api/v1/workspaces, with the same errors. A slug that the workspace has already is kept, as a reserved word that a later release added to the list is. */
  slug?: string;
}

export interface VerifyEmailBody {
  /** The token of the link that verifies the email address: what follows token= in the fragment of the URL that the link opens */
  token: string;
}

export interface VersionBody {
  /** The revision of the source the binary was built from */
  commit?: string;
  /** The time of that revision, in RFC 3339 */
  commit_time?: string;
  /** The version of Go the binary was built with */
  go_version: string;
  /** The release version, or (devel) for any other build, such as the main branch's image */
  version: string;
}

export interface WorkspaceBody {
  /** When the workspace was created */
  created_at: string;
  /** The workspace's default locale, as a BCP 47 tag of a locale that the server has a catalog for */
  default_locale: string;
  /** The workspace's ID */
  id: string;
  /** The workspace's name */
  name: string;
  /** The role that the signed-in user acts with in the workspace: their membership's, or owner for an instance admin when STORYCREEP_INSTANCE_ADMIN_ACCESS is on */
  role: "owner" | "admin" | "editor" | "commenter" | "viewer";
  /** The workspace's slug, which is unique on the instance */
  slug: string;
  /** When its name, slug, or settings last changed */
  updated_at: string;
}

export interface WorkspaceFields {
  /** When the workspace was created */
  created_at: string;
  /** The workspace's default locale, as a BCP 47 tag of a locale that the server has a catalog for */
  default_locale: string;
  /** The workspace's ID */
  id: string;
  /** The workspace's name */
  name: string;
  /** The workspace's slug, which is unique on the instance */
  slug: string;
  /** When its name, slug, or settings last changed */
  updated_at: string;
}

/** The API's operations, by operation ID: what each takes, and what it answers with each status that is not an error's. */
export interface Operations {
  /**
   * Accept an invitation
   *
   * Accepts an invitation with its link as the signed-in user, who joins its workspace with its role, and returns the membership. An invitation to an address that is not the account's is answered with 403 invite_email_mismatch (FR-TEN-02 AC3), while a link invitation may be accepted by any account. A user who is a member of the workspace already is answered with 409 already_member, and the invitation stays open. Accepting again an invitation that the user accepted, as registering with it does, returns the membership again, while the user is still a member. A token that belongs to no invitation, as one that sending the invitation anew replaced, or whose workspace no longer exists, is answered with 400 token_invalid, an invitation that has been revoked with 410 invite_revoked, one that has been accepted with 410 invite_accepted, and one that has expired with 410 invite_expired (FR-TEN-02 AC1). It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, and reading, accepting, or registering with an invitation's link share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, and accepting one share one of 10 a minute for each account. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  acceptInvite: {
    input: {
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
      body: InviteTokenBody;
    };
    responses: {
      200: MembershipBody;
    };
  };
  /**
   * Change the account's password
   *
   * Changes the signed-in account's password, if current_password is the account's, or the answer is 403 wrong_password. The new password must have at least 12 characters, counted as Unicode code points once it is in NFKC, and must not be one of the 10,000 most common passwords, or the answer is 422 weak_password. Changing it revokes every other session of the account, stops the account's links that reset its password from working, and gives the session of the request a new secret, in the Set-Cookie header, and a new CSRF token, in the body, so that the old ones are no longer valid. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, and reading, accepting, or registering with an invitation's link share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, and accepting one share one of 10 a minute for each account. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  changePassword: {
    input: {
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
      body: PasswordBody;
    };
    responses: {
      200: SignedInBody;
    };
  };
  /**
   * Complete first-run setup
   *
   * Completes the instance's first-run setup, once: creates the first owner's account, which is an instance admin, and the first workspace, which the owner owns, and records the signup policy and the SMTP server. Until then, POST /api/v1/auth/register answers 403 setup_required, whatever the policy. The setup code, which the server writes to its log while setup is pending, must be the instance's, or the answer is 403 invalid_setup_code. A field that the request does not allow, as an SMTP server's host that is not one, or a username without a password, or an email address that has an account, as one that storycreep create-user made, is answered with 422 validation_failed. Of two setups at once, one completes, and the other is answered with 404 not_found. The owner then signs in with POST /api/v1/auth/login. Once setup has completed, the answer is 404 not_found, for everyone, and stays so. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, and reading, accepting, or registering with an invitation's link share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, and accepting one share one of 10 a minute for each account. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  completeSetup: {
    input: {
      body: CompleteSetupBody;
    };
    responses: {
      201: CompletedSetupBody;
    };
  };
  /**
   * Invite someone to a workspace
   *
   * Invites an email address, or, without one, whoever holds the invitation's link, to join a workspace with a role, for 14 days. Only an owner invites someone as owner, and an admin is answered with 403 owner_required. Only an owner or an admin of the workspace may, and anyone else with a role in it is answered with 403 insufficient_role. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. An address that belongs to a member of the workspace is answered with 409 already_member, and one that has an open invitation of the workspace already, expired or not, with 409 invite_exists. On an instance that sends email, as GET /api/v1/instance says, an invitation to an address is emailed, with a link that lasts its 14 days from when the email is sent, and the answer holds no link. A workspace emails an address one invitation in 10 minutes at most, so the email of an invitation made sooner after the workspace last emailed the address, as after revoking an invitation that was emailed and inviting the address again, is sent once those 10 minutes have passed. Otherwise, and for a link invitation on every instance, the answer's url is the link, which the signed-in user gives the person invited; the server keeps no copy of it. The email names the instance and the role, but neither the workspace nor who invites, which the page that the link opens shows instead (POST /api/v1/invites/lookup). It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, and reading, accepting, or registering with an invitation's link share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, and accepting one share one of 10 a minute for each account. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  createInvite: {
    input: {
      path: {
        /** The workspace's ID */
        workspace_id: string;
      };
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
      body: CreateInviteBody;
    };
    responses: {
      201: InviteBody;
    };
  };
  /**
   * Create a workspace
   *
   * Creates a workspace, whose owner the signed-in user becomes. Where the instance always sends email, as hosted mode does, a user who has not verified their email address is answered with 403 email_unverified, whatever the request holds. A name or a default locale that its field does not allow is answered with 422 validation_failed. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed.
   */
  createWorkspace: {
    input: {
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
      body: CreateWorkspaceBody;
    };
    responses: {
      201: WorkspaceBody;
    };
  };
  /** Report that the process is alive */
  getHealth: {
    input: Record<string, never>;
    responses: {
      200: StatusBody;
    };
  };
  /**
   * Get what the instance offers
   *
   * Says whether the instance sends email, so that a page can say that email is not configured, and where the links that reset a password and verify an email address come from.
   */
  getInstance: {
    input: Record<string, never>;
    responses: {
      200: InstanceBody;
    };
  };
  /**
   * Get the API's OpenAPI document
   *
   * The OpenAPI 3.1 document of the server's operations, this one included. It is the same for every server of a release, and is committed to the repository as schema/api/v1/openapi.json.
   */
  getOpenAPIDocument: {
    input: {
      headers?: {
        /** The ETag of the copy of the document that the client has */
        "If-None-Match"?: string;
      };
    };
    responses: {
      200: Record<string, unknown>;
      304: undefined;
    };
  };
  /**
   * Report whether the server can serve
   *
   * Answers 200 when the server has finished starting, the database has every migration applied, and the database answers. Otherwise it answers 503 with not_ready, and details.reason says why.
   */
  getReadiness: {
    input: Record<string, never>;
    responses: {
      200: StatusBody;
    };
  };
  /**
   * Get the session of the request
   *
   * Returns the signed-in account's ID, the session of the request, and its CSRF token, which a page that the browser loads again reads here. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated.
   */
  getSession: {
    input: Record<string, never>;
    responses: {
      200: SignedInBody;
    };
  };
  /**
   * Get what first-run setup asks for
   *
   * Tells a setup screen that the instance's first-run setup is pending, and whether it can choose the signup policy and the SMTP server. Once setup has completed, the answer is 404 not_found, for everyone, and stays so.
   */
  getSetup: {
    input: Record<string, never>;
    responses: {
      200: SetupBody;
    };
  };
  /** Describe the server's build */
  getVersion: {
    input: Record<string, never>;
    responses: {
      200: VersionBody;
    };
  };
  /**
   * Get a workspace
   *
   * Returns a workspace, with the role that the signed-in user acts with in it. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated.
   */
  getWorkspace: {
    input: {
      path: {
        /** The workspace's ID */
        workspace_id: string;
      };
    };
    responses: {
      200: WorkspaceBody;
    };
  };
  /**
   * Take a link that resets a password
   *
   * Returns a link that resets the password of the active account that the email address belongs to, which lasts an hour, and is used once with POST /api/v1/auth/password-reset/confirm. It is for an instance that sends no email, whose links an instance admin gives out instead, to the account's user alone, because whoever holds a link can use it; an instance that sends email answers 403 email_configured. A user who is not an instance admin, such as a workspace's owner or admin, is answered with 403 instance_admin_required, whatever the request holds. An address that belongs to no active account is answered with 404 not_found. The audit log records the issue. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, and reading, accepting, or registering with an invitation's link share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, and accepting one share one of 10 a minute for each account. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  issuePasswordResetLink: {
    input: {
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
      body: IssueLinkBody;
    };
    responses: {
      201: LinkBody;
    };
  };
  /**
   * Take a link that verifies an email address
   *
   * Returns a link that verifies the email address of the active account that it belongs to, which lasts 24 hours, and is used once with POST /api/v1/auth/verify-email. It is for an instance that sends no email, whose links an instance admin gives out instead, to the account's user alone, because whoever holds a link can use it; an instance that sends email answers 403 email_configured. A user who is not an instance admin, such as a workspace's owner or admin, is answered with 403 instance_admin_required, whatever the request holds. An address that belongs to no active account is answered with 404 not_found. The audit log records the issue. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, and reading, accepting, or registering with an invitation's link share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, and accepting one share one of 10 a minute for each account. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  issueVerificationLink: {
    input: {
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
      body: IssueLinkBody;
    };
    responses: {
      201: LinkBody;
    };
  };
  /**
   * List a workspace's invitations
   *
   * Lists the open invitations of a workspace, those that have been neither accepted nor revoked, expired or not, in the order of their IDs, oldest first. Their links are not among them: the server keeps none, and sending an invitation anew gives a new one. Only an owner or an admin of the workspace may, and anyone else with a role in it is answered with 403 insufficient_role. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated.
   */
  listInvites: {
    input: {
      path: {
        /** The workspace's ID */
        workspace_id: string;
      };
      query?: {
        /** The next_cursor of the previous page; empty for the first page */
        cursor?: string;
        /** How many items the page holds at most */
        limit?: number;
      };
    };
    responses: {
      200: PageInviteBody;
    };
  };
  /**
   * List a workspace's members
   *
   * Lists the members of a workspace, guests included, in the order of their memberships' IDs, oldest first, with their display names and roles, to every member with a role in it. Only its owners and admins also see their email addresses. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated.
   */
  listMembers: {
    input: {
      path: {
        /** The workspace's ID */
        workspace_id: string;
      };
      query?: {
        /** The next_cursor of the previous page; empty for the first page */
        cursor?: string;
        /** How many items the page holds at most */
        limit?: number;
      };
    };
    responses: {
      200: PageMemberBody;
    };
  };
  /**
   * List the account's sessions
   *
   * Lists the signed-in account's active sessions, newest first, each with the IP address and user agent that it started with, and when it was last used. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated.
   */
  listSessions: {
    input: {
      query?: {
        /** The next_cursor of the previous page; empty for the first page */
        cursor?: string;
        /** How many items the page holds at most */
        limit?: number;
      };
    };
    responses: {
      200: PageSessionBody;
    };
  };
  /**
   * List the account's workspaces
   *
   * Lists the workspaces that the signed-in user is a member of, in the order of their IDs, oldest first, each with the role of the user's membership, guest included. A client switches between them by naming one by its ID in the routes that it calls: the server keeps no current workspace. Every account has one at least from its first sign-in, which gives it its personal workspace. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated.
   */
  listWorkspaces: {
    input: {
      query?: {
        /** The next_cursor of the previous page; empty for the first page */
        cursor?: string;
        /** How many items the page holds at most */
        limit?: number;
      };
    };
    responses: {
      200: PageMembershipBody;
    };
  };
  /**
   * Sign in
   *
   * Signs in with an account's email address and password, and starts a session, whose secret the Set-Cookie header gives the client, and whose CSRF token the body holds. The account's first sign-in also gives it its personal workspace, named after its display name, which it owns; GET /api/v1/workspaces lists it. Where the instance always sends email, as hosted mode does, that is the first sign-in once the account's email address is verified, or the verification itself. An address of no account, a wrong password, and an account that cannot sign in, such as a suspended one, are all answered with 401 invalid_credentials, after the same work, so that the answer does not tell them apart. A request that a browser's Sec-Fetch-Site header says comes from another site, or from another origin of the same site, is answered with 403 csrf_failed; browsers send that header only to https URLs and to loopback addresses, such as localhost. After 10 failed sign-ins in a row with an email address, whether or not it belongs to an account, the address is locked for a minute, and each further failure, once a lock ends, locks it for twice as long as the lock before, up to 15 minutes: while it is locked, every sign-in with it is answered with 429 rate_limited, whose details.reason is locked, even with the right password. A successful sign-in forgets the failures, and so do 24 hours without one. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, and reading, accepting, or registering with an invitation's link share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, and accepting one share one of 10 a minute for each account. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  login: {
    input: {
      body: LoginBody;
    };
    responses: {
      200: SignedInBody;
    };
  };
  /**
   * Sign out
   *
   * Ends the session of the request: deletes it, so that its secret is no longer valid on any server, and clears its cookie. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed.
   */
  logout: {
    input: {
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
    };
    responses: {
      204: undefined;
    };
  };
  /**
   * Read an invitation with its link
   *
   * Returns what the holder of an invitation's link sees of it, so that the page that accepts it can show it: the workspace's name, the role, and the invited address, if it has one. It is a POST, so that the token travels in the body, never in a URL that a log could hold. A token that belongs to no invitation, as one that sending the invitation anew replaced, or whose workspace no longer exists, is answered with 400 token_invalid, an invitation that has been revoked with 410 invite_revoked, one that has been accepted with 410 invite_accepted, and one that has expired with 410 invite_expired (FR-TEN-02 AC1). Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, and reading, accepting, or registering with an invitation's link share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, and accepting one share one of 10 a minute for each account. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  lookupInvite: {
    input: {
      body: InviteTokenBody;
    };
    responses: {
      200: InvitePreviewBody;
    };
  };
  /**
   * Register an account
   *
   * Creates an account with an email address, a display name, and a password, if the instance's signup policy lets anyone register: the policy that STORYCREEP_SIGNUP sets, or else the one that first-run setup chose. Until setup has completed, the answer is 403 setup_required, whatever the policy. Under closed the answer is 403 signup_closed, and under invite 403 invite_required, because only an invitation creates an account then; each comes whatever the request holds. An email address or a display name that its field does not allow is answered with 422 validation_failed. The password must have at least 12 characters, counted as Unicode code points once it is in NFKC, and must not be one of the 10,000 most common passwords, compared without regard to case, or the answer is 422 weak_password. Otherwise the answer is 202 without a body, whether or not the address has an account already, which then stays as it was, so that the answer does not tell whether an address has an account. On an instance that sends email, as GET /api/v1/instance says, a new account is emailed a link that verifies its address, which POST /api/v1/auth/verify-email uses, and an address that has an account already is emailed nothing. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, and reading, accepting, or registering with an invitation's link share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, and accepting one share one of 10 a minute for each account. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  registerAccount: {
    input: {
      body: RegisterBody;
    };
    responses: {
      202: undefined;
    };
  };
  /**
   * Register an account with an invitation
   *
   * Creates an account, as POST /api/v1/auth/register does, and accepts an invitation with its link as the new account, which joins the invitation's workspace with its role, under the invite and the open signup policies. Under closed, the answer is 403 signup_closed, whatever the request holds, and only an account that exists already accepts an invitation, with POST /api/v1/invites/accept. An invitation to an address registers that address, and another is answered with 403 invite_email_mismatch (FR-TEN-02 AC3). A token that belongs to no invitation, as one that sending the invitation anew replaced, or whose workspace no longer exists, is answered with 400 token_invalid, an invitation that has been revoked with 410 invite_revoked, one that has been accepted with 410 invite_accepted, and one that has expired with 410 invite_expired (FR-TEN-02 AC1). The fields are checked as POST /api/v1/auth/register checks them, with its errors. Otherwise the answer is 202 without a body, whether or not the address has an account already, so that it does not tell whether an address has one: an account that exists stays as it was, and the invitation stays open for it. The client then signs in, and accepts the invitation with POST /api/v1/invites/accept, which returns the membership either way. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, and reading, accepting, or registering with an invitation's link share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, and accepting one share one of 10 a minute for each account. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  registerWithInvite: {
    input: {
      body: RegisterWithInviteBody;
    };
    responses: {
      202: undefined;
    };
  };
  /**
   * Remove a member from a workspace
   *
   * Removes a membership of a workspace, and with it the member's project grants there; the account stays as it was. It also revokes the workspace's open invitations, expired or not, that the member made, those whose link the member was last given as text, as by sending one anew, and those to the member's address, so that no link that they hold lets them back in. Only an owner removes an owner, and an admin is answered with 403 owner_required. A workspace keeps an owner whose account is active, so removing its last one is answered with 409 last_owner (FR-TEN-02 AC2). An owner or an admin may remove themselves, as long as the workspace keeps an owner. Only an owner or an admin of the workspace may, and anyone else with a role in it is answered with 403 insufficient_role. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. So is a membership that the workspace does not have. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed.
   */
  removeMember: {
    input: {
      path: {
        /** The ID of the membership to remove */
        membership_id: string;
        /** The workspace's ID */
        workspace_id: string;
      };
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
    };
    responses: {
      204: undefined;
    };
  };
  /**
   * Ask for a link that verifies the account's email address
   *
   * Emails a link that verifies the signed-in account's email address, which lasts 24 hours, and is used once with POST /api/v1/auth/verify-email. An address that is verified already is emailed nothing, and nor is one that was emailed such a link in the last 10 minutes that still works: the answer is the same, and the earlier link keeps working. Registering emails one too. An instance that sends no email answers 403 email_not_configured, whatever the request holds, as GET /api/v1/instance says it will. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, and reading, accepting, or registering with an invitation's link share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, and accepting one share one of 10 a minute for each account. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  requestEmailVerification: {
    input: {
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
    };
    responses: {
      202: undefined;
    };
  };
  /**
   * Ask for a link that resets a password
   *
   * Emails a link that resets the password of the active account that the email address belongs to, which lasts an hour, and is used once with POST /api/v1/auth/password-reset/confirm. The answer is 202 without a body, as soon, whether or not the address belongs to an account, so that it does not tell whether it does; an address of no account is emailed nothing. Nor is an account that was emailed such a link in the last 10 minutes that still works, so that asking again and again does not flood its inbox: the answer is the same, and the earlier link keeps working. An instance that sends no email answers 403 email_not_configured, whatever the request holds, as GET /api/v1/instance says it will. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, and reading, accepting, or registering with an invitation's link share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, and accepting one share one of 10 a minute for each account. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  requestPasswordReset: {
    input: {
      body: PasswordResetBody;
    };
    responses: {
      202: undefined;
    };
  };
  /**
   * Send an invitation anew
   *
   * Gives an open invitation, expired or not, a new link and 14 days more. The old link stops working at once. As when it was made, the new link is emailed, or is the answer's url, and an email waits until 10 minutes have passed since the workspace last emailed the address, so sending an invitation anew again and again emails it once in 10 minutes at most, with the latest link. Only an owner sends an invitation as owner anew, and an admin is answered with 403 owner_required. Only an owner or an admin of the workspace may, and anyone else with a role in it is answered with 403 insufficient_role. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. So is an invitation that the workspace does not have. One that has been accepted is answered with 410 invite_accepted, one that has been revoked with 410 invite_revoked, and one to an address that belongs to a member of the workspace by now with 409 already_member. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, and reading, accepting, or registering with an invitation's link share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, and accepting one share one of 10 a minute for each account. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  resendInvite: {
    input: {
      path: {
        /** The invitation's ID */
        invite_id: string;
        /** The workspace's ID */
        workspace_id: string;
      };
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
    };
    responses: {
      200: InviteBody;
    };
  };
  /**
   * Reset a password with a link
   *
   * Replaces the password of the account of a link that resets it, and uses the link, which works once. It signs the account out everywhere: every session of the account is revoked, and the user then signs in with the new password. The account's other reset links stop working too. A token that does not exist, of another kind of link, or of an account that can no longer use it, is answered with 400 token_invalid, a link that has been used with 400 token_used, and one that has expired with 400 token_expired. The new password must have at least 12 characters, counted as Unicode code points once it is in NFKC, and must not be one of the 10,000 most common passwords, or the answer is 422 weak_password, which leaves the link as it was. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, and reading, accepting, or registering with an invitation's link share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, and accepting one share one of 10 a minute for each account. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  resetPassword: {
    input: {
      body: ResetPasswordBody;
    };
    responses: {
      204: undefined;
    };
  };
  /**
   * Revoke an invitation
   *
   * Revokes an open invitation, whose link is then answered with 410 invite_revoked (FR-TEN-02 AC1). Revoking it again changes nothing. An admin may revoke an invitation as owner too. Only an owner or an admin of the workspace may, and anyone else with a role in it is answered with 403 insufficient_role. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. So is an invitation that the workspace does not have. One that has been accepted is answered with 410 invite_accepted. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed.
   */
  revokeInvite: {
    input: {
      path: {
        /** The invitation's ID */
        invite_id: string;
        /** The workspace's ID */
        workspace_id: string;
      };
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
    };
    responses: {
      204: undefined;
    };
  };
  /**
   * Revoke the account's other sessions
   *
   * Revokes every active session of the signed-in account but the session of the request, which signs the account out everywhere else. POST /api/v1/auth/logout ends the session of the request. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed.
   */
  revokeOtherSessions: {
    input: {
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
    };
    responses: {
      200: RevokedBody;
    };
  };
  /**
   * Revoke a session
   *
   * Revokes one of the signed-in account's active sessions, which is then no longer valid on any server. Revoking the session of the request signs it out, and the answer clears its cookie. A session that is not an active session of the account is answered with 404 not_found. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed.
   */
  revokeSession: {
    input: {
      path: {
        /** The session's ID */
        session_id: string;
      };
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
    };
    responses: {
      204: undefined;
    };
  };
  /**
   * Change a workspace's settings
   *
   * Changes a workspace's name, slug, or default locale; a field that the body leaves out, or sets to null, stays as it is. Only an owner or an admin of the workspace may, and anyone else with a role in it is answered with 403 insufficient_role. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. A name or a default locale that its field does not allow is answered with 422 validation_failed. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed.
   */
  updateWorkspace: {
    input: {
      path: {
        /** The workspace's ID */
        workspace_id: string;
      };
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
      body: UpdateWorkspaceBody;
    };
    responses: {
      200: WorkspaceBody;
    };
  };
  /**
   * Verify an email address with a link
   *
   * Records that the email address of the account of a link that verifies it is verified, and uses the link, which works once, and only while the address is still the account's. Where the instance requires a verified address before a workspace can be created, as hosted mode does, it also gives the account its personal workspace, if it has had none; should that fail, the answer is still 204, because the address is verified, and the account's next sign-in gives the workspace. A token that does not exist, of another kind of link, or of an account that can no longer use it, is answered with 400 token_invalid, a link that has been used with 400 token_used, and one that has expired with 400 token_expired. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, and reading, accepting, or registering with an invitation's link share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, and accepting one share one of 10 a minute for each account. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  verifyEmail: {
    input: {
      body: VerifyEmailBody;
    };
    responses: {
      204: undefined;
    };
  };
}

/** The method and path of each operation. */
export const routes = {
  acceptInvite: { method: "POST", path: "/api/v1/invites/accept" },
  changePassword: { method: "POST", path: "/api/v1/auth/password" },
  completeSetup: { method: "POST", path: "/api/v1/setup" },
  createInvite: { method: "POST", path: "/api/v1/workspaces/{workspace_id}/invites" },
  createWorkspace: { method: "POST", path: "/api/v1/workspaces" },
  getHealth: { method: "GET", path: "/healthz" },
  getInstance: { method: "GET", path: "/api/v1/instance" },
  getOpenAPIDocument: { method: "GET", path: "/api/v1/openapi.json" },
  getReadiness: { method: "GET", path: "/readyz" },
  getSession: { method: "GET", path: "/api/v1/auth/session" },
  getSetup: { method: "GET", path: "/api/v1/setup" },
  getVersion: { method: "GET", path: "/version" },
  getWorkspace: { method: "GET", path: "/api/v1/workspaces/{workspace_id}" },
  issuePasswordResetLink: { method: "POST", path: "/api/v1/admin/password-reset-links" },
  issueVerificationLink: { method: "POST", path: "/api/v1/admin/verification-links" },
  listInvites: { method: "GET", path: "/api/v1/workspaces/{workspace_id}/invites" },
  listMembers: { method: "GET", path: "/api/v1/workspaces/{workspace_id}/members" },
  listSessions: { method: "GET", path: "/api/v1/auth/sessions" },
  listWorkspaces: { method: "GET", path: "/api/v1/workspaces" },
  login: { method: "POST", path: "/api/v1/auth/login" },
  logout: { method: "POST", path: "/api/v1/auth/logout" },
  lookupInvite: { method: "POST", path: "/api/v1/invites/lookup" },
  registerAccount: { method: "POST", path: "/api/v1/auth/register" },
  registerWithInvite: { method: "POST", path: "/api/v1/invites/register" },
  removeMember: { method: "DELETE", path: "/api/v1/workspaces/{workspace_id}/members/{membership_id}" },
  requestEmailVerification: { method: "POST", path: "/api/v1/auth/email-verification" },
  requestPasswordReset: { method: "POST", path: "/api/v1/auth/password-reset" },
  resendInvite: { method: "POST", path: "/api/v1/workspaces/{workspace_id}/invites/{invite_id}/resend" },
  resetPassword: { method: "POST", path: "/api/v1/auth/password-reset/confirm" },
  revokeInvite: { method: "DELETE", path: "/api/v1/workspaces/{workspace_id}/invites/{invite_id}" },
  revokeOtherSessions: { method: "DELETE", path: "/api/v1/auth/sessions" },
  revokeSession: { method: "DELETE", path: "/api/v1/auth/sessions/{session_id}" },
  updateWorkspace: { method: "PATCH", path: "/api/v1/workspaces/{workspace_id}" },
  verifyEmail: { method: "POST", path: "/api/v1/auth/verify-email" },
} as const satisfies Record<keyof Operations, { method: string; path: string }>;
