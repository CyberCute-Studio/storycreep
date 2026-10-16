// Generated from schema/api/v1/openapi.json. Do not edit; run
// "go test ./internal/httpserver -run TestOpenAPIDocument -update" in server/
// instead.

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

export interface LoginBody {
  /** The account's email address, in any case */
  email: string;
  /** The account's password */
  password: string;
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

export interface RegisterBody {
  /** The name that other people see, of 1 to 100 characters, not all of them blank, and without control characters, bidirectional controls and line separators among them. White space around it is removed. */
  display_name: string;
  /** The account's email address, which signs in: an address alone, as jo@example.com, of at most 254 bytes, without spaces or characters that are not printed. Addresses compare without regard to case or to how their characters are composed. */
  email: string;
  /** The password: at least 12 characters, and not one of the most common passwords, or the answer is weak_password. */
  password: string;
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

/** The API's operations, by operation ID: what each takes, and what it answers with each status that is not an error's. */
export interface Operations {
  /**
   * Change the account's password
   *
   * Changes the signed-in account's password, if current_password is the account's, or the answer is 403 wrong_password. The new password must have at least 12 characters, counted as Unicode code points once it is in NFKC, and must not be one of the 10,000 most common passwords, or the answer is 422 weak_password. Changing it revokes every other session of the account, and gives the session of the request a new secret, in the Set-Cookie header, and a new CSRF token, in the body, so that the old ones are no longer valid. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Signing in, registering, and changing a password share a rate limit of 20 requests a minute for each IP address. Registering also has one of 10 a minute for each email address, and changing a password one of 10 a minute for each account. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
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
  /** Report that the process is alive */
  getHealth: {
    input: Record<string, never>;
    responses: {
      200: StatusBody;
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
  /** Describe the server's build */
  getVersion: {
    input: Record<string, never>;
    responses: {
      200: VersionBody;
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
   * Sign in
   *
   * Signs in with an account's email address and password, and starts a session, whose secret the Set-Cookie header gives the client, and whose CSRF token the body holds. An address of no account, a wrong password, and an account that cannot sign in, such as a suspended one, are all answered with 401 invalid_credentials, after the same work, so that the answer does not tell them apart. A request that a browser's Sec-Fetch-Site header says comes from another site, or from another origin of the same site, is answered with 403 csrf_failed; browsers send that header only to https URLs and to loopback addresses, such as localhost. After 10 failed sign-ins in a row with an email address, whether or not it belongs to an account, the address is locked for a minute, and each further failure, once a lock ends, locks it for twice as long as the lock before, up to 15 minutes: while it is locked, every sign-in with it is answered with 429 rate_limited, whose details.reason is locked, even with the right password. A successful sign-in forgets the failures, and so do 24 hours without one. Signing in, registering, and changing a password share a rate limit of 20 requests a minute for each IP address. Registering also has one of 10 a minute for each email address, and changing a password one of 10 a minute for each account. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
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
   * Register an account
   *
   * Creates an account with an email address, a display name, and a password, if the instance's signup policy, STORYCREEP_SIGNUP, lets anyone register. Under closed the answer is 403 signup_closed, and under invite 403 invite_required, because only an invitation creates an account then; either comes whatever the request holds. An email address or a display name that its field does not allow is answered with 422 validation_failed. The password must have at least 12 characters, counted as Unicode code points once it is in NFKC, and must not be one of the 10,000 most common passwords, compared without regard to case, or the answer is 422 weak_password. Otherwise the answer is 202 without a body, whether or not the address has an account already, which then stays as it was, so that the answer does not tell whether an address has an account. Signing in, registering, and changing a password share a rate limit of 20 requests a minute for each IP address. Registering also has one of 10 a minute for each email address, and changing a password one of 10 a minute for each account. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
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
}

/** The method and path of each operation. */
export const routes = {
  changePassword: { method: "POST", path: "/api/v1/auth/password" },
  getHealth: { method: "GET", path: "/healthz" },
  getOpenAPIDocument: { method: "GET", path: "/api/v1/openapi.json" },
  getReadiness: { method: "GET", path: "/readyz" },
  getSession: { method: "GET", path: "/api/v1/auth/session" },
  getVersion: { method: "GET", path: "/version" },
  listSessions: { method: "GET", path: "/api/v1/auth/sessions" },
  login: { method: "POST", path: "/api/v1/auth/login" },
  logout: { method: "POST", path: "/api/v1/auth/logout" },
  registerAccount: { method: "POST", path: "/api/v1/auth/register" },
  revokeOtherSessions: { method: "DELETE", path: "/api/v1/auth/sessions" },
  revokeSession: { method: "DELETE", path: "/api/v1/auth/sessions/{session_id}" },
} as const satisfies Record<keyof Operations, { method: string; path: string }>;
