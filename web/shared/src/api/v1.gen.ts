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
  /** Describe the server's build */
  getVersion: {
    input: Record<string, never>;
    responses: {
      200: VersionBody;
    };
  };
}

/** The method and path of each operation. */
export const routes = {
  getHealth: { method: "GET", path: "/healthz" },
  getOpenAPIDocument: { method: "GET", path: "/api/v1/openapi.json" },
  getReadiness: { method: "GET", path: "/readyz" },
  getVersion: { method: "GET", path: "/version" },
} as const satisfies Record<keyof Operations, { method: string; path: string }>;
