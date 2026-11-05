// A fake of the page's real-time channel (document/realtime.ts), for the
// tests of pages: what they subscribe to, and the operations of other
// sessions that a test sends them, as the server would.

import type { OperationMessage } from "@storycreep/shared/realtime";
import type { Realtime, Subscriber } from "../document/realtime";

export interface FakeRealtime extends Realtime {
  /** Reports whether a store subscribes to projectId. */
  subscribed(projectId: string): boolean;
  /**
   * Holds the subscription of projectId, as the server answers it, after
   * which the store is live.
   */
  hold(projectId: string): void;
  /**
   * Sends the store of projectId an operation of the next seq of its
   * document, of commands, which left revisions, as of another session.
   */
  send(
    projectId: string,
    commands: { type: string; payload: unknown }[],
    revisions: { id: string; rev: number }[],
  ): void;
}

/** Returns a fake of the page's real-time channel. */
export function fakeRealtime(): FakeRealtime {
  const subscribers = new Map<string, Subscriber>();
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
    reconnect() {
      // The fake's subscriptions go with no session's cookie.
    },
    close() {
      subscribers.clear();
    },
    subscribed: (projectId) => subscribers.has(projectId),
    hold(projectId) {
      const subscriber = subscribers.get(projectId);
      if (subscriber === undefined) {
        throw new Error(`Nothing subscribes to ${projectId}.`);
      }
      const since = subscriber.since();
      subscriber.subscribed(since, since);
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
        seq: subscriber.since() + 1,
        commands: commands.map((command) => ({
          ...command,
          command_id: `00000000-0000-7000-8000-${String(++ids).padStart(12, "0")}`,
        })),
        revisions,
        created_at: "2026-11-05T10:00:00Z",
      };
      subscriber.operation(message);
    },
  };
}
