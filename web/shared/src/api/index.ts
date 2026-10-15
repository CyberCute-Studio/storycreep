// The client of the server's API, typed by the OpenAPI document (PRD
// FR-API-02). See client.ts.

import { type Client, type ClientOptions, createClient } from "./client";
import { type Operations, routes } from "./v1.gen";

export {
  type Answer,
  ApiError,
  type CallOptions,
  type Client,
  type ClientOptions,
  createClient,
} from "./client";
export * from "./v1.gen";

/** Returns a client of the server's API. */
export function createApiClient(options: ClientOptions): Client<Operations> {
  return createClient<Operations>(routes, options);
}
