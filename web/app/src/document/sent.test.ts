import { afterEach, describe, expect, it, vi } from "vitest";
import { createStore } from "zustand/vanilla";
import { whenSent } from "./sent";
import type { DocumentState, ProjectDocument } from "./store";

// What a read of the server waits for: the page's batches, for as long as
// they take, or for at most the wait that the read gives.

afterEach(() => {
  vi.useRealTimers();
});

/** A document that sends sending of the page's batches, as its store says. */
function sendingDocument(sending: number) {
  const store = createStore<Pick<DocumentState, "sending">>(() => ({
    sending,
  }));
  return { doc: { store } as unknown as ProjectDocument, store };
}

/** Returns whether the promise has resolved, once the timers due have run. */
async function settled(promise: Promise<void>): Promise<boolean> {
  let done = false;
  void promise.then(() => {
    done = true;
  });
  await vi.advanceTimersByTimeAsync(0);
  return done;
}

describe("whenSent", () => {
  it("resolves at once where the page sends no batch", async () => {
    vi.useFakeTimers();
    const { doc } = sendingDocument(0);
    expect(await settled(whenSent(doc, new AbortController().signal))).toBe(
      true,
    );
  });

  it("waits for the page's batches, however long they take, without a wait", async () => {
    vi.useFakeTimers();
    const { doc, store } = sendingDocument(1);
    const sent = whenSent(doc, new AbortController().signal);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(await settled(sent)).toBe(false);
    store.setState({ sending: 0 });
    expect(await settled(sent)).toBe(true);
  });

  it("goes on after the wait that it is given, as while a batch is queued offline", async () => {
    vi.useFakeTimers();
    const { doc } = sendingDocument(1);
    const sent = whenSent(doc, new AbortController().signal, 3_000);
    await vi.advanceTimersByTimeAsync(2_999);
    expect(await settled(sent)).toBe(false);
    await vi.advanceTimersByTimeAsync(1);
    expect(await settled(sent)).toBe(true);
  });

  it("ends the wait once its signal ends", async () => {
    vi.useFakeTimers();
    const { doc } = sendingDocument(1);
    const abort = new AbortController();
    const sent = whenSent(doc, abort.signal, 3_000);
    abort.abort();
    expect(await settled(sent)).toBe(true);
  });
});
