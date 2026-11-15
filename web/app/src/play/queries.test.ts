import { afterEach, describe, expect, it, vi } from "vitest";
import { createStore } from "zustand/vanilla";
import type { DocumentState, ProjectDocument } from "../document/store";
import { fakeApi } from "../test/fake";
import { readPlayState } from "./queries";

// A saved play state is read as play loads it once the page's batches have
// been answered, as the list is, so that it is the state as the page's last
// command left it, as the undo of a replace sent a moment before.

afterEach(() => {
  vi.restoreAllMocks();
});

const projectId = `prj_${"P".padStart(26, "0")}`;
const playStateId = `pst_${"1".padStart(26, "0")}`;

describe("readPlayState", () => {
  it("reads the play state once the page's batches have been answered", async () => {
    const store = createStore<Pick<DocumentState, "sending">>(() => ({
      sending: 1,
    }));
    const doc = { projectId, store } as unknown as ProjectDocument;
    const calls = fakeApi(() => ({ id: playStateId }));
    const read = readPlayState(doc, playStateId);
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(calls).not.toHaveBeenCalled();
    store.setState({ sending: 0 });
    expect((await read).id).toBe(playStateId);
    expect(calls).toHaveBeenCalledWith("getPlayState", {
      path: { project_id: projectId, play_state_id: playStateId },
    });
  });
});
