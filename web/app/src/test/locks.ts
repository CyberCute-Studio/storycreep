// A fake of the browser's Web Locks, which the device's queue holds its
// documents' batches by (offline/queue.ts), for the tests, whose browser
// (jsdom) has none: each lock exclusive, granted in the order asked for,
// and none to a request whose signal has aborted already, which it refuses
// at once, as the browser does.

import type { QueueLocks } from "../offline/queue";

/** Returns locks of their own, which only what holds them shares. */
export function fakeLocks(): QueueLocks {
  const held = new Set<string>();
  const waiting = new Map<string, (() => void)[]>();
  const hold = (name: string, signal?: AbortSignal) =>
    new Promise<() => void>((resolve, reject) => {
      if (signal?.aborted) {
        reject(new DOMException("Aborted.", "AbortError"));
        return;
      }
      const grant = () => {
        held.add(name);
        let released = false;
        resolve(() => {
          if (released) {
            return;
          }
          released = true;
          held.delete(name);
          waiting.get(name)?.shift()?.();
        });
      };
      if (!held.has(name)) {
        grant();
        return;
      }
      const queue = waiting.get(name) ?? [];
      waiting.set(name, queue);
      queue.push(grant);
      signal?.addEventListener("abort", () => {
        const at = queue.indexOf(grant);
        if (at >= 0) {
          queue.splice(at, 1);
          reject(new DOMException("Aborted.", "AbortError"));
        }
      });
    });
  return {
    hold,
    run: async (name, task) => {
      const release = await hold(name);
      try {
        return await task();
      } finally {
        release();
      }
    },
    held: async () => new Set(held),
  };
}
