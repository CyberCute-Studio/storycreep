// Requests made around the API client, each of which the lint rule in
// ../direct-request.grit reports, on the lines 9 to 23 (lint.test.ts).

export function requests(url: string): unknown[] {
  const made: unknown[] = [];
  const g = globalThis as unknown as Record<string, () => unknown>;
  const send = (value: unknown) => made.push(value);
  // The globals, called and named.
  send(fetch(url));
  send(new XMLHttpRequest());
  send(new EventSource(url));
  send(new WebSocket(url));
  send(fetch);
  // The same as properties.
  send(window.fetch(url));
  send(navigator.sendBeacon(url, "{}"));
  send(globalThis.XMLHttpRequest);
  // biome-ignore lint/complexity/useLiteralKeys: the rule is tested with a computed key
  send(g["fetch"]);
  // The same as properties that are destructured.
  const { fetch: get } = window;
  const into: { beacon?: unknown } = {};
  ({ sendBeacon: into.beacon } = navigator);
  send([get, into]);
  return made;
}
