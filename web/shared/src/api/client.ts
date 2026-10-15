// The API client (PRD FR-API-02). Its types are generated from the OpenAPI
// document into v1.gen.ts, so nothing here describes a request or a
// response by hand: a call names an operation of Operations, passes the
// input that the operation takes, and gets the status and body of the
// answer, typed by status. An error, which the server answers with the error
// envelope, is thrown as an ApiError.

import type { ErrorBody } from "./v1.gen";

/** A value of a parameter, which is sent as String writes it. */
type ParameterValue = string | number | boolean;

/** What an operation of a generated Operations interface has. */
export interface OperationShape {
  input: {
    path?: Record<string, ParameterValue>;
    query?: Record<string, ParameterValue | undefined>;
    headers?: Record<string, ParameterValue | undefined>;
    body?: unknown;
  };
  responses: Record<number, unknown>;
}

/** The method and path of an operation, as the generated routes hold them. */
export interface Route {
  readonly method: string;
  readonly path: string;
}

/**
 * The answer to a call of an operation: one of the statuses that it answers
 * with, other than an error's, with that status's body, and the response.
 */
export type Answer<Op extends OperationShape> = {
  [S in keyof Op["responses"] & number]: {
    status: S;
    body: Op["responses"][S];
    response: Response;
  };
}[keyof Op["responses"] & number];

export interface ClientOptions {
  /**
   * The server's URL, such as "https://storycreep.example.com", which the
   * operations' paths are appended to; "" for the page's own origin.
   */
  baseUrl: string;
  /** The fetch that calls the server; the global one if not set. */
  fetch?: typeof fetch;
}

export interface CallOptions {
  signal?: AbortSignal;
  /** Headers to send, besides the operation's header parameters. */
  headers?: HeadersInit;
}

/** The arguments of a call: the input, which may be left out if all of its parts are optional, and options. */
type CallArgs<Op extends OperationShape> =
  Record<string, never> extends Op["input"]
    ? [input?: Op["input"], options?: CallOptions]
    : [input: Op["input"], options?: CallOptions];

export interface Client<Ops extends Record<keyof Ops, OperationShape>> {
  /**
   * Calls an operation, and resolves to its answer, or rejects with an ApiError if the server answers with an
   * error, and with an Error if it answers with a body that is not JSON.
   */
  call<K extends keyof Ops>(
    operation: K,
    ...args: CallArgs<Ops[K]>
  ): Promise<Answer<Ops[K]>>;
}

/** An answer with a 4xx or 5xx status. */
export class ApiError extends Error {
  readonly status: number;
  /** The error envelope's object, or undefined if the body is not the envelope, as a proxy's error page is not. */
  readonly error: ErrorBody | undefined;
  /** The answer, whose body is unread, whatever it holds. */
  readonly response: Response;

  constructor(response: Response, error: ErrorBody | undefined) {
    super(
      error === undefined
        ? `The server answered with the status ${response.status}.`
        : `${error.code}: ${error.message}`,
    );
    this.name = "ApiError";
    this.status = response.status;
    this.error = error;
    this.response = response;
  }
}

/** Returns a client of the operations that routes locates. */
export function createClient<Ops extends Record<keyof Ops, OperationShape>>(
  routes: Record<keyof Ops, Route>,
  options: ClientOptions,
): Client<Ops> {
  const baseUrl = options.baseUrl.replace(/\/+$/, "");
  // The global fetch is looked up at each call, and called as a function of
  // the global object, as a browser requires.
  const doFetch =
    options.fetch ??
    ((input: RequestInfo | URL, init?: RequestInit) =>
      globalThis.fetch(input, init));
  return {
    async call(operation, ...[input, callOptions]) {
      const route = routes[operation];
      const parts = (input ?? {}) as OperationShape["input"];
      const headers = new Headers(callOptions?.headers);
      headers.set("Accept", "application/json");
      for (const [name, value] of Object.entries(parts.headers ?? {})) {
        if (value !== undefined) {
          headers.set(name, String(value));
        }
      }
      let body: string | undefined;
      if (parts.body !== undefined) {
        body = JSON.stringify(parts.body);
        headers.set("Content-Type", "application/json");
      }
      const url =
        baseUrl + fillPath(route.path, parts.path ?? {}) + search(parts.query);
      const response = await doFetch(url, {
        method: route.method,
        headers,
        body,
        signal: callOptions?.signal,
      });
      if (response.status >= 400) {
        throw new ApiError(response, await readEnvelope(response));
      }
      return {
        status: response.status,
        body: await readBody(response),
        response,
      } as Answer<Ops[typeof operation]>;
    },
  };
}

/**
 * Returns path with each {name} replaced by the parameter's value. It
 * refuses "", which leaves the segment empty, and "." and "..", which a URL
 * drops, with the segment before "..", even when they are percent-encoded:
 * each would make the URL name another path.
 */
function fillPath(
  path: string,
  params: Record<string, ParameterValue>,
): string {
  return path.replace(/\{([^{}]*)\}/g, (_, name: string) => {
    const value = params[name];
    if (value === undefined) {
      throw new TypeError(`The path parameter ${name} is missing.`);
    }
    const s = String(value);
    if (s === "" || s === "." || s === "..") {
      throw new TypeError(
        `The path parameter ${name} cannot be ${JSON.stringify(s)}.`,
      );
    }
    return encodeURIComponent(s);
  });
}

/** Returns the query string of the parameters that have values, with its "?". */
function search(
  query: Record<string, ParameterValue | undefined> | undefined,
): string {
  const params = new URLSearchParams();
  for (const [name, value] of Object.entries(query ?? {})) {
    if (value !== undefined) {
      params.append(name, String(value));
    }
  }
  const s = params.toString();
  return s === "" ? "" : `?${s}`;
}

function isJSON(response: Response): boolean {
  const type = (response.headers.get("Content-Type") ?? "")
    .split(";")[0]
    .trim()
    .toLowerCase();
  return type === "application/json" || type.endsWith("+json");
}

/**
 * Returns the JSON body of a response, or undefined if it has none. A body
 * that is not JSON, such as the page that a proxy or a development server
 * answers with for a path it does not pass on, is not an answer of the API,
 * so it is refused rather than taken for an answer without a body.
 */
async function readBody(response: Response): Promise<unknown> {
  const text = await response.text();
  if (text === "") {
    return undefined;
  }
  if (!isJSON(response)) {
    const type = response.headers.get("Content-Type") ?? "none";
    throw new Error(
      `The server answered with the status ${response.status} and a body that is not JSON (Content-Type: ${type}).`,
    );
  }
  return JSON.parse(text);
}

/**
 * Returns the error envelope's object, or undefined if the body is not the
 * envelope. The response's body stays unread, for the caller to read from
 * the ApiError, whatever it holds.
 */
async function readEnvelope(
  response: Response,
): Promise<ErrorBody | undefined> {
  if (!isJSON(response)) {
    return undefined;
  }
  try {
    const body = await readBody(response.clone());
    const error = (body as { error?: Partial<ErrorBody> } | undefined)?.error;
    if (
      typeof error?.code === "string" &&
      typeof error.message === "string" &&
      typeof error.request_id === "string"
    ) {
      return error as ErrorBody;
    }
  } catch {
    // A body that is not JSON is not the envelope.
  }
  return undefined;
}
