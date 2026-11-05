import {
  type ApiError,
  type Board,
  type CommandBody,
  type CommandResultBody,
  type Folder,
  NetworkError,
  type Node,
  type UndoCommandBody,
  type Variable,
} from "@storycreep/shared/api";
import { afterEach, describe, expect, it, onTestFinished, vi } from "vitest";
import { createQueryClient, sessionQuery } from "../api";
import { createErrorLog } from "../errors/log";
import { projectQuery } from "../project/queries";
import { testSession } from "../test/app";
import { fakeApi, refused, testProject } from "../test/fake";
import { fakeRealtime } from "../test/realtime";
import { boardPart } from "./doc";
import { Documents, keptUnshown } from "./documents";
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

/** Returns a node of the board of boardId of the test project. */
function node(id: string, boardId: string, rev = 3): Node {
  return {
    id,
    project_id: testProject.id,
    board_id: boardId,
    type: "element",
    kind: "dialogue",
    title: "",
    text: { type: "doc", content: [{ type: "paragraph" }] },
    color: "none",
    tags: [],
    position: { x: 0, y: 0 },
    is_ending: false,
    rev,
    created_at: "2026-11-01T10:00:00Z",
    updated_at: "2026-11-01T10:00:00Z",
  };
}

/** Returns a global variable of the test project. */
function variable(id: string): Variable {
  return {
    id,
    project_id: testProject.id,
    scope: "global",
    order: "V",
    key: id.slice("var_".length),
    type: "int",
    default: 0,
    description: "",
    reset_on_enter: false,
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

/** Returns a change of the description of the board of id that expects rev. */
function redescribe(id: string, description: string, rev: number): Command {
  return {
    type: "board.describe",
    payload: { id, description },
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
async function open(
  options: { checkSession?: () => void; now?: () => string } = {},
) {
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
    checkSession: options.checkSession,
    storage: window.sessionStorage,
    now: options.now ?? (() => time),
  });
  // The document goes after the setup has cleared the tab's storage for the
  // next test, which it writes what its history recorded to as it goes
  // (History.close): it is cleared again once it has.
  onTestFinished(() => {
    doc.dispose();
    window.sessionStorage.clear();
  });
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
  // The channel's resync_required: the operations after seq follow.
  const resync = (seq: number) => subscribers.at(-1)?.resync(seq);
  const subscriber = () => subscribers.at(-1) as Subscriber;
  return { doc, realtime, log, queryClient, deliver, resync, subscriber };
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

  it("reads the project again once its first read failed, as a page asks for a part again, rather than fail as that read did for as long as the document is kept", async () => {
    let projectReads = 0;
    serve({
      ...tree([board("bd_a", "Harbour")]),
      getProject: () => {
        if (++projectReads === 1) {
          throw refused(429, { code: "rate_limited" });
        }
        return testProject;
      },
    });
    const log = createErrorLog();
    const queryClient = createQueryClient(log);
    queryClient.setQueryData(sessionQuery.queryKey, testSession);
    const doc = new ProjectDocument(testProject.id, { queryClient, log });
    onTestFinished(() => doc.dispose());
    await expect(doc.load("tree")).rejects.toMatchObject({ status: 429 });
    await doc.load("tree");
    expect(projectReads).toBe(2);
    expect(shown(doc, "bd_a")).toMatchObject({ name: "Harbour" });
  });

  it("records a read of a part that failed, which a loader shows as the error page that offers the error console, but for one of what is not there, which it shows as the page of no page", async () => {
    let reads = 0;
    serve({
      ...tree([]),
      listVariables: () => {
        throw ++reads === 1
          ? refused(503, { code: "unavailable" })
          : refused(404, { code: "not_found" });
      },
    });
    const { doc, log } = await open();
    await expect(doc.load("variables")).rejects.toMatchObject({ status: 503 });
    expect(log.entries()).toMatchObject([{ source: "request", count: 1 }]);
    await expect(doc.load("variables")).rejects.toMatchObject({ status: 404 });
    expect(log.entries()).toHaveLength(1);
  });

  it("has the page ask for its session once a read is answered that the session may have ended, without recording it, as the app's queries do", async () => {
    vi.spyOn(Math, "random").mockReturnValue(1);
    let signedOut = false;
    serve({
      ...tree([board("bd_a", "Harbour")]),
      getProject: () => {
        if (signedOut) {
          throw refused(401, { code: "unauthenticated" });
        }
        return testProject;
      },
      listVariables: () => {
        throw refused(401, { code: "unauthenticated" });
      },
    });
    const checkSession = vi.fn();
    const { doc, log, deliver } = await open({ checkSession });
    await expect(doc.load("variables")).rejects.toMatchObject({ status: 401 });
    expect(checkSession).toHaveBeenCalledOnce();
    // A gap, after which the document reads the project anew, which fails.
    signedOut = true;
    deliver(3, [], []);
    await vi.waitFor(() => expect(checkSession).toHaveBeenCalledTimes(2));
    expect(log.entries()).toEqual([]);
  });

  it("holds a part whose own first read failed once the project read anew meanwhile has read it with the others, rather than take it out", async () => {
    const first = Promise.withResolvers<unknown>();
    let variableReads = 0;
    serve({
      ...tree([board("bd_a", "Harbour")]),
      listVariables: () =>
        ++variableReads === 1 ? first.promise : page([variable("var_met")]),
    });
    const { doc, deliver } = await open();
    const loaded = doc.load("variables");
    await vi.waitFor(() => expect(variableReads).toBe(1));
    // A gap, after which the document reads the project anew, with the
    // variables, which a page asked for.
    deliver(3, [], []);
    await vi.waitFor(() => expect(doc.doc.seq).toBe(3));
    first.reject(new NetworkError(new TypeError("Failed to fetch")));
    await loaded;
    expect(doc.holds("variables")).toBe(true);
    expect([...doc.store.getState().view.variables.keys()]).toEqual([
      "var_met",
    ]);
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

  it("reads the project anew once the read of what an operation's appliers could not know failed, rather than keep what they predicted", async () => {
    let boards = [board("bd_a", "Harbour")];
    const server = serve({
      getProject: () => testProject,
      listFolders: () => page([]),
      listBoards: () => page(boards),
      getBoard: () => {
        throw refused(503, { code: "unavailable" });
      },
    });
    const { doc, realtime } = await open();
    // Another session creates a board without a key of order, which the
    // server picks.
    boards = [
      board("bd_a", "Harbour"),
      { ...board("bd_c", "Lighthouse", 1), order: "X" },
    ];
    realtime.send(
      testProject.id,
      [
        {
          type: "board.create",
          payload: { id: "bd_c", name: "Lighthouse", slug: "c" },
        },
      ],
      [{ id: "bd_c", rev: 1 }],
    );
    expect(shown(doc, "bd_c")?.order).not.toBe("X");
    await vi.waitFor(() => expect(shown(doc, "bd_c")?.order).toBe("X"));
    expect(server.calls("getBoard")).toHaveLength(1);
    expect(server.calls("getProject")).toHaveLength(2);
  });

  it("drops a board that went to the trash, keeping that it went while a read that began before is under way, and forgetting it once none is", async () => {
    const listed = Promise.withResolvers<unknown>();
    serve({
      ...tree([board("bd_a", "Harbour"), board("bd_b", "Lighthouse")]),
      listVariables: () => listed.promise,
    });
    const { doc, realtime } = await open();
    const loaded = doc.load("variables");
    await flush();
    realtime.send(
      testProject.id,
      [{ type: "board.delete", payload: { id: "bd_a" } }],
      [{ id: "bd_a", rev: 4 }],
    );
    expect(shown(doc, "bd_a")).toBeUndefined();
    expect(shown(doc, "bd_b")).toBeDefined();
    expect(doc.doc.gone.get("bd_a")).toEqual({ rev: 4, seq: 2 });
    listed.resolve(page([]));
    await loaded;
    realtime.send(testProject.id, [], []);
    expect(doc.doc.gone.size).toBe(0);
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
  it("takes what the server picked of a board that another session created, which the appliers could not know, once it has read it at the same revision", async () => {
    const theirs = { ...board("bd_q", "Quay", 1), slug: "quay-2", order: "W" };
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      getBoard: () => theirs,
    });
    const { doc, realtime } = await open();
    realtime.send(
      testProject.id,
      [{ type: "board.create", payload: { id: "bd_q", name: "Quay" } }],
      [{ id: "bd_q", rev: 1 }],
    );
    await vi.waitFor(() =>
      expect(shown(doc, "bd_q")).toMatchObject({
        slug: "quay-2",
        order: "W",
        rev: 1,
      }),
    );
    expect(server.calls("getBoard")).toHaveLength(1);
  });

  it("tells the project's read what an operation took out of the project, as an unarchive does its archiving, with the role that it read", async () => {
    serve({
      ...tree([]),
      getProject: () => ({
        ...testProject,
        archived_at: "2026-11-04T10:00:00Z",
      }),
    });
    const { queryClient, realtime } = await open();
    realtime.send(
      testProject.id,
      [{ type: "project.unarchive", payload: {} }],
      [{ id: testProject.id, rev: 2 }],
    );
    const body = queryClient.getQueryData(
      projectQuery(testProject.id).queryKey,
    );
    expect(body).not.toHaveProperty("archived_at");
    expect(body).toMatchObject({ role: "owner", rev: 2, seq: 2 });
  });

  it("leaves the project's read as it is where it is as of a later seq than the document, as one that a page made once the document began from an earlier, until the operations up to it have come", async () => {
    serve(tree([]));
    const { queryClient, realtime } = await open();
    const key = projectQuery(testProject.id).queryKey;
    // The page read the project again, as its layout does once the read
    // that the document began from is stale, after two renames.
    queryClient.setQueryData(key, {
      ...testProject,
      name: "Lighthouse",
      rev: 3,
      seq: 3,
    });
    realtime.send(
      testProject.id,
      [{ type: "project.rename", payload: { name: "Harbour at night" } }],
      [{ id: testProject.id, rev: 2 }],
    );
    expect(queryClient.getQueryData(key)).toMatchObject({
      name: "Lighthouse",
      rev: 3,
      seq: 3,
    });
    realtime.send(
      testProject.id,
      [{ type: "project.rename", payload: { name: "Lighthouse" } }],
      [{ id: testProject.id, rev: 3 }],
    );
    realtime.send(
      testProject.id,
      [{ type: "project.rename", payload: { name: "Old light" } }],
      [{ id: testProject.id, rev: 4 }],
    );
    expect(queryClient.getQueryData(key)).toMatchObject({
      name: "Old light",
      rev: 4,
      seq: 4,
    });
  });

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
    const server = serve({
      ...tree([board("bd_a", "Harbour"), board("bd_b", "Lighthouse")]),
      applyCommands: () => applied(3, [{ id: "bd_a", rev: 4 }]),
    });
    const { doc, realtime } = await open();
    realtime.hold(testProject.id);
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
    realtime.send(testProject.id, server.batches[0], [{ id: "bd_a", rev: 4 }]);
    expect(doc.doc.seq).toBe(3);
    expect(doc.store.getState().view).toBe(doc.doc);
    expect(shown(doc, "bd_a")).toMatchObject({
      name: "Harbour at night",
      rev: 4,
    });
  });

  it("shows what the appliers were not sure of at the revision that the answer gave, which the next batch expects, and what the server has once it has read it", async () => {
    const read = Promise.withResolvers<unknown>();
    serve({
      ...tree([board("bd_a", "Harbour")], [folder("fld_a", "Act one")]),
      getBoard: () => read.promise,
      applyCommands: () => applied(2, [{ id: "bd_a", rev: 4 }]),
    });
    const { doc } = await open();
    // A move without a key of order, which the server picks.
    await doc.send([
      {
        type: "board.move",
        payload: { id: "bd_a", folder_id: "fld_a" },
        expected_rev: { bd_a: 3 },
      },
    ]);
    expect(shown(doc, "bd_a")).toMatchObject({ folder_id: "fld_a", rev: 4 });
    read.resolve({
      ...board("bd_a", "Harbour", 4),
      folder_id: "fld_a",
      order: "X",
    });
    await vi.waitFor(() =>
      expect(shown(doc, "bd_a")).toMatchObject({ order: "X", rev: 4 }),
    );
  });

  it("gives what a batch changed, which it took from the batch's answer, the server's time of its operation, which the channel brings after", async () => {
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: () => applied(2, [{ id: "bd_a", rev: 4 }]),
    });
    const { doc, realtime, subscriber } = await open();
    realtime.hold(testProject.id);
    await doc.send([rename("bd_a", "Harbour at night", 3)]);
    expect(doc.doc.boards.get("bd_a")).toMatchObject({
      rev: 4,
      updated_at: time,
    });
    // The server's clock, which the page's is not.
    const committed = "2026-11-05T10:00:01.234Z";
    subscriber().operation({
      type: "operation",
      project_id: testProject.id,
      seq: 2,
      commands: server.batches[0],
      revisions: [{ id: "bd_a", rev: 4 }],
      created_at: committed,
    });
    expect(doc.doc.boards.get("bd_a")).toMatchObject({
      name: "Harbour at night",
      rev: 4,
      created_at: "2026-11-01T10:00:00Z",
      updated_at: committed,
    });
    expect(shown(doc, "bd_a")?.updated_at).toBe(committed);
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

  it("moves what a batch expects past the page's own batch that applied on top of that revision, as a dialog's that opened before the answer to it came, rather than be refused for the page's own change", async () => {
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: inTurn(
        applied(2, [{ id: "bd_a", rev: 4 }]),
        applied(3, [{ id: "bd_a", rev: 5 }]),
      ),
    });
    const { doc } = await open();
    await doc.send([redescribe("bd_a", "Where the tale ends", 3)]);
    // A dialog that opened while the batch was under way holds the
    // revision that the board had then.
    await doc.send([rename("bd_a", "Harbour at night", 3)]);
    expect(server.batches[1][0].expected_rev).toEqual({ bd_a: 4 });
    expect(shown(doc, "bd_a")).toMatchObject({
      name: "Harbour at night",
      description: "Where the tale ends",
      rev: 5,
    });
  });

  it("moves what a batch expects past the page's own batches alone, and is refused for a change made elsewhere after them", async () => {
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: inTurn(
        applied(2, [{ id: "bd_a", rev: 4 }]),
        conflict(
          {
            ...board("bd_a", "Old harbour", 5),
            description: "Where the tale ends",
          },
          4,
        ),
      ),
    });
    const { doc, realtime } = await open();
    await doc.send([redescribe("bd_a", "Where the tale ends", 3)]);
    realtime.send(
      testProject.id,
      [{ type: "board.rename", payload: { id: "bd_a", name: "Old harbour" } }],
      [{ id: "bd_a", rev: 5 }],
    );
    const refusal = await doc
      .send([rename("bd_a", "Harbour at night", 3)])
      .catch((error: unknown) => error);
    expect(refusal).toBeInstanceOf(ConflictError);
    expect((refusal as ConflictError).overlaps[0].fields).toEqual([
      { field: "name", mine: "Harbour at night", theirs: "Old harbour" },
    ]);
    expect(server.batches[1][0].expected_rev).toEqual({ bd_a: 4 });
  });

  it("leaves the view as it was where nothing changed while a batch is under way, rather than apply the batch anew", async () => {
    const answer = Promise.withResolvers<CommandResultBody>();
    serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: () => answer.promise,
    });
    const { doc, deliver } = await open();
    const sent = doc.send([rename("bd_a", "Harbour at night", 3)]);
    const view = doc.store.getState().view;
    // An operation that the document has already, which it passes over.
    deliver(testProject.seq, [], []);
    expect(doc.store.getState().view).toBe(view);
    answer.resolve(applied(2, [{ id: "bd_a", rev: 4 }]));
    await sent;
    expect(shown(doc, "bd_a")).toMatchObject({
      name: "Harbour at night",
      rev: 4,
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

  it("gives a batch the same as one that is queued or under way IDs of its own, as once a change goes back and forth, each expecting what the answer before it left", async () => {
    const first = Promise.withResolvers<CommandResultBody>();
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: inTurn(
        first.promise,
        applied(3, [{ id: "bd_a", rev: 5 }]),
        applied(4, [{ id: "bd_a", rev: 6 }]),
      ),
    });
    const { doc } = await open();
    const sent = [
      doc.send([rename("bd_a", "Harbour at dusk", 3)]),
      doc.send([rename("bd_a", "Harbour at night", 3)]),
      doc.send([rename("bd_a", "Harbour at dusk", 3)]),
    ];
    await vi.waitFor(() => expect(server.batches).toHaveLength(1));
    first.resolve(applied(2, [{ id: "bd_a", rev: 4 }]));
    await Promise.all(sent);
    expect(
      new Set(server.batches.map((batch) => batch[0].command_id)).size,
    ).toBe(3);
    expect(server.batches.map((batch) => batch[0].expected_rev)).toEqual([
      { bd_a: 3 },
      { bd_a: 4 },
      { bd_a: 5 },
    ]);
    expect(shown(doc, "bd_a")).toMatchObject({
      name: "Harbour at dusk",
      rev: 6,
    });
  });

  it("sends a batch sent again as it was, queued once the operation of the batch that no answer came to comes, and is answered as that one", async () => {
    const other = Promise.withResolvers<CommandResultBody>();
    const server = serve({
      ...tree([board("bd_a", "Harbour"), board("bd_b", "Lighthouse")]),
      applyCommands: inTurn(
        new NetworkError(new TypeError("Failed to fetch")),
        other.promise,
        applied(2, [{ id: "bd_a", rev: 4 }]),
      ),
    });
    const { doc, realtime, deliver } = await open();
    realtime.hold(testProject.id);
    const night = [rename("bd_a", "Harbour at night", 3)];
    await expect(doc.send(night)).rejects.toBeInstanceOf(NetworkError);
    const light = doc.send([rename("bd_b", "Old light", 3)]);
    const again = doc.send(night);
    await vi.waitFor(() => expect(server.batches).toHaveLength(2));
    // The batch that no answer came to applied as seq 2, whose operation,
    // with its commands' IDs, comes while the one sent again waits behind
    // another.
    deliver(2, server.batches[0], [{ id: "bd_a", rev: 4 }]);
    other.resolve(applied(3, [{ id: "bd_b", rev: 4 }]));
    await light;
    await expect(again).resolves.toMatchObject({ status: "applied", seq: 2 });
    expect(server.batches[2][0].command_id).toBe(
      server.batches[0][0].command_id,
    );
    expect(doc.store.getState().sending).toBe(0);
    expect(doc.store.getState().view).toBe(doc.doc);
    expect(shown(doc, "bd_a")).toMatchObject({
      name: "Harbour at night",
      rev: 4,
    });
  });

  it("does not send a batch queued while the page signed in to another account as that account's", async () => {
    const first = Promise.withResolvers<CommandResultBody>();
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: inTurn(first.promise),
    });
    const { doc, queryClient } = await open();
    const dusk = doc.send([rename("bd_a", "Harbour at dusk", 3)]);
    const night = doc.send([rename("bd_a", "Harbour at night", 3)]);
    await vi.waitFor(() => expect(server.batches).toHaveLength(1));
    const other = {
      ...testSession,
      user_id: "usr_other",
      csrf_token: "other-csrf-token",
    };
    queryClient.setQueryData(sessionQuery.queryKey, other);
    first.resolve(applied(2, [{ id: "bd_a", rev: 4 }]));
    await dusk;
    await expect(night).rejects.toThrow(/another account/);
    expect(server.batches).toHaveLength(1);
  });
});

describe("a batch refused for a revision that changed elsewhere (§7.10)", () => {
  it("is sent again where what it changes changed elsewhere to what it writes, as once two users give a board the same name", async () => {
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: inTurn(
        conflict(board("bd_a", "Harbour at night", 4)),
        applied(2, [{ id: "bd_a", rev: 5 }]),
      ),
    });
    const { doc } = await open();
    await doc.send([rename("bd_a", "Harbour at night", 3)]);
    expect(server.batches.map((batch) => batch[0].expected_rev)).toEqual([
      { bd_a: 3 },
      { bd_a: 4 },
    ]);
  });

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

  it("is refused with both values of a field that changed elsewhere since the revision that it expects, though the page shows the entity as it is now, as a form that holds what it showed does", async () => {
    const theirs = {
      ...board("bd_a", "Harbour", 4),
      description: "Where the tale ends",
    };
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: inTurn(conflict(theirs)),
    });
    const { doc, realtime } = await open();
    realtime.send(
      testProject.id,
      [
        {
          type: "board.describe",
          payload: { id: "bd_a", description: "Where the tale ends" },
        },
      ],
      [{ id: "bd_a", rev: 4 }],
    );
    expect(shown(doc, "bd_a")?.rev).toBe(4);
    const error = await doc
      .send([redescribe("bd_a", "Where it begins", 3)])
      .catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ConflictError);
    expect((error as ConflictError).overlaps[0].fields).toEqual([
      {
        field: "description",
        mine: "Where it begins",
        theirs: "Where the tale ends",
      },
    ]);
    expect(server.batches).toHaveLength(1);
  });

  it("is sent again where what changed elsewhere since the revision that it expects is none of what it changes, though the page shows the entity as it is now", async () => {
    const theirs = { ...board("bd_a", "Harbour", 4), color: "blue" as const };
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: inTurn(
        conflict(theirs),
        applied(3, [{ id: "bd_a", rev: 5 }]),
      ),
    });
    const { doc, realtime } = await open();
    realtime.send(
      testProject.id,
      [{ type: "board.set_color", payload: { id: "bd_a", color: "blue" } }],
      [{ id: "bd_a", rev: 4 }],
    );
    await doc.send([redescribe("bd_a", "Where it begins", 3)]);
    expect(server.batches.map((batch) => batch[0].expected_rev)).toEqual([
      { bd_a: 3 },
      { bd_a: 4 },
    ]);
    expect(shown(doc, "bd_a")).toMatchObject({
      description: "Where it begins",
      color: "blue",
      rev: 5,
    });
  });

  it("is sent again where what changed elsewhere, which the project read anew brought, is none of what it changes, as a form that holds what it showed before expects", async () => {
    const theirs = { ...board("bd_a", "Harbour", 4), color: "blue" as const };
    let boards = [board("bd_a", "Harbour")];
    const server = serve({
      getProject: () => testProject,
      listFolders: () => page([]),
      listBoards: () => page(boards),
      applyCommands: inTurn(
        conflict(theirs),
        applied(4, [{ id: "bd_a", rev: 5 }]),
      ),
    });
    const { doc, deliver } = await open();
    // A gap, after which the document reads the board anew, as it is now.
    boards = [theirs];
    deliver(3, [], []);
    await vi.waitFor(() => expect(shown(doc, "bd_a")?.rev).toBe(4));
    await doc.send([redescribe("bd_a", "Where it begins", 3)]);
    expect(server.batches.map((batch) => batch[0].expected_rev)).toEqual([
      { bd_a: 3 },
      { bd_a: 4 },
    ]);
    expect(shown(doc, "bd_a")).toMatchObject({
      description: "Where it begins",
      color: "blue",
      rev: 5,
    });
  });

  it("is refused with both values of each field that it changes that has another value now, where the document no longer knows the entity at the revision that it expects", async () => {
    const theirs = {
      ...board("bd_a", "Harbour", 4),
      description: "Where the tale ends",
      color: "blue" as const,
    };
    // A thousand other boards, whose states before the project read anew
    // the document keeps after the board's, which it forgets.
    const others = Array.from({ length: 1_000 }, (_, i) =>
      board(`bd_${i}`, `Board ${i}`),
    );
    let boards = [board("bd_a", "Harbour"), ...others];
    serve({
      getProject: () => testProject,
      listFolders: () => page([]),
      listBoards: () => page(boards),
      applyCommands: inTurn(conflict(theirs)),
    });
    const { doc, deliver } = await open();
    // A gap, after which the document reads the boards anew, as they are now.
    boards = [theirs, ...others.map((b) => ({ ...b, rev: 4 }))];
    deliver(3, [], []);
    await vi.waitFor(() => expect(shown(doc, "bd_a")?.rev).toBe(4));
    const error = await doc
      .send([redescribe("bd_a", "Where it begins", 3)])
      .catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ConflictError);
    expect((error as ConflictError).overlaps[0].fields).toEqual([
      {
        field: "description",
        mine: "Where it begins",
        theirs: "Where the tale ends",
      },
    ]);
  });

  it("is refused for a field that changed elsewhere, rather than written over it, where it was made on top of a batch that was sent again before it", async () => {
    // Another session changed the board's description, after which the
    // page's rename, made before the page knew, is refused and sent again,
    // and the page's change of the description, made on top of the rename
    // while it was under way, does not expect what the other session wrote.
    const theirs = { ...board("bd_a", "Harbour", 4), description: "Theirs" };
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: inTurn(
        conflict(theirs),
        applied(3, [{ id: "bd_a", rev: 5 }]),
        conflict({ ...theirs, name: "Harbour at dusk", rev: 5 }),
      ),
    });
    const { doc, realtime } = await open();
    realtime.hold(testProject.id);
    const dusk = doc.send([rename("bd_a", "Harbour at dusk", 3)]);
    const mine = doc.send([redescribe("bd_a", "Mine", 3)]);
    await dusk;
    const error = await mine.catch((e: unknown) => e);
    expect(server.batches.map((batch) => batch[0].expected_rev)).toEqual([
      { bd_a: 3 },
      { bd_a: 4 },
      { bd_a: 3 },
    ]);
    expect(error).toBeInstanceOf(ConflictError);
    expect((error as ConflictError).overlaps[0].fields).toEqual([
      { field: "description", mine: "Mine", theirs: "Theirs" },
    ]);
    expect(shown(doc, "bd_a")).toMatchObject({
      name: "Harbour at dusk",
      description: "Theirs",
      rev: 5,
    });
  });

  it("is sent again where a field that it changes has the value elsewhere that the page showed, whatever the order of its object's fields", async () => {
    const theirs = {
      ...node("nd_a", "bd_a", 4),
      title: "Theirs",
      position: { y: 0, x: 0 },
    };
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      listBoardNodes: () => page([node("nd_a", "bd_a")]),
      listBoardConnections: () => page([]),
      applyCommands: inTurn(
        refused(409, {
          code: "revision_conflict",
          details: {
            conflicts: [
              {
                command: 0,
                command_id: "00000000-0000-7000-8000-000000000001",
                type: "node.move",
                id: "nd_a",
                expected_rev: 3,
                exists: true,
                current_rev: 4,
              },
            ],
            states: { nd_a: theirs },
          },
        }),
        applied(2, [{ id: "nd_a", rev: 5 }]),
      ),
    });
    const { doc } = await open();
    await doc.load(boardPart("bd_a"));
    await doc.send([
      {
        type: "node.move",
        payload: { nodes: [{ id: "nd_a", position: { x: 10, y: 0 } }] },
        expected_rev: { nd_a: 3 },
      },
    ]);
    expect(server.batches.map((batch) => batch[0].expected_rev)).toEqual([
      { nd_a: 3 },
      { nd_a: 4 },
    ]);
  });

  it("is refused for a field that changed elsewhere where it was made on top of a batch answered with already_applied, rather than moved to the revisions that the answer gives, which may be past that change", async () => {
    const first = Promise.withResolvers<CommandResultBody>();
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: inTurn(
        first.promise,
        conflict(
          { ...board("bd_a", "Harbour at night", 5), description: "Theirs" },
          3,
        ),
      ),
    });
    const { doc } = await open();
    const sent = doc.send([rename("bd_a", "Harbour at night", 3)]);
    const next = doc.send([redescribe("bd_a", "Mine", 3)]);
    first.resolve({
      status: "already_applied",
      seq: 3,
      revisions: [{ id: "bd_a", rev: 5 }],
      not_reconnected: [],
      undo: null,
    });
    await sent;
    const error = await next.catch((e: unknown) => e);
    expect(server.batches[1][0].expected_rev).toEqual({ bd_a: 3 });
    expect(error).toBeInstanceOf(ConflictError);
    expect((error as ConflictError).overlaps[0].fields).toEqual([
      { field: "description", mine: "Mine", theirs: "Theirs" },
    ]);
  });

  it("reads an entity that the refusal gives in the trash, and no longer shows it once the server has none", async () => {
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      getBoard: () => {
        throw refused(404, { code: "not_found" });
      },
      applyCommands: inTurn(
        conflict({
          ...board("bd_a", "Harbour", 4),
          deleted_at: "2026-11-05T09:00:00Z",
        } as Board),
      ),
    });
    const { doc } = await open();
    const error = await doc
      .send([rename("bd_a", "Harbour at night", 3)])
      .catch((e: unknown) => e);
    expect((error as ConflictError).overlaps[0]).toMatchObject({
      id: "bd_a",
      gone: true,
    });
    await vi.waitFor(() => expect(shown(doc, "bd_a")).toBeUndefined());
    expect(server.calls("getBoard")).toHaveLength(1);
  });

  it("tells the project's read the state of the project that a refusal for its revision gave", async () => {
    serve({
      ...tree([]),
      applyCommands: inTurn(
        refused(409, {
          code: "revision_conflict",
          details: {
            conflicts: [
              {
                command: 0,
                command_id: "00000000-0000-7000-8000-000000000001",
                type: "project.rename",
                id: testProject.id,
                expected_rev: 1,
                exists: true,
                current_rev: 2,
              },
            ],
            states: {
              [testProject.id]: { ...testProject, name: "Theirs", rev: 2 },
            },
          },
        }),
      ),
    });
    const { doc, queryClient } = await open();
    const error = await doc
      .send([
        {
          type: "project.rename",
          payload: { name: "Mine" },
          expected_rev: { [testProject.id]: 1 },
        },
      ])
      .catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ConflictError);
    expect(
      queryClient.getQueryData(projectQuery(testProject.id).queryKey),
    ).toMatchObject({ name: "Theirs", rev: 2, role: "owner" });
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

  it("keeps a step that the server refused for when it was sent, as with too many requests, or for a failure of its own, which may apply later", async () => {
    serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: inTurn(
        applied(
          2,
          [{ id: "bd_a", rev: 4 }],
          [renameBack("bd_a", "Harbour", 4)],
        ),
        refused(429, { code: "rate_limited" }),
        refused(503, { code: "not_ready" }),
        applied(3, [{ id: "bd_a", rev: 5 }], []),
      ),
    });
    const { doc } = await open();
    await doc.send([rename("bd_a", "Harbour at night", 3)]);
    await expect(doc.undo()).rejects.toMatchObject({ status: 429 });
    expect(doc.store.getState()).toMatchObject({
      undoable: 1,
      stepping: false,
    });
    await expect(doc.undo()).rejects.toMatchObject({ status: 503 });
    expect(doc.store.getState().undoable).toBe(1);
    await doc.undo();
    expect(shown(doc, "bd_a")).toMatchObject({ name: "Harbour", rev: 5 });
    expect(doc.store.getState().undoable).toBe(0);
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

  it("moves what the other steps expect from what a batch expected, whose operation came before the answer to it sent again, as once no answer came to it at first", async () => {
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: inTurn(
        applied(
          2,
          [{ id: "bd_a", rev: 4 }],
          [renameBack("bd_a", "Harbour", 4)],
        ),
        new NetworkError(new TypeError("Failed to fetch")),
        // The batch sent again, which applied the first time, as seq 3, is
        // answered as it was then.
        applied(3, [{ id: "bd_a", rev: 5 }], [renameBack("bd_a", "Dusk", 5)]),
        applied(4, [{ id: "bd_a", rev: 6 }], []),
        applied(5, [{ id: "bd_a", rev: 7 }], []),
      ),
    });
    const { doc, realtime } = await open();
    realtime.hold(testProject.id);
    await doc.send([rename("bd_a", "Dusk", 3)]);
    const night = [rename("bd_a", "Night", 4)];
    await expect(doc.send(night)).rejects.toBeInstanceOf(NetworkError);
    realtime.send(testProject.id, server.batches[1], [{ id: "bd_a", rev: 5 }]);
    await doc.send(night);
    await doc.undo();
    expect(server.batches[3][0].expected_rev).toEqual({ bd_a: 5 });
    // The board was at 4 before the batch that the undo undid, which the
    // first batch's undo expects, and is the same again, at 6.
    await doc.undo();
    expect(server.batches[4][0]).toMatchObject({
      payload: { id: "bd_a", name: "Harbour" },
      expected_rev: { bd_a: 6 },
    });
  });

  it("sends a batch that the page made while a redo waited for the operations before it once the redo has applied, as it was asked for after it, rather than end what the redo would redo", async () => {
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
        applied(
          4,
          [{ id: "bd_a", rev: 5 }],
          [renameBack("bd_a", "Harbour at night", 5)],
        ),
        applied(
          5,
          [{ id: "bd_a", rev: 6 }],
          [renameBack("bd_a", "Harbour", 6)],
        ),
        applied(
          6,
          [{ id: "bd_b", rev: 4 }],
          [renameBack("bd_b", "Lighthouse", 4)],
        ),
      ),
    });
    const { doc, realtime } = await open();
    realtime.hold(testProject.id);
    await doc.send([rename("bd_a", "Harbour at night", 3)]);
    await doc.undo();
    expect(doc.store.getState().redoable).toBe(1);
    const redone = doc.redo();
    await flush();
    const sent = doc.send([rename("bd_b", "Old light", 3)]);
    await flush();
    // The batch waits for the redo, which waits for the operations up to
    // the undo that it redoes, and the page shows it at once all the same.
    expect(server.batches).toHaveLength(2);
    expect(doc.store.getState().redoable).toBe(1);
    expect(shown(doc, "bd_b")).toMatchObject({ name: "Old light" });
    expect(shown(doc, "bd_a")).toMatchObject({ name: "Harbour" });
    expect(doc.store.getState().sending).toBe(1);
    realtime.send(
      testProject.id,
      [{ type: "board.describe", payload: { id: "bd_b", description: "" } }],
      [],
    );
    realtime.send(testProject.id, server.batches[1], [{ id: "bd_a", rev: 5 }]);
    await expect(redone).resolves.toBe(true);
    await sent;
    expect(server.batches[2][0]).toMatchObject({
      payload: { id: "bd_a", name: "Harbour at night" },
      expected_rev: { bd_a: 5 },
    });
    expect(server.batches[3][0]).toMatchObject({
      payload: { id: "bd_b", name: "Old light" },
      expected_rev: { bd_b: 3 },
    });
    expect(doc.store.getState()).toMatchObject({
      undoable: 2,
      redoable: 0,
      stepping: false,
    });
  });

  it("shows a batch that the page made while an undo waited as of the time that it was made at, once it is queued too, rather than as of then", async () => {
    const queued = Promise.withResolvers<CommandResultBody>();
    const server = serve({
      ...tree([board("bd_a", "Harbour"), board("bd_b", "Lighthouse")]),
      applyCommands: inTurn(
        applied(
          3,
          [{ id: "bd_a", rev: 4 }],
          [renameBack("bd_a", "Harbour", 4)],
        ),
        applied(4, [{ id: "bd_a", rev: 5 }], []),
        queued.promise,
      ),
    });
    let now = "2026-11-05T10:00:01Z";
    const { doc, realtime, deliver } = await open({ now: () => now });
    realtime.hold(testProject.id);
    await doc.send([rename("bd_a", "Harbour at night", 3)]);
    const undone = doc.undo();
    await flush();
    void doc.send([rename("bd_b", "Old light", 3)]);
    expect(shown(doc, "bd_b")?.updated_at).toBe("2026-11-05T10:00:01Z");
    // The undo waits for the operations up to the batch that it undoes.
    now = "2026-11-05T10:00:05Z";
    deliver(2, [], []);
    deliver(3, server.batches[0], [{ id: "bd_a", rev: 4 }]);
    await undone;
    await vi.waitFor(() => expect(server.batches).toHaveLength(3));
    expect(shown(doc, "bd_b")).toMatchObject({
      name: "Old light",
      updated_at: "2026-11-05T10:00:01Z",
    });
    queued.resolve(applied(5, [{ id: "bd_b", rev: 4 }], []));
  });

  it("undoes the page's batch that was under way when it was asked for, once its answer has joined the history, rather than the step before it, over it", async () => {
    const night = Promise.withResolvers<CommandResultBody>();
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: inTurn(
        applied(
          2,
          [{ id: "bd_a", rev: 4 }],
          [renameBack("bd_a", "Harbour", 4)],
        ),
        night.promise,
        applied(4, [{ id: "bd_a", rev: 6 }], [renameBack("bd_a", "Night", 6)]),
      ),
    });
    const { doc } = await open();
    await doc.send([rename("bd_a", "Dusk", 3)]);
    const sent = doc.send([rename("bd_a", "Night", 4)]);
    await vi.waitFor(() => expect(server.batches).toHaveLength(2));
    const undone = doc.undo();
    night.resolve(
      applied(3, [{ id: "bd_a", rev: 5 }], [renameBack("bd_a", "Dusk", 5)]),
    );
    await sent;
    await expect(undone).resolves.toBe(true);
    expect(server.batches[2]).toEqual([
      { ...renameBack("bd_a", "Dusk", 5), command_id: expect.any(String) },
    ]);
    expect(shown(doc, "bd_a")).toMatchObject({ name: "Dusk", rev: 6 });
    expect(doc.store.getState()).toMatchObject({ undoable: 1, redoable: 1 });
  });

  it("undoes the step that was the latest once the page's batches before it have joined the history, and sends a batch that the page made once the undo was asked for after it, moved past what the undo left, rather than undo that batch, which the user did not ask to undo", async () => {
    const night = Promise.withResolvers<CommandResultBody>();
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: inTurn(
        applied(
          2,
          [{ id: "bd_a", rev: 4 }],
          [renameBack("bd_a", "Harbour", 4)],
        ),
        night.promise,
        applied(4, [{ id: "bd_a", rev: 6 }], [renameBack("bd_a", "Night", 6)]),
        applied(5, [{ id: "bd_a", rev: 7 }], [renameBack("bd_a", "Dusk", 7)]),
      ),
    });
    const { doc } = await open();
    await doc.send([rename("bd_a", "Dusk", 3)]);
    const nightSent = doc.send([rename("bd_a", "Night", 4)]);
    await vi.waitFor(() => expect(server.batches).toHaveLength(2));
    const undone = doc.undo();
    // A change made once the undo was asked for, while it waits, of what
    // the page showed then.
    const dawnSent = doc.send([rename("bd_a", "Dawn", 4)]);
    night.resolve(
      applied(3, [{ id: "bd_a", rev: 5 }], [renameBack("bd_a", "Dusk", 5)]),
    );
    await nightSent;
    await expect(undone).resolves.toBe(true);
    await dawnSent;
    expect(server.batches[2]).toEqual([
      { ...renameBack("bd_a", "Dusk", 5), command_id: expect.any(String) },
    ]);
    // What the change expects is moved past the page's own batches that
    // applied on top of what it expected: the one under way, and the undo.
    expect(server.batches[3]).toEqual([
      { ...rename("bd_a", "Dawn", 6), command_id: expect.any(String) },
    ]);
    expect(shown(doc, "bd_a")).toMatchObject({ name: "Dawn", rev: 7 });
    expect(doc.store.getState()).toMatchObject({ undoable: 2, redoable: 0 });
  });

  it("sends what was asked for while a step of the history was under way in the order that it was asked for, the page's batches among the steps", async () => {
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
          [{ id: "bd_a", rev: 5 }],
          [renameBack("bd_a", "Harbour at night", 5)],
        ),
        applied(
          4,
          [{ id: "bd_b", rev: 4 }],
          [renameBack("bd_b", "Lighthouse", 4)],
        ),
        applied(
          5,
          [{ id: "bd_b", rev: 5 }],
          [renameBack("bd_b", "Old light", 5)],
        ),
        applied(
          6,
          [{ id: "bd_b", rev: 6 }],
          [renameBack("bd_b", "Lighthouse", 6)],
        ),
        applied(
          7,
          [{ id: "bd_a", rev: 6 }],
          [renameBack("bd_a", "Harbour", 6)],
        ),
      ),
    });
    const { doc } = await open();
    await doc.send([rename("bd_a", "Harbour at night", 3)]);
    // An undo, a change, and then a redo, which the change has ended.
    const undone = doc.undo();
    const sent = doc.send([rename("bd_b", "Old light", 3)]);
    const redone = doc.redo();
    await expect(undone).resolves.toBe(true);
    await sent;
    await expect(redone).resolves.toBe(false);
    expect(server.batches.map((b) => b[0].payload)).toEqual([
      { id: "bd_a", name: "Harbour at night" },
      { id: "bd_a", name: "Harbour" },
      { id: "bd_b", name: "Old light" },
    ]);
    // A redo, and then a change, which waits for it.
    await doc.undo();
    const redoneAgain = doc.redo();
    const sentAgain = doc.send([rename("bd_a", "Dusk", 5)]);
    await expect(redoneAgain).resolves.toBe(true);
    await sentAgain;
    expect(server.batches.slice(3).map((b) => b[0].payload)).toEqual([
      { id: "bd_b", name: "Lighthouse" },
      { id: "bd_b", name: "Old light" },
      { id: "bd_a", name: "Dusk" },
    ]);
  });

  it("sends a redo asked for while an undo is under way once the undo has settled, rather than settle as the undo", async () => {
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
    const undone = doc.undo();
    const redone = doc.redo();
    await expect(undone).resolves.toBe(true);
    await expect(redone).resolves.toBe(true);
    expect(server.batches[2][0]).toMatchObject({
      payload: { id: "bd_a", name: "Harbour at night" },
      expected_rev: { bd_a: 5 },
    });
    expect(shown(doc, "bd_a")).toMatchObject({
      name: "Harbour at night",
      rev: 6,
    });
  });

  it("sends no redo, and resolves with none, once a change of the page's that was under way when it was asked for has ended what could be redone", async () => {
    const lighthouse = Promise.withResolvers<CommandResultBody>();
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
          [{ id: "bd_a", rev: 5 }],
          [renameBack("bd_a", "Harbour at night", 5)],
        ),
        lighthouse.promise,
      ),
    });
    const { doc } = await open();
    await doc.send([rename("bd_a", "Harbour at night", 3)]);
    await doc.undo();
    const sent = doc.send([rename("bd_b", "Old light", 3)]);
    await vi.waitFor(() => expect(server.batches).toHaveLength(3));
    const redone = doc.redo();
    lighthouse.resolve(
      applied(
        4,
        [{ id: "bd_b", rev: 4 }],
        [renameBack("bd_b", "Lighthouse", 4)],
      ),
    );
    await sent;
    await expect(redone).resolves.toBe(false);
    expect(server.batches).toHaveLength(3);
    expect(doc.store.getState()).toMatchObject({
      undoable: 1,
      redoable: 0,
      stepping: false,
    });
  });

  it("settles an undo asked for while another is under way as that one, as once it is refused", async () => {
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: inTurn(
        applied(
          2,
          [{ id: "bd_a", rev: 4 }],
          [renameBack("bd_a", "Harbour", 4)],
        ),
        conflict(board("bd_a", "Old harbour", 5), 4),
      ),
    });
    const { doc } = await open();
    await doc.send([rename("bd_a", "Harbour at night", 3)]);
    const first = doc.undo();
    const second = doc.undo();
    await expect(first).rejects.toBeInstanceOf(ConflictError);
    await expect(second).rejects.toBeInstanceOf(ConflictError);
    expect(server.batches).toHaveLength(2);
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

  it("is taken out of the tab's storage once the page finds itself signed out as it loads, as once the session ended while the tab reloaded", async () => {
    const key = historyKey(testSession.user_id, testProject.id);
    window.sessionStorage.setItem(key, "{}");
    const log = createErrorLog();
    const queryClient = createQueryClient(log);
    const documents = new Documents({
      queryClient,
      log,
      storage: window.sessionStorage,
    });
    onTestFinished(() => documents.close());
    // The page's first answer of its session: it has none.
    queryClient.setQueryData(sessionQuery.queryKey, null);
    await vi.waitFor(() =>
      expect(window.sessionStorage.getItem(key)).toBeNull(),
    );
  });

  it("is not written to the tab's storage again by the answer to a batch that comes once the page is signed out", async () => {
    const answer = Promise.withResolvers<CommandResultBody>();
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: () => answer.promise,
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
    const sent = doc.send([rename("bd_a", "Harbour at night", 3)]);
    await vi.waitFor(() => expect(server.batches).toHaveLength(1));
    queryClient.setQueryData(sessionQuery.queryKey, null);
    answer.resolve(
      applied(2, [{ id: "bd_a", rev: 4 }], [renameBack("bd_a", "Harbour", 4)]),
    );
    await sent;
    expect(
      window.sessionStorage.getItem(
        historyKey(testSession.user_id, testProject.id),
      ),
    ).toBeNull();
  });

  it("writes a full storage a few times for each change, rather than once for each step that it leaves out", async () => {
    const limit = 1_000;
    let writes = 0;
    const setItem = Storage.prototype.setItem;
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(function (
      this: Storage,
      key: string,
      value: string,
    ) {
      writes++;
      if (value.length > limit) {
        throw new DOMException("The quota is exceeded.", "QuotaExceededError");
      }
      setItem.call(this, key, value);
    });
    const server = renamingServer();
    const { doc } = await open();
    for (let i = 1; i <= 100; i++) {
      await doc.send([rename("bd_a", `Name ${i}`, server.state.rev)]);
    }
    writes = 0;
    await doc.send([rename("bd_a", "Name 101", server.state.rev)]);
    // Leaving out one step at a time, of the 101 that do not fit, would
    // write the history some 90 times for each time that it keeps it.
    expect(writes).toBeGreaterThan(0);
    expect(writes).toBeLessThan(30);
    expect(doc.store.getState().undoable).toBe(101);
  });

  it("writes the history once for each batch that joins it, and for each step that applies, rather than once for each of what that does to it", async () => {
    const server = renamingServer();
    const { doc } = await open();
    await doc.send([rename("bd_a", "Name 1", server.state.rev)]);
    const key = historyKey(testSession.user_id, testProject.id);
    const setItem = vi.spyOn(Storage.prototype, "setItem");
    const writes = () => setItem.mock.calls.filter(([k]) => k === key).length;
    await doc.send([rename("bd_a", "Name 2", server.state.rev)]);
    expect(writes()).toBe(1);
    setItem.mockClear();
    await doc.undo();
    expect(writes()).toBe(1);
    expect(doc.store.getState()).toMatchObject({ undoable: 1, redoable: 1 });
  });

  it("serialises each step once, rather than the whole history each time that it writes it", async () => {
    const server = renamingServer();
    const { doc } = await open();
    for (let i = 1; i <= 20; i++) {
      await doc.send([rename("bd_a", `Name ${i}`, server.state.rev)]);
    }
    const stringify = vi.spyOn(JSON, "stringify");
    await doc.send([rename("bd_a", "Name 21", server.state.rev)]);
    const serialised = stringify.mock.results.reduce(
      (n, result) =>
        n + (typeof result.value === "string" ? result.value.length : 0),
      0,
    );
    stringify.mockRestore();
    const kept =
      window.sessionStorage.getItem(
        historyKey(testSession.user_id, testProject.id),
      ) ?? "";
    expect(doc.store.getState().undoable).toBe(21);
    expect(serialised).toBeLessThan(kept.length / 4);
  });

  it("serialises again only the steps that an undo moves, rather than every step of the history", async () => {
    const server = renamingServer();
    const { doc } = await open();
    for (let i = 1; i <= 20; i++) {
      await doc.send([rename("bd_a", `Name ${i}`, server.state.rev)]);
    }
    const stringify = vi.spyOn(JSON, "stringify");
    await doc.undo();
    const serialised = stringify.mock.results.reduce(
      (n, result) =>
        n + (typeof result.value === "string" ? result.value.length : 0),
      0,
    );
    stringify.mockRestore();
    const kept =
      window.sessionStorage.getItem(
        historyKey(testSession.user_id, testProject.id),
      ) ?? "";
    expect(doc.store.getState()).toMatchObject({ undoable: 19, redoable: 1 });
    expect(serialised).toBeLessThan(kept.length / 4);
  });

  it("is none where what the tab's storage holds of it cannot be read, rather than a part of one", async () => {
    serve(tree([board("bd_a", "Harbour")]));
    window.sessionStorage.setItem(
      historyKey(testSession.user_id, testProject.id),
      JSON.stringify({ v: 1, redo: [], captured: {} }),
    );
    const { doc } = await open();
    expect(doc.store.getState()).toMatchObject({ undoable: 0, redoable: 0 });
  });
});

describe("what the document reads while operations come", () => {
  it("reads a part that a page asks for while the project is read anew with the others", async () => {
    const project = Promise.withResolvers<unknown>();
    let projectReads = 0;
    serve({
      getProject: () => (++projectReads === 2 ? project.promise : testProject),
      listFolders: () => page([]),
      listBoards: () => page([board("bd_a", "Harbour")]),
      listVariables: () => page([variable("var_met")]),
    });
    const { doc, deliver } = await open();
    // A gap, after which the document reads the project anew.
    deliver(3, [], [{ id: "bd_a", rev: 3 }]);
    await vi.waitFor(() => expect(projectReads).toBe(2));
    const loaded = doc.load("variables");
    await flush();
    project.resolve(testProject);
    await loaded;
    await vi.waitFor(() => expect(doc.doc.seq).toBe(3));
    expect(doc.holds("variables")).toBe(true);
    expect([...doc.store.getState().view.variables.keys()]).toEqual([
      "var_met",
    ]);
  });

  it("holds a board's part no longer once the board went to the trash, and reads it anew once asked for again", async () => {
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      getBoard: () => board("bd_a", "Harbour", 5),
      listBoardNodes: () => page([node("nd_a", "bd_a")]),
      listBoardConnections: () => page([]),
    });
    const { doc, realtime } = await open();
    await doc.load("board:bd_a");
    expect(doc.store.getState().view.nodes.has("nd_a")).toBe(true);
    realtime.send(
      testProject.id,
      [{ type: "board.delete", payload: { id: "bd_a" } }],
      [{ id: "bd_a", rev: 4 }],
    );
    expect(doc.holds("board:bd_a")).toBe(false);
    expect(doc.store.getState().view.nodes.size).toBe(0);
    realtime.send(
      testProject.id,
      [{ type: "board.restore", payload: { id: "bd_a" } }],
      [{ id: "bd_a", rev: 5 }],
    );
    await vi.waitFor(() => expect(shown(doc, "bd_a")?.rev).toBe(5));
    await doc.load("board:bd_a");
    expect(server.calls("listBoardNodes")).toHaveLength(2);
    expect(doc.store.getState().view.nodes.has("nd_a")).toBe(true);
  });

  it("reads a part again whole, rather than each of its entities by its own read, once an operation leaves many of them to read", async () => {
    const nodes = Array.from({ length: 25 }, (_, i) => node(`nd_${i}`, "bd_a"));
    let rev = 3;
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      listBoardNodes: () => page(nodes.map((n) => ({ ...n, rev }))),
      listBoardConnections: () => page([]),
    });
    const { doc, realtime } = await open();
    await doc.load("board:bd_a");
    rev = 4;
    // A variable's rename, which compiles anew the expressions of the
    // board's nodes that name it.
    realtime.send(
      testProject.id,
      [{ type: "variable.rename", payload: { id: "var_met", key: "met" } }],
      [{ id: "var_met", rev: 2 }, ...nodes.map((n) => ({ id: n.id, rev: 4 }))],
    );
    await vi.waitFor(() => expect(doc.doc.nodes.get("nd_24")?.rev).toBe(4));
    expect(server.calls("listBoardNodes")).toHaveLength(2);
    expect(server.calls("getNode")).toHaveLength(0);
  });

  it("keeps a folder that an operation put in while the folders were read, which their list did not hold", async () => {
    const folders = Promise.withResolvers<unknown>();
    let folderReads = 0;
    serve({
      getProject: () => testProject,
      listFolders: () => (++folderReads === 2 ? folders.promise : page([])),
      listBoards: () => page([]),
    });
    const { doc, realtime } = await open();
    // A folder that came back from the trash, which the document reads.
    realtime.send(
      testProject.id,
      [{ type: "folder.restore", payload: { id: "fld_b" } }],
      [{ id: "fld_b", rev: 2 }],
    );
    await vi.waitFor(() => expect(folderReads).toBe(2));
    realtime.send(
      testProject.id,
      [
        {
          type: "folder.create",
          payload: {
            id: "fld_c",
            kind: "board",
            name: "Act three",
            order: "W",
          },
        },
      ],
      [{ id: "fld_c", rev: 1 }],
    );
    folders.resolve(page([folder("fld_b", "Act two")]));
    await vi.waitFor(() =>
      expect(doc.store.getState().view.folders.has("fld_b")).toBe(true),
    );
    expect(doc.store.getState().view.folders.has("fld_c")).toBe(true);
  });

  it("reads the folders of the part of a folder's kind alone once an operation leaves the folder to read, rather than those of every part", async () => {
    const server = serve({
      ...tree([]),
      listVariables: () => page([]),
      listComponentTypes: () => page([]),
      listComponents: () => page([]),
    });
    const { doc, realtime } = await open();
    await doc.load("variables");
    await doc.load("components");
    const before = server.calls("listFolders").length;
    // A folder created without a key of order, which the server picks.
    realtime.send(
      testProject.id,
      [
        {
          type: "folder.create",
          payload: { id: "fld_c", kind: "board", name: "Act three" },
        },
      ],
      [{ id: "fld_c", rev: 1 }],
    );
    await vi.waitFor(() =>
      expect(server.calls("listFolders").length).toBeGreaterThan(before),
    );
    await flush();
    expect(
      server
        .calls("listFolders")
        .slice(before)
        .map((c) => c.query?.kind),
    ).toEqual(["board"]);
  });

  it("reads again what an operation took to the trash while a part was read for the first time, whose list, answered before, still has it", async () => {
    const listed = Promise.withResolvers<unknown>();
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      listVariables: () => listed.promise,
      getVariable: () => {
        throw refused(404, { code: "not_found" });
      },
    });
    const { doc, realtime } = await open();
    const loaded = doc.load("variables");
    await flush();
    // Another session takes a variable to the trash, which the document does
    // not hold yet, and which its own read finds gone before the list comes.
    realtime.send(
      testProject.id,
      [{ type: "variable.delete", payload: { id: "var_met" } }],
      [{ id: "var_met", rev: 2 }],
    );
    await vi.waitFor(() => expect(server.calls("getVariable")).toHaveLength(1));
    await flush();
    listed.resolve(page([variable("var_met")]));
    await loaded;
    await vi.waitFor(() => expect(server.calls("getVariable")).toHaveLength(2));
    await vi.waitFor(() =>
      expect(doc.store.getState().view.variables.has("var_met")).toBe(false),
    );
  });

  it("resolves a page's reading anew of the project, while the channel is live, once the operations up to the seq that its subscription holds as of have come, rather than at once", async () => {
    const server = serve(tree([board("bd_a", "Harbour")]));
    const { doc, deliver, subscriber } = await open();
    // The socket is back, and the subscription holds as of 3, after which
    // the channel sends the operations after 1.
    subscriber().subscribed(3);
    subscriber().live(true);
    let refreshed = false;
    const refreshing = doc.refresh().then(() => {
      refreshed = true;
    });
    await flush();
    expect(refreshed).toBe(false);
    deliver(
      2,
      [
        {
          type: "board.create",
          payload: { id: "bd_b", name: "Lighthouse", slug: "b", order: "W" },
        },
      ],
      [{ id: "bd_b", rev: 1 }],
    );
    deliver(3, [], []);
    await refreshing;
    expect(shown(doc, "bd_b")).toMatchObject({ name: "Lighthouse" });
    expect(server.calls("getProject")).toHaveLength(1);
  });

  it("reads the project anew again, later, once it failed to, and applies the operations that came meanwhile once it has", async () => {
    // The wait before it tries again is none.
    vi.spyOn(Math, "random").mockReturnValue(0);
    let projectReads = 0;
    serve({
      getProject: () => {
        projectReads++;
        if (projectReads === 2 || projectReads === 3) {
          throw refused(503, { code: "unavailable" });
        }
        return testProject;
      },
      listFolders: () => page([]),
      listBoards: () => page([board("bd_a", "Harbour", 6)]),
    });
    const { doc, deliver } = await open();
    deliver(3, [], [{ id: "bd_a", rev: 6 }]);
    deliver(
      4,
      [{ type: "board.rename", payload: { id: "bd_a", name: "Dawn" } }],
      [{ id: "bd_a", rev: 7 }],
    );
    await vi.waitFor(() => expect(doc.doc.seq).toBe(4));
    expect(projectReads).toBe(4);
    expect(shown(doc, "bd_a")).toMatchObject({ name: "Dawn", rev: 7 });
  });

  it("does not take into the project read anew what a read that began before it answered after, as a board that went meanwhile", async () => {
    const stale = Promise.withResolvers<unknown>();
    let projectReads = 0;
    const server = serve({
      getProject: () =>
        ++projectReads === 1 ? testProject : { ...testProject, seq: 4 },
      listFolders: () => page([]),
      listBoards: () => page([board("bd_a", "Harbour")]),
      getBoard: () => stale.promise,
    });
    const { doc, deliver } = await open();
    // An operation that changed a board that the document does not hold,
    // which it reads.
    deliver(2, [], [{ id: "bd_b", rev: 1 }]);
    await vi.waitFor(() => expect(server.calls("getBoard")).toHaveLength(1));
    // A gap, after which the document reads the project anew, which no
    // longer has the board, which went to the trash meanwhile.
    deliver(4, [], []);
    await vi.waitFor(() => expect(doc.doc.seq).toBe(4));
    stale.resolve(board("bd_b", "Lighthouse", 1));
    await flush();
    expect(shown(doc, "bd_b")).toBeUndefined();
  });

  it("reads the parts that it holds a few at a time once it reads the project anew, rather than one after another", async () => {
    const boards = Promise.withResolvers<unknown>();
    let boardReads = 0;
    const server = serve({
      getProject: () => testProject,
      listFolders: () => page([]),
      listBoards: () =>
        ++boardReads === 2 ? boards.promise : page([board("bd_a", "Harbour")]),
      listVariables: () => page([variable("var_met")]),
    });
    const { doc, deliver } = await open();
    await doc.load("variables");
    deliver(3, [], []);
    // The variables are read while the tree is.
    await vi.waitFor(() =>
      expect(server.calls("listVariables")).toHaveLength(2),
    );
    boards.resolve(page([board("bd_a", "Harbour")]));
    await vi.waitFor(() => expect(doc.doc.seq).toBe(3));
    expect(doc.holds("variables")).toBe(true);
  });

  it("reads no more of the parts that it holds once the read of one failed, as it reads the project anew later", async () => {
    // The wait before it tries again is longer than the test.
    vi.spyOn(Math, "random").mockReturnValue(1);
    const nodes = Promise.withResolvers<void>();
    let treeReads = 0;
    let anew = false;
    const server = serve({
      getProject: () => testProject,
      listFolders: () => page([]),
      listBoards: () => {
        if (++treeReads === 2) {
          throw refused(503, { code: "unavailable" });
        }
        return page([board("bd_a", "Harbour")]);
      },
      listBoardNodes: async () => {
        if (anew) {
          await nodes.promise;
        }
        return page([]);
      },
      listBoardConnections: () => page([]),
    });
    const { doc, deliver } = await open();
    for (let i = 0; i < 7; i++) {
      await doc.load(boardPart(`bd_${i}`));
    }
    anew = true;
    deliver(3, [], []);
    await vi.waitFor(() => expect(treeReads).toBe(2));
    nodes.resolve();
    await flush();
    // The tree and five boards are read at once, and the other two boards
    // are not, once the tree's read failed.
    expect(server.calls("listBoardNodes")).toHaveLength(7 + 5);
    expect(doc.doc.seq).toBe(1);
  });

  it("reads the project anew at once, once its read failed and waits to try again, as a page asks for it", async () => {
    // The wait before it tries again is longer than the test.
    vi.spyOn(Math, "random").mockReturnValue(1);
    let projectReads = 0;
    serve({
      getProject: () => {
        if (++projectReads === 2) {
          throw refused(503, { code: "unavailable" });
        }
        return testProject;
      },
      listFolders: () => page([]),
      listBoards: () => page([board("bd_a", "Harbour at night", 4)]),
    });
    const { doc, deliver } = await open();
    deliver(3, [], []);
    await vi.waitFor(() => expect(projectReads).toBe(2));
    await flush();
    await doc.refresh();
    expect(projectReads).toBe(3);
    expect(doc.doc.seq).toBe(3);
    expect(shown(doc, "bd_a")).toMatchObject({
      name: "Harbour at night",
      rev: 4,
    });
  });

  it("rejects a page's reading anew of the project that failed with what it failed with, rather than resolve as though it had read it, and reads it at once once asked again", async () => {
    // The wait before it tries again is longer than the test.
    vi.spyOn(Math, "random").mockReturnValue(1);
    let projectReads = 0;
    serve({
      getProject: () => {
        if (++projectReads === 2) {
          throw refused(503, { code: "unavailable" });
        }
        return testProject;
      },
      listFolders: () => page([]),
      listBoards: () => page([board("bd_a", "Harbour")]),
    });
    const { doc } = await open();
    await expect(doc.refresh()).rejects.toMatchObject({ status: 503 });
    await doc.refresh();
    expect(projectReads).toBe(3);
  });

  it("reads again the part alone that a page asks to read anew, while the channel is not live, rather than the project and every part that the session has opened", async () => {
    let boards = [board("bd_a", "Harbour")];
    const server = serve({
      getProject: () => testProject,
      listFolders: () => page([]),
      listBoards: () => page(boards),
      listBoardNodes: () => page([]),
      listBoardConnections: () => page([]),
    });
    const { doc } = await open();
    await doc.load("board:bd_a");
    // Another session made a board, which the channel, not live, does not
    // bring.
    boards = [...boards, board("bd_b", "Lighthouse")];
    await doc.refresh("tree");
    expect(shown(doc, "bd_b")).toMatchObject({ name: "Lighthouse" });
    expect(server.calls("listBoards")).toHaveLength(2);
    expect(server.calls("getProject")).toHaveLength(1);
    expect(server.calls("listBoardNodes")).toHaveLength(1);
  });

  it("does not read the project anew once its subscription holds as of a seq that the page's own batch, answered before, has moved the document past", async () => {
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: () => applied(2, [{ id: "bd_a", rev: 4 }]),
    });
    const { doc, subscriber } = await open();
    // The channel sends the subscription, as of seq 1, and the server holds
    // it before the batch commits, whose answer comes first.
    const since = subscriber().since();
    await doc.send([rename("bd_a", "Harbour at night", 3)]);
    expect(doc.doc.seq).toBe(2);
    subscriber().subscribed(since);
    subscriber().live(true);
    await flush();
    expect(server.calls("getProject")).toHaveLength(1);
  });

  it("forgets the history, and the revisions that the page's own batches moved entities to, once the channel tells that the server's seq went back behind what it had, as of a database restored from an older backup, rather than undo, or send a dialog's batch, over a change made since that took the same revision", async () => {
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: inTurn(
        applied(
          2,
          [{ id: "bd_a", rev: 4 }],
          [renameBack("bd_a", "Harbour", 4)],
        ),
        applied(2, [{ id: "bd_a", rev: 4 }]),
      ),
    });
    const { doc, realtime, resync, subscriber } = await open();
    realtime.hold(testProject.id);
    await doc.send([rename("bd_a", "Harbour at night", 3)]);
    expect(doc.store.getState().undoable).toBe(1);
    // The database is restored from a backup of before the rename, at seq
    // 1, which the subscription sent anew from seq 2 is told to read anew.
    expect(subscriber().since()).toBe(2);
    resync(testProject.seq);
    await vi.waitFor(() => expect(server.calls("getProject")).toHaveLength(2));
    await vi.waitFor(() =>
      expect(shown(doc, "bd_a")).toMatchObject({ name: "Harbour", rev: 3 }),
    );
    expect(doc.store.getState()).toMatchObject({ undoable: 0, redoable: 0 });
    await expect(doc.undo()).resolves.toBe(false);
    // A dialog that opened before the restore, with the revision that the
    // page's own rename moved the board from, which is the board's again.
    await doc.send([rename("bd_a", "Lighthouse", 3)]);
    expect(server.batches[1][0].expected_rev).toEqual({ bd_a: 3 });
  });

  it("tells the project's read the project as the server has it once a database restored from an older backup took the project's seq back, rather than keep what it read before", async () => {
    serve(tree([]));
    const { queryClient, realtime, resync } = await open();
    const key = projectQuery(testProject.id).queryKey;
    realtime.send(
      testProject.id,
      [{ type: "project.rename", payload: { name: "Lighthouse" } }],
      [{ id: testProject.id, rev: 2 }],
    );
    expect(queryClient.getQueryData(key)).toMatchObject({
      name: "Lighthouse",
      seq: 2,
    });
    // The database is restored from a backup of before the rename, and the
    // subscription sent anew is told to read the project anew as of the seq
    // that it had then.
    resync(testProject.seq);
    await vi.waitFor(() =>
      expect(queryClient.getQueryData(key)).toMatchObject({
        name: "Harbour",
        rev: testProject.rev,
        seq: testProject.seq,
      }),
    );
  });

  it("reads again a part that operations ask to read again while it is read again once more after, rather than once for each", async () => {
    let reads = 0;
    const pending = Promise.withResolvers<unknown>();
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      listBoardNodes: () => {
        reads++;
        return reads === 1
          ? page([])
          : reads === 2
            ? pending.promise
            : page([node("nd_x", "bd_a", 6)]);
      },
      listBoardConnections: () => page([]),
      // The node, which the first read again found as of before the later
      // operations, is read by itself too.
      getNode: () => node("nd_x", "bd_a", 6),
    });
    const { doc, realtime } = await open();
    await doc.load(boardPart("bd_a"));
    // A node that came back from the trash, and back again, and again, as
    // by undo and redo, whose board the document reads again each time.
    for (let rev = 4; rev <= 6; rev++) {
      realtime.send(
        testProject.id,
        [{ type: "node.restore", payload: { id: "nd_x" } }],
        [{ id: "nd_x", rev }],
      );
    }
    await vi.waitFor(() => expect(reads).toBe(2));
    pending.resolve(page([node("nd_x", "bd_a", 4)]));
    await vi.waitFor(() => expect(reads).toBe(3));
    await flush();
    expect(server.calls("listBoardNodes")).toHaveLength(3);
    expect(doc.doc.nodes.has("nd_x")).toBe(true);
  });

  it("reads the types again once an attribute definition came back from the trash, which brings it back in its type", async () => {
    const stamps = {
      created_at: "2026-11-01T10:00:00Z",
      updated_at: "2026-11-01T10:00:00Z",
    };
    const definition = (id: string, rev: number) => ({
      id,
      component_type_id: "ct_a",
      order: id === "atr_a" ? "V" : "W",
      key: id,
      name: id,
      type: "int",
      default: 0,
      runtime: false,
      rev,
      ...stamps,
    });
    let attributes = [definition("atr_a", 1)];
    const server = serve({
      ...tree([]),
      listComponentTypes: () =>
        page([
          {
            id: "ct_a",
            project_id: testProject.id,
            key: "hero",
            name: "Hero",
            icon: "none",
            attributes,
            rev: 1,
            ...stamps,
          },
        ]),
      listComponents: () => page([]),
    });
    const { doc, realtime } = await open();
    await doc.load("components");
    attributes = [definition("atr_a", 1), definition("atr_b", 3)];
    realtime.send(
      testProject.id,
      [{ type: "attribute.restore", payload: { id: "atr_b" } }],
      [{ id: "atr_b", rev: 3 }],
    );
    await vi.waitFor(() =>
      expect(
        doc.doc.componentTypes.get("ct_a")?.attributes.map((a) => a.id),
      ).toEqual(["atr_a", "atr_b"]),
    );
    expect(server.calls("listComponentTypes")).toHaveLength(2);
  });

  it("reads the project anew again once a resync_required came while it read it, of a seq past what it read", async () => {
    const read = Promise.withResolvers<unknown>();
    let projectReads = 0;
    serve({
      getProject: () => {
        projectReads++;
        if (projectReads === 2) {
          return read.promise;
        }
        return { ...testProject, seq: projectReads === 1 ? 1 : 5 };
      },
      listFolders: () => page([]),
      listBoards: () => page([board("bd_a", "Harbour")]),
    });
    const { doc, deliver, resync } = await open();
    deliver(3, [], []);
    await vi.waitFor(() => expect(projectReads).toBe(2));
    // The channel's operations after 5 follow, of which none has come yet.
    resync(5);
    read.resolve({ ...testProject, seq: 3 });
    await vi.waitFor(() => expect(doc.doc.seq).toBe(5));
    expect(projectReads).toBe(3);
  });
});

describe("the page's own batches while the channel is not live", () => {
  it("are undone once the project read anew, as the answer to one was past the next seq, has the operations up to it, which tell what the other steps expect", async () => {
    const reread = Promise.withResolvers<unknown>();
    let projectReads = 0;
    let boards = [board("bd_a", "Harbour"), board("bd_b", "Lighthouse")];
    const server = serve({
      getProject: () => (++projectReads === 2 ? reread.promise : testProject),
      listFolders: () => page([]),
      listBoards: () => page(boards),
      applyCommands: inTurn(
        applied(
          2,
          [{ id: "bd_a", rev: 4 }],
          [renameBack("bd_a", "Harbour", 4)],
        ),
        // Another session's batch applied as seq 3, which the channel, not
        // live, does not bring.
        applied(4, [{ id: "bd_a", rev: 5 }], [renameBack("bd_a", "Dusk", 5)]),
        applied(5, [{ id: "bd_a", rev: 6 }], [renameBack("bd_a", "Night", 6)]),
        applied(6, [{ id: "bd_a", rev: 7 }], []),
      ),
    });
    const { doc } = await open();
    await doc.send([rename("bd_a", "Dusk", 3)]);
    await doc.send([rename("bd_a", "Night", 4)]);
    await vi.waitFor(() => expect(projectReads).toBe(2));
    const undone = doc.undo();
    await flush();
    expect(server.batches).toHaveLength(2);
    boards = [board("bd_a", "Night", 5), board("bd_b", "Old light", 4)];
    reread.resolve({ ...testProject, seq: 4 });
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

  it("read the project anew once the answer to one is past the next seq, as other sessions' operations came before it", async () => {
    let boards = [board("bd_a", "Harbour"), board("bd_b", "Lighthouse")];
    const server = serve({
      getProject: () => testProject,
      listFolders: () => page([]),
      listBoards: () => page(boards),
      applyCommands: () => {
        boards = [
          board("bd_a", "Harbour at night", 4),
          board("bd_b", "Old light", 4),
        ];
        return applied(3, [{ id: "bd_a", rev: 4 }]);
      },
    });
    const { doc } = await open();
    await doc.send([rename("bd_a", "Harbour at night", 3)]);
    await vi.waitFor(() => expect(doc.doc.seq).toBe(3));
    expect(server.calls("getProject")).toHaveLength(2);
    expect(doc.store.getState().view).toBe(doc.doc);
    expect(shown(doc, "bd_b")).toMatchObject({ name: "Old light", rev: 4 });
  });

  it("read the project anew once an answer of already_applied is past what the document has, as to a batch sent again once the server no longer kept its first answer", async () => {
    let boards = [board("bd_a", "Harbour"), board("bd_b", "Lighthouse")];
    const server = serve({
      getProject: () => testProject,
      listFolders: () => page([]),
      listBoards: () => page(boards),
      applyCommands: (): CommandResultBody => {
        boards = [
          board("bd_a", "Harbour at night", 4),
          board("bd_b", "Old light", 4),
        ];
        return {
          status: "already_applied",
          seq: 3,
          revisions: [{ id: "bd_a", rev: 4 }],
          not_reconnected: [],
          undo: null,
        };
      },
    });
    const { doc } = await open();
    await doc.send([rename("bd_a", "Harbour at night", 3)]);
    await vi.waitFor(() => expect(doc.doc.seq).toBe(3));
    expect(server.calls("getProject")).toHaveLength(2);
    expect(doc.store.getState().view).toBe(doc.doc);
    expect(shown(doc, "bd_b")).toMatchObject({ name: "Old light", rev: 4 });
  });

  it("follow the project read anew as of its seq, which the next answer follows, and tell the project's read of it", async () => {
    let seq = testProject.seq;
    const server = serve({
      getProject: () => ({ ...testProject, seq }),
      listFolders: () => page([]),
      listBoards: () => page([board("bd_a", "Harbour", 5)]),
      applyCommands: () => applied(6, [{ id: "bd_a", rev: 6 }]),
    });
    const { doc, queryClient } = await open();
    seq = 5;
    await doc.refresh();
    expect(doc.doc.seq).toBe(5);
    expect(
      queryClient.getQueryData(projectQuery(testProject.id).queryKey)?.seq,
    ).toBe(5);
    await doc.send([rename("bd_a", "Harbour at night", 5)]);
    expect(doc.doc.seq).toBe(6);
    expect(doc.store.getState().view).toBe(doc.doc);
    expect(server.calls("getProject")).toHaveLength(2);
  });

  it("tell the project's read the role that the project read anew gave, as once the user's changed while the channel was not live", async () => {
    let role: "owner" | "viewer" = "owner";
    serve({ ...tree([]), getProject: () => ({ ...testProject, role }) });
    const { doc, queryClient } = await open();
    role = "viewer";
    await doc.refresh();
    expect(
      queryClient.getQueryData(projectQuery(testProject.id).queryKey)?.role,
    ).toBe("viewer");
  });

  it("are sent again where one gives a value to a component that the document holds without its values, whose other fields changed elsewhere", async () => {
    const stamps = {
      created_at: "2026-11-01T10:00:00Z",
      updated_at: "2026-11-01T10:00:00Z",
    };
    const summary = {
      id: "cmp_a",
      project_id: testProject.id,
      component_type_id: "ct_a",
      order: "V",
      key: "elara",
      name: "Elara",
      rev: 3,
      ...stamps,
    };
    const theirs = {
      ...summary,
      name: "Elara the bold",
      description: { type: "doc", content: [{ type: "paragraph" }] },
      attributes: { atr_a: 1 },
      rev: 4,
    };
    const server = serve({
      ...tree([]),
      listComponentTypes: () => page([]),
      listComponents: () => page([summary]),
      getComponent: () => ({ ...theirs, attributes: { atr_a: 5 }, rev: 5 }),
      applyCommands: inTurn(
        refused(409, {
          code: "revision_conflict",
          details: {
            conflicts: [
              {
                command: 0,
                command_id: "00000000-0000-7000-8000-000000000001",
                type: "component.set_attribute",
                id: "cmp_a",
                expected_rev: 3,
                exists: true,
                current_rev: 4,
              },
            ],
            states: { cmp_a: theirs },
          },
        }),
        applied(2, [{ id: "cmp_a", rev: 5 }]),
      ),
    });
    const { doc } = await open();
    await doc.load("components");
    await doc.send([
      {
        type: "component.set_attribute",
        payload: { id: "cmp_a", attribute_id: "atr_a", value: 5 },
        expected_rev: { cmp_a: 3 },
      },
    ]);
    expect(server.batches.map((batch) => batch[0].expected_rev)).toEqual([
      { cmp_a: 3 },
      { cmp_a: 4 },
    ]);
  });
});

describe("undo and redo, as answers and reads come", () => {
  it("keeps the rest of an undo sent in parts that no answer came to, each command expecting what the parts that applied left", async () => {
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
        new NetworkError(new TypeError("Failed to fetch")),
        applied(
          4,
          [{ id: "bd_a", rev: 6 }],
          [renameBack("bd_a", "Harbour at night", 6)],
        ),
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
    await expect(doc.undo()).rejects.toBeInstanceOf(NetworkError);
    expect(doc.store.getState()).toMatchObject({ undoable: 1, redoable: 1 });
    await doc.undo();
    // The rest is sent again as it was, with the IDs that its commands had.
    expect(server.batches[4]).toEqual([
      {
        ...renameBack("bd_a", "Harbour", 5),
        command_id: server.batches[3][0].command_id,
      },
    ]);
    expect(shown(doc, "bd_a")).toMatchObject({
      name: "Harbour",
      color: "none",
      rev: 6,
    });
    expect(doc.store.getState()).toMatchObject({ undoable: 0, redoable: 2 });
  });

  it("moves what the other steps expect, once the rest of an undo sent in parts applies, from what the parts that applied left", async () => {
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
          [
            {
              type: "board.set_color",
              payload: { id: "bd_a", color: "none" },
              expected_rev: { bd_a: 5 },
            },
            renameBack("bd_a", "Harbour at dusk", 5),
          ],
        ),
        undoTooLarge(),
        applied(
          4,
          [{ id: "bd_a", rev: 6 }],
          [
            {
              type: "board.set_color",
              payload: { id: "bd_a", color: "blue" },
              expected_rev: { bd_a: 6 },
            },
          ],
        ),
        new NetworkError(new TypeError("Failed to fetch")),
        applied(
          5,
          [{ id: "bd_a", rev: 7 }],
          [renameBack("bd_a", "Harbour at night", 7)],
        ),
        applied(6, [{ id: "bd_a", rev: 8 }], []),
      ),
    });
    const { doc } = await open();
    await doc.send([rename("bd_a", "Harbour at dusk", 3)]);
    await doc.send([
      rename("bd_a", "Harbour at night", 4),
      {
        type: "board.set_color",
        payload: { id: "bd_a", color: "blue" },
        expected_rev: { bd_a: 4 },
      },
    ]);
    await expect(doc.undo()).rejects.toBeInstanceOf(NetworkError);
    await doc.undo();
    await doc.undo();
    // The first batch's undo expects what the board was before the second
    // batch, which the first half brought it back to, as of 6, and the rest
    // left as of 7.
    expect(server.batches.at(-1)).toEqual([
      { ...renameBack("bd_a", "Harbour", 7), command_id: expect.any(String) },
    ]);
    expect(shown(doc, "bd_a")).toMatchObject({ name: "Harbour", rev: 8 });
  });

  it("moves what the other steps expect from the revision that the undone batch expected, though the document read the entity as the batch left it before its answer came", async () => {
    const second = Promise.withResolvers<CommandResultBody>();
    const server = serve({
      // The second board comes after the first, which a move without a key
      // places after it.
      ...tree([board("bd_a", "Harbour"), board("bd_b", "Lighthouse")]),
      // The read of the board whose key of order the first batch left to
      // the server, answered once the second batch has applied.
      getBoard: () => ({ ...board("bd_a", "Harbour at night", 5), order: "W" }),
      applyCommands: inTurn(
        applied(
          2,
          [{ id: "bd_a", rev: 4 }],
          [
            {
              type: "board.move",
              payload: { id: "bd_a", order: "V" },
              expected_rev: { bd_a: 4 },
            },
          ],
        ),
        second.promise,
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
      {
        type: "board.move",
        payload: { id: "bd_a" },
        expected_rev: { bd_a: 3 },
      },
    ]);
    const sent = doc.send([rename("bd_a", "Harbour at night", 4)]);
    await vi.waitFor(() => expect(doc.doc.boards.get("bd_a")?.rev).toBe(5));
    second.resolve(
      applied(3, [{ id: "bd_a", rev: 5 }], [renameBack("bd_a", "Harbour", 5)]),
    );
    await sent;
    await doc.undo();
    await doc.undo();
    // The board was at 4 before the second batch, which the first batch's
    // undo expects, and is the same again, at 6.
    expect(server.batches[3][0]).toMatchObject({
      type: "board.move",
      expected_rev: { bd_a: 6 },
    });
  });
});

describe("a database restored from an older backup", () => {
  // Verifies: FR-EDIT-08/AC4
  it("forgets the history once the subscription sent anew is answered as of a seq before the one that the page's own batch, answered before it was sent, applied as, rather than take another session's batch of that seq for the page's own, and undo it over that", async () => {
    let boards = [board("bd_a", "Harbour")];
    const server = serve({
      getProject: () => testProject,
      listFolders: () => page([]),
      listBoards: () => page(boards),
      applyCommands: () =>
        applied(
          3,
          [{ id: "bd_a", rev: 4 }],
          [renameBack("bd_a", "Harbour", 4)],
        ),
    });
    const { doc, realtime, deliver, subscriber } = await open();
    realtime.hold(testProject.id);
    // The rename applied as seq 3, whose operations the channel has yet to
    // bring once the socket is lost.
    await doc.send([rename("bd_a", "Harbour at night", 3)]);
    expect(doc.store.getState().undoable).toBe(1);
    subscriber().live(false);
    // The database is restored from a backup at seq 2, after which another
    // session renames the board, at the revision that the page's rename
    // gave it, as seq 3, and the subscription sent anew from seq 1 is
    // answered as of seq 2.
    expect(subscriber().since()).toBe(1);
    subscriber().subscribed(2);
    subscriber().live(true);
    await vi.waitFor(() => expect(server.calls("getProject")).toHaveLength(2));
    await vi.waitFor(() => expect(doc.doc.seq).toBe(2));
    expect(shown(doc, "bd_a")).toMatchObject({ name: "Harbour", rev: 3 });
    boards = [board("bd_a", "Lighthouse", 4)];
    deliver(
      3,
      [{ type: "board.rename", payload: { id: "bd_a", name: "Lighthouse" } }],
      [{ id: "bd_a", rev: 4 }],
    );
    expect(shown(doc, "bd_a")).toMatchObject({ name: "Lighthouse", rev: 4 });
    await expect(doc.undo()).resolves.toBe(false);
    expect(server.batches).toHaveLength(1);
  });

  // Verifies: FR-EDIT-08/AC4
  it("forgets the history kept over a reload once the subscription is answered as of a seq before the batches that its steps undo, as of a database restored while the tab reloaded", async () => {
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: () =>
        applied(
          3,
          [{ id: "bd_a", rev: 4 }],
          [renameBack("bd_a", "Harbour", 4)],
        ),
    });
    const before = await open();
    await before.doc.send([rename("bd_a", "Harbour at night", 3)]);
    before.doc.dispose();
    const { doc, subscriber } = await open();
    expect(doc.store.getState().undoable).toBe(1);
    // The project read is as of seq 1, and the database was restored from a
    // backup at seq 2, as of which the subscription is answered.
    subscriber().since();
    subscriber().subscribed(2);
    expect(doc.store.getState()).toMatchObject({ undoable: 0, redoable: 0 });
    await expect(doc.undo()).resolves.toBe(false);
    expect(server.batches).toHaveLength(1);
  });

  it("keeps the history kept over a reload once the subscription is answered as of the seq of the batches that its steps undo, or past it", async () => {
    serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: () =>
        applied(
          3,
          [{ id: "bd_a", rev: 4 }],
          [renameBack("bd_a", "Harbour", 4)],
        ),
    });
    const before = await open();
    await before.doc.send([rename("bd_a", "Harbour at night", 3)]);
    before.doc.dispose();
    const { doc, subscriber } = await open();
    subscriber().since();
    subscriber().subscribed(3);
    expect(doc.store.getState().undoable).toBe(1);
  });

  it("does not apply the operations that came while it read the project anew once a resync_required is of a seq before them, as of a database restored meanwhile, and reads the project again", async () => {
    const read = Promise.withResolvers<unknown>();
    let projectReads = 0;
    serve({
      getProject: () => {
        projectReads++;
        if (projectReads === 2) {
          return read.promise;
        }
        return { ...testProject, seq: projectReads === 1 ? 1 : 4 };
      },
      listFolders: () => page([]),
      listBoards: () => page([board("bd_a", "Harbour")]),
    });
    const { doc, deliver, resync, subscriber } = await open();
    // A gap, after which the project is read anew, while the board is
    // renamed twice, which the document keeps until it has.
    deliver(3, [], []);
    await vi.waitFor(() => expect(projectReads).toBe(2));
    deliver(
      4,
      [{ type: "board.rename", payload: { id: "bd_a", name: "Dusk" } }],
      [{ id: "bd_a", rev: 4 }],
    );
    deliver(
      5,
      [{ type: "board.rename", payload: { id: "bd_a", name: "Night" } }],
      [{ id: "bd_a", rev: 5 }],
    );
    // The socket is lost, and the database is restored from a backup at seq
    // 4, before the renames, which the subscription sent anew from seq 1 is
    // told to read anew.
    expect(subscriber().since()).toBe(1);
    resync(4);
    read.resolve({ ...testProject, seq: 3 });
    await vi.waitFor(() => expect(projectReads).toBe(3));
    await vi.waitFor(() => expect(doc.doc.seq).toBe(4));
    expect(shown(doc, "bd_a")).toMatchObject({ name: "Harbour", rev: 3 });
  });

  // Verifies: FR-EDIT-08/AC4
  it("forgets the history before the undo of the page's batch that its answer tells applied once the server's seq went back, rather than once the channel says so after, and shows the batch until the project read anew has it", async () => {
    const read = Promise.withResolvers<unknown>();
    let projectReads = 0;
    let boards = [board("bd_a", "Harbour"), board("bd_b", "Lighthouse")];
    const server = serve({
      getProject: () => {
        projectReads++;
        return projectReads === 2 ? read.promise : testProject;
      },
      listFolders: () => page([]),
      listBoards: () => page(boards),
      applyCommands: inTurn(
        applied(
          2,
          [{ id: "bd_a", rev: 4 }],
          [renameBack("bd_a", "Harbour", 4)],
        ),
        // The database is restored from a backup at seq 1, after which the
        // next rename applies as seq 2 again.
        applied(
          2,
          [{ id: "bd_b", rev: 4 }],
          [renameBack("bd_b", "Lighthouse", 4)],
        ),
        applied(3, [{ id: "bd_b", rev: 5 }], []),
      ),
    });
    const { doc, realtime, resync, subscriber } = await open();
    realtime.hold(testProject.id);
    await doc.send([rename("bd_a", "Harbour at night", 3)]);
    expect(doc.doc.seq).toBe(2);
    await doc.send([rename("bd_b", "Old light", 3)]);
    expect(doc.store.getState().undoable).toBe(1);
    expect(projectReads).toBe(2);
    // The document is as of seq 1 until the project read anew has the
    // rename, which the page shows meanwhile, and the subscription sent
    // anew is from there.
    expect(doc.doc.seq).toBe(1);
    expect(shown(doc, "bd_b")).toMatchObject({ name: "Old light", rev: 4 });
    expect(subscriber().since()).toBe(1);
    // The channel tells of the restore after the answer.
    resync(2);
    expect(doc.store.getState().undoable).toBe(1);
    boards = [board("bd_a", "Harbour"), board("bd_b", "Old light", 4)];
    read.resolve({ ...testProject, seq: 2 });
    await vi.waitFor(() => expect(doc.doc.seq).toBe(2));
    expect(shown(doc, "bd_b")).toMatchObject({ name: "Old light", rev: 4 });
    expect(projectReads).toBe(2);
    await doc.undo();
    expect(server.batches[2]).toEqual([
      {
        ...renameBack("bd_b", "Lighthouse", 4),
        command_id: expect.any(String),
      },
    ]);
  });

  // Verifies: FR-EDIT-08/AC4
  it("forgets the history, and reads the project anew, once the operation of the seq that the page's own batch was answered with is of another batch, as of a database restored from a backup of before the channel had sent the operations up to it", async () => {
    let boards = [board("bd_a", "Harbour")];
    const server = serve({
      getProject: () => testProject,
      listFolders: () => page([]),
      listBoards: () => page(boards),
      applyCommands: () =>
        applied(
          3,
          [{ id: "bd_a", rev: 4 }],
          [renameBack("bd_a", "Harbour", 4)],
        ),
    });
    const { doc, realtime, deliver } = await open();
    realtime.hold(testProject.id);
    await doc.send([rename("bd_a", "Harbour at night", 3)]);
    expect(doc.store.getState().undoable).toBe(1);
    // The database is restored from a backup at seq 2, after which another
    // session renames the board, at the revision that the page's rename
    // gave it, as seq 3.
    deliver(2, [], []);
    boards = [board("bd_a", "Lighthouse", 4)];
    deliver(
      3,
      [{ type: "board.rename", payload: { id: "bd_a", name: "Lighthouse" } }],
      [{ id: "bd_a", rev: 4 }],
    );
    expect(doc.store.getState().undoable).toBe(0);
    await vi.waitFor(() => expect(server.calls("getProject")).toHaveLength(2));
    await vi.waitFor(() => expect(doc.doc.seq).toBe(3));
    expect(shown(doc, "bd_a")).toMatchObject({ name: "Lighthouse", rev: 4 });
    await expect(doc.undo()).resolves.toBe(false);
    expect(server.batches).toHaveLength(1);
  });

  it("does not wait for the operations up to the batch that an undo undoes once the server's seq went back behind it, and sends nothing", async () => {
    const server = serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: () =>
        applied(
          3,
          [{ id: "bd_a", rev: 4 }],
          [renameBack("bd_a", "Harbour", 4)],
        ),
    });
    const { doc, realtime, deliver, resync } = await open();
    realtime.hold(testProject.id);
    await doc.send([rename("bd_a", "Harbour at night", 3)]);
    const undone = doc.undo();
    await flush();
    expect(doc.store.getState().stepping).toBe(true);
    deliver(2, [], []);
    // The database is restored from a backup at seq 1.
    resync(1);
    const waited = new Promise((resolve) => setTimeout(resolve, 100, "waits"));
    await expect(Promise.race([undone, waited])).resolves.toBe(false);
    expect(doc.store.getState().stepping).toBe(false);
    expect(server.batches).toHaveLength(1);
  });

  it("resolves a page's reading anew of the project once it has read it again, as the server's seq went back while it was read, rather than as it was before", async () => {
    const first = Promise.withResolvers<unknown>();
    const second = Promise.withResolvers<unknown>();
    let projectReads = 0;
    let boards = [board("bd_a", "Harbour")];
    serve({
      getProject: () => {
        projectReads++;
        return projectReads === 2
          ? first.promise
          : projectReads === 3
            ? second.promise
            : testProject;
      },
      listFolders: () => page([]),
      listBoards: () => page(boards),
    });
    const { doc, deliver, resync, subscriber } = await open();
    deliver(3, [], []);
    await vi.waitFor(() => expect(projectReads).toBe(2));
    let refreshed = false;
    const refreshing = doc.refresh().then(() => {
      refreshed = true;
    });
    // The database is restored from a backup at seq 2 while the project is
    // read anew, after which another session makes a board.
    subscriber().since();
    resync(2);
    first.resolve({ ...testProject, seq: 3 });
    await vi.waitFor(() => expect(projectReads).toBe(3));
    await flush();
    expect(refreshed).toBe(false);
    boards = [board("bd_a", "Harbour"), board("bd_b", "Lighthouse")];
    second.resolve({ ...testProject, seq: 3 });
    await refreshing;
    expect(shown(doc, "bd_b")).toMatchObject({ name: "Lighthouse" });
  });
});

describe("the page's documents", () => {
  /** Returns the page's documents over a fake channel, with the subscribers that its documents gave it. */
  function documentsOf(storage?: Storage) {
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
    const documents = new Documents({ queryClient, realtime, log, storage });
    // As the document that open makes.
    onTestFinished(() => {
      documents.close();
      window.sessionStorage.clear();
    });
    return { documents, subscribers, queryClient, realtime };
  }

  it("drop every document, and have the channel's socket opened anew, once the page's session is of another account, so that the next documents do not follow their projects as the account before", async () => {
    serve(tree([board("bd_a", "Harbour")]));
    const { documents, queryClient, realtime } = documentsOf();
    const reconnect = vi.spyOn(realtime, "reconnect");
    const doc = documents.get(testProject.id);
    await doc.ready;
    expect(realtime.subscribed(testProject.id)).toBe(true);
    const other = { ...testSession, user_id: "usr_other" };
    queryClient.setQueryData(sessionQuery.queryKey, other);
    expect(realtime.subscribed(testProject.id)).toBe(false);
    expect(reconnect).toHaveBeenCalledOnce();
    expect(documents.get(testProject.id)).not.toBe(doc);
  });

  it("make the document of a project anew once the one that they had found the project gone, and no page shows it", async () => {
    serve(tree([board("bd_a", "Harbour")]));
    const { documents, subscribers } = documentsOf();
    const doc = documents.get(testProject.id);
    await doc.ready;
    const release = documents.acquire(testProject.id);
    subscribers.at(-1)?.gone();
    expect(doc.store.getState().gone).toBe(true);
    // The page that shows it shows the page of no page.
    expect(documents.get(testProject.id)).toBe(doc);
    release();
    const again = documents.get(testProject.id);
    expect(again).not.toBe(doc);
    expect(again.store.getState().gone).toBe(false);
  });

  it("keep a document that no page shows while it reads what a route's loader waits for, longer than they keep one otherwise", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    onTestFinished(() => {
      vi.useRealTimers();
    });
    const boards = Promise.withResolvers<unknown>();
    serve({
      getProject: () => testProject,
      listFolders: () => page([]),
      listBoards: () => boards.promise,
    });
    const { documents } = documentsOf();
    const doc = documents.get(testProject.id);
    const loaded = doc.load("tree");
    await vi.advanceTimersByTimeAsync(keptUnshown + 1);
    expect(documents.get(testProject.id)).toBe(doc);
    boards.resolve(page([]));
    await loaded;
    await vi.advanceTimersByTimeAsync(keptUnshown + 1);
    expect(documents.get(testProject.id)).not.toBe(doc);
  });

  it("keep a document that no page shows while it reads the project anew, as a board's loader waits for before it says that there is no such board, longer than they keep one otherwise", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    onTestFinished(() => {
      vi.useRealTimers();
    });
    let reads = 0;
    const again = Promise.withResolvers<unknown>();
    serve({
      getProject: () => (++reads === 1 ? testProject : again.promise),
      listFolders: () => page([]),
      listBoards: () => page([board("bd_a", "Harbour")]),
    });
    const { documents } = documentsOf();
    const doc = documents.get(testProject.id);
    await doc.load("tree");
    const refreshed = doc.refresh();
    await vi.advanceTimersByTimeAsync(keptUnshown + 1);
    expect(documents.get(testProject.id)).toBe(doc);
    again.resolve(testProject);
    await refreshed;
    expect(shown(doc, "bd_a")).toMatchObject({ name: "Harbour" });
    await vi.advanceTimersByTimeAsync(keptUnshown + 1);
    expect(documents.get(testProject.id)).not.toBe(doc);
  });

  it("keep a document that no page shows while a batch of the page's is under way, longer than they keep one otherwise, so that its answer joins the history that the tab's storage keeps", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    onTestFinished(() => {
      vi.useRealTimers();
    });
    const answer = Promise.withResolvers<unknown>();
    serve({
      ...tree([board("bd_a", "Harbour")]),
      applyCommands: () => answer.promise,
    });
    const { documents } = documentsOf(window.sessionStorage);
    const doc = documents.get(testProject.id);
    await doc.load("tree");
    const sent = doc.send([rename("bd_a", "Harbour at night", 3)]);
    await vi.advanceTimersByTimeAsync(keptUnshown + 1);
    expect(documents.get(testProject.id)).toBe(doc);
    answer.resolve(
      applied(2, [{ id: "bd_a", rev: 4 }], [renameBack("bd_a", "Harbour", 4)]),
    );
    await sent;
    const kept = window.sessionStorage.getItem(
      historyKey(testSession.user_id, testProject.id),
    );
    expect(JSON.parse(kept ?? "null")).toMatchObject({
      undo: [{ seqs: [2] }],
    });
    await vi.advanceTimersByTimeAsync(keptUnshown + 1);
    expect(documents.get(testProject.id)).not.toBe(doc);
  });
});
