// Code that the lint rule in ../direct-request.grit leaves alone
// (lint.test.ts): the API client's calls, the types of the browser's
// requests, and names that only resemble theirs.

import { createApiClient } from "@storycreep/shared/api";

const api = createApiClient({ baseUrl: "" });

export async function version(socket: WebSocket, request: XMLHttpRequest) {
  const answer = await api.call("getVersion");
  const query = { refetch: () => answer, fetchQuery: () => answer };
  return [
    socket.readyState,
    request.status,
    query.refetch(),
    query.fetchQuery(),
  ];
}
