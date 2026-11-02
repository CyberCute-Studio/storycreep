// Requests made around the API client, each of which the lint rule in
// ../direct-request.grit reports, on the lines 9 to 33 (lint.test.ts).

export function requests(url: string): unknown[] {
  const made: unknown[] = [];
  const g = globalThis as unknown as Record<string, () => unknown>;
  const send = (value: unknown) => made.push(value);
  // The globals, called and named, and in an object's shorthand.
  send(fetch(url));
  send(new XMLHttpRequest());
  send(new EventSource(url));
  send(new WebSocket(url));
  send(fetch);
  send({ fetch, x: 1 });
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
  // In shorthand, whose names are reported where they are used too, in a
  // block of their own, where they do not hide the globals above.
  {
    const { EventSource } = window;
    let WebSocket: unknown = null;
    ({ WebSocket } = window);
    send(EventSource);
    send(WebSocket);
  }
  return made;
}
