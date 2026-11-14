// What a read of the server waits for, so that it finds what the page
// shows: the page's batches, which the page shows before the server has
// answered them.

import type { ProjectDocument } from "./store";

/**
 * Resolves once doc sends none of the page's batches, at once where it
 * sends none, or once signal ends the wait: what the page shows of them
 * before the server has answered, as a node that it creates, the server
 * does not have yet, and a read of it made meanwhile would find nothing.
 */
export function whenSent(
  doc: ProjectDocument,
  signal: AbortSignal,
): Promise<void> {
  return new Promise((resolve) => {
    if (doc.store.getState().sending === 0 || signal.aborted) {
      resolve();
      return;
    }
    const done = () => {
      stop();
      signal.removeEventListener("abort", done);
      resolve();
    };
    const stop = doc.store.subscribe((state) => {
      if (state.sending === 0) {
        done();
      }
    });
    signal.addEventListener("abort", done);
  });
}
