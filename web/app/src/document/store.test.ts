import {
  type ApiError,
  type Board,
  type CommandBody,
  type CommandResultBody,
  type Folder,
  NetworkError,
  type UndoCommandBody,
} from "@storycreep/shared/api";
import { afterEach, describe, expect, it, onTestFinished, vi } from "vitest";
import { createQueryClient, sessionQuery } from "../api";
import { createErrorLog } from "../errors/log";
import { testSession } from "../test/app";
import { fakeApi, refused, testProject } from "../test/fake";
import { fakeRealtime } from "../test/realtime";
import { Documents } from "./documents";
import type { Subscriber } from "./realtime";
import { type Command, ConflictError, ProjectDocument } from "./store";
import { historyKey } from "./undo";

// The document of a project as a page holds it, over a fake of the API's
// client, which plays the server, and of the real-time channel, which brings
// the operations of other sessions: what it reads of the project, how it
// applies the operations that come, how it sends the page's own batches, and
// what it does once the server refuses one, and the undo history of them,
// over a reload too.

afterEach(() => {
  vi.restoreAllMocks();
});

/** When the server applied the operations that the fake channel sends. */
const time = "2026-11-05T10:00:00Z";

/** What a call of the API's client was given, as the fake server reads it. */
interface Input {
  path?: Record<string, string>;
  query?: { kind?: string; cursor?: string; limit?: number };
  body?: { commands: CommandBody[] };
}

type Handlers = Partial<Record<string, (input: Input) => unknown>>;

/**
 * Answers the API's client with handlers, by operation, and fails the
 * request of an operation that it has none for. It returns the batches that
 * the document sent, each as it sent it, and the inputs of each operation's
 * calls.
 */
function serve(handlers: Handlers) {
  const batches: CommandBody[][] = [];
  const spy = fakeApi((operation, input) => {
    const call = (input ?? {}) as Input;
    if (operation === "applyCommands") {
      batches.push(structuredClone(call.body?.commands ?? []));
    }
    const handler = handlers[operation];
    if (handler === undefined) {
      throw new Error(`unexpected ${operation}`);
    }
    return handler(call);
  });
  return {
    batches,
    calls: (operation: string): Input[] =>
      spy.mock.calls
        .filter(([op]) => op === operation)
        .map(([, input]) => input as unknown as Input),
  };
}

/** Returns the one page of a list that holds items. */
function page<T>(items: T[]) {
  return { items, next_cursor: null };
}

/** Returns the handlers of what the tree part reads: the project, its folders of boards, and its boards. */
function tree(boards: Board[], folders: Folder[] = []): Handlers {
  return {
    getProject: () => testProject,
    listFolders: () => page(folders),
    listBoards: () => page(boards),
  };
}

/** Returns a board of the test project. */
function board(id: string, name: string, rev = 3): Board {
  return {
    id,
    project_id: testProject.id,
    name,
    slug: id.slice("bd_".length),
    description: "",
    color: "none",
    order: "V",
    rev,
    created_at: "2026-11-01T10:00:00Z",
    updated_at: "2026-11-01T10:00:00Z",
  };
}

/** Returns a folder of boards of the test project. */
function folder(id: string, name: string): Folder {
  return {
    id,
    project_id: testProject.id,
    kind: "board",
    name,
    order: "U",
    rev: 1,
    created_at: "2026-11-01T10:00:00Z",
    updated_at: "2026-11-01T10:00:00Z",
  };
}

/** Returns a rename of the board of id that expects rev. */
function rename(id: string, name: string, rev: number): Command {
  return {
    type: "board.rename",
    payload: { id, name },
    expected_rev: { [id]: rev },
  };
}

/** Returns the command of an undo that renames the board of id back, expecting rev. */
function renameBack(id: string, name: string, rev: number): UndoCommandBody {
  return {
    type: "board.rename",
    payload: { id, name },
    expected_rev: { [id]: rev },
  };
}

/** Returns the answer to a batch that applied as seq, leaving revisions, with undo. */
function applied(
  seq: number,
  revisions: { id: string; rev: number }[],
  undo: UndoCommandBody[] | null = [],
): CommandResultBody {
  return { status: "applied", seq, revisions, not_reconnected: [], undo };
}

/**
 * Returns the server's refusal of a batch of one command for the revision of
 * the board of state, which changed elsewhere since the page read it at
 * expected, with the board as it is now.
 */
function conflict(state: Board, expected = 3): ApiError {
  return refused(409, {
    code: "revision_conflict",
    details: {
      conflicts: [
        {
          command: 0,
          command_id: "00000000-0000-7000-8000-000000000001",
          type: "board.rename",
          id: state.id,
          expected_rev: expected,
          exists: true,
          current_rev: state.rev,
        },
      ],
      states: { [state.id]: state },
    },
  });
}

/** The server's refusal of an undo whose own undo would not fit in a request. */
function undoTooLarge(): ApiError {
  return refused(422, {
    code: "validation_failed",
    details: { reason: "undo_too_large" },
  });
}

/** Answers each batch with the next of answers, in turn: an error is thrown, anything else answered. */
function inTurn(...answers: unknown[]): () => unknown {
  return () => {
    if (answers.length === 0) {
      throw new Error("unexpected batch");
    }
    const next = answers.shift();
    if (next instanceof Error) {
      throw next;
    }
    return next;
  };
}

/** Lets what the document does once its requests are answered run. */
function flush(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

/**
 * Makes the document of the test project, as a page of the test account
 * does, over the tab's session storage, and waits until it has read the
 * project and the tree. It returns the document, its fake channel, and a
 * way to send it an operation of any seq, as the channel's server does.
 */
async function open() {
  const log = createErrorLog();
  const queryClient = createQueryClient(log);
  queryClient.setQueryData(sessionQuery.queryKey, testSession);
  const realtime = fakeRealtime();
  const subscribers: Subscriber[] = [];
  const subscribe = realtime.subscribe;
  realtime.subscribe = (projectId, subscriber) => {
    subscribers.push(subscriber);
    return subscribe(projectId, subscriber);
  };
  const doc = new ProjectDocument(testProject.id, {
    queryClient,
    realtime,
    log,
    storage: window.sessionStorage,
    now: () => time,
  });
  onTestFinished(() => doc.dispose());
  await doc.ready;
  await doc.load("tree");
  let ids = 0;
  const deliver = (
    seq: number,
    commands: { type: string; payload: unknown; command_id?: string }[],
    revisions: { id: string; rev: number }[],
  ) =>
    subscribers.at(-1)?.operation({
      type: "operation",
      project_id: testProject.id,
      seq,
      commands: commands.map((command) => ({
        command_id: `00000000-0000-7000-8000-${String(++ids).padStart(12, "f")}`,
        ...command,
      })),
      revisions,
      created_at: time,
    });
  return { doc, realtime, log, queryClient, deliver };
}

/** Returns the board of id as the page shows it. */
function shown(doc: ProjectDocument, id: string): Board | undefined {
  return doc.store.getState().view.boards.get(id);
}

describe("the project document's reads", () => {
  it("reads the tree from the project's folders of boards and its boards, every page of each", async () => {
    const server = serve({
      getProject: () => testProject,
      listFolders: ({ query }) =>
        query?.cursor === undefined
          ? { items: [folder("fld_a", "Act one")], next_cursor: "fld-2" }
          : page([folder("fld_b", "Act two")]),
      listBoards: ({ query }) =>
        query?.cursor === undefined
          ? { items: [board("bd_a", "Harbour")], next_cursor: "bd-2" }
          : page([board("bd_b", "Lighthouse")]),
    });
    const { doc } = await open();
    expect(server.calls("listFolders").map((c) => c.query)).toEqual([
      { kind: "board", limit: 200 },
      { kind: "board", cursor: "fld-2", limit: 200 },
    ]);
    expect(server.calls("listBoards").map((c) => c.query)).toEqual([
      { limit: 200 },
      { cursor: "bd-2", limit: 200 },
    ]);
    const { view } = doc.store.getState();
    expect([...view.folders.keys()]).toEqual(["fld_a", "fld_b"]);
    expect([...view.boards.keys()]).toEqual(["bd_a", "bd_b"]);
    expect(view.project?.name).toBe("Harbour");
    expect(view.seq).toBe(testProject.seq);
    expect(doc.holds("tree")).toBe(true);
  });
});

describe("an operation of another session", () => {
  it("applies a rename without a request, at the operation's revision", async () => {
    const server = serve(
      tree([board("bd_a", "Harbour"), board("bd_b", "Lighthouse")]),
    );
    const { doc, realtime } = await open();
    const reads = server.calls("listBoards").length;
    realtime.send(
      testProject.id,
      [
        {
          type: "board.rename",
          payload: { id: "bd_a", name: "Harbour at night" },
        },
      ],
      [{ id: "bd_a", rev: 4 }],
    );
    expect(shown(doc, "bd_a")).toMatchObject({
      name: "Harbour at night",
      rev: 4,
      updated_at: time,
    });
    expect(doc.doc.seq).toBe(2);
    await flush();
    expect(server.calls("getBoard")).toHaveLength(0);
    expect(server.calls("listBoards")).toHaveLength(reads);
  });

  it("passes over an operation whose seq the document has already", async () => {
    serve(tree([board("bd_a", "Harbour")]));
    const { doc, deliver } = await open();
    deliver(
      1,
      [{ type: "board.rename", payload: { id: "bd_a", name: "Elsewhere" } }],
      [{ id: "bd_a", rev: 4 }],
    );
    expect(shown(doc, "bd_a")).toMatchObject({ name: "Harbour", rev: 3 });
    expect(doc.doc.seq).toBe(1);
  });

  it("keeps an entity that the document holds at a later revision than the operation's, as one read since", async () => {
    serve(tree([board("bd_a", "Harbour", 5)]));
    const { doc, realtime } = await open();
    realtime.send(
      testProject.id,
      [{ type: "board.rename", payload: { id: "bd_a", name: "Older" } }],
      [{ id: "bd_a", rev: 4 }],
    );
    expect(shown(doc, "bd_a")).toMatchObject({ name: "Harbour", rev: 5 });
    expect(doc.doc.seq).toBe(2);
  });

  it("reads from the server an entity that the operation's revisions name and its appliers did not change", async () => {
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      getBoard: () => board("bd_c", "Restored", 2),
    });
    const { doc, realtime } = await open();
    // A board that comes back from the trash is none that the document holds.
    realtime.send(
      testProject.id,
      [{ type: "board.restore", payload: { id: "bd_c" } }],
      [{ id: "bd_c", rev: 2 }],
    );
    await vi.waitFor(() =>
      expect(shown(doc, "bd_c")).toMatchObject({ name: "Restored", rev: 2 }),
    );
    expect(server.calls("getBoard").map((c) => c.path)).toEqual([
      { project_id: testProject.id, board_id: "bd_c" },
    ]);
  });

  it("takes out an entity that it reads from the server once the server has none", async () => {
    serve({
      ...tree([board("bd_a", "Harbour"), board("bd_b", "Lighthouse")]),
      getBoard: () => {
        throw refused(404, { code: "not_found" });
      },
    });
    const { doc, realtime } = await open();
    // A command of a type that a later server registers changes nothing
    // that the page can tell.
    realtime.send(
      testProject.id,
      [{ type: "future.command", payload: { id: "bd_a" } }],
      [{ id: "bd_a", rev: 4 }],
    );
    await vi.waitFor(() => expect(shown(doc, "bd_a")).toBeUndefined());
    expect(shown(doc, "bd_b")).toBeDefined();
  });

  it("drops a board that went to the trash", async () => {
    serve(tree([board("bd_a", "Harbour"), board("bd_b", "Lighthouse")]));
    const { doc, realtime } = await open();
    realtime.send(
      testProject.id,
      [{ type: "board.delete", payload: { id: "bd_a" } }],
      [{ id: "bd_a", rev: 4 }],
    );
    expect(shown(doc, "bd_a")).toBeUndefined();
    expect(shown(doc, "bd_b")).toBeDefined();
    expect(doc.doc.gone.get("bd_a")).toEqual({ rev: 4, seq: 2 });
  });

  it("reads the project and the parts that the document holds anew once an operation comes after a gap", async () => {
    let theirs = board("bd_a", "Harbour");
    const server = serve({
      getProject: () => testProject,
      listFolders: () => page([]),
      listBoards: () => page([theirs]),
    });
    const { doc, deliver } = await open();
    theirs = board("bd_a", "Harbour at night", 6);
    deliver(
      3,
      [{ type: "board.describe", payload: { id: "bd_a", description: "" } }],
      [{ id: "bd_a", rev: 6 }],
    );
    await vi.waitFor(() =>
      expect(shown(doc, "bd_a")).toMatchObject({
        name: "Harbour at night",
        rev: 6,
      }),
    );
    expect(server.calls("getProject")).toHaveLength(2);
    expect(server.calls("listFolders")).toHaveLength(2);
    expect(server.calls("listBoards")).toHaveLength(2);
    // What it read is as of that operation at least, so the channel's next
    // operation follows on.
    await vi.waitFor(() => expect(doc.doc.seq).toBe(3));
    deliver(
      4,
      [{ type: "board.rename", payload: { id: "bd_a", name: "Dawn" } }],
      [{ id: "bd_a", rev: 7 }],
    );
    expect(shown(doc, "bd_a")).toMatchObject({ name: "Dawn", rev: 7 });
    expect(server.calls("getProject")).toHaveLength(2);
  });

  // Verifies: FR-RT-02/AC3
  it("leaves what it did not change as it was, so that what the page reads of that is not shown anew", async () => {
    serve(
      tree(
        [board("bd_a", "Harbour"), board("bd_b", "Lighthouse")],
        [folder("fld_a", "Act one")],
      ),
    );
    const { doc, realtime } = await open();
    const before = doc.store.getState().view;
    // What a component selects of the folders, as the tree's, hears of no
    // change of them.
    const folders = vi.fn();
    doc.store.subscribe((state, previous) => {
      if (state.view.folders !== previous.view.folders) {
        folders();
      }
    });
    realtime.send(
      testProject.id,
      [
        {
          type: "board.rename",
          payload: { id: "bd_a", name: "Harbour at night" },
        },
      ],
      [{ id: "bd_a", rev: 4 }],
    );
    const after = doc.store.getState().view;
    expect(after.boards.get("bd_a")?.name).toBe("Harbour at night");
    expect(after.boards).not.toBe(before.boards);
    expect(after.boards.get("bd_b")).toBe(before.boards.get("bd_b"));
    expect(after.folders).toBe(before.folders);
    expect(after.project).toBe(before.project);
    expect(after.nodes).toBe(before.nodes);
    expect(after.connections).toBe(before.connections);
    expect(after.variables).toBe(before.variables);
    expect(after.componentTypes).toBe(before.componentTypes);
    expect(after.components).toBe(before.components);
    expect(after.parts).toBe(before.parts);
    expect(folders).not.toHaveBeenCalled();
  });
});

describe("the page's own batches", () => {
  it("shows a batch at once, before the server answers, and takes the answer's revisions once it has", async () => {
    const answer = Promise.withResolvers<CommandResultBody>();
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: () => answer.promise,
    });
    const { doc } = await open();
    const sent = doc.send([rename("bd_a", "Harbour at night", 3)]);
    expect(shown(doc, "bd_a")).toMatchObject({
      name: "Harbour at night",
      rev: 3,
    });
    expect(doc.doc.boards.get("bd_a")?.name).toBe("Harbour");
    expect(doc.store.getState().sending).toBe(1);
    await vi.waitFor(() => expect(server.batches).toHaveLength(1));
    expect(server.batches[0]).toEqual([
      {
        command_id: expect.any(String),
        type: "board.rename",
        payload: { id: "bd_a", name: "Harbour at night" },
        expected_rev: { bd_a: 3 },
      },
    ]);
    answer.resolve(applied(2, [{ id: "bd_a", rev: 4 }]));
    await expect(sent).resolves.toMatchObject({ seq: 2 });
    // The answer's seq is the next one, so the server has confirmed it.
    expect(doc.doc.seq).toBe(2);
    expect(doc.doc.boards.get("bd_a")).toMatchObject({
      name: "Harbour at night",
      rev: 4,
    });
    expect(doc.store.getState().view).toBe(doc.doc);
    expect(doc.store.getState().sending).toBe(0);
  });

  it("shows an answered batch on top of what the server confirmed until the channel brings the operations before it", async () => {
    serve({
      ...tree([board("bd_a", "Harbour"), board("bd_b", "Lighthouse")]),
      applyCommands: () => applied(3, [{ id: "bd_a", rev: 4 }]),
    });
    const { doc, realtime } = await open();
    await doc.send([rename("bd_a", "Harbour at night", 3)]);
    expect(doc.doc.seq).toBe(1);
    expect(shown(doc, "bd_a")).toMatchObject({
      name: "Harbour at night",
      rev: 4,
    });
    realtime.send(
      testProject.id,
      [{ type: "board.rename", payload: { id: "bd_b", name: "Old light" } }],
      [{ id: "bd_b", rev: 4 }],
    );
    expect(doc.doc.seq).toBe(2);
    expect(shown(doc, "bd_a")?.name).toBe("Harbour at night");
    expect(shown(doc, "bd_b")?.name).toBe("Old light");
    realtime.send(
      testProject.id,
      [
        {
          type: "board.rename",
          payload: { id: "bd_a", name: "Harbour at night" },
        },
      ],
      [{ id: "bd_a", rev: 4 }],
    );
    expect(doc.doc.seq).toBe(3);
    expect(doc.store.getState().view).toBe(doc.doc);
    expect(shown(doc, "bd_a")).toMatchObject({
      name: "Harbour at night",
      rev: 4,
    });
  });

  it("applies a batch once when the channel brings its operation before the server's answer", async () => {
    const answer = Promise.withResolvers<CommandResultBody>();
    const server = serve({
      ...tree([board("bd_a", "Harbour"), board("bd_b", "Lighthouse")]),
      applyCommands: () => answer.promise,
    });
    const { doc, deliver } = await open();
    const sent = doc.send([rename("bd_a", "Harbour at night", 3)]);
    await vi.waitFor(() => expect(server.batches).toHaveLength(1));
    deliver(2, server.batches[0], [{ id: "bd_a", rev: 4 }]);
    expect(doc.doc.seq).toBe(2);
    expect(doc.store.getState().view).toBe(doc.doc);
    expect(shown(doc, "bd_a")).toMatchObject({
      name: "Harbour at night",
      rev: 4,
    });
    answer.resolve(applied(2, [{ id: "bd_a", rev: 4 }]));
    await sent;
    expect(doc.doc.seq).toBe(2);
    expect(doc.store.getState().view).toBe(doc.doc);
    // The next operation is the next seq's, which the document applies.
    deliver(
      3,
      [{ type: "board.rename", payload: { id: "bd_b", name: "Old light" } }],
      [{ id: "bd_b", rev: 4 }],
    );
    expect(shown(doc, "bd_b")?.name).toBe("Old light");
    expect(server.calls("getProject")).toHaveLength(1);
  });

  it("sends batches one at a time, moving what one made on top of another expects to the revisions that the other's answer gave", async () => {
    const first = Promise.withResolvers<CommandResultBody>();
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: inTurn(
        first.promise,
        applied(3, [{ id: "bd_a", rev: 5 }]),
      ),
    });
    const { doc } = await open();
    const a = doc.send([rename("bd_a", "Harbour at dusk", 3)]);
    // The page shows the board at the revision that it read, which the
    // batch that it makes next expects.
    expect(shown(doc, "bd_a")).toMatchObject({
      name: "Harbour at dusk",
      rev: 3,
    });
    const b = doc.send([rename("bd_a", "Harbour at night", 3)]);
    expect(shown(doc, "bd_a")?.name).toBe("Harbour at night");
    await vi.waitFor(() => expect(server.batches).toHaveLength(1));
    await flush();
    expect(server.batches).toHaveLength(1);
    expect(doc.store.getState().sending).toBe(2);
    first.resolve(applied(2, [{ id: "bd_a", rev: 4 }]));
    await a;
    await b;
    expect(server.batches.map((batch) => batch[0].expected_rev)).toEqual([
      { bd_a: 3 },
      { bd_a: 4 },
    ]);
    expect(doc.doc.seq).toBe(3);
    expect(shown(doc, "bd_a")).toMatchObject({
      name: "Harbour at night",
      rev: 5,
    });
  });

  it("takes a batch that no answer came to out of the view, and sends it again with the IDs that its commands had", async () => {
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: inTurn(
        new NetworkError(new TypeError("Failed to fetch")),
        applied(2, [{ id: "bd_a", rev: 4 }]),
        applied(3, [{ id: "bd_a", rev: 5 }]),
      ),
    });
    const { doc } = await open();
    const commands = [rename("bd_a", "Harbour at night", 3)];
    await expect(doc.send(commands)).rejects.toBeInstanceOf(NetworkError);
    expect(shown(doc, "bd_a")?.name).toBe("Harbour");
    expect(doc.store.getState().sending).toBe(0);
    // While the channel is not live, the tree is read again, as the batch
    // may have applied all the same.
    await vi.waitFor(() => expect(server.calls("listBoards")).toHaveLength(2));
    await doc.send(commands);
    expect(server.batches[1][0].command_id).toBe(
      server.batches[0][0].command_id,
    );
    expect(shown(doc, "bd_a")?.name).toBe("Harbour at night");
    // Once answered, the same commands are another batch, with IDs of its own.
    await doc.send(commands);
    expect(server.batches[2][0].command_id).not.toBe(
      server.batches[0][0].command_id,
    );
  });
});

describe("a batch refused for a revision that changed elsewhere (§7.10)", () => {
  it("is sent again by itself, expecting the current revision, with new command IDs, where nothing that it changes changed elsewhere", async () => {
    const theirs = { ...board("bd_a", "Harbour", 4), color: "blue" as const };
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: inTurn(
        conflict(theirs),
        applied(2, [{ id: "bd_a", rev: 5 }]),
      ),
    });
    const { doc } = await open();
    await doc.send([rename("bd_a", "Harbour at night", 3)]);
    expect(server.batches.map((batch) => batch[0].expected_rev)).toEqual([
      { bd_a: 3 },
      { bd_a: 4 },
    ]);
    expect(server.batches[1][0].command_id).not.toBe(
      server.batches[0][0].command_id,
    );
    expect(shown(doc, "bd_a")).toMatchObject({
      name: "Harbour at night",
      color: "blue",
      rev: 5,
    });
  });

  it("is refused with both values of each field that it changes that changed elsewhere too, and the page shows theirs", async () => {
    const theirs = board("bd_a", "Old harbour", 4);
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: inTurn(conflict(theirs)),
    });
    const { doc } = await open();
    const error = await doc
      .send([rename("bd_a", "Harbour at night", 3)])
      .catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ConflictError);
    expect((error as ConflictError).overlaps).toEqual([
      {
        id: "bd_a",
        state: theirs,
        fields: [
          { field: "name", mine: "Harbour at night", theirs: "Old harbour" },
        ],
        gone: false,
      },
    ]);
    expect(server.batches).toHaveLength(1);
    expect(shown(doc, "bd_a")).toMatchObject({ name: "Old harbour", rev: 4 });
  });
});

describe("undo and redo (FR-EDIT-08)", () => {
  it("undoes a batch by sending its answer's undo with new command IDs, and redoes it by sending the undo's answer's undo", async () => {
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: inTurn(
        applied(
          2,
          [{ id: "bd_a", rev: 4 }],
          [renameBack("bd_a", "Harbour", 4)],
        ),
        applied(
          3,
          [{ id: "bd_a", rev: 5 }],
          [renameBack("bd_a", "Harbour at night", 5)],
        ),
        applied(
          4,
          [{ id: "bd_a", rev: 6 }],
          [renameBack("bd_a", "Harbour", 6)],
        ),
      ),
    });
    const { doc } = await open();
    await doc.send([rename("bd_a", "Harbour at night", 3)]);
    expect(doc.store.getState()).toMatchObject({ undoable: 1, redoable: 0 });
    await doc.undo();
    expect(server.batches[1]).toEqual([
      { ...renameBack("bd_a", "Harbour", 4), command_id: expect.any(String) },
    ]);
    expect(server.batches[1][0].command_id).not.toBe(
      server.batches[0][0].command_id,
    );
    expect(shown(doc, "bd_a")).toMatchObject({ name: "Harbour", rev: 5 });
    expect(doc.store.getState()).toMatchObject({
      undoable: 0,
      redoable: 1,
      stepping: false,
    });
    await doc.redo();
    expect(server.batches[2]).toEqual([
      {
        ...renameBack("bd_a", "Harbour at night", 5),
        command_id: expect.any(String),
      },
    ]);
    expect(shown(doc, "bd_a")).toMatchObject({
      name: "Harbour at night",
      rev: 6,
    });
    expect(doc.store.getState()).toMatchObject({ undoable: 1, redoable: 0 });
  });

  it("moves what the other steps expect of an entity that an undo or a redo brought back to the revision that it left", async () => {
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: inTurn(
        applied(
          2,
          [{ id: "bd_a", rev: 4 }],
          [renameBack("bd_a", "Harbour", 4)],
        ),
        applied(3, [{ id: "bd_a", rev: 5 }], [renameBack("bd_a", "Dusk", 5)]),
        applied(4, [{ id: "bd_a", rev: 6 }], [renameBack("bd_a", "Night", 6)]),
        applied(5, [{ id: "bd_a", rev: 7 }], [renameBack("bd_a", "Dusk", 7)]),
        applied(
          6,
          [{ id: "bd_a", rev: 8 }],
          [renameBack("bd_a", "Harbour", 8)],
        ),
        applied(7, [{ id: "bd_a", rev: 9 }], [renameBack("bd_a", "Dusk", 9)]),
      ),
    });
    const { doc } = await open();
    await doc.send([rename("bd_a", "Dusk", 3)]);
    await doc.send([rename("bd_a", "Night", 4)]);
    await doc.undo();
    await doc.undo();
    // The first batch's undo expected the revision that it left, 4, which
    // the second batch's undo brought the board back to, as of 6.
    expect(server.batches[2][0].expected_rev).toEqual({ bd_a: 5 });
    expect(server.batches[3][0]).toMatchObject({
      payload: { id: "bd_a", name: "Harbour" },
      expected_rev: { bd_a: 6 },
    });
    expect(shown(doc, "bd_a")).toMatchObject({ name: "Harbour", rev: 7 });
    await doc.redo();
    await doc.redo();
    expect(server.batches[4][0]).toMatchObject({
      payload: { id: "bd_a", name: "Dusk" },
      expected_rev: { bd_a: 7 },
    });
    expect(server.batches[5][0]).toMatchObject({
      payload: { id: "bd_a", name: "Night" },
      expected_rev: { bd_a: 8 },
    });
    expect(shown(doc, "bd_a")).toMatchObject({ name: "Night", rev: 9 });
  });

  it("waits, while the channel is live, until the document has the operations up to the batch that it undoes, which tell what the other steps expect", async () => {
    const server = serve({
      ...tree([board("bd_a", "Harbour"), board("bd_b", "Lighthouse")]),
      applyCommands: inTurn(
        applied(
          2,
          [{ id: "bd_a", rev: 4 }],
          [renameBack("bd_a", "Harbour", 4)],
        ),
        // Another session's batch applied as seq 3, which the channel has
        // yet to bring.
        applied(4, [{ id: "bd_a", rev: 5 }], [renameBack("bd_a", "Dusk", 5)]),
        applied(5, [{ id: "bd_a", rev: 6 }], [renameBack("bd_a", "Night", 6)]),
        applied(6, [{ id: "bd_a", rev: 7 }], []),
      ),
    });
    const { doc, realtime } = await open();
    realtime.hold(testProject.id);
    await doc.send([rename("bd_a", "Dusk", 3)]);
    await doc.send([rename("bd_a", "Night", 4)]);
    expect(doc.doc.seq).toBe(2);
    const undone = doc.undo();
    await flush();
    expect(server.batches).toHaveLength(2);
    realtime.send(
      testProject.id,
      [{ type: "board.rename", payload: { id: "bd_b", name: "Old light" } }],
      [{ id: "bd_b", rev: 4 }],
    );
    realtime.send(testProject.id, server.batches[1], [{ id: "bd_a", rev: 5 }]);
    await undone;
    expect(server.batches[2][0].expected_rev).toEqual({ bd_a: 5 });
    // The board was at 4 before the batch that the undo undid, which the
    // first batch's undo expects, and is the same again, at 6.
    await doc.undo();
    expect(server.batches[3][0]).toMatchObject({
      payload: { id: "bd_a", name: "Harbour" },
      expected_rev: { bd_a: 6 },
    });
  });

  // Verifies: FR-EDIT-08/AC4
  it("rejects an undo refused for a change elsewhere since, and drops its step, changing nothing", async () => {
    const theirs = board("bd_b", "Old light", 5);
    const server = serve({
      ...tree([board("bd_a", "Harbour"), board("bd_b", "Lighthouse")]),
      applyCommands: inTurn(
        applied(
          2,
          [{ id: "bd_a", rev: 4 }],
          [renameBack("bd_a", "Harbour", 4)],
        ),
        applied(
          3,
          [{ id: "bd_b", rev: 4 }],
          [renameBack("bd_b", "Lighthouse", 4)],
        ),
        conflict(theirs, 4),
        applied(4, [{ id: "bd_a", rev: 5 }], []),
      ),
    });
    const { doc } = await open();
    await doc.send([rename("bd_a", "Harbour at night", 3)]);
    await doc.send([rename("bd_b", "Lighthouse at night", 3)]);
    const error = await doc.undo().catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ConflictError);
    expect((error as ConflictError).overlaps[0].fields).toEqual([
      { field: "name", mine: "Lighthouse", theirs: "Old light" },
    ]);
    expect(server.batches).toHaveLength(3);
    // The board is as the other change left it, and the undo of the other
    // batch is as it was.
    expect(shown(doc, "bd_b")).toMatchObject({ name: "Old light", rev: 5 });
    expect(shown(doc, "bd_a")).toMatchObject({
      name: "Harbour at night",
      rev: 4,
    });
    expect(doc.store.getState()).toMatchObject({
      undoable: 1,
      redoable: 0,
      stepping: false,
      sending: 0,
    });
    await doc.undo();
    expect(server.batches[3]).toEqual([
      { ...renameBack("bd_a", "Harbour", 4), command_id: expect.any(String) },
    ]);
  });

  it("sends an undo too large for one request in halves, each expecting what those before it left, and redoes it as one step, the last half's undo first", async () => {
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: inTurn(
        applied(
          2,
          [{ id: "bd_a", rev: 4 }],
          [
            {
              type: "board.set_color",
              payload: { id: "bd_a", color: "none" },
              expected_rev: { bd_a: 4 },
            },
            renameBack("bd_a", "Harbour", 4),
          ],
        ),
        undoTooLarge(),
        applied(
          3,
          [{ id: "bd_a", rev: 5 }],
          [
            {
              type: "board.set_color",
              payload: { id: "bd_a", color: "blue" },
              expected_rev: { bd_a: 5 },
            },
          ],
        ),
        applied(
          4,
          [{ id: "bd_a", rev: 6 }],
          [renameBack("bd_a", "Harbour at night", 6)],
        ),
        applied(5, [{ id: "bd_a", rev: 7 }], []),
      ),
    });
    const { doc } = await open();
    await doc.send([
      rename("bd_a", "Harbour at night", 3),
      {
        type: "board.set_color",
        payload: { id: "bd_a", color: "blue" },
        expected_rev: { bd_a: 3 },
      },
    ]);
    await doc.undo();
    expect(server.batches.slice(1).map((batch) => batch.length)).toEqual([
      2, 1, 1,
    ]);
    expect(server.batches[2][0]).toMatchObject({
      type: "board.set_color",
      payload: { id: "bd_a", color: "none" },
      expected_rev: { bd_a: 4 },
    });
    // The second half expects what the first half left of the board.
    expect(server.batches[3][0]).toMatchObject({
      type: "board.rename",
      payload: { id: "bd_a", name: "Harbour" },
      expected_rev: { bd_a: 5 },
    });
    expect(shown(doc, "bd_a")).toMatchObject({
      name: "Harbour",
      color: "none",
      rev: 6,
    });
    expect(doc.store.getState()).toMatchObject({ undoable: 0, redoable: 1 });
    await doc.redo();
    expect(server.batches[4]).toEqual([
      {
        ...renameBack("bd_a", "Harbour at night", 6),
        command_id: expect.any(String),
      },
      {
        type: "board.set_color",
        payload: { id: "bd_a", color: "blue" },
        expected_rev: { bd_a: 6 },
        command_id: expect.any(String),
      },
    ]);
  });
});

/**
 * Answers the reads of the tree, and batches of one rename of bd_a each, as
 * the server applies them: each is the project's next seq, and leaves the
 * board at its next revision, with the undo that renames it back.
 */
function renamingServer() {
  const state = { name: "Harbour", rev: 3, seq: testProject.seq };
  const server = serve({
    getProject: () => ({ ...testProject, seq: state.seq }),
    listFolders: () => page([]),
    listBoards: () => page([board("bd_a", state.name, state.rev)]),
    applyCommands: ({ body }) => {
      const [command] = body?.commands ?? [];
      if (command?.type !== "board.rename") {
        throw new Error("unexpected batch");
      }
      const was = state.name;
      state.name = command.payload.name;
      state.rev++;
      state.seq++;
      return applied(
        state.seq,
        [{ id: "bd_a", rev: state.rev }],
        [renameBack("bd_a", was, state.rev)],
      );
    },
  });
  return { ...server, state };
}

describe("the undo history over a reload", () => {
  // Verifies: FR-EDIT-08/AC3
  it("keeps at least the last 50 steps over a reload, and undoes the latest first", async () => {
    const server = renamingServer();
    const before = await open();
    for (let i = 1; i <= 60; i++) {
      await before.doc.send([rename("bd_a", `Name ${i}`, server.state.rev)]);
    }
    before.doc.dispose();
    const { doc } = await open();
    expect(doc.store.getState().undoable).toBeGreaterThanOrEqual(50);
    await doc.undo();
    await doc.undo();
    expect(server.batches.slice(-2)).toEqual([
      [
        {
          ...renameBack("bd_a", "Name 59", 63),
          command_id: expect.any(String),
        },
      ],
      // What the second step expects is moved to what the first left.
      [
        {
          ...renameBack("bd_a", "Name 58", 64),
          command_id: expect.any(String),
        },
      ],
    ]);
    expect(shown(doc, "bd_a")?.name).toBe("Name 58");
  });

  it("keeps the latest steps that fit in a full storage, leaving out the oldest, rather than fail", async () => {
    const limit = 1_000;
    const setItem = Storage.prototype.setItem;
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(function (
      this: Storage,
      key: string,
      value: string,
    ) {
      if (value.length > limit) {
        throw new DOMException("The quota is exceeded.", "QuotaExceededError");
      }
      setItem.call(this, key, value);
    });
    const server = renamingServer();
    const before = await open();
    for (let i = 1; i <= 20; i++) {
      await before.doc.send([rename("bd_a", `Name ${i}`, server.state.rev)]);
    }
    expect(before.doc.store.getState().undoable).toBe(20);
    const kept = window.sessionStorage.getItem(
      historyKey(testSession.user_id, testProject.id),
    );
    expect(kept?.length).toBeLessThanOrEqual(limit);
    before.doc.dispose();
    const { doc } = await open();
    const { undoable } = doc.store.getState();
    expect(undoable).toBeGreaterThan(0);
    expect(undoable).toBeLessThan(20);
    await doc.undo();
    expect(server.batches.at(-1)).toEqual([
      { ...renameBack("bd_a", "Name 19", 23), command_id: expect.any(String) },
    ]);
  });

  it("is taken out of the tab's storage, with every other history, once the page is signed out", async () => {
    serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: () =>
        applied(
          2,
          [{ id: "bd_a", rev: 4 }],
          [renameBack("bd_a", "Harbour", 4)],
        ),
    });
    const log = createErrorLog();
    const queryClient = createQueryClient(log);
    queryClient.setQueryData(sessionQuery.queryKey, testSession);
    const documents = new Documents({
      queryClient,
      log,
      storage: window.sessionStorage,
    });
    onTestFinished(() => documents.close());
    const doc = documents.get(testProject.id);
    await doc.ready;
    await doc.load("tree");
    await doc.send([rename("bd_a", "Harbour at night", 3)]);
    const key = historyKey(testSession.user_id, testProject.id);
    const other = historyKey(testSession.user_id, "prj_other");
    expect(window.sessionStorage.getItem(key)).not.toBeNull();
    window.sessionStorage.setItem(other, "{}");
    window.sessionStorage.setItem("storycreep.workspace", "ws_studio");
    queryClient.setQueryData(sessionQuery.queryKey, null);
    await vi.waitFor(() =>
      expect(window.sessionStorage.getItem(key)).toBeNull(),
    );
    expect(window.sessionStorage.getItem(other)).toBeNull();
    expect(window.sessionStorage.getItem("storycreep.workspace")).toBe(
      "ws_studio",
    );
  });
});
