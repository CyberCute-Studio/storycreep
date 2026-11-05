// The real-time channel's client (PRD FR-RT-01, FR-RT-02): one WebSocket of
// GET /api/v1/ws for the page, over which each project that a page shows is
// subscribed to, from the seq of the latest operation that its store has,
// and sent the operations that it missed, and then each that commits. The
// socket is a notification path, never the source of truth (FR-RT-01 AC3):
// a store that is told to reload, or that the socket cannot reach, reads
// the project from the API.
//
// It connects once a project is subscribed to, and again, once it is lost,
// with exponential backoff and full jitter, at once when the browser says
// that its connection is back. The server sends a heartbeat every 25
// seconds, so a socket that has heard nothing for a minute is taken for
// lost. A socket closed with 4001, whose session has ended or been given a
// new secret, connects again only once the page has asked for its session,
// and only while it still has one: a page whose session has ended is
// signed out by that question (checkSession), and its stores go with it.

import type {
  ClientMessage,
  OperationMessage,
  ServerMessage,
} from "@storycreep/shared/realtime";
import type { ErrorLog } from "../errors/log";

/** What a store of a project hears of its subscription. */
export interface Subscriber {
  /**
   * The seq of the latest operation of the project that the store has, from
   * which the subscription starts: the operations after it are sent first.
   * The channel asks for it as it sends the subscription, and it changes
   * nothing, so that a fake of the channel may ask for it too.
   */
  since(): number;
  /**
   * The subscription holds: the operations after since, the seq that it was
   * sent from, up to seq, follow.
   */
  subscribed(seq: number, since: number): void;
  /** An operation of the project, its seq the next after the one before. */
  operation(message: OperationMessage): void;
  /**
   * The operations that the store missed cannot be sent, so it reads the
   * project again, from the API: the operations after seq follow, of which
   * it passes over what it has already, by their revisions.
   */
  resync(seq: number): void;
  /**
   * The project can no longer be read, as once it went to the trash, or the
   * user lost their access to it, which the server does not tell apart from
   * one that does not exist.
   */
  gone(): void;
  /**
   * Whether the subscription is live: once it holds, until its socket is
   * lost, during which the operations that commit reach the store only once
   * the socket is back, as those that it missed.
   */
  live(live: boolean): void;
}

/** What the page's channel offers its stores. */
export interface Realtime {
  /**
   * Subscribes to projectId, until what it returns is called. A project has
   * one subscriber at a time; another replaces it.
   */
  subscribe(projectId: string, subscriber: Subscriber): () => void;
  /**
   * Closes the socket at once, and opens one anew for the projects that are
   * subscribed to, then or later, as once the page's session is of another
   * account, or of none: a socket's subscriptions are of the session whose
   * cookie opened it, which they would otherwise go on being for a while.
   */
  reconnect(): void;
  /** Closes the socket for good, as once the page signs out. */
  close(): void;
}

/** What the channel needs of a WebSocket, which tests fake. */
export interface SocketLike {
  readonly readyState: number;
  onopen: ((event: Event) => void) | null;
  onmessage: ((event: MessageEvent) => void) | null;
  onclose: ((event: CloseEvent) => void) | null;
  onerror: ((event: Event) => void) | null;
  send(data: string): void;
  close(code?: number, reason?: string): void;
}

/** The state of a socket that is open (WebSocket.OPEN). */
const open = 1;

/** The close code of a socket whose session or token no longer holds (FR-RT-01). */
export const closeUnauthenticated = 4001;

/** The first wait before connecting again, in milliseconds, which doubles with each failure. */
export const backoffBase = 1_000;

/** The longest wait before connecting again, in milliseconds. */
export const backoffMax = 30_000;

/**
 * How long a socket may hear nothing, in milliseconds, before the channel
 * takes it for lost: the server sends a heartbeat every 25 seconds.
 */
export const silenceTimeout = 60_000;

/**
 * How long a socket that no project needs stays open, in milliseconds, so
 * that going from one project's pages to another's does not close it and
 * open another.
 */
export const lingerTimeout = 5_000;

export interface RealtimeOptions {
  /** Returns the URL of the socket, as wss://host/api/v1/ws. */
  url: () => string;
  /** Opens a socket; the browser's WebSocket if not set. */
  connect?: (url: string) => SocketLike;
  /**
   * Asks the server for the page's session, which signs the page out if it
   * has ended, as once a socket is closed with 4001.
   */
  checkSession: () => Promise<void>;
  /** Reports whether the page is signed in, as once checkSession has answered. */
  signedIn: () => boolean;
  /** Where a message that the channel cannot use is recorded. */
  log: ErrorLog;
  /** Returns a number from 0 to 1, for the jitter; Math.random if not set. */
  random?: () => number;
  /** The window whose online event connects again at once; none if not set. */
  window?: Pick<Window, "addEventListener" | "removeEventListener">;
}

/** Returns the URL of the real-time channel of the server that serves the page at location. */
export function socketURL(
  location: Pick<Location, "protocol" | "host">,
): string {
  const scheme = location.protocol === "https:" ? "wss:" : "ws:";
  return `${scheme}//${location.host}/api/v1/ws`;
}

/**
 * Returns how long to wait before connecting again after failures failures
 * in a row: a random time up to backoffBase doubled for each, at most
 * backoffMax (full jitter), so that the clients of a server that restarts do
 * not all come back at once.
 */
export function backoff(failures: number, random: () => number): number {
  const ceiling = Math.min(
    backoffMax,
    backoffBase * 2 ** Math.min(failures, 16),
  );
  return Math.round(ceiling * random());
}

/** Returns the page's channel. */
export function createRealtime(options: RealtimeOptions): Realtime {
  const connect =
    options.connect ??
    ((url: string): SocketLike =>
      // biome-ignore lint/plugin: the real-time channel's socket
      new WebSocket(url));
  const random = options.random ?? Math.random;
  const subscribers = new Map<string, Subscriber>();
  // The projects whose subscription the socket holds now.
  const holding = new Set<string>();
  let socket: SocketLike | undefined;
  let failures = 0;
  let closed = false;
  let retry: ReturnType<typeof setTimeout> | undefined;
  let silence: ReturnType<typeof setTimeout> | undefined;
  let linger: ReturnType<typeof setTimeout> | undefined;
  const resubscribes = new Map<string, ReturnType<typeof setTimeout>>();
  // The seq that each project's latest subscription was sent from, which
  // its store compares the seq that the subscription holds as of with.
  const sentFrom = new Map<string, number>();

  const send = (message: ClientMessage) => {
    if (socket?.readyState === open) {
      socket.send(JSON.stringify(message));
    }
  };
  const subscribeTo = (projectId: string) => {
    const subscriber = subscribers.get(projectId);
    if (subscriber !== undefined) {
      const since = subscriber.since();
      sentFrom.set(projectId, since);
      send({ type: "subscribe", project_id: projectId, since });
    }
  };
  const notLive = () => {
    for (const projectId of holding) {
      subscribers.get(projectId)?.live(false);
    }
    holding.clear();
    // A subscription that waited out a rate limit is sent anew with the
    // others once a socket opens again.
    for (const timer of resubscribes.values()) {
      clearTimeout(timer);
    }
    resubscribes.clear();
  };
  const heard = () => {
    clearTimeout(silence);
    silence = setTimeout(() => {
      // A socket that has heard nothing, not even a heartbeat, is gone
      // without a word, as behind a network that dropped it: it is closed,
      // and opened anew.
      lost(socket);
    }, silenceTimeout);
  };
  /** Closes the socket s, the channel's, which is the channel's no longer. */
  const drop = (s: SocketLike) => {
    socket = undefined;
    clearTimeout(silence);
    s.onopen = s.onmessage = s.onclose = s.onerror = null;
    s.close();
    notLive();
  };
  /** Drops the socket s, if it is still the channel's, and connects again later. */
  const lost = (s: SocketLike | undefined, code?: number) => {
    if (s === undefined || s !== socket) {
      return;
    }
    drop(s);
    if (closed || subscribers.size === 0) {
      return;
    }
    failures++;
    const wait = backoff(failures, random);
    if (code === closeUnauthenticated) {
      // The session that opened the socket no longer holds: the page asks
      // for its session, which signs it out if it has ended, and connects
      // again only if it is still signed in, as with the cookie of a new
      // secret, which a change of the password set.
      void options.checkSession().then(
        () => {
          if (options.signedIn()) {
            later(wait);
          }
        },
        () => later(wait),
      );
      return;
    }
    later(wait);
  };
  const later = (wait: number) => {
    clearTimeout(retry);
    retry = setTimeout(start, wait);
  };
  const start = () => {
    clearTimeout(retry);
    retry = undefined;
    if (closed || socket !== undefined || subscribers.size === 0) {
      return;
    }
    let s: SocketLike;
    try {
      s = connect(options.url());
    } catch (error) {
      options.log.record("error", error);
      failures++;
      later(backoff(failures, random));
      return;
    }
    socket = s;
    heard();
    s.onopen = () => {
      heard();
      for (const projectId of subscribers.keys()) {
        subscribeTo(projectId);
      }
    };
    s.onmessage = (event) => {
      heard();
      receive(event.data);
    };
    s.onerror = () => {
      // The close that follows says what became of the socket.
    };
    s.onclose = (event) => lost(s, event.code);
  };
  const receive = (data: unknown) => {
    let message: ServerMessage;
    try {
      message = JSON.parse(String(data)) as ServerMessage;
    } catch (error) {
      options.log.record("error", error);
      return;
    }
    if (typeof message !== "object" || message === null) {
      options.log.record(
        "error",
        new Error("The real-time channel sent a message that is no object."),
      );
      return;
    }
    if (message.type === "heartbeat") {
      return;
    }
    if (message.type === "error") {
      options.log.record(
        "error",
        new Error(`The real-time channel refused a message: ${message.code}`),
      );
      return;
    }
    const subscriber = subscribers.get(message.project_id);
    if (subscriber === undefined) {
      return;
    }
    switch (message.type) {
      case "subscribed":
        // A socket that has held a subscription has connected well, so the
        // next that is lost waits from the start again, as one does whose
        // subscription holds once its store has read the project anew.
        failures = 0;
        holding.add(message.project_id);
        subscriber.subscribed(
          message.seq,
          sentFrom.get(message.project_id) ?? message.seq,
        );
        subscriber.live(true);
        return;
      case "resync_required":
        failures = 0;
        holding.add(message.project_id);
        subscriber.resync(message.seq);
        subscriber.live(true);
        return;
      case "operation":
        subscriber.operation(message);
        return;
      case "unsubscribed":
        if (message.reason === "not_found") {
          holding.delete(message.project_id);
          subscriber.live(false);
          subscriber.gone();
        }
        return;
      case "subscription_failed":
        switch (message.code) {
          case "not_found":
            holding.delete(message.project_id);
            subscriber.live(false);
            subscriber.gone();
            return;
          case "rate_limited": {
            const projectId = message.project_id;
            clearTimeout(resubscribes.get(projectId));
            resubscribes.set(
              projectId,
              setTimeout(
                () => {
                  resubscribes.delete(projectId);
                  subscribeTo(projectId);
                },
                (message.retry_after ?? 1) * 1000,
              ),
            );
            return;
          }
          default:
            // An ID that is not a project's, or more subscriptions than a
            // socket may hold, which the app never sends.
            options.log.record(
              "error",
              new Error(
                `The real-time channel refused the subscription of ${message.project_id}: ${message.code}`,
              ),
            );
        }
    }
  };
  const online = () => {
    // The browser has its connection back: a socket that waits to connect
    // again does so at once, and from the start of its backoff.
    if (socket === undefined && retry !== undefined) {
      failures = 0;
      start();
    }
  };
  options.window?.addEventListener("online", online);

  return {
    subscribe(projectId, subscriber) {
      clearTimeout(linger);
      subscribers.set(projectId, subscriber);
      holding.delete(projectId);
      if (socket === undefined) {
        if (retry === undefined) {
          start();
        }
      } else {
        subscribeTo(projectId);
      }
      return () => {
        if (subscribers.get(projectId) !== subscriber) {
          return;
        }
        subscribers.delete(projectId);
        holding.delete(projectId);
        sentFrom.delete(projectId);
        clearTimeout(resubscribes.get(projectId));
        resubscribes.delete(projectId);
        send({ type: "unsubscribe", project_id: projectId });
        if (subscribers.size === 0) {
          clearTimeout(linger);
          linger = setTimeout(() => {
            if (subscribers.size === 0) {
              clearTimeout(retry);
              retry = undefined;
              lost(socket);
            }
          }, lingerTimeout);
        }
      };
    },
    reconnect() {
      clearTimeout(linger);
      clearTimeout(retry);
      retry = undefined;
      if (socket !== undefined) {
        drop(socket);
      }
      failures = 0;
      start();
    },
    close() {
      closed = true;
      options.window?.removeEventListener("online", online);
      clearTimeout(retry);
      clearTimeout(linger);
      for (const timer of resubscribes.values()) {
        clearTimeout(timer);
      }
      resubscribes.clear();
      lost(socket);
      subscribers.clear();
    },
  };
}
