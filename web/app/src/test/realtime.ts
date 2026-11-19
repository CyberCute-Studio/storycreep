// A fake of the page's real-time channel (document/realtime.ts), for the
// tests of pages: what they subscribe to, and the operations of other
// sessions that a test sends them, as the server would, and the messages
// that tell the page's listeners that the account's notifications changed
// (FR-COMMENT-02).

import type { OperationMessage } from "@storycreep/shared/realtime";
import type { Realtime, Subscriber } from "../document/realtime";

export interface FakeRealtime extends Realtime {
  /** Reports whether a store subscribes to projectId. */
  subscribed(projectId: string): boolean;
  /**
   * Holds the subscription of projectId, as the server answers it, as of
   * seq, the project's seq on the server, after which the store is live. The
   * seq is the store's own unless given, as of a store that has every
   * operation that the server has; one that knows of a later seq, as the
   * answer to its page's batch that it has yet to take for its operation,
   * or a history kept over a reload, is given the server's, which is not
   * before that, as a seq before it tells the store of a database restored
   * from an older backup.
   */
  hold(projectId: string, seq?: number): void;
  /**
   * Sends the store of projectId an operation of the next seq of its
   * document, of commands, which left revisions, as of another session, or
   * of the page's own batch, whose commands have the IDs that it gave them.
   */
  send(
    projectId: string,
    commands: { type: string; payload: unknown; command_id?: string }[],
    revisions: { id: string; rev: number }[],
  ): void;
  /** Reports how many listeners of the account's notifications listen, as the shell's of a signed-in page. */
  listening(): number;
  /** Tells each listener of the account's notifications that they changed, as {"type": "notifications"} does. */
  notify(): void;
}

/** Returns a fake of the page's real-time channel. */
export function fakeRealtime(): FakeRealtime {
  const subscribers = new Map<string, Subscriber>();
  const listeners = new Set<() => void>();
  let ids = 0;
  return {
    subscribe(projectId, subscriber) {
      subscribers.set(projectId, subscriber);
      return () => {
        if (subscribers.get(projectId) === subscriber) {
          subscribers.delete(projectId);
        }
      };
    },
    listen(listener) {
      const own = () => listener();
      listeners.add(own);
      return () => {
        listeners.delete(own);
      };
    },
    reconnect() {
      // The fake's subscriptions go with no session's cookie.
    },
    close() {
      subscribers.clear();
      listeners.clear();
    },
    listening: () => listeners.size,
    notify() {
      for (const listener of [...listeners]) {
        listener();
      }
    },
    subscribed: (projectId) => subscribers.has(projectId),
    hold(projectId, seq) {
      const subscriber = subscribers.get(projectId);
      if (subscriber === undefined) {
        throw new Error(`Nothing subscribes to ${projectId}.`);
      }
      const since = subscriber.since();
      subscriber.subscribed(seq ?? since);
      subscriber.live(true);
    },
    send(projectId, commands, revisions) {
      const subscriber = subscribers.get(projectId);
      if (subscriber === undefined) {
        throw new Error(`Nothing subscribes to ${projectId}.`);
      }
      const message: OperationMessage = {
        type: "operation",
        project_id: projectId,
        // The store's seq, asked for as no subscription, as the real channel
        // asks for it only as it sends one (Subscriber.since).
        seq: subscriber.since(false) + 1,
        commands: commands.map((command) => ({
          command_id: `00000000-0000-7000-8000-${String(++ids).padStart(12, "0")}`,
          ...command,
        })),
        revisions,
        created_at: "2026-11-05T10:00:00Z",
      };
      subscriber.operation(message);
    },
  };
}
