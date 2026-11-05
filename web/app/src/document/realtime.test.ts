import type {
  OperationMessage,
  ServerMessage,
} from "@storycreep/shared/realtime";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createErrorLog } from "../errors/log";
import {
  backoff,
  backoffMax,
  createRealtime,
  lingerTimeout,
  type RealtimeOptions,
  type SocketLike,
  type Subscriber,
  silenceTimeout,
  socketURL,
} from "./realtime";

const url = "wss://storycreep.example/api/v1/ws";

/** A socket that the test plays the server of. */
class FakeSocket implements SocketLike {
  readyState = 0;
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  /** What the channel sent, each message parsed. */
  readonly sent: unknown[] = [];
  /** Whether the channel closed the socket. */
  closed = false;

  constructor(readonly url: string) {}

  send(data: string) {
    this.sent.push(JSON.parse(data));
  }

  close() {
    this.closed = true;
    this.readyState = 3;
  }

  /** The server accepts the socket. */
  open() {
    this.readyState = 1;
    this.onopen?.(new Event("open"));
  }

  /** The server sends a message, or text that is not one. */
  deliver(message: ServerMessage | string) {
    const data =
      typeof message === "string" ? message : JSON.stringify(message);
    this.onmessage?.(new MessageEvent("message", { data }));
  }

  /** The connection ends, as the server or the network ends it. */
  drop(code = 1006) {
    this.readyState = 3;
    this.onclose?.(new CloseEvent("close", { code }));
  }
}

/** A store's subscriber, which records what it hears, in order. */
interface FakeStore extends Subscriber {
  /** The seq of the latest operation that the store has, which since returns. */
  seq: number;
  heard: unknown[][];
  operations: OperationMessage[];
}

function fakeStore(seq = 0): FakeStore {
  const store: FakeStore = {
    seq,
    heard: [],
    operations: [],
    since: () => store.seq,
    subscribed(seq) {
      store.heard.push(["subscribed", seq]);
    },
    operation(message) {
      store.seq = message.seq;
      store.operations.push(message);
      store.heard.push(["operation", message.seq]);
    },
    resync(seq) {
      store.heard.push(["resync", seq]);
    },
    gone() {
      store.heard.push(["gone"]);
    },
    live(live) {
      store.heard.push(["live", live]);
    },
  };
  return store;
}

function operation(projectId: string, seq: number): OperationMessage {
  return {
    type: "operation",
    project_id: projectId,
    seq,
    commands: [
      { command_id: `cmd_${seq}`, type: "node.update", payload: { seq } },
    ],
    revisions: [{ id: "nd_1", rev: seq }],
    created_at: "2026-11-05T12:00:00Z",
  };
}

function setup(options: Partial<RealtimeOptions> = {}) {
  const sockets: FakeSocket[] = [];
  const refusals: unknown[] = [];
  const log = createErrorLog();
  const browser = new EventTarget();
  const addListener = vi.spyOn(browser, "addEventListener");
  const removeListener = vi.spyOn(browser, "removeEventListener");
  const session = { signedIn: true };
  const checkSession = vi.fn(() => Promise.resolve());
  const realtime = createRealtime({
    url: () => url,
    connect(u) {
      if (refusals.length > 0) {
        throw refusals.shift();
      }
      const socket = new FakeSocket(u);
      sockets.push(socket);
      return socket;
    },
    checkSession,
    signedIn: () => session.signedIn,
    log,
    random: () => 1,
    window: browser,
    ...options,
  });
  /** Returns the socket that the channel opened last. */
  const socket = () => {
    const last = sockets.at(-1);
    if (last === undefined) {
      throw new Error("The channel has opened no socket.");
    }
    return last;
  };
  /** Makes the next connect throw error. */
  const refuse = (error: unknown) => {
    refusals.push(error);
  };
  const online = () => browser.dispatchEvent(new Event("online"));
  return {
    realtime,
    sockets,
    socket,
    refuse,
    log,
    online,
    addListener,
    removeListener,
    session,
    checkSession,
  };
}

type Harness = ReturnType<typeof setup>;

/**
 * Drops the channel's socket again and again, and expects it to connect again
 * after each of waits, in milliseconds, and not a millisecond before.
 */
function expectWaits(h: Harness, waits: number[]) {
  for (const wait of waits) {
    const count = h.sockets.length;
    h.socket().drop();
    vi.advanceTimersByTime(wait - 1);
    expect(h.sockets).toHaveLength(count);
    vi.advanceTimersByTime(1);
    expect(h.sockets).toHaveLength(count + 1);
  }
}

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("socketURL", () => {
  it("names the socket of the page's server, over wss for a page served over https and ws for one over http", () => {
    expect(socketURL({ protocol: "https:", host: "storycreep.example" })).toBe(
      "wss://storycreep.example/api/v1/ws",
    );
    expect(socketURL({ protocol: "http:", host: "192.168.1.5:8080" })).toBe(
      "ws://192.168.1.5:8080/api/v1/ws",
    );
  });
});

describe("backoff", () => {
  it("waits up to twice as long after each failure in a row, and at most 30 seconds", () => {
    const most = () => 1;
    expect([1, 2, 3, 4, 5, 6].map((n) => backoff(n, most))).toEqual([
      2_000, 4_000, 8_000, 16_000, 30_000, 30_000,
    ]);
    expect(backoff(1_000, most)).toBe(backoffMax);
  });

  it("waits a random part of that time, from none of it to all of it (full jitter)", () => {
    const half = () => 0.5;
    expect([1, 2, 3, 4, 5].map((n) => backoff(n, half))).toEqual([
      1_000, 2_000, 4_000, 8_000, 15_000,
    ]);
    expect(backoff(3, () => 0)).toBe(0);
  });
});

describe("createRealtime", () => {
  it("connects only once a project is subscribed to, with one socket for every project", () => {
    const h = setup();
    expect(h.sockets).toHaveLength(0);
    h.realtime.subscribe("prj_a", fakeStore());
    h.realtime.subscribe("prj_b", fakeStore());
    expect(h.sockets.map((s) => s.url)).toEqual([url]);
  });

  it("subscribes to every project once the socket opens, from the seq that each store has then", () => {
    const h = setup();
    const a = fakeStore(3);
    h.realtime.subscribe("prj_a", a);
    h.realtime.subscribe("prj_b", fakeStore());
    expect(h.socket().sent).toEqual([]);
    // The store read more of its project while the socket connected.
    a.seq = 7;
    h.socket().open();
    expect(h.socket().sent).toEqual([
      { type: "subscribe", project_id: "prj_a", since: 7 },
      { type: "subscribe", project_id: "prj_b", since: 0 },
    ]);
  });

  it("subscribes to a project at once while the socket is open", () => {
    const h = setup();
    h.realtime.subscribe("prj_a", fakeStore());
    h.socket().open();
    h.realtime.subscribe("prj_b", fakeStore(12));
    expect(h.socket().sent).toEqual([
      { type: "subscribe", project_id: "prj_a", since: 0 },
      { type: "subscribe", project_id: "prj_b", since: 12 },
    ]);
  });

  it("tells a store that its subscription holds, and then that it is live", () => {
    const h = setup();
    const a = fakeStore(5);
    h.realtime.subscribe("prj_a", a);
    h.socket().open();
    h.socket().deliver({ type: "subscribed", project_id: "prj_a", seq: 8 });
    expect(a.heard).toEqual([
      ["subscribed", 8],
      ["live", true],
    ]);
  });

  it("passes each operation to the store of its project", () => {
    const h = setup();
    const a = fakeStore();
    const b = fakeStore();
    h.realtime.subscribe("prj_a", a);
    h.realtime.subscribe("prj_b", b);
    h.socket().open();
    h.socket().deliver(operation("prj_a", 1));
    h.socket().deliver(operation("prj_b", 4));
    expect(a.operations).toEqual([operation("prj_a", 1)]);
    expect(b.operations).toEqual([operation("prj_b", 4)]);
  });

  it("tells a store to read its project again when the operations that it missed cannot be sent, and then that it is live", () => {
    const h = setup();
    const a = fakeStore(5);
    h.realtime.subscribe("prj_a", a);
    h.socket().open();
    h.socket().deliver({
      type: "resync_required",
      project_id: "prj_a",
      seq: 20_000,
    });
    expect(a.heard).toEqual([
      ["resync", 20_000],
      ["live", true],
    ]);
  });

  it("tells a store that its project is gone once the server ends the subscription because the project can no longer be read", () => {
    const h = setup();
    const a = fakeStore();
    h.realtime.subscribe("prj_a", a);
    h.socket().open();
    h.socket().deliver({ type: "subscribed", project_id: "prj_a", seq: 0 });
    a.heard = [];
    h.socket().deliver({
      type: "unsubscribed",
      project_id: "prj_a",
      reason: "not_found",
    });
    expect(a.heard).toEqual([["live", false], ["gone"]]);
    // The subscription no longer holds, so losing the socket tells it nothing.
    h.socket().drop();
    expect(a.heard).toEqual([["live", false], ["gone"]]);
  });

  it("ignores the end of a subscription that the page asked for, as the answer to a store that left before another came", () => {
    const h = setup();
    const stop = h.realtime.subscribe("prj_a", fakeStore());
    h.socket().open();
    h.socket().deliver({ type: "subscribed", project_id: "prj_a", seq: 0 });
    stop();
    const next = fakeStore(2);
    h.realtime.subscribe("prj_a", next);
    h.socket().deliver({
      type: "unsubscribed",
      project_id: "prj_a",
      reason: "requested",
    });
    expect(next.heard).toEqual([]);
    h.socket().deliver({ type: "subscribed", project_id: "prj_a", seq: 2 });
    expect(next.heard).toEqual([
      ["subscribed", 2],
      ["live", true],
    ]);
  });

  it("tells a store that its project is gone once the server refuses the subscription as not found", () => {
    const h = setup();
    const a = fakeStore();
    h.realtime.subscribe("prj_a", a);
    h.socket().open();
    h.socket().deliver({
      type: "subscription_failed",
      project_id: "prj_a",
      code: "not_found",
    });
    expect(a.heard).toEqual([["live", false], ["gone"]]);
  });

  it("subscribes again after the wait that the rate limit's refusal names, from the seq that the store has then", () => {
    const h = setup();
    const a = fakeStore(4);
    h.realtime.subscribe("prj_a", a);
    h.socket().open();
    h.socket().deliver({
      type: "subscription_failed",
      project_id: "prj_a",
      code: "rate_limited",
      retry_after: 3,
    });
    a.seq = 6;
    vi.advanceTimersByTime(2_999);
    expect(h.socket().sent).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(h.socket().sent).toEqual([
      { type: "subscribe", project_id: "prj_a", since: 4 },
      { type: "subscribe", project_id: "prj_a", since: 6 },
    ]);
    expect(a.heard).toEqual([]);
  });

  it("subscribes again after a second when the rate limit's refusal names no wait", () => {
    const h = setup();
    h.realtime.subscribe("prj_a", fakeStore());
    h.socket().open();
    h.socket().deliver({
      type: "subscription_failed",
      project_id: "prj_a",
      code: "rate_limited",
    });
    vi.advanceTimersByTime(999);
    expect(h.socket().sent).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(h.socket().sent).toHaveLength(2);
  });

  it("records a refusal of an ID that is not a project's, of more subscriptions than a socket may hold, and of a message, in the error log", () => {
    const h = setup();
    const a = fakeStore();
    h.realtime.subscribe("prj_a", a);
    h.socket().open();
    h.socket().deliver({
      type: "subscription_failed",
      project_id: "prj_a",
      code: "invalid_id",
    });
    h.socket().deliver({
      type: "subscription_failed",
      project_id: "prj_a",
      code: "too_many_subscriptions",
    });
    h.socket().deliver({ type: "error", code: "invalid_message" });
    expect(h.log.entries().map((e) => [e.source, e.message])).toEqual([
      [
        "error",
        "The real-time channel refused the subscription of prj_a: invalid_id",
      ],
      [
        "error",
        "The real-time channel refused the subscription of prj_a: too_many_subscriptions",
      ],
      ["error", "The real-time channel refused a message: invalid_message"],
    ]);
    expect(a.heard).toEqual([]);
  });

  it("records a message that is not JSON in the error log", () => {
    const h = setup();
    h.realtime.subscribe("prj_a", fakeStore());
    h.socket().open();
    h.socket().deliver("{not json");
    expect(h.log.entries()).toMatchObject([
      { source: "error", name: "SyntaxError" },
    ]);
  });

  it("ignores heartbeats, and the messages of a project that no store subscribes to", () => {
    const h = setup();
    const a = fakeStore();
    h.realtime.subscribe("prj_a", a);
    h.socket().open();
    h.socket().deliver({ type: "heartbeat" });
    h.socket().deliver({ type: "subscribed", project_id: "prj_b", seq: 3 });
    h.socket().deliver(operation("prj_b", 4));
    h.socket().deliver({
      type: "subscription_failed",
      project_id: "prj_b",
      code: "invalid_id",
    });
    expect(a.heard).toEqual([]);
    expect(h.log.entries()).toEqual([]);
  });

  // Verifies: FR-RT-01/AC3
  it("subscribes again from the latest seq that the store has once a dropped connection is back, so that no committed operation is lost", () => {
    const h = setup();
    const a = fakeStore(10);
    h.realtime.subscribe("prj_a", a);
    h.socket().open();
    h.socket().deliver({ type: "subscribed", project_id: "prj_a", seq: 10 });
    h.socket().deliver(operation("prj_a", 11));
    h.socket().deliver(operation("prj_a", 12));
    h.socket().drop();
    expect(a.heard.at(-1)).toEqual(["live", false]);
    vi.advanceTimersByTime(2_000);
    expect(h.sockets).toHaveLength(2);
    h.socket().open();
    expect(h.socket().sent).toEqual([
      { type: "subscribe", project_id: "prj_a", since: 12 },
    ]);
    // The operations that committed while the connection was down follow.
    h.socket().deliver({ type: "subscribed", project_id: "prj_a", seq: 14 });
    h.socket().deliver(operation("prj_a", 13));
    h.socket().deliver(operation("prj_a", 14));
    expect(a.operations.map((o) => o.seq)).toEqual([11, 12, 13, 14]);
    expect(a.heard.slice(-4)).toEqual([
      ["subscribed", 14],
      ["live", true],
      ["operation", 13],
      ["operation", 14],
    ]);
  });

  it("tells the stores whose subscription held that they are not live once the socket is lost", () => {
    const h = setup();
    const a = fakeStore();
    const b = fakeStore();
    h.realtime.subscribe("prj_a", a);
    h.realtime.subscribe("prj_b", b);
    h.socket().open();
    h.socket().deliver({ type: "subscribed", project_id: "prj_a", seq: 0 });
    h.socket().drop();
    expect(a.heard).toEqual([
      ["subscribed", 0],
      ["live", true],
      ["live", false],
    ]);
    expect(b.heard).toEqual([]);
    expect(h.sockets[0]?.closed).toBe(true);
  });

  it("connects again after each loss in a row with a wait that doubles, up to 30 seconds", () => {
    const h = setup();
    h.realtime.subscribe("prj_a", fakeStore());
    expectWaits(h, [2_000, 4_000, 8_000, 16_000, 30_000, 30_000]);
  });

  it("waits a random part of each doubled wait before connecting again (full jitter)", () => {
    const h = setup({ random: () => 0.5 });
    h.realtime.subscribe("prj_a", fakeStore());
    expectWaits(h, [1_000, 2_000, 4_000]);
  });

  it("waits from the start again after a loss once a subscription has held", () => {
    const h = setup();
    h.realtime.subscribe("prj_a", fakeStore());
    expectWaits(h, [2_000, 4_000]);
    h.socket().open();
    h.socket().deliver({ type: "subscribed", project_id: "prj_a", seq: 0 });
    expectWaits(h, [2_000]);
  });

  it("waits from the start again after a loss once a subscription has held after its store was told to read its project again", () => {
    const h = setup();
    h.realtime.subscribe("prj_a", fakeStore());
    expectWaits(h, [2_000, 4_000]);
    h.socket().open();
    h.socket().deliver({
      type: "resync_required",
      project_id: "prj_a",
      seq: 3,
    });
    expectWaits(h, [2_000]);
  });

  it("records a socket that cannot be opened in the error log, and tries again later", () => {
    const h = setup();
    h.refuse(new SyntaxError("The URL is not one of a WebSocket."));
    h.realtime.subscribe("prj_a", fakeStore());
    expect(h.sockets).toHaveLength(0);
    expect(h.log.entries()).toMatchObject([
      {
        source: "error",
        name: "SyntaxError",
        message: "The URL is not one of a WebSocket.",
      },
    ]);
    vi.advanceTimersByTime(1_999);
    expect(h.sockets).toHaveLength(0);
    vi.advanceTimersByTime(1);
    expect(h.sockets).toHaveLength(1);
  });

  it("takes a socket that has heard nothing for a minute for lost, closes it, and connects again", () => {
    const h = setup();
    const a = fakeStore();
    h.realtime.subscribe("prj_a", a);
    h.socket().open();
    h.socket().deliver({ type: "subscribed", project_id: "prj_a", seq: 0 });
    vi.advanceTimersByTime(silenceTimeout - 1);
    expect(h.sockets[0]?.closed).toBe(false);
    vi.advanceTimersByTime(1);
    expect(h.sockets[0]?.closed).toBe(true);
    expect(a.heard.at(-1)).toEqual(["live", false]);
    // What the socket that it gave up on says after is not heard.
    h.sockets[0]?.deliver(operation("prj_a", 1));
    expect(a.operations).toEqual([]);
    vi.advanceTimersByTime(2_000);
    expect(h.sockets).toHaveLength(2);
  });

  it("takes a socket that has not opened within a minute for lost", () => {
    const h = setup();
    h.realtime.subscribe("prj_a", fakeStore());
    vi.advanceTimersByTime(silenceTimeout);
    expect(h.sockets[0]?.closed).toBe(true);
    vi.advanceTimersByTime(2_000);
    expect(h.sockets).toHaveLength(2);
  });

  it("waits a minute from each message that the socket hears, a heartbeat too, before taking it for lost", () => {
    const h = setup();
    h.realtime.subscribe("prj_a", fakeStore());
    h.socket().open();
    vi.advanceTimersByTime(30_000);
    h.socket().deliver({ type: "subscribed", project_id: "prj_a", seq: 0 });
    vi.advanceTimersByTime(50_000);
    h.socket().deliver({ type: "heartbeat" });
    vi.advanceTimersByTime(silenceTimeout - 1);
    expect(h.socket().closed).toBe(false);
    vi.advanceTimersByTime(1);
    expect(h.socket().closed).toBe(true);
  });

  it("asks for the session once the socket is closed with 4001, and connects again once it is answered while the page is signed in", async () => {
    const h = setup();
    const answer = Promise.withResolvers<void>();
    h.checkSession.mockReturnValue(answer.promise);
    h.realtime.subscribe("prj_a", fakeStore());
    h.socket().open();
    h.socket().drop(4001);
    expect(h.checkSession).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(10_000);
    h.online();
    expect(h.sockets).toHaveLength(1);
    answer.resolve();
    await vi.advanceTimersByTimeAsync(1_999);
    expect(h.sockets).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(h.sockets).toHaveLength(2);
  });

  it("does not connect again after 4001 once the question of the session has signed the page out", async () => {
    const h = setup();
    h.checkSession.mockImplementation(async () => {
      h.session.signedIn = false;
    });
    const a = fakeStore();
    h.realtime.subscribe("prj_a", a);
    h.socket().open();
    h.socket().deliver({ type: "subscribed", project_id: "prj_a", seq: 0 });
    h.socket().drop(4001);
    await vi.advanceTimersByTimeAsync(backoffMax * 4);
    h.online();
    expect(h.checkSession).toHaveBeenCalledTimes(1);
    expect(h.sockets).toHaveLength(1);
    expect(a.heard.at(-1)).toEqual(["live", false]);
  });

  it("connects again after 4001 when the question of the session fails, as without a connection", async () => {
    const h = setup();
    h.checkSession.mockRejectedValue(new TypeError("Failed to fetch"));
    h.realtime.subscribe("prj_a", fakeStore());
    h.socket().open();
    h.socket().drop(4001);
    await vi.advanceTimersByTimeAsync(2_000);
    expect(h.sockets).toHaveLength(2);
  });

  it("connects again at once when the browser says that it is back online while the channel waits to, and from the start of its backoff", () => {
    const h = setup();
    h.realtime.subscribe("prj_a", fakeStore());
    h.socket().open();
    h.online();
    expect(h.sockets).toHaveLength(1);
    expectWaits(h, [2_000]);
    h.socket().drop();
    vi.advanceTimersByTime(1_000);
    h.online();
    expect(h.sockets).toHaveLength(3);
    // The wait that it cut short connects nothing more.
    vi.advanceTimersByTime(3_000);
    expect(h.sockets).toHaveLength(3);
    expectWaits(h, [2_000]);
  });

  it("sends an unsubscription when a store leaves, and keeps the socket for the others", () => {
    const h = setup();
    const a = fakeStore();
    const stop = h.realtime.subscribe("prj_a", a);
    h.realtime.subscribe("prj_b", fakeStore());
    h.socket().open();
    stop();
    expect(h.socket().sent.at(-1)).toEqual({
      type: "unsubscribe",
      project_id: "prj_a",
    });
    h.socket().deliver(operation("prj_a", 1));
    expect(a.operations).toEqual([]);
    vi.advanceTimersByTime(lingerTimeout * 2);
    expect(h.socket().closed).toBe(false);
  });

  it("closes the socket once no store has needed it for five seconds, and does not connect again", () => {
    const h = setup();
    const stop = h.realtime.subscribe("prj_a", fakeStore());
    h.socket().open();
    stop();
    vi.advanceTimersByTime(lingerTimeout - 1);
    expect(h.socket().closed).toBe(false);
    vi.advanceTimersByTime(1);
    expect(h.socket().closed).toBe(true);
    vi.advanceTimersByTime(backoffMax * 4);
    expect(h.sockets).toHaveLength(1);
  });

  it("keeps the socket when another project is subscribed to within five seconds of the last store leaving", () => {
    const h = setup();
    const stop = h.realtime.subscribe("prj_a", fakeStore());
    h.socket().open();
    stop();
    vi.advanceTimersByTime(lingerTimeout - 2_000);
    h.realtime.subscribe("prj_b", fakeStore(9));
    expect(h.socket().sent.at(-1)).toEqual({
      type: "subscribe",
      project_id: "prj_b",
      since: 9,
    });
    vi.advanceTimersByTime(lingerTimeout * 2);
    expect(h.socket().closed).toBe(false);
    expect(h.sockets).toHaveLength(1);
  });

  it("does nothing when a store that another has replaced leaves", () => {
    const h = setup();
    const first = fakeStore();
    const stopFirst = h.realtime.subscribe("prj_a", first);
    h.socket().open();
    const second = fakeStore(3);
    const stopSecond = h.realtime.subscribe("prj_a", second);
    expect(h.socket().sent).toEqual([
      { type: "subscribe", project_id: "prj_a", since: 0 },
      { type: "subscribe", project_id: "prj_a", since: 3 },
    ]);
    stopFirst();
    expect(h.socket().sent).toHaveLength(2);
    h.socket().deliver(operation("prj_a", 4));
    expect(first.operations).toEqual([]);
    expect(second.operations).toEqual([operation("prj_a", 4)]);
    vi.advanceTimersByTime(lingerTimeout * 2);
    expect(h.socket().closed).toBe(false);
    stopSecond();
    expect(h.socket().sent.at(-1)).toEqual({
      type: "unsubscribe",
      project_id: "prj_a",
    });
  });

  it("does not subscribe again after the rate limit's wait to a project whose store has left", () => {
    const h = setup();
    const stop = h.realtime.subscribe("prj_a", fakeStore());
    h.realtime.subscribe("prj_b", fakeStore());
    h.socket().open();
    h.socket().deliver({
      type: "subscription_failed",
      project_id: "prj_a",
      code: "rate_limited",
      retry_after: 2,
    });
    stop();
    const sent = h.socket().sent.length;
    vi.advanceTimersByTime(3_000);
    expect(h.socket().sent).toHaveLength(sent);
  });

  it("does not connect again once the last store has left while the channel waits to", () => {
    const h = setup();
    const stop = h.realtime.subscribe("prj_a", fakeStore());
    h.socket().drop();
    stop();
    vi.advanceTimersByTime(backoffMax * 4);
    expect(h.sockets).toHaveLength(1);
  });

  it("closes the socket at once once asked to connect anew, and opens another for the next subscription, rather than one that went with the session before", () => {
    const h = setup();
    const a = fakeStore();
    const stop = h.realtime.subscribe("prj_a", a);
    h.socket().open();
    h.socket().deliver({ type: "subscribed", project_id: "prj_a", seq: 0 });
    stop();
    h.realtime.reconnect();
    expect(h.sockets[0].closed).toBe(true);
    expect(h.sockets).toHaveLength(1);
    h.realtime.subscribe("prj_b", fakeStore(4));
    expect(h.sockets).toHaveLength(2);
    h.socket().open();
    expect(h.socket().sent).toEqual([
      { type: "subscribe", project_id: "prj_b", since: 4 },
    ]);
  });

  it("opens a socket anew at once for the projects still subscribed to once asked to connect anew, and from the start of its backoff", () => {
    const h = setup();
    const a = fakeStore(2);
    h.realtime.subscribe("prj_a", a);
    h.socket().drop();
    vi.advanceTimersByTime(2_000);
    h.socket().drop();
    h.realtime.reconnect();
    expect(h.sockets).toHaveLength(3);
    h.socket().open();
    expect(h.socket().sent).toEqual([
      { type: "subscribe", project_id: "prj_a", since: 2 },
    ]);
    // The wait that it cut short connects nothing more.
    vi.advanceTimersByTime(backoffMax * 2);
    expect(h.sockets).toHaveLength(3);
    expectWaits(h, [2_000]);
  });

  it("closes for good: the socket, its waits to connect again, and its listener of the browser's online event", () => {
    const h = setup();
    const a = fakeStore();
    h.realtime.subscribe("prj_a", a);
    h.socket().open();
    h.socket().deliver({ type: "subscribed", project_id: "prj_a", seq: 0 });
    h.realtime.close();
    expect(h.socket().closed).toBe(true);
    expect(a.heard.at(-1)).toEqual(["live", false]);
    vi.advanceTimersByTime(backoffMax * 4);
    h.online();
    h.realtime.subscribe("prj_b", fakeStore());
    expect(h.sockets).toHaveLength(1);
    const listener = h.addListener.mock.calls[0]?.[1];
    expect(listener).toBeTypeOf("function");
    expect(h.removeListener).toHaveBeenCalledWith("online", listener);
  });

  it("does not connect again once closed while it waits to, or while it asks for the session", async () => {
    const h = setup();
    h.realtime.subscribe("prj_a", fakeStore());
    h.socket().drop();
    h.realtime.close();
    vi.advanceTimersByTime(backoffMax * 4);
    expect(h.sockets).toHaveLength(1);

    const g = setup();
    const answer = Promise.withResolvers<void>();
    g.checkSession.mockReturnValue(answer.promise);
    g.realtime.subscribe("prj_a", fakeStore());
    g.socket().drop(4001);
    g.realtime.close();
    answer.resolve();
    await vi.advanceTimersByTimeAsync(backoffMax * 4);
    expect(g.sockets).toHaveLength(1);
  });
});
