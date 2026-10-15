// Generated from server/internal/apicontract/testdata/base.json. Do not
// edit; run "go test ./internal/apicontract -run TestTypeScript -update" in
// server/ instead.

export interface ErrorBody {
  code: string;
  details?: Record<string, unknown>;
  message: string;
  request_id: string;
}

export interface ErrorEnvelope {
  error: ErrorBody;
}

/** A thing, as the API answers with it. */
export interface Thing {
  counts?: Record<string, number>;
  created_at: string;
  /** The thing's identifier */
  id: string;
  kind: "note" | "choice";
  name: string;
  note: string | null;
  /** @deprecated */
  "old-name"?: string;
  parent?: Thing;
  tags: Array<string>;
}

export interface ThingInput {
  kind?: "note" | "choice";
  name: string;
  note?: string | null;
  size?: number;
}

export interface ThingPage {
  items: Array<Thing>;
  next_cursor: string | null;
}

/** The API's operations, by operation ID: what each takes, and what it answers with each status that is not an error's. */
export interface Operations {
  /**
   * Create a thing
   *
   * Answers with the thing.
   *
   * Its *\/ is escaped.
   */
  createThing: {
    input: {
      body: ThingInput;
    };
    responses: {
      201: Thing;
    };
  };
  /** @deprecated */
  deleteThing: {
    input: {
      path: {
        id: string;
      };
    };
    responses: {
      204: undefined;
    };
  };
  /** Get a thing */
  getThing: {
    input: {
      path: {
        id: string;
      };
      headers?: {
        "If-None-Match"?: string;
      };
    };
    responses: {
      200: Thing;
      304: undefined;
    };
  };
  /** List the things */
  listThings: {
    input: {
      query?: {
        archived?: boolean;
        /** The next_cursor of the previous page */
        cursor?: string;
        kind?: "note" | "choice";
        limit?: number;
      };
    };
    responses: {
      200: ThingPage;
    };
  };
}

/** The method and path of each operation. */
export const routes = {
  createThing: { method: "POST", path: "/things" },
  deleteThing: { method: "DELETE", path: "/things/{id}" },
  getThing: { method: "GET", path: "/things/{id}" },
  listThings: { method: "GET", path: "/things" },
} as const satisfies Record<keyof Operations, { method: string; path: string }>;
