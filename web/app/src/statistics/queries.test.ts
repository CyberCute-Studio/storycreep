import type { NodeStatistics } from "@storycreep/shared/api";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createStore } from "zustand/vanilla";
import { api } from "../api";
import type { DocumentState, ProjectDocument } from "../document/store";
import { refused } from "../test/fake";
import { nodeStatisticsQuery } from "./queries";

// A node's writing statistics, as its inspector reads them: once the page's
// batches have been answered, or after a wait, and, of a node that the
// server does not have yet while they are unanswered, as one that one of
// them creates, once they have been, however long they take.

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

/** The statistics of the node nd_a, as the server answers them. */
const counts: NodeStatistics = {
  id: "nd_a",
  seq: 4,
  words: 3,
  characters: 15,
  choices: 0,
  conditions: 0,
  complete: true,
};

/** A document of the project pr_a that sends sending of the page's batches, as its store says. */
function sendingDocument(sending: number) {
  const store = createStore<Pick<DocumentState, "sending">>(() => ({
    sending,
  }));
  return {
    doc: { projectId: "pr_a", store } as unknown as ProjectDocument,
    store,
  };
}

/** Reads the statistics of nd_a of doc, as the inspector's query does. */
function read(doc: ProjectDocument): Promise<NodeStatistics> {
  const queryFn = nodeStatisticsQuery(doc, "nd_a").queryFn as (context: {
    signal: AbortSignal;
  }) => Promise<NodeStatistics>;
  return queryFn({ signal: new AbortController().signal });
}

describe("a node's statistics", () => {
  it("are read again once the page's batches are answered, however long they take, where the server had no such node while they were not", async () => {
    vi.useFakeTimers();
    const { doc, store } = sendingDocument(1);
    const call = vi
      .spyOn(api, "call")
      .mockRejectedValueOnce(refused(404, { code: "not_found" }))
      .mockResolvedValueOnce({
        status: 200,
        body: counts,
        response: new Response(),
      } as never);
    let answer: NodeStatistics | undefined;
    void read(doc).then((stats) => {
      answer = stats;
    });
    // The read goes on after its wait, and the server has no such node yet.
    await vi.advanceTimersByTimeAsync(3_000);
    expect(call).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(call).toHaveBeenCalledTimes(1);
    expect(answer).toBeUndefined();
    store.setState({ sending: 0 });
    await vi.advanceTimersByTimeAsync(0);
    expect(call).toHaveBeenCalledTimes(2);
    expect(answer).toEqual(counts);
  });

  it("are refused as of no such node where the page sends none of its batches", async () => {
    const { doc } = sendingDocument(0);
    const call = vi
      .spyOn(api, "call")
      .mockRejectedValueOnce(refused(404, { code: "not_found" }));
    await expect(read(doc)).rejects.toMatchObject({ status: 404 });
    expect(call).toHaveBeenCalledTimes(1);
  });
});
