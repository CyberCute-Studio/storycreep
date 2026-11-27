// What a read of the server waits for, so that it finds what the page
// shows: the page's batches, which the page shows before the server has
// answered them.

import type { DocumentState, ProjectDocument } from "./store";

/**
 * Reports whether the document of state sends none of the page's batches
 * but those that the device's queue keeps (PRD FR-OFF-03), which are no
 * longer the page's to lose, as a reload, and which a read that waits for
 * the page's batches would wait for until the server answers again.
 */
function unsent(state: Pick<DocumentState, "sending" | "queued">): boolean {
  return state.sending - state.queued <= 0;
}

/**
 * How long, in milliseconds, a read that the project's document has read
 * again as its operations apply (Follower), as of the writing statistics,
 * waits for the page's batches at most, which the server answers in far
 * less, so that one that waits for long, as one queued while the page is
 * offline, keeps neither the page from opening nor what it shows loading:
 * the read goes on without it, and the document has it read again once it
 * applies.
 */
export const sentWait = 3_000;

/**
 * Resolves once doc sends none of the page's batches, at once where it
 * sends none, or once signal ends the wait, or, given wait, once that many
 * milliseconds have passed: what the page shows of them before the server
 * has answered, as a node that it creates, the server does not have yet,
 * and a read of it made meanwhile would find nothing. A read that the page
 * reads again once they apply, as the writing statistics, gives a wait, so
 * that a batch that waits for long, as one queued while the page is
 * offline, holds it for no longer than that.
 */
export function whenSent(
  doc: ProjectDocument,
  signal: AbortSignal,
  wait?: number,
): Promise<void> {
  return new Promise((resolve) => {
    if (unsent(doc.store.getState()) || signal.aborted) {
      resolve();
      return;
    }
    let timer: ReturnType<typeof setTimeout> | undefined;
    const done = () => {
      stop();
      clearTimeout(timer);
      signal.removeEventListener("abort", done);
      resolve();
    };
    const stop = doc.store.subscribe((state) => {
      if (unsent(state)) {
        done();
      }
    });
    signal.addEventListener("abort", done);
    if (wait !== undefined) {
      timer = setTimeout(done, wait);
    }
  });
}
