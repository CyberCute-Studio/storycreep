import "fake-indexeddb/auto";
import {
  type Board,
  type CommandBody,
  type CommandResultBody,
  NetworkError,
  type SignedInBody,
  type UndoCommandBody,
} from "@storycreep/shared/api";
import { IDBFactory } from "fake-indexeddb";
import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  onTestFinished,
  vi,
} from "vitest";
import { createQueryClient, sessionQuery } from "../api";
import { createErrorLog } from "../errors/log";
import { OfflineDatabase } from "../offline/db";
import {
  deviceQueue,
  type QueueBroadcast,
  type QueueDiscard,
  type QueueLocks,
  type QueueNews,
} from "../offline/queue";
import { testSession } from "../test/app";
import { fakeApi, refused, testProject } from "../test/fake";
import { fakeLocks } from "../test/locks";
import type { QueueStore } from "./queue";
import { whenSent } from "./sent";
import type { Command } from "./store";
import { ProjectDocument } from "./store";

// The device's queue of the page's batches that the server has yet to
// answer (PRD FR-OFF-03, plan note N98), over the IndexedDB of
// fake-indexeddb and a fake of the browser's Web Locks: a batch made offline
// is kept, which the next page's document shows and sends once the server
// answers, with its commands' IDs, as after the browser restarted (AC1); the
// commands that it keeps are those sent online, which the page shows
// through the same appliers (AC2); undo takes a batch that never reached the
// server out of the queue, and redo puts it back, with no connection (AC4);
// a batch that no answer came to is sent again; one that the server refuses
// for what it does is kept, needing review, with every batch after it;
// another tab's batches are taken once its document has gone, and not
// before; and signing out takes the account's off the device, after which
// no document that began before writes one there again.

const time = "2026-11-27T10:00:00Z";
const user = testSession.user_id;

let factory: IDBFactory;
/** The time that the documents read, as their batches are made. */
let clock = time;

beforeEach(() => {
  factory = new IDBFactory();
  globalThis.indexedDB = factory;
  clock = time;
});

afterEach(() => {
  vi.restoreAllMocks();
});

/**
 * A fake of the browser's BroadcastChannel between tabs: each tab's
 * channel hears what the others post, and not what it posts itself.
 */
function fakeBroadcast() {
  const listeners = new Set<(news: QueueNews | QueueDiscard) => void>();
  return {
    tab(): QueueBroadcast {
      const own = new Set<(news: QueueNews | QueueDiscard) => void>();
      return {
        post: (news) => {
          for (const listener of listeners) {
            if (!own.has(listener)) {
              listener(structuredClone(news));
            }
          }
        },
        listen: (listener) => {
          own.add(listener);
          listeners.add(listener);
          return () => {
            own.delete(listener);
            listeners.delete(listener);
          };
        },
      };
    },
  };
}

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

function rename(id: string, name: string, rev: number): Command {
  return {
    type: "board.rename",
    payload: { id, name },
    expected_rev: { [id]: rev },
  };
}

function renameBack(id: string, name: string, rev: number): UndoCommandBody {
  return {
    type: "board.rename",
    payload: { id, name },
    expected_rev: { [id]: rev },
  };
}

function applied(
  seq: number,
  revisions: { id: string; rev: number }[],
  undo: UndoCommandBody[] | null = [],
): CommandResultBody {
  return { status: "applied", seq, revisions, not_reconnected: [], undo };
}

/**
 * Serves the test project, as of seq, if given, whose tree holds the board
 * bd_a, Harbour, and answers each batch with answer, which may throw;
 * returns the batches that the documents sent, each as sent.
 */
function serve(
  answer: (commands: CommandBody[]) => unknown,
  seq = () => testProject.seq,
) {
  const batches: CommandBody[][] = [];
  fakeApi((operation, input) => {
    switch (operation) {
      case "getProject":
        return { ...testProject, seq: seq() };
      case "listFolders":
        return { items: [], next_cursor: null };
      case "listBoards":
        return { items: [board("bd_a", "Harbour")], next_cursor: null };
      case "applyCommands": {
        const commands = (input as { body: { commands: CommandBody[] } }).body
          .commands;
        batches.push(structuredClone(commands));
        return answer(commands);
      }
    }
    throw new Error(`unexpected ${operation}`);
  });
  return batches;
}

/** The browser's connection, as the documents read it and hear of it. */
function connection(online: boolean) {
  const listeners = new Set<(online: boolean) => void>();
  const state = {
    online,
    set(now: boolean) {
      state.online = now;
      for (const listener of listeners) {
        listener(now);
      }
    },
    listen(listener: (online: boolean) => void) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
  };
  return state;
}

/**
 * Opens the document of the test project, as a page of the test account
 * does, over the device's database db and the browser's locks, or the
 * queue given, with the tree read, and the session given.
 */
async function open(
  db: OfflineDatabase,
  locks: QueueLocks,
  net: ReturnType<typeof connection>,
  session = testSession,
  queue: QueueStore | undefined = deviceQueue(db, { secure: true, locks }),
) {
  const log = createErrorLog();
  const queryClient = createQueryClient(log);
  queryClient.setQueryData(sessionQuery.queryKey, session);
  const doc = new ProjectDocument(testProject.id, {
    queryClient,
    log,
    now: () => clock,
    online: () => net.online,
    onConnection: (listener) => net.listen(listener),
    queue,
    random: () => 0.01,
  });
  onTestFinished(() => doc.dispose());
  await doc.ready;
  await doc.load("tree");
  return { doc, queryClient, log };
}

/** Resolves once doc's state passes check. */
function until(
  doc: ProjectDocument,
  check: (state: ReturnType<ProjectDocument["store"]["getState"]>) => boolean,
): Promise<void> {
  return vi.waitFor(() => {
    expect(check(doc.store.getState())).toBe(true);
  });
}

/** The name that the first command of commands, a rename, gives. */
function nameOf(commands: readonly CommandBody[]): string | undefined {
  return (commands[0]?.payload as { name?: string } | undefined)?.name;
}

/**
 * Serves the board bd_a as the server keeps it, from its third revision: a
 * batch of a rename of it that expects the revision that it has applies,
 * and one that expects another is refused for it, with the board as it is
 * now; returns the batches that the documents sent, each as sent.
 */
function serveBoard() {
  let state = board("bd_a", "Harbour");
  let seq = testProject.seq;
  return serve((commands) => {
    const expected = commands[0]?.expected_rev?.bd_a;
    if (expected !== state.rev) {
      throw refused(409, {
        code: "revision_conflict",
        details: {
          conflicts: [
            {
              command: 0,
              command_id: commands[0]?.command_id,
              type: "board.rename",
              id: "bd_a",
              expected_rev: expected,
              exists: true,
              current_rev: state.rev,
            },
          ],
          states: { bd_a: state },
        },
      });
    }
    seq++;
    state = { ...state, name: nameOf(commands) ?? "", rev: state.rev + 1 };
    return applied(seq, [{ id: "bd_a", rev: state.rev }]);
  });
}

/** The name of the board bd_a, as the page shows it. */
function shownName(doc: ProjectDocument): string | undefined {
  return doc.store.getState().view.boards.get("bd_a")?.name;
}

describe("the device's queue of the page's batches", () => {
  // Verifies: FR-OFF-03/AC1
  it("keeps a batch made offline, which the next page shows, after the browser restarted, and sends once online, with its commands' IDs", async () => {
    const batches = serve(() =>
      applied(2, [{ id: "bd_a", rev: 4 }], [renameBack("bd_a", "Harbour", 4)]),
    );
    const net = connection(false);
    const db = new OfflineDatabase();
    const first = await open(db, fakeLocks(), net);
    await expect(first.doc.send([rename("bd_a", "Quay", 3)])).resolves.toEqual({
      status: "queued",
    });
    expect(shownName(first.doc)).toBe("Quay");
    expect(first.doc.store.getState()).toMatchObject({
      queued: 1,
      holding: true,
    });
    const [kept] = await db.queued(user, testProject.id);
    expect(kept?.commands).toEqual([
      { ...rename("bd_a", "Quay", 3), command_id: expect.any(String) },
    ]);
    // The tab closes, and the browser restarts: a database opened anew, and
    // no lock of the tab before held.
    first.doc.dispose();
    await db.close();
    const again = new OfflineDatabase();
    const second = await open(again, fakeLocks(), net);
    expect(shownName(second.doc)).toBe("Quay");
    expect(second.doc.store.getState()).toMatchObject({ queued: 1 });
    expect(batches).toEqual([]);
    net.set(true);
    await until(second.doc, (s) => s.queued === 0);
    expect(batches).toEqual([kept?.commands]);
    await vi.waitFor(async () => {
      expect(await again.queued(user, testProject.id)).toEqual([]);
    });
    // Its undo is the user's latest step, as any batch's.
    expect(second.doc.store.getState().undoable).toBe(1);
    await again.close();
  });

  // Verifies: FR-OFF-03/AC2
  it("sends the commands that it keeps as the page sends them online, which the page shows by the same appliers", async () => {
    let answer: (value: CommandResultBody) => void = () => {};
    const batches = serve(
      () =>
        new Promise<CommandResultBody>((resolve) => {
          answer = resolve;
        }),
    );
    const db = new OfflineDatabase();
    const offline = await open(db, fakeLocks(), connection(false));
    const onlineDb = new OfflineDatabase();
    const online = await open(onlineDb, fakeLocks(), connection(true));
    const batch = [rename("bd_a", "Quay", 3)];
    await offline.doc.send(batch);
    void online.doc.send(batch);
    await vi.waitFor(() => expect(batches).toHaveLength(1));
    // What the page shows of either before the server has answered.
    expect(offline.doc.store.getState().view.boards).toEqual(
      online.doc.store.getState().view.boards,
    );
    const [kept] = await db.queued(user, testProject.id);
    const strip = (commands: CommandBody[] | undefined) =>
      commands?.map(({ command_id: _, ...command }) => command);
    expect(strip(kept?.commands)).toEqual(strip(batches[0]));
    answer(applied(2, [{ id: "bd_a", rev: 4 }]));
    await db.close();
    await onlineDb.close();
  });

  it("keeps a batch that no answer came to, and sends it again, with its commands' IDs, once the server answers", async () => {
    let failures = 1;
    const batches = serve(() => {
      if (failures-- > 0) {
        throw new NetworkError(new TypeError("Failed to fetch"));
      }
      return applied(
        2,
        [{ id: "bd_a", rev: 4 }],
        [renameBack("bd_a", "Harbour", 4)],
      );
    });
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), connection(true));
    await expect(doc.send([rename("bd_a", "Quay", 3)])).resolves.toEqual({
      status: "queued",
    });
    await until(doc, (s) => s.queued === 0);
    expect(batches).toHaveLength(2);
    expect(batches[1]).toEqual(batches[0]);
    expect(shownName(doc)).toBe("Quay");
    expect(doc.store.getState().undoable).toBe(1);
    await db.close();
  });

  it("keeps a queued batch that the server refuses for what it does, as needing review, with every batch after it", async () => {
    const batches = serve(() => {
      throw refused(422, { code: "validation_failed" });
    });
    const net = connection(false);
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), net);
    await doc.send([rename("bd_a", "Quay", 3)]);
    await doc.send([rename("bd_a", "Pier", 3)]);
    net.set(true);
    await until(doc, (s) => s.review === 1);
    expect(batches).toHaveLength(1);
    expect(doc.store.getState()).toMatchObject({ queued: 2, review: 1 });
    // Shown as the user made them, until the user decides (plan item 92).
    expect(shownName(doc)).toBe("Pier");
    await vi.waitFor(async () => {
      const kept = await db.queued(user, testProject.id);
      expect(kept.map((b) => b.state)).toEqual(["review", "pending"]);
      expect(kept[0]?.refusal).toMatchObject({
        status: 422,
        error: { code: "validation_failed" },
      });
    });
    expect(doc.pendingBatches().map((b) => b.state)).toEqual([
      "review",
      "queued",
    ]);
    // A batch made since waits behind it too, in the queue.
    await expect(doc.send([rename("bd_a", "Dock", 3)])).resolves.toEqual({
      status: "queued",
    });
    expect(batches).toHaveLength(1);
    await db.close();
  });

  it("tries a queued batch again later that the server refused for when it was sent", async () => {
    let failures = 1;
    const batches = serve(() => {
      if (failures-- > 0) {
        throw refused(503, { code: "not_ready" });
      }
      return applied(2, [{ id: "bd_a", rev: 4 }]);
    });
    const net = connection(false);
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), net);
    await doc.send([rename("bd_a", "Quay", 3)]);
    net.set(true);
    await until(doc, (s) => s.queued === 0);
    expect(batches).toHaveLength(2);
    expect(batches[1]).toEqual(batches[0]);
    await db.close();
  });

  // Verifies: FR-OFF-03/AC4
  it("undoes and redoes offline over the queue: undo takes the latest batch out of it, and redo puts it back", async () => {
    const batches = serve(() => applied(2, [{ id: "bd_a", rev: 4 }]));
    const net = connection(false);
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), net);
    await doc.send([rename("bd_a", "Quay", 3)]);
    await doc.send([rename("bd_a", "Pier", 3)]);
    expect(doc.store.getState()).toMatchObject({
      undoable: 2,
      undoLocal: true,
      redoable: 0,
    });
    await expect(doc.undo()).resolves.toBe(true);
    expect(shownName(doc)).toBe("Quay");
    expect(doc.store.getState()).toMatchObject({
      undoable: 1,
      redoable: 1,
      redoLocal: true,
    });
    expect(await db.queued(user, testProject.id)).toHaveLength(1);
    await expect(doc.redo()).resolves.toBe(true);
    expect(shownName(doc)).toBe("Pier");
    await vi.waitFor(async () => {
      expect(await db.queued(user, testProject.id)).toHaveLength(2);
    });
    await doc.undo();
    await doc.undo();
    expect(shownName(doc)).toBe("Harbour");
    expect(doc.store.getState()).toMatchObject({
      undoable: 0,
      redoable: 2,
      queued: 0,
    });
    expect(await db.queued(user, testProject.id)).toEqual([]);
    // A new change ends what redo would put back.
    await doc.send([rename("bd_a", "Dock", 3)]);
    expect(doc.store.getState()).toMatchObject({ undoable: 1, redoable: 0 });
    // What undo took out never reaches the server.
    net.set(true);
    await until(doc, (s) => s.queued === 0);
    expect(batches.map((b) => b[0]?.payload)).toEqual([
      { id: "bd_a", name: "Dock" },
    ]);
    await db.close();
  });

  it("does not undo a queued batch that was sent and got no answer, which may have applied", async () => {
    const batches = serve(() => {
      throw new NetworkError(new TypeError("Failed to fetch"));
    });
    const net = connection(true);
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), net);
    await doc.send([rename("bd_a", "Quay", 3)]);
    // The browser goes offline after the try, before the next.
    net.set(false);
    expect(batches).toHaveLength(1);
    expect(doc.store.getState()).toMatchObject({
      queued: 1,
      undoLocal: false,
    });
    await expect(doc.undo()).resolves.toBe(false);
    expect(shownName(doc)).toBe("Quay");
    await db.close();
  });

  it("takes the batches of another tab's document once it has gone, and not while it is kept", async () => {
    const batches = serve(() => applied(2, [{ id: "bd_a", rev: 4 }]));
    const net = connection(false);
    const locks = fakeLocks();
    const db = new OfflineDatabase();
    const other = await open(db, locks, net);
    await other.doc.send([rename("bd_a", "Quay", 3)]);
    const db2 = new OfflineDatabase();
    const mine = await open(db2, locks, net);
    expect(mine.doc.store.getState().queued).toBe(0);
    expect(shownName(mine.doc)).toBe("Harbour");
    other.doc.dispose();
    await until(mine.doc, (s) => s.queued === 1);
    expect(shownName(mine.doc)).toBe("Quay");
    net.set(true);
    await until(mine.doc, (s) => s.queued === 0);
    expect(batches).toHaveLength(1);
    await db.close();
    await db2.close();
  });

  it("refuses a batch that the device cannot keep, as for its quota, as it would without a queue", async () => {
    serve(() => applied(2, [{ id: "bd_a", rev: 4 }]));
    const db = new OfflineDatabase();
    const { doc, log } = await open(db, fakeLocks(), connection(false));
    vi.spyOn(db, "saveQueued").mockRejectedValue(
      new DOMException("Full.", "QuotaExceededError"),
    );
    await expect(doc.send([rename("bd_a", "Quay", 3)])).rejects.toBeInstanceOf(
      NetworkError,
    );
    expect(shownName(doc)).toBe("Harbour");
    expect(doc.store.getState().queued).toBe(0);
    // A full device is no fault of the app's.
    expect(log.entries()).toEqual([]);
    await db.close();
  });

  it("keeps the batches while the page holds the session that the device kept, without its CSRF token, until it has its own", async () => {
    const batches = serve(() => applied(2, [{ id: "bd_a", rev: 4 }]));
    const db = new OfflineDatabase();
    const { doc, queryClient } = await open(db, fakeLocks(), connection(true), {
      ...testSession,
      csrf_token: "",
    });
    await expect(doc.send([rename("bd_a", "Quay", 3)])).resolves.toEqual({
      status: "queued",
    });
    expect(batches).toEqual([]);
    queryClient.setQueryData(sessionQuery.queryKey, testSession);
    await until(doc, (s) => s.queued === 0);
    expect(batches).toHaveLength(1);
    await db.close();
  });

  it("takes a queued batch off the device once it applied, though the document has yet to have its operation", async () => {
    // Another session's changes came before it, which the document has yet
    // to have: the answer's seq is past the next.
    const batches = serve(() => applied(5, [{ id: "bd_a", rev: 4 }]));
    const net = connection(false);
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), net);
    await doc.send([rename("bd_a", "Quay", 3)]);
    net.set(true);
    await vi.waitFor(() => expect(batches).toHaveLength(1));
    await until(doc, (s) => s.queued === 0);
    await vi.waitFor(async () => {
      expect(await db.queued(user, testProject.id)).toEqual([]);
    });
    await db.close();
  });

  it("records a queued batch as sent before it sends it, which a page that loads meanwhile does not undo", async () => {
    let answer: (value: CommandResultBody) => void = () => {};
    const batches = serve(
      () =>
        new Promise<CommandResultBody>((resolve) => {
          answer = resolve;
        }),
    );
    const net = connection(false);
    const db = new OfflineDatabase();
    const first = await open(db, fakeLocks(), net);
    await first.doc.send([rename("bd_a", "Quay", 3)]);
    expect((await db.queued(user, testProject.id))[0]?.sent).toBe(false);
    net.set(true);
    await vi.waitFor(() => expect(batches).toHaveLength(1));
    expect((await db.queued(user, testProject.id))[0]?.sent).toBe(true);
    // The page reloads before the answer comes, and opens offline.
    first.doc.dispose();
    net.set(false);
    const again = new OfflineDatabase();
    const second = await open(again, fakeLocks(), net);
    expect(second.doc.store.getState()).toMatchObject({
      queued: 1,
      undoLocal: false,
    });
    answer(applied(2, [{ id: "bd_a", rev: 4 }]));
    await db.close();
    await again.close();
  });

  it("reaches the server, as it says, once the page has its own session, whether or not it keeps a batch", async () => {
    serve(() => applied(2, [{ id: "bd_a", rev: 4 }]));
    const db = new OfflineDatabase();
    const { doc, queryClient } = await open(db, fakeLocks(), connection(true), {
      ...testSession,
      csrf_token: "",
    });
    expect(doc.store.getState().holding).toBe(true);
    queryClient.setQueryData(sessionQuery.queryKey, testSession);
    expect(doc.store.getState().holding).toBe(false);
    await db.close();
  });

  it("lets what waits for the page's batches go on while a queued batch needs review", async () => {
    serve(() => {
      throw refused(422, { code: "validation_failed" });
    });
    const net = connection(false);
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), net);
    await doc.send([rename("bd_a", "Quay", 3)]);
    net.set(true);
    await until(doc, (s) => s.review === 1);
    await expect(doc.sent()).resolves.toBeUndefined();
    await db.close();
  });

  it("reaches the server while the queue waits after a refusal for when a batch was sent, and keeps the batches made meanwhile", async () => {
    const batches = serve(() => {
      throw refused(403, { code: "forbidden" });
    });
    const net = connection(false);
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), net);
    await doc.send([rename("bd_a", "Quay", 3)]);
    net.set(true);
    await vi.waitFor(async () => {
      const [kept] = await db.queued(user, testProject.id);
      expect(kept?.refusal?.status).toBe(403);
    });
    expect(batches.length).toBeGreaterThan(0);
    expect(doc.store.getState()).toMatchObject({
      queued: 1,
      review: 0,
      holding: false,
    });
    await expect(doc.send([rename("bd_a", "Pier", 3)])).resolves.toEqual({
      status: "queued",
    });
    doc.dispose();
    await db.close();
  });

  it("refuses a batch with the device's refusal where it cannot keep it while a batch before it needs review", async () => {
    serve(() => {
      throw refused(422, { code: "validation_failed" });
    });
    const net = connection(false);
    const db = new OfflineDatabase();
    const { doc, log } = await open(db, fakeLocks(), net);
    await doc.send([rename("bd_a", "Quay", 3)]);
    net.set(true);
    await until(doc, (s) => s.review === 1);
    await vi.waitFor(async () => {
      expect((await db.queued(user, testProject.id))[0]?.state).toBe("review");
    });
    const full = new DOMException("Full.", "QuotaExceededError");
    vi.spyOn(db, "saveQueued").mockRejectedValue(full);
    await expect(doc.send([rename("bd_a", "Pier", 3)])).rejects.toBe(full);
    // Which the error console has, as the form says.
    expect(log.entries()).toHaveLength(1);
    await db.close();
  });

  it("refuses a batch that met a fault of the page's own, as it would without a queue, rather than keep it", async () => {
    const fault = new Error(
      "The server answered a batch with a list of results.",
    );
    serve(() => {
      throw fault;
    });
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), connection(true));
    await expect(doc.send([rename("bd_a", "Quay", 3)])).rejects.toBe(fault);
    expect(doc.store.getState()).toMatchObject({ queued: 0, holding: false });
    expect(await db.queued(user, testProject.id)).toEqual([]);
    await db.close();
  });

  it("sends no step of the history before a batch that the queue kept as the step waited for it", async () => {
    let failing: (() => void) | undefined;
    let seq = 1;
    const batches = serve(
      (commands) => {
        const name = nameOf(commands);
        if (name === "Quay") {
          seq = 2;
          return applied(
            2,
            [{ id: "bd_a", rev: 4 }],
            [renameBack("bd_a", "Harbour", 4)],
          );
        }
        if (name === "Pier" && failing === undefined) {
          return new Promise((_, reject) => {
            failing = () =>
              reject(new NetworkError(new TypeError("Failed to fetch")));
          });
        }
        throw new NetworkError(new TypeError("Failed to fetch"));
      },
      () => seq,
    );
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), connection(true));
    await doc.send([rename("bd_a", "Quay", 3)]);
    const pier = doc.send([rename("bd_a", "Pier", 4)]);
    await vi.waitFor(() => expect(failing).toBeDefined());
    const undo = doc.undo();
    failing?.();
    await expect(pier).resolves.toEqual({ status: "queued" });
    await expect(undo).resolves.toBe(false);
    expect(batches.some((b) => nameOf(b) === "Harbour")).toBe(false);
    expect(shownName(doc)).toBe("Pier");
    doc.dispose();
    await db.close();
  });

  it("redoes a change of the history undone after undo took a batch out of the queue before that batch", async () => {
    // A server that applies each rename of bd_a, whose undo renames it back.
    let seq = 1;
    let rev = 3;
    let name = "Harbour";
    const batches: CommandBody[][] = [];
    fakeApi((operation, input) => {
      switch (operation) {
        case "getProject":
          return { ...testProject, seq };
        case "listFolders":
          return { items: [], next_cursor: null };
        case "listBoards":
          return { items: [board("bd_a", name, rev)], next_cursor: null };
        case "applyCommands": {
          const commands = (input as { body: { commands: CommandBody[] } }).body
            .commands;
          batches.push(structuredClone(commands));
          const was = name;
          name = nameOf(commands) ?? name;
          seq++;
          rev++;
          return applied(
            seq,
            [{ id: "bd_a", rev }],
            [renameBack("bd_a", was, rev)],
          );
        }
      }
      throw new Error(`unexpected ${operation}`);
    });
    const net = connection(false);
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), net);
    await doc.send([rename("bd_a", "Quay", 3)]);
    await doc.send([rename("bd_a", "Pier", 3)]);
    await doc.undo();
    net.set(true);
    await until(doc, (s) => s.queued === 0 && s.sending === 0);
    // The change that the queue sent is undone on the server.
    await expect(doc.undo()).resolves.toBe(true);
    expect(shownName(doc)).toBe("Harbour");
    expect(doc.store.getState()).toMatchObject({
      redoable: 2,
      redoLocal: false,
    });
    await expect(doc.redo()).resolves.toBe(true);
    expect(shownName(doc)).toBe("Quay");
    expect(doc.store.getState().redoLocal).toBe(true);
    await doc.redo();
    await until(doc, (s) => s.sending === 0);
    expect(batches.map((b) => nameOf(b))).toEqual([
      "Quay",
      "Harbour",
      "Quay",
      "Pier",
    ]);
    await db.close();
  });

  it("hears of another tab's batches each time that it keeps some, as once its queue was empty as this one opened", async () => {
    serve(() => applied(2, [{ id: "bd_a", rev: 4 }]));
    const net = connection(false);
    const locks = fakeLocks();
    const hub = fakeBroadcast();
    const db = new OfflineDatabase();
    const other = await open(
      db,
      locks,
      net,
      testSession,
      deviceQueue(db, { secure: true, locks, broadcast: hub.tab() }),
    );
    await other.doc.send([rename("bd_a", "Quay", 3)]);
    net.set(true);
    await until(other.doc, (s) => s.queued === 0);
    await vi.waitFor(async () => {
      expect(await db.queued(user, testProject.id)).toEqual([]);
    });
    const db2 = new OfflineDatabase();
    const mine = await open(
      db2,
      locks,
      net,
      testSession,
      deviceQueue(db2, { secure: true, locks, broadcast: hub.tab() }),
    );
    net.set(false);
    await other.doc.send([rename("bd_a", "Pier", 3)]);
    await vi.waitFor(async () => {
      expect(await db.queued(user, testProject.id)).toHaveLength(1);
    });
    other.doc.dispose();
    await until(mine.doc, (s) => s.queued === 1);
    expect(shownName(mine.doc)).toBe("Pier");
    await db.close();
    await db2.close();
  });

  it("writes none of the batches that signing out took off the device there again, in this tab or another, but those made since", async () => {
    const batches = serve(() => new Promise<never>(() => {}));
    const net = connection(false);
    const locks = fakeLocks();
    const hub = fakeBroadcast();
    const db = new OfflineDatabase();
    const queue = deviceQueue(db, {
      secure: true,
      locks,
      broadcast: hub.tab(),
    }) as QueueStore;
    const db2 = new OfflineDatabase();
    const other = await open(
      db2,
      locks,
      net,
      testSession,
      deviceQueue(db2, { secure: true, locks, broadcast: hub.tab() }),
    );
    const mine = await open(db, locks, net, testSession, queue);
    await mine.doc.send([rename("bd_a", "Quay", 3)]);
    await other.doc.send([rename("bd_a", "Dock", 3)]);
    expect(await db.queued(user, testProject.id)).toHaveLength(2);
    await queue.discard(user);
    expect(await db.queued(user, testProject.id)).toEqual([]);
    // A change made since is the account's to keep.
    await mine.doc.send([rename("bd_a", "Pier", 3)]);
    // Each tab sends what it held, which it records as sent before it does,
    // as of an answer that comes after.
    net.set(true);
    await vi.waitFor(() => expect(batches).toHaveLength(2));
    expect(
      (await db.queued(user, testProject.id)).map((b) => nameOf(b.commands)),
    ).toEqual(["Pier"]);
    mine.doc.dispose();
    other.doc.dispose();
    await db.close();
    await db2.close();
  });

  it("sends another tab's batch, taken as that tab closed, as it was made, and keeps it for review where this one's changed what it changes, rather than write over it", async () => {
    const batches = serveBoard();
    const net = connection(false);
    const locks = fakeLocks();
    const db = new OfflineDatabase();
    const other = await open(db, locks, net);
    await other.doc.send([rename("bd_a", "Dock", 3)]);
    const db2 = new OfflineDatabase();
    const mine = await open(db2, locks, net);
    await mine.doc.send([rename("bd_a", "Quay", 3)]);
    other.doc.dispose();
    await until(mine.doc, (s) => s.queued === 2);
    net.set(true);
    await until(mine.doc, (s) => s.review === 1);
    // The other tab's was made on what the server had, without this one's.
    expect(batches.map((b) => [nameOf(b), b[0]?.expected_rev])).toEqual([
      ["Quay", { bd_a: 3 }],
      ["Dock", { bd_a: 3 }],
    ]);
    await db.close();
    await db2.close();
  });

  it("moves a batch made on top of another tab's, taken as that tab closed, past it once it applies, as the next page does after this one closed too", async () => {
    const batches = serveBoard();
    const net = connection(false);
    const locks = fakeLocks();
    const db = new OfflineDatabase();
    const other = await open(db, locks, net);
    await other.doc.send([rename("bd_a", "Dock", 3)]);
    const db2 = new OfflineDatabase();
    const mine = await open(db2, locks, net);
    other.doc.dispose();
    await until(mine.doc, (s) => s.queued === 1);
    expect(shownName(mine.doc)).toBe("Dock");
    await mine.doc.send([rename("bd_a", "Pier", 3)]);
    mine.doc.dispose();
    const db3 = new OfflineDatabase();
    const next = await open(db3, locks, net);
    await until(next.doc, (s) => s.queued === 2);
    net.set(true);
    await until(next.doc, (s) => s.queued === 0);
    expect(batches.map((b) => [nameOf(b), b[0]?.expected_rev])).toEqual([
      ["Dock", { bd_a: 3 }],
      ["Pier", { bd_a: 4 }],
    ]);
    expect(shownName(next.doc)).toBe("Pier");
    await db.close();
    await db2.close();
    await db3.close();
  });

  it("undoes the latest change that the queue keeps offline, by when it was made, as of another tab's that it took after its own as that tab closed", async () => {
    serve(() => applied(2, [{ id: "bd_a", rev: 4 }]));
    const net = connection(false);
    const locks = fakeLocks();
    const db = new OfflineDatabase();
    const other = await open(db, locks, net);
    await other.doc.send([rename("bd_a", "Dock", 3)]);
    const db2 = new OfflineDatabase();
    const mine = await open(db2, locks, net);
    clock = "2026-11-27T10:05:00Z";
    await mine.doc.send([rename("bd_a", "Quay", 3)]);
    other.doc.dispose();
    await until(mine.doc, (s) => s.queued === 2);
    expect(shownName(mine.doc)).toBe("Dock");
    expect(mine.doc.store.getState()).toMatchObject({
      undoable: 2,
      undoLocal: true,
    });
    const names = async () =>
      (await db2.queued(user, testProject.id)).map((b) => nameOf(b.commands));
    // This page's, made after the other tab's.
    await expect(mine.doc.undo()).resolves.toBe(true);
    expect(await names()).toEqual(["Dock"]);
    expect(shownName(mine.doc)).toBe("Dock");
    await expect(mine.doc.undo()).resolves.toBe(true);
    expect(await names()).toEqual([]);
    expect(shownName(mine.doc)).toBe("Harbour");
    await expect(mine.doc.redo()).resolves.toBe(true);
    await expect(mine.doc.redo()).resolves.toBe(true);
    await vi.waitFor(async () =>
      expect(await names()).toEqual(["Dock", "Quay"]),
    );
    expect(shownName(mine.doc)).toBe("Quay");
    await db.close();
    await db2.close();
  });

  it("keeps a batch that no answer came to before one made after it that the queue kept first, in the order that they were made", async () => {
    let fail: (error: unknown) => void = () => {};
    serve(
      () =>
        new Promise<CommandResultBody>((_, reject) => {
          fail = reject;
        }),
    );
    const net = connection(true);
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), net);
    const first = doc.send([rename("bd_a", "Quay", 3)]);
    await vi.waitFor(() => expect(doc.store.getState().sending).toBe(1));
    // The browser goes offline as the first is on its way.
    net.set(false);
    clock = "2026-11-27T10:05:00Z";
    await doc.send([rename("bd_a", "Pier", 3)]);
    fail(new NetworkError(new TypeError("Failed to fetch")));
    await expect(first).resolves.toEqual({ status: "queued" });
    expect(
      (await db.queued(user, testProject.id)).map((b) => nameOf(b.commands)),
    ).toEqual(["Quay", "Pier"]);
    await db.close();
  });

  it("keeps its lock once dropped until the try under way is answered, so that another tab's document sends none of its batches meanwhile", async () => {
    let answer: (value: CommandResultBody) => void = () => {};
    const batches = serve(
      () =>
        new Promise<CommandResultBody>((resolve) => {
          answer = resolve;
        }),
    );
    const net = connection(false);
    const locks = fakeLocks();
    const db = new OfflineDatabase();
    const other = await open(db, locks, net);
    await other.doc.send([rename("bd_a", "Quay", 3)]);
    const db2 = new OfflineDatabase();
    const mine = await open(db2, locks, net);
    net.set(true);
    await vi.waitFor(() => expect(batches).toHaveLength(1));
    other.doc.dispose();
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(mine.doc.store.getState().queued).toBe(0);
    answer(applied(2, [{ id: "bd_a", rev: 4 }]));
    await vi.waitFor(async () => {
      expect(await db.queued(user, testProject.id)).toEqual([]);
    });
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(batches).toHaveLength(1);
    expect(mine.doc.store.getState().queued).toBe(0);
    await db.close();
    await db2.close();
  });

  it("counts a batch as the queue's once the device has written it, which what waits for the page's batches waits for, as a reload", async () => {
    serve(() => applied(2, [{ id: "bd_a", rev: 4 }]));
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), connection(false));
    const save = db.saveQueued.bind(db);
    let write = () => {};
    vi.spyOn(db, "saveQueued").mockImplementation(async (...args) => {
      await new Promise<void>((resolve) => {
        write = resolve;
      });
      return save(...args);
    });
    const sending = doc.send([rename("bd_a", "Quay", 3)]);
    let sent = false;
    void whenSent(doc, new AbortController().signal).then(() => {
      sent = true;
    });
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(sent).toBe(false);
    expect(doc.store.getState()).toMatchObject({ sending: 1, queued: 0 });
    write();
    await expect(sending).resolves.toEqual({ status: "queued" });
    await vi.waitFor(() => expect(sent).toBe(true));
    expect(doc.store.getState().queued).toBe(1);
    await db.close();
  });

  it("puts nothing back by redo of a change that undo took out of the queue as the device refused to keep it", async () => {
    const batches = serve(() => applied(2, [{ id: "bd_a", rev: 4 }]));
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), connection(false));
    let refuse = () => {};
    const saves = vi.spyOn(db, "saveQueued").mockImplementationOnce(
      () =>
        new Promise<void>((_, reject) => {
          refuse = () =>
            reject(new DOMException("Full.", "QuotaExceededError"));
        }),
    );
    const sending = doc.send([rename("bd_a", "Quay", 3)]);
    await vi.waitFor(() => expect(saves).toHaveBeenCalledOnce());
    const undoing = doc.undo();
    refuse();
    await expect(sending).rejects.toBeInstanceOf(NetworkError);
    await expect(undoing).resolves.toBe(true);
    expect(doc.store.getState()).toMatchObject({
      redoable: 0,
      redoLocal: false,
    });
    await expect(doc.redo()).resolves.toBe(false);
    expect(shownName(doc)).toBe("Harbour");
    expect(batches).toEqual([]);
    await db.close();
  });

  it("does not reach the server, as it says, through the try after one that no answer came to, until it is answered, and keeps the batches made meanwhile", async () => {
    let failures = 1;
    let answer: (value: CommandResultBody) => void = () => {};
    const batches = serve(() => {
      if (failures-- > 0) {
        throw new NetworkError(new TypeError("Failed to fetch"));
      }
      return new Promise<CommandResultBody>((resolve) => {
        answer = resolve;
      });
    });
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), connection(true));
    await expect(doc.send([rename("bd_a", "Quay", 3)])).resolves.toEqual({
      status: "queued",
    });
    await vi.waitFor(() => expect(batches).toHaveLength(2));
    expect(doc.store.getState().holding).toBe(true);
    await expect(doc.send([rename("bd_a", "Pier", 3)])).resolves.toEqual({
      status: "queued",
    });
    answer(applied(2, [{ id: "bd_a", rev: 4 }]));
    await vi.waitFor(() => expect(batches).toHaveLength(3));
    expect(doc.store.getState().holding).toBe(false);
    answer(applied(3, [{ id: "bd_a", rev: 5 }]));
    await until(doc, (s) => s.queued === 0);
    await db.close();
  });

  it("keeps none of the batches on their way as the page signs out, but those that another account's signing in leaves", async () => {
    for (const after of [null, { ...testSession, user_id: "usr_other" }]) {
      let answer: (value: CommandResultBody) => void = () => {};
      const batches = serve(
        () =>
          new Promise<CommandResultBody>((resolve) => {
            answer = resolve;
          }),
      );
      const db = new OfflineDatabase();
      const { doc, queryClient } = await open(
        db,
        fakeLocks(),
        connection(true),
      );
      const first = doc.send([rename("bd_a", "Quay", 3)]);
      const second = doc.send([rename("bd_a", "Pier", 3)]);
      await vi.waitFor(() => expect(batches).toHaveLength(1));
      queryClient.setQueryData(sessionQuery.queryKey, after);
      doc.dispose();
      answer(applied(2, [{ id: "bd_a", rev: 4 }]));
      await first;
      if (after === null) {
        await expect(second).rejects.toThrow();
        expect(await db.queued(user)).toEqual([]);
      } else {
        await expect(second).resolves.toEqual({ status: "queued" });
        expect((await db.queued(user)).map((b) => nameOf(b.commands))).toEqual([
          "Pier",
        ]);
        await db.deleteQueued(user);
      }
      expect(batches).toHaveLength(1);
      await db.close();
    }
  });

  it("keeps its lock once dropped until a batch that waits for a step of the history has been kept, as the account's once another signed in", async () => {
    let answer: (value: CommandResultBody) => void = () => {};
    let calls = 0;
    let seq = 1;
    serve(
      () => {
        calls++;
        if (calls === 1) {
          seq = 2;
          return applied(
            2,
            [{ id: "bd_a", rev: 4 }],
            [renameBack("bd_a", "Harbour", 4)],
          );
        }
        return new Promise<CommandResultBody>((resolve) => {
          answer = resolve;
        });
      },
      () => seq,
    );
    const db = new OfflineDatabase();
    const { doc, queryClient } = await open(db, fakeLocks(), connection(true));
    await doc.send([rename("bd_a", "Quay", 3)]);
    const undoing = doc.undo();
    await vi.waitFor(() => expect(calls).toBe(2));
    const after = doc.send([rename("bd_a", "Pier", 4)]);
    const other: SignedInBody = { ...testSession, user_id: "usr_other" };
    queryClient.setQueryData(sessionQuery.queryKey, other);
    doc.dispose();
    answer(applied(3, [{ id: "bd_a", rev: 5 }]));
    await undoing;
    await expect(after).resolves.toEqual({ status: "queued" });
    expect((await db.queued(user)).map((b) => nameOf(b.commands))).toEqual([
      "Pier",
    ]);
    await db.close();
  });

  it("keeps no queue in a browser without Web Locks, which could not tell an open tab's batches from a closed one's", () => {
    expect(deviceQueue(new OfflineDatabase(), { secure: true })).toBe(
      undefined,
    );
  });
});
