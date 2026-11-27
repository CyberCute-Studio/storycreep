import "fake-indexeddb/auto";
import {
  ApiError,
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
import { OfflineDatabase, type QueueRecord } from "../offline/db";
import {
  deviceQueue,
  type QueueBroadcast,
  type QueueLocks,
  type QueueNews,
} from "../offline/queue";
import { testSession } from "../test/app";
import { fakeApi, refused, testProject } from "../test/fake";
import { fakeLocks } from "../test/locks";
import { fakeRealtime } from "../test/realtime";
import { Documents } from "./documents";
import type { QueueStore } from "./queue";
import { whenSent } from "./sent";
import type { Command, DocumentOptions } from "./store";
import { ConflictError, ProjectDocument } from "./store";

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
 * A fake of the browser's BroadcastChannel between tabs, as the queue's
 * channel uses it (queueBroadcast): each listener, on a channel object of
 * its own, hears what any tab posts, its own tab's too.
 */
function fakeBroadcast() {
  const listeners = new Set<(news: QueueNews) => void>();
  const signedOut = new Set<(user: string) => void>();
  return {
    tab(): QueueBroadcast {
      return {
        post: (news) => {
          for (const listener of [...listeners]) {
            listener(structuredClone(news));
          }
        },
        listen: (listener) => {
          const own = (news: QueueNews) => listener(news);
          listeners.add(own);
          return () => {
            listeners.delete(own);
          };
        },
        signedOut: (user) => {
          for (const listener of [...signedOut]) {
            listener(user);
          }
        },
        listenSignedOut: (listener) => {
          const own = (user: string) => listener(user);
          signedOut.add(own);
          return () => {
            signedOut.delete(own);
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
 * queue given, with the tree read, and the session given, and the other
 * options given, as a real-time channel.
 */
async function open(
  db: OfflineDatabase,
  locks: QueueLocks,
  net: ReturnType<typeof connection>,
  session = testSession,
  queue: QueueStore | undefined = deviceQueue(db, { secure: true, locks }),
  options: Pick<
    DocumentOptions,
    "realtime" | "random" | "storage" | "answerWait"
  > = {},
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
    ...options,
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

  it("takes what signing out could not take off the device off it before a document takes the account's batches again, and keeps a batch made after", async () => {
    const batches = serve(() => new Promise<never>(() => {}));
    const net = connection(false);
    const locks = fakeLocks();
    const db = new OfflineDatabase();
    const queue = deviceQueue(db, {
      secure: true,
      locks,
      storage: window.localStorage,
      now: () => "2026-11-27T10:01:00Z",
    }) as QueueStore;
    const first = await open(db, locks, net, testSession, queue);
    await first.doc.send([rename("bd_a", "Quay", 3)]);
    await first.doc.send([rename("bd_a", "Dock", 3)]);
    const [named, unnamed] = await db.queued(user, testProject.id);
    expect([named, unnamed].map((b) => nameOf(b?.commands ?? []))).toEqual([
      "Quay",
      "Dock",
    ]);
    first.doc.dispose();
    await first.doc.lettingGo();
    // The device refuses each removal as the page signs out: of the change
    // that the dialog named, and then of any, as of a device that it could
    // not read.
    const deletes = vi
      .spyOn(db, "deleteQueued")
      .mockRejectedValue(new Error("The database connection is closing."));
    await expect(queue.discard(user, named ? [named] : [])).rejects.toThrow();
    await expect(queue.discard(user)).rejects.toThrow();
    expect(await db.queued(user, testProject.id)).toHaveLength(2);
    // Until the device takes them off it, no document takes the account's.
    const blocked = await open(db, locks, net, testSession, queue);
    expect(blocked.doc.store.getState().queued).toBe(0);
    blocked.doc.dispose();
    await blocked.doc.lettingGo();
    deletes.mockRestore();
    // A change made after signing out, as once the account signed in again.
    clock = "2026-11-27T10:05:00Z";
    const later = await open(db, locks, net, testSession, queue);
    expect(later.doc.store.getState().queued).toBe(0);
    await later.doc.send([rename("bd_a", "Pier", 3)]);
    later.doc.dispose();
    await later.doc.lettingGo();
    const again = await open(db, locks, net, testSession, queue);
    expect(shownName(again.doc)).toBe("Pier");
    expect(again.doc.store.getState().queued).toBe(1);
    expect(
      (await db.queued(user, testProject.id)).map((b) => nameOf(b.commands)),
    ).toEqual(["Pier"]);
    expect(window.localStorage.length).toBe(0);
    expect(batches).toEqual([]);
    again.doc.dispose();
    await again.doc.lettingGo();
    await db.close();
  });

  it("takes what the user signed out anyway with off the device before a document takes the account's batches again, as of a tab that closed before it had, but not once forgotten, as of a page still signed in", async () => {
    const batches = serve(() => new Promise<never>(() => {}));
    const net = connection(false);
    const locks = fakeLocks();
    const db = new OfflineDatabase();
    const queue = deviceQueue(db, {
      secure: true,
      locks,
      storage: window.localStorage,
    }) as QueueStore;
    const first = await open(db, locks, net, testSession, queue);
    await first.doc.send([rename("bd_a", "Quay", 3)]);
    await first.doc.send([rename("bd_a", "Dock", 3)]);
    const [named] = await db.queued(user, testProject.id);
    first.doc.dispose();
    await first.doc.lettingGo();
    // The server did not sign the page out: the change stays.
    (await queue.intend(user, named ? [named] : []))(false);
    expect(window.localStorage.length).toBe(0);
    // It did, and the tab closed before it took the change off the device.
    (await queue.intend(user, named ? [named] : []))(true);
    const next = await open(db, locks, net, testSession, queue);
    expect(next.doc.store.getState().queued).toBe(1);
    expect(
      (await db.queued(user, testProject.id)).map((b) => nameOf(b.commands)),
    ).toEqual(["Dock"]);
    expect(window.localStorage.length).toBe(0);
    expect(batches).toEqual([]);
    next.doc.dispose();
    await next.doc.lettingGo();
    await db.close();
  });

  it("takes off the device too what another tab remembers as it takes off it what signing out could not, rather than forget it", async () => {
    serve(() => new Promise<never>(() => {}));
    const net = connection(false);
    const locks = fakeLocks();
    const db = new OfflineDatabase();
    const queue = deviceQueue(db, {
      secure: true,
      locks,
      storage: window.localStorage,
    }) as QueueStore;
    const first = await open(db, locks, net, testSession, queue);
    await first.doc.send([rename("bd_a", "Quay", 3)]);
    await first.doc.send([rename("bd_a", "Dock", 3)]);
    const [quayBatch, dockBatch] = await db.queued(user, testProject.id);
    first.doc.dispose();
    await first.doc.lettingGo();
    const deleting = db.deleteQueued.bind(db);
    const deletes = vi.spyOn(db, "deleteQueued");
    deletes.mockRejectedValueOnce(
      new Error("The database connection is closing."),
    );
    await expect(
      queue.discard(user, quayBatch ? [quayBatch] : []),
    ).rejects.toThrow();
    // As the next document takes it off the device, another tab's user
    // signs out anyway with the other, and the server signs that tab out.
    deletes.mockImplementationOnce(async (...args) => {
      (await queue.intend(user, dockBatch ? [dockBatch] : []))(true);
      return deleting(...args);
    });
    const next = await open(db, locks, net, testSession, queue);
    expect(next.doc.store.getState().queued).toBe(0);
    expect(await db.queued(user, testProject.id)).toEqual([]);
    expect(window.localStorage.length).toBe(0);
    next.doc.dispose();
    await next.doc.lettingGo();
    await db.close();
  });

  it("neither takes off the device nor takes what a sign-out under way names until the server has answered, and tells the other tabs once it has signed the page out", async () => {
    const batches = serve(() => new Promise<never>(() => {}));
    const net = connection(false);
    const locks = fakeLocks();
    const db = new OfflineDatabase();
    const hub = fakeBroadcast();
    const queue = deviceQueue(db, {
      secure: true,
      locks,
      broadcast: hub.tab(),
      storage: window.localStorage,
    }) as QueueStore;
    const heard: string[] = [];
    const other = deviceQueue(db, {
      secure: true,
      locks,
      broadcast: hub.tab(),
      storage: window.localStorage,
      signedOutElsewhere: (userId) => heard.push(userId),
    }) as QueueStore;
    const first = await open(db, locks, net, testSession, queue);
    await first.doc.send([rename("bd_a", "Quay", 3)]);
    await first.doc.send([rename("bd_a", "Dock", 3)]);
    const [named] = await db.queued(user, testProject.id);
    first.doc.dispose();
    await first.doc.lettingGo();
    // As the server answers the sign-out, another tab's document starts,
    // which takes the change that the sign-out did not name alone.
    const settle = await queue.intend(user, named ? [named] : []);
    const during = await open(db, locks, net, testSession, other);
    expect(during.doc.store.getState().queued).toBe(1);
    expect(
      (await db.queued(user, testProject.id)).map((b) => nameOf(b.commands)),
    ).toEqual(["Quay", "Dock"]);
    during.doc.dispose();
    await during.doc.lettingGo();
    // The page is still signed in: the change stays, which the next takes.
    settle(false);
    expect(window.localStorage.length).toBe(0);
    const after = await open(db, locks, net, testSession, other);
    expect(after.doc.store.getState().queued).toBe(2);
    after.doc.dispose();
    await after.doc.lettingGo();
    expect(heard).toEqual([]);
    // Signed out, the page tells the other tabs, and the next take takes
    // the change off the device, as of a tab that closed before it had.
    (await queue.intend(user, named ? [named] : []))(true);
    expect(heard).toEqual([user]);
    const next = await open(db, locks, net, testSession, other);
    expect(next.doc.store.getState().queued).toBe(1);
    expect(
      (await db.queued(user, testProject.id)).map((b) => nameOf(b.commands)),
    ).toEqual(["Dock"]);
    expect(window.localStorage.length).toBe(0);
    expect(batches).toEqual([]);
    next.doc.dispose();
    await next.doc.lettingGo();
    await db.close();
  });

  it("keeps a batch whose try no answer comes to for a while on the device, as sent, with those after it, and settles it once the answer comes", async () => {
    let answer = (_: unknown) => {};
    const batches = serve(
      () =>
        new Promise((resolve) => {
          answer = resolve;
        }),
    );
    const net = connection(true);
    const locks = fakeLocks();
    const db = new OfflineDatabase();
    const queue = deviceQueue(db, { secure: true, locks });
    const { doc } = await open(db, locks, net, testSession, queue, {
      answerWait: 20,
    });
    const quay = doc.send([rename("bd_a", "Quay", 3)]);
    await until(doc, (s) => s.queued === 1);
    expect(await quay).toEqual({ status: "queued" });
    expect(doc.store.getState().holding).toBe(true);
    // A change made while the try goes on is kept too, and waits for it.
    expect(await doc.send([rename("bd_a", "Dock", 3)])).toEqual({
      status: "queued",
    });
    expect(
      (await db.queued(user, testProject.id)).map((b) => [
        nameOf(b.commands),
        b.sent,
      ]),
    ).toEqual([
      ["Quay", true],
      ["Dock", false],
    ]);
    expect(batches).toHaveLength(1);
    answer(applied(testProject.seq + 1, [{ id: "bd_a", rev: 4 }]));
    await until(doc, (s) => s.queued === 1 && batches.length === 2);
    expect(
      (await db.queued(user, testProject.id)).map((b) => nameOf(b.commands)),
    ).toEqual(["Dock"]);
    expect(batches[1]?.[0]?.expected_rev).toEqual({ bd_a: 4 });
    answer(applied(testProject.seq + 2, [{ id: "bd_a", rev: 5 }]));
    await until(doc, (s) => s.queued === 0 && !s.holding);
    expect(await db.queued(user, testProject.id)).toEqual([]);
    expect(shownName(doc)).toBe("Dock");
    doc.dispose();
    await doc.lettingGo();
    await db.close();
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
    // Kept all the same, which a step of the history waits for.
    expect(doc.store.getState()).toMatchObject({
      sending: 1,
      queued: 0,
      holdsQueued: true,
      sendsQueued: false,
    });
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

  it("keeps a batch that waited behind a try once dropped, as the try's answer leaves the page unable to send it, and refuses it once the page signed out", async () => {
    for (const after of [{ ...testSession, user_id: "usr_other" }, null]) {
      let fail: () => void = () => {};
      const batches = serve(
        () =>
          new Promise<CommandResultBody>((_, reject) => {
            fail = () =>
              reject(new NetworkError(new TypeError("Failed to fetch")));
          }),
      );
      const net = connection(true);
      const locks = fakeLocks();
      const db = new OfflineDatabase();
      const { doc, queryClient } = await open(db, locks, net);
      const first = doc.send([rename("bd_a", "Quay", 3)]);
      const second = doc.send([rename("bd_a", "Pier", 3)]);
      await vi.waitFor(() => expect(batches).toHaveLength(1));
      queryClient.setQueryData(sessionQuery.queryKey, after);
      doc.dispose();
      // The browser goes offline as the first is on its way, which no answer
      // comes to.
      net.set(false);
      fail();
      if (after === null) {
        await expect(first).rejects.toBeInstanceOf(NetworkError);
        await expect(second).rejects.toThrow();
        expect(await db.queued(user)).toEqual([]);
      } else {
        await expect(first).resolves.toEqual({ status: "queued" });
        await expect(second).resolves.toEqual({ status: "queued" });
        // And the document lets go of its lock, after which the project's
        // next takes both.
        const next = await open(db, locks, net);
        await until(next.doc, (s) => s.queued === 2);
        expect(shownName(next.doc)).toBe("Pier");
        next.doc.dispose();
        await db.deleteQueued(user);
      }
      await db.close();
    }
  });

  it("takes the batches kept before once the device's database opens, where it opened too late for the document's start", async () => {
    serve(() => applied(2, [{ id: "bd_a", rev: 4 }]));
    const net = connection(false);
    const db = new OfflineDatabase();
    const first = await open(db, fakeLocks(), net);
    await first.doc.send([rename("bd_a", "Quay", 3)]);
    first.doc.dispose();
    await db.close();
    // The browser restarts, and its database is slow to open, as one that
    // another tab blocks.
    const again = new OfflineDatabase();
    const opening = again.open.bind(again);
    let unblock = () => {};
    const blocked = new Promise<void>((resolve) => {
      unblock = resolve;
    });
    vi.spyOn(again, "open").mockImplementation(async () => {
      await blocked;
      return opening();
    });
    const locks = fakeLocks();
    const second = await open(
      again,
      locks,
      net,
      testSession,
      deviceQueue(again, { secure: true, locks, wait: 10 }),
    );
    expect(second.doc.store.getState().queued).toBe(0);
    expect(shownName(second.doc)).toBe("Harbour");
    unblock();
    await until(second.doc, (s) => s.queued === 1);
    expect(shownName(second.doc)).toBe("Quay");
    await again.close();
  });

  it("keeps no queue where the browser refuses the document its lock, as without Web Locks, so that another tab's takes none of its batches for a closed one's", async () => {
    serve(() => {
      throw new NetworkError(new TypeError("Failed to fetch"));
    });
    const db = new OfflineDatabase();
    let refuse: () => void = () => {};
    const locks: QueueLocks = {
      ...fakeLocks(),
      hold: () =>
        new Promise((_, reject) => {
          refuse = () =>
            reject(new DOMException("Not active.", "InvalidStateError"));
        }),
    };
    const log = createErrorLog();
    const queryClient = createQueryClient(log);
    queryClient.setQueryData(sessionQuery.queryKey, testSession);
    const doc = new ProjectDocument(testProject.id, {
      queryClient,
      log,
      now: () => clock,
      online: () => false,
      queue: deviceQueue(db, { secure: true, locks }),
    });
    onTestFinished(() => doc.dispose());
    // A batch kept before the browser refused the lock is not written.
    const before = doc.send([rename("bd_a", "Quay", 3)]);
    refuse();
    await expect(before).rejects.toBeInstanceOf(NetworkError);
    await doc.ready;
    await doc.load("tree");
    // One made after is refused as without a queue.
    await expect(doc.send([rename("bd_a", "Pier", 3)])).rejects.toBeInstanceOf(
      NetworkError,
    );
    expect(doc.store.getState()).toMatchObject({ queued: 0, holding: false });
    expect(await db.queued(user)).toEqual([]);
    await db.close();
  });

  it("keeps a queued batch as not sent where the document was dropped as it recorded it as sent, before it sent it", async () => {
    const batches = serve(() => applied(2, [{ id: "bd_a", rev: 4 }]));
    const net = connection(false);
    const db = new OfflineDatabase();
    const locks = fakeLocks();
    const { doc } = await open(db, locks, net);
    await doc.send([rename("bd_a", "Quay", 3)]);
    const save = db.saveQueued.bind(db);
    let write = () => {};
    vi.spyOn(db, "saveQueued").mockImplementationOnce(async (...args) => {
      await new Promise<void>((resolve) => {
        write = resolve;
      });
      return save(...args);
    });
    net.set(true);
    await vi.waitFor(() => expect(db.saveQueued).toHaveBeenCalledOnce());
    doc.dispose();
    write();
    // Once its writes are done, the document lets go of its lock.
    await vi.waitFor(async () => expect((await locks.held()).size).toBe(0));
    expect((await db.queued(user)).map((b) => b.sent)).toEqual([false]);
    expect(batches).toEqual([]);
    await db.close();
  });

  it("writes nothing of a batch that the device refused to keep, whose caller was told so, by a write that came after", async () => {
    let answer: (value: CommandResultBody) => void = () => {};
    const batches = serve((commands) =>
      nameOf(commands) === "Quay"
        ? new Promise<CommandResultBody>((resolve) => {
            answer = resolve;
          })
        : applied(3, [{ id: "bd_a", rev: 5 }]),
    );
    const net = connection(true);
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), net);
    const first = doc.send([rename("bd_a", "Quay", 3)]);
    await vi.waitFor(() => expect(batches).toHaveLength(1));
    net.set(false);
    let refuse = () => {};
    const saves = vi.spyOn(db, "saveQueued").mockImplementationOnce(
      () =>
        new Promise<void>((_, reject) => {
          refuse = () =>
            reject(new DOMException("Full.", "QuotaExceededError"));
        }),
    );
    const second = doc.send([rename("bd_a", "Pier", 3)]);
    await vi.waitFor(() => expect(saves).toHaveBeenCalledOnce());
    // The answer to the first moves what the second expects, which a write
    // after the one under way writes (moveQueued).
    answer(applied(2, [{ id: "bd_a", rev: 4 }]));
    await first;
    refuse();
    await expect(second).rejects.toBeInstanceOf(NetworkError);
    await doc.queueWritten();
    expect(await db.queued(user, testProject.id)).toEqual([]);
    await db.close();
  });

  it("tells the caller of a batch that the device refused to keep as it went on its way that it is queued, once a write after keeps it, as no answer came", async () => {
    const batches = serve(() => {
      throw new NetworkError(new TypeError("Failed to fetch"));
    });
    const net = connection(false);
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), net);
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
    // The browser is online again as the device writes it, which puts it on
    // its way.
    net.set(true);
    refuse();
    await expect(sending).resolves.toEqual({ status: "queued" });
    expect(batches.length).toBeGreaterThan(0);
    await doc.queueWritten();
    expect(
      (await db.queued(user, testProject.id)).map((b) => nameOf(b.commands)),
    ).toEqual(["Quay"]);
    doc.dispose();
    await db.close();
  });

  it("keeps none of the batches whose writes waited as the page signed out, and refuses them, as without a queue", async () => {
    serve(() => applied(2, [{ id: "bd_a", rev: 4 }]));
    const db = new OfflineDatabase();
    const { doc, queryClient, log } = await open(
      db,
      fakeLocks(),
      connection(false),
    );
    const save = db.saveQueued.bind(db);
    let write = () => {};
    const saves = vi
      .spyOn(db, "saveQueued")
      .mockImplementationOnce(async (...args) => {
        await new Promise<void>((resolve) => {
          write = resolve;
        });
        return save(...args);
      });
    const first = doc.send([rename("bd_a", "Quay", 3)]);
    await vi.waitFor(() => expect(saves).toHaveBeenCalledOnce());
    const second = doc.send([rename("bd_a", "Pier", 3)]);
    queryClient.setQueryData(sessionQuery.queryKey, null);
    doc.dispose();
    write();
    // The first was written before the page signed out, which signing out
    // takes off the device (discard).
    await expect(first).resolves.toEqual({ status: "queued" });
    await expect(second).rejects.toBeInstanceOf(NetworkError);
    expect((await db.queued(user)).map((b) => nameOf(b.commands))).toEqual([
      "Quay",
    ]);
    expect(log.entries()).toEqual([]);
    await db.close();
  });

  it("takes the batches that another tab's document took as their tab closed once that one has gone too, as it tells the other tabs that it took them", async () => {
    serve(() => applied(2, [{ id: "bd_a", rev: 4 }]));
    const net = connection(false);
    const locks = fakeLocks();
    const hub = fakeBroadcast();
    const tab = async () => {
      const db = new OfflineDatabase();
      const opened = await open(
        db,
        locks,
        net,
        testSession,
        deviceQueue(db, { secure: true, locks, broadcast: hub.tab() }),
      );
      return { db, doc: opened.doc };
    };
    const closing = await tab();
    await closing.doc.send([rename("bd_a", "Quay", 3)]);
    const tabs = [await tab(), await tab()];
    closing.doc.dispose();
    // One takes them, whichever the browser gives the lock first.
    await vi.waitFor(() =>
      expect(tabs.map((t) => t.doc.store.getState().queued).sort()).toEqual([
        0, 1,
      ]),
    );
    const taker = tabs.find((t) => t.doc.store.getState().queued === 1);
    const other = tabs.find((t) => t !== taker);
    taker?.doc.dispose();
    await until(other?.doc as ProjectDocument, (s) => s.queued === 1);
    expect(shownName(other?.doc as ProjectDocument)).toBe("Quay");
    for (const t of [closing, ...tabs]) {
      await t.db.close();
    }
  });

  it("waits out a refusal for when a batch was sent, as of too many requests, though the channel is live again", async () => {
    const batches = serve(() => {
      throw refused(429, { code: "rate_limited" });
    });
    const net = connection(false);
    const db = new OfflineDatabase();
    const locks = fakeLocks();
    const realtime = fakeRealtime();
    const { doc } = await open(
      db,
      locks,
      net,
      testSession,
      deviceQueue(db, { secure: true, locks }),
      { realtime, random: () => 0.99 },
    );
    await doc.send([rename("bd_a", "Quay", 3)]);
    net.set(true);
    await vi.waitFor(async () => {
      const [kept] = await db.queued(user, testProject.id);
      expect(kept?.refusal?.status).toBe(429);
    });
    expect(batches).toHaveLength(1);
    realtime.hold(testProject.id);
    expect(doc.store.getState().live).toBe(true);
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(batches).toHaveLength(1);
    doc.dispose();
    await db.close();
  });

  it("waits out a refusal for when a batch was sent, as of too many requests, though the browser is online again", async () => {
    const batches = serve(() => {
      throw refused(429, { code: "rate_limited" });
    });
    const net = connection(false);
    const db = new OfflineDatabase();
    const locks = fakeLocks();
    const { doc } = await open(
      db,
      locks,
      net,
      testSession,
      deviceQueue(db, { secure: true, locks }),
      { random: () => 0.99 },
    );
    await doc.send([rename("bd_a", "Quay", 3)]);
    net.set(true);
    await vi.waitFor(async () => {
      const [kept] = await db.queued(user, testProject.id);
      expect(kept?.refusal?.status).toBe(429);
    });
    net.set(false);
    net.set(true);
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(batches).toHaveLength(1);
    doc.dispose();
    await db.close();
  });

  it("writes a batch whose write the device refused with the next write, rather than leave the device's record of it behind", async () => {
    const batches = serve(() => {
      throw refused(429, { code: "rate_limited" });
    });
    const net = connection(false);
    const db = new OfflineDatabase();
    const locks = fakeLocks();
    const { doc } = await open(
      db,
      locks,
      net,
      testSession,
      deviceQueue(db, { secure: true, locks }),
      { random: () => 0.99 },
    );
    await doc.send([rename("bd_a", "Quay", 3)]);
    // The device refuses the write of the refusal, as for its quota.
    const save = db.saveQueued.bind(db);
    let refusedWrite = false;
    vi.spyOn(db, "saveQueued").mockImplementation(async (...args) => {
      if (!refusedWrite && args[2].some((b) => b.refusal !== undefined)) {
        refusedWrite = true;
        throw new DOMException("The quota was exceeded.", "QuotaExceededError");
      }
      return save(...args);
    });
    net.set(true);
    await vi.waitFor(() => expect(refusedWrite).toBe(true));
    net.set(false);
    expect((await db.queued(user, testProject.id))[0]?.refusal).toBeUndefined();
    // The next write, of a change made offline, carries it.
    await doc.send([rename("bd_a", "Dock", 3)]);
    const [kept] = await db.queued(user, testProject.id);
    expect(kept?.refusal?.status).toBe(429);
    expect(batches).toHaveLength(1);
    doc.dispose();
    await db.close();
  });

  it("refuses a batch whose operation the channel brought before no answer came to it, as without a queue, rather than leave its caller waiting", async () => {
    const realtime = fakeRealtime();
    const batches = serve((commands) => {
      realtime.send(testProject.id, commands, [{ id: "bd_a", rev: 4 }]);
      throw new NetworkError(new TypeError("Failed to fetch"));
    });
    const locks = fakeLocks();
    const db = new OfflineDatabase();
    const { doc } = await open(
      db,
      locks,
      connection(true),
      testSession,
      deviceQueue(db, { secure: true, locks }),
      { realtime },
    );
    realtime.hold(testProject.id);
    await expect(doc.send([rename("bd_a", "Quay", 3)])).rejects.toBeInstanceOf(
      NetworkError,
    );
    // It applied, as its operation tells, and the document is idle again.
    expect(shownName(doc)).toBe("Quay");
    await vi.waitFor(() => expect(doc.busy).toBe(false));
    expect(doc.store.getState()).toMatchObject({ queued: 0, sending: 0 });
    expect(await db.queued(user, testProject.id)).toEqual([]);
    expect(batches).toHaveLength(1);
    doc.dispose();
    await db.close();
  });

  it("takes a queued batch off the device whose operation the channel brought before no answer came to it", async () => {
    const realtime = fakeRealtime();
    const batches = serve((commands) => {
      realtime.send(testProject.id, commands, [{ id: "bd_a", rev: 4 }]);
      throw new NetworkError(new TypeError("Failed to fetch"));
    });
    const net = connection(false);
    const locks = fakeLocks();
    const db = new OfflineDatabase();
    const { doc } = await open(
      db,
      locks,
      net,
      testSession,
      deviceQueue(db, { secure: true, locks }),
      { realtime },
    );
    realtime.hold(testProject.id);
    await doc.send([rename("bd_a", "Quay", 3)]);
    net.set(true);
    await vi.waitFor(async () => {
      expect(await db.queued(user, testProject.id)).toEqual([]);
    });
    await until(doc, (s) => s.queued === 0 && s.sending === 0);
    expect(shownName(doc)).toBe("Quay");
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(batches).toHaveLength(1);
    doc.dispose();
    await db.close();
  });

  it("sends another tab's batches, taken as that tab closed while a step of the history waited, after the step", async () => {
    // A server that applies each rename of bd_a, whose undo renames it back,
    // and whose read of the project's seq, which a step waits for while the
    // channel is not live, waits while checking does.
    let seq = 1;
    let rev = 3;
    let name = "Harbour";
    let checking: Promise<void> | undefined;
    let reads = 0;
    const batches: CommandBody[][] = [];
    fakeApi(async (operation, input) => {
      switch (operation) {
        case "getProject":
          reads++;
          await checking;
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
    const locks = fakeLocks();
    const db = new OfflineDatabase();
    const other = await open(db, locks, connection(false));
    await other.doc.send([rename("bd_a", "Pier", 3)]);
    const db2 = new OfflineDatabase();
    const mine = await open(db2, locks, connection(true));
    await mine.doc.send([rename("bd_a", "Quay", 3)]);
    let checked = () => {};
    checking = new Promise((resolve) => {
      checked = resolve;
    });
    const before = reads;
    const undo = mine.doc.undo();
    await vi.waitFor(() => expect(reads).toBeGreaterThan(before));
    // The other tab closes as the step waits.
    other.doc.dispose();
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(batches.map((b) => nameOf(b))).toEqual(["Quay"]);
    checked();
    await expect(undo).resolves.toBe(true);
    await until(mine.doc, (s) => s.queued === 0 && s.sending === 0);
    expect(batches.map((b) => nameOf(b))).toEqual(["Quay", "Harbour", "Pier"]);
    // The other tab's change, sent last, is the latest step.
    expect(mine.doc.store.getState()).toMatchObject({
      undoable: 1,
      redoable: 0,
    });
    mine.doc.dispose();
    await db.close();
    await db2.close();
  });

  it("takes, in the same tab, the batches that a document dropped before it kept since, once that one has let go", async () => {
    let fail: (error: Error) => void = () => {};
    let tries = 0;
    const batches = serve(() => {
      tries++;
      if (tries === 1) {
        return new Promise((_, reject) => {
          fail = reject;
        });
      }
      return applied(2, [{ id: "bd_a", rev: 4 }]);
    });
    const locks = fakeLocks();
    const db = new OfflineDatabase();
    // The tab's queue, which its documents share, as the page's do.
    const queue = deviceQueue(db, {
      secure: true,
      locks,
      broadcast: fakeBroadcast().tab(),
    });
    const net = connection(true);
    const first = await open(db, locks, net, testSession, queue);
    const sending = first.doc.send([rename("bd_a", "Quay", 3)]);
    await vi.waitFor(() => expect(batches).toHaveLength(1));
    // Dropped as its try is under way, and made anew, as once the project
    // could not be read (Documents.forget).
    first.doc.dispose();
    const second = await open(db, locks, net, testSession, queue);
    fail(new NetworkError(new TypeError("Failed to fetch")));
    await expect(sending).resolves.toEqual({ status: "queued" });
    await vi.waitFor(() => expect(batches).toHaveLength(2));
    expect(batches[1]).toEqual(batches[0]);
    await until(second.doc, (s) => s.queued === 0 && s.sending === 0);
    expect(shownName(second.doc)).toBe("Quay");
    await vi.waitFor(async () => {
      expect(await db.queued(user, testProject.id)).toEqual([]);
    });
    second.doc.dispose();
    await db.close();
  });

  it("says that a queued batch was sent and got no answer, rather than the refusal of the try before", async () => {
    let tries = 0;
    serve(() => {
      tries++;
      if (tries === 1) {
        throw refused(429, { code: "rate_limited" });
      }
      throw new NetworkError(new TypeError("Failed to fetch"));
    });
    const net = connection(false);
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), net);
    await doc.send([rename("bd_a", "Quay", 3)]);
    net.set(true);
    await vi.waitFor(async () => {
      expect(tries).toBeGreaterThan(1);
      const [kept] = await db.queued(user, testProject.id);
      expect(kept?.sent).toBe(true);
      expect(kept?.refusal).toBeUndefined();
    });
    expect(doc.pendingBatches()[0]?.refusal).toBeUndefined();
    doc.dispose();
    await db.close();
  });

  it("leaves what the history could redo where the device refused to keep a change, whose caller is told that it failed", async () => {
    let seq = 1;
    let rev = 3;
    let name = "Harbour";
    serve(
      (commands) => {
        const was = name;
        name = nameOf(commands) ?? name;
        seq++;
        rev++;
        return applied(
          seq,
          [{ id: "bd_a", rev }],
          [renameBack("bd_a", was, rev)],
        );
      },
      () => seq,
    );
    const net = connection(true);
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), net);
    await doc.send([rename("bd_a", "Quay", 3)]);
    await expect(doc.undo()).resolves.toBe(true);
    expect(doc.store.getState().redoable).toBe(1);
    net.set(false);
    vi.spyOn(db, "saveQueued").mockRejectedValue(
      new DOMException("Full.", "QuotaExceededError"),
    );
    await expect(doc.send([rename("bd_a", "Pier", 5)])).rejects.toBeInstanceOf(
      NetworkError,
    );
    expect(doc.store.getState().redoable).toBe(1);
    doc.dispose();
    await db.close();
  });

  it("keeps no queue in a browser without Web Locks, which could not tell an open tab's batches from a closed one's", () => {
    expect(deviceQueue(new OfflineDatabase(), { secure: true })).toBe(
      undefined,
    );
  });

  it("leaves another tab's batches, as it closes, to the page that opens the project next, rather than to the keeper's document, which no page shows, until a page shows that", async () => {
    serve(() => applied(2, [{ id: "bd_a", rev: 4 }]));
    const net = connection(false);
    const locks = fakeLocks();
    const hub = fakeBroadcast();
    const dbs: OfflineDatabase[] = [];
    const tab = async () => {
      const db = new OfflineDatabase();
      dbs.push(db);
      const opened = await open(
        db,
        locks,
        net,
        testSession,
        deviceQueue(db, { secure: true, locks, broadcast: hub.tab() }),
      );
      return opened.doc;
    };
    // The tab that keeps the copies holds the project's document, which no
    // page of it shows.
    const log = createErrorLog();
    const queryClient = createQueryClient(log);
    queryClient.setQueryData(sessionQuery.queryKey, testSession);
    const keeperDb = new OfflineDatabase();
    dbs.push(keeperDb);
    const documents = new Documents({
      queryClient,
      log,
      online: () => net.online,
      onConnection: (listener) => net.listen(listener),
      queue: deviceQueue(keeperDb, {
        secure: true,
        locks,
        broadcast: hub.tab(),
      }),
    });
    onTestFinished(() => documents.close());
    const kept = documents.get(testProject.id, true);
    documents.acquire(kept, true);
    await kept.ready;
    await kept.load("tree");
    const editing = await tab();
    await editing.send([rename("bd_a", "Quay", 3)]);
    // The tab that the user edits in reloads.
    editing.dispose();
    await vi.waitFor(async () => {
      const held = await locks.held();
      expect(
        [...held].filter((name) => name.startsWith("storycreep-queue:")),
      ).toHaveLength(1);
    });
    await new Promise((resolve) => setTimeout(resolve, 50));
    const next = await tab();
    await until(next, (s) => s.queued === 1);
    expect(shownName(next)).toBe("Quay");
    expect(kept.store.getState().queued).toBe(0);
    // A page of the keeper's tab shows the project too, whose document takes
    // them once the tab that holds them closes.
    documents.get(testProject.id);
    documents.acquire(kept);
    next.dispose();
    await until(kept, (s) => s.queued === 1);
    expect(shownName(kept)).toBe("Quay");
    for (const db of dbs) {
      await db.close();
    }
  });

  it("refuses a batch that the device refused to keep as no answer came to it, and that was on its way again, as without a queue, rather than keep it for review", async () => {
    let conflict = () => {};
    const batches = serve((commands) => {
      if (batches.length === 1) {
        throw new NetworkError(new TypeError("Failed to fetch"));
      }
      return new Promise<never>((_, reject) => {
        conflict = () =>
          reject(
            refused(409, {
              code: "revision_conflict",
              details: {
                conflicts: [
                  {
                    command: 0,
                    command_id: commands[0]?.command_id,
                    type: "board.rename",
                    id: "bd_a",
                    expected_rev: 3,
                    exists: true,
                    current_rev: 4,
                  },
                ],
                states: { bd_a: board("bd_a", "Pier", 4) },
              },
            }),
          );
      });
    });
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), connection(true));
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
    // Tried again as the device writes it, and refused for what it does
    // once the device has refused to keep it.
    await vi.waitFor(() => expect(batches).toHaveLength(2));
    refuse();
    await doc.queueWritten();
    conflict();
    await expect(sending).rejects.toBeInstanceOf(ConflictError);
    expect(doc.store.getState()).toMatchObject({ queued: 0, review: 0 });
    await doc.queueWritten();
    expect(await db.queued(user, testProject.id)).toEqual([]);
    await db.close();
  });

  it("sends a queued batch that the device refuses to record as sent all the same, as of a database whose connection was lost, rather than hold it, and every change made meanwhile", async () => {
    const db = new OfflineDatabase();
    const recorded: (boolean | undefined)[] = [];
    const batches = serve(async () => {
      recorded.push((await db.queued(user, testProject.id))[0]?.sent);
      return applied(batches.length + 1, [
        { id: "bd_a", rev: batches.length + 3 },
      ]);
    });
    const net = connection(false);
    const { doc, log } = await open(db, fakeLocks(), net);
    await doc.send([rename("bd_a", "Quay", 3)]);
    // The device refuses every write from now on, as Safari's database does
    // once its connection is lost.
    vi.spyOn(db, "saveQueued").mockRejectedValue(
      new DOMException(
        "Connection to Indexed Database server lost.",
        "UnknownError",
      ),
    );
    net.set(true);
    await until(doc, (s) => s.queued === 0);
    expect(batches).toHaveLength(1);
    // The device keeps it as it was, which the error console records.
    expect(recorded).toEqual([false]);
    expect(log.entries()).toHaveLength(1);
    // A change made since is sent, rather than kept, and refused as the
    // device refuses it.
    await expect(doc.send([rename("bd_a", "Pier", 4)])).resolves.toMatchObject({
      status: "applied",
    });
    expect(batches).toHaveLength(2);
    await db.close();
  });

  it("sends a queued batch that the device's quota refuses to record as sent all the same, rather than hold it, and every change made meanwhile, for as long as the device is full", async () => {
    const db = new OfflineDatabase();
    const recorded: (boolean | undefined)[] = [];
    const batches = serve(async () => {
      recorded.push((await db.queued(user, testProject.id))[0]?.sent);
      return applied(batches.length + 1, [
        { id: "bd_a", rev: batches.length + 3 },
      ]);
    });
    const net = connection(false);
    const { doc } = await open(db, fakeLocks(), net);
    await doc.send([rename("bd_a", "Quay", 3)]);
    // The device is full from now on.
    vi.spyOn(db, "saveQueued").mockRejectedValue(
      new DOMException("Full.", "QuotaExceededError"),
    );
    net.set(true);
    await until(doc, (s) => s.queued === 0);
    expect(batches).toHaveLength(1);
    // The device keeps it as it was.
    expect(recorded).toEqual([false]);
    // A change made since is sent, rather than kept, and refused for the
    // device's quota.
    await expect(doc.send([rename("bd_a", "Pier", 4)])).resolves.toMatchObject({
      status: "applied",
    });
    expect(batches).toHaveLength(2);
    await db.close();
  });

  it("refuses a batch, as without a queue, where the device's database does not open in time, and sends a queued batch all the same, rather than wait for it, and every change made meanwhile", async () => {
    const batches = serve(() =>
      applied(batches.length + 1, [{ id: "bd_a", rev: batches.length + 3 }]),
    );
    const net = connection(false);
    const db = new OfflineDatabase();
    const locks = fakeLocks();
    const { doc } = await open(
      db,
      locks,
      net,
      testSession,
      deviceQueue(db, { secure: true, locks, wait: 10 }),
    );
    await doc.send([rename("bd_a", "Quay", 3)]);
    // The database's connection is lost, and its open never settles from
    // now on, as a browser's may not.
    vi.spyOn(db, "open").mockReturnValue(new Promise(() => {}));
    await expect(doc.send([rename("bd_a", "Pier", 3)])).rejects.toBeInstanceOf(
      NetworkError,
    );
    net.set(true);
    await until(doc, (s) => s.queued === 0 && s.sending === 0);
    expect(batches.map(nameOf)).toEqual(["Quay"]);
    // A change made since is sent, rather than kept.
    await expect(doc.send([rename("bd_a", "Dock", 4)])).resolves.toMatchObject({
      status: "applied",
    });
    expect(batches.map(nameOf)).toEqual(["Quay", "Dock"]);
    await db.close();
  });

  it("takes none of the batches of a tab that closed once dropped as the device's database opened, which it would hold, having let go of its lock, and send none of", async () => {
    serve(() => applied(2, [{ id: "bd_a", rev: 4 }]));
    const net = connection(false);
    const locks = fakeLocks();
    const db = new OfflineDatabase();
    const closing = await open(db, locks, net);
    await closing.doc.send([rename("bd_a", "Quay", 3)]);
    closing.doc.dispose();
    await vi.waitFor(async () => expect(await locks.held()).toEqual(new Set()));
    const [kept] = await db.queued(user, testProject.id);
    await db.close();
    // The next page's database is slow to open, as one that another tab
    // blocks, and the page goes before it has.
    const again = new OfflineDatabase();
    const opening = again.open.bind(again);
    let unblock = () => {};
    const blocked = new Promise<void>((resolve) => {
      unblock = resolve;
    });
    const opens = vi.spyOn(again, "open").mockImplementation(async () => {
      await blocked;
      return opening();
    });
    const takes = vi.spyOn(again, "takeQueued");
    const log = createErrorLog();
    const queryClient = createQueryClient(log);
    queryClient.setQueryData(sessionQuery.queryKey, testSession);
    const doc = new ProjectDocument(testProject.id, {
      queryClient,
      log,
      now: () => clock,
      online: () => net.online,
      onConnection: (listener) => net.listen(listener),
      queue: deviceQueue(again, { secure: true, locks, wait: 60_000 }),
    });
    // It waits for the database to take the batches of the tab that closed.
    await vi.waitFor(() => expect(opens).toHaveBeenCalled());
    doc.dispose();
    unblock();
    await again.open();
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(takes).not.toHaveBeenCalled();
    // The project's next document takes it, as of a tab that closed.
    expect((await again.queued(user, testProject.id))[0]?.owner).toBe(
      kept?.owner,
    );
    const next = await open(again, locks, net);
    await until(next.doc, (s) => s.queued === 1);
    expect(shownName(next.doc)).toBe("Quay");
    await again.close();
  });

  it("tries a queued batch again later that a proxy refused with a page of its own, as for its sign-in, rather than keep it for review", async () => {
    let failures = 1;
    const batches = serve(() => {
      if (failures-- > 0) {
        throw new ApiError(
          new Response("<html>Sign in</html>", {
            status: 401,
            headers: { "Content-Type": "text/html" },
          }),
          undefined,
        );
      }
      return applied(2, [{ id: "bd_a", rev: 4 }]);
    });
    const net = connection(false);
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), net);
    await doc.send([rename("bd_a", "Quay", 3)]);
    net.set(true);
    await until(doc, (s) => s.queued === 0);
    expect(doc.store.getState().review).toBe(0);
    expect(batches).toHaveLength(2);
    expect(batches[1]).toEqual(batches[0]);
    await db.close();
  });

  it("keeps a batch made online that a proxy answers for the server with a page of a failure, as while it restarts, and sends it again with its commands' IDs", async () => {
    let failures = 1;
    const batches = serve(() => {
      if (failures-- > 0) {
        throw new ApiError(
          new Response("<html>Bad gateway</html>", {
            status: 502,
            headers: { "Content-Type": "text/html" },
          }),
          undefined,
        );
      }
      return applied(2, [{ id: "bd_a", rev: 4 }]);
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
    await db.close();
  });

  it("tries a queued batch refused for too many requests again no sooner than the answer asks", async () => {
    let failures = 1;
    const batches = serve(() => {
      if (failures-- > 0) {
        throw refused(429, { code: "rate_limited" }, { "Retry-After": "1" });
      }
      return applied(2, [{ id: "bd_a", rev: 4 }]);
    });
    const net = connection(false);
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), net);
    await doc.send([rename("bd_a", "Quay", 3)]);
    const refusedAt = Date.now();
    net.set(true);
    await vi.waitFor(() => expect(batches).toHaveLength(1));
    await vi.waitFor(() => expect(doc.store.getState().queued).toBe(0), {
      timeout: 3_000,
    });
    expect(batches).toHaveLength(2);
    expect(Date.now() - refusedAt).toBeGreaterThanOrEqual(900);
    await db.close();
  });

  it("tries a queued batch that the server refused as it was not ready yet again no sooner than its Retry-After asks, as a date", async () => {
    let failures = 1;
    const batches = serve(() => {
      if (failures-- > 0) {
        throw refused(
          503,
          { code: "not_ready" },
          { "Retry-After": new Date(Date.now() + 2_000).toUTCString() },
        );
      }
      return applied(2, [{ id: "bd_a", rev: 4 }]);
    });
    const net = connection(false);
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), net);
    await doc.send([rename("bd_a", "Quay", 3)]);
    const refusedAt = Date.now();
    net.set(true);
    await vi.waitFor(() => expect(batches).toHaveLength(1));
    await vi.waitFor(() => expect(doc.store.getState().queued).toBe(0), {
      timeout: 4_000,
    });
    expect(batches).toHaveLength(2);
    // An HTTP date has whole seconds: a second at least.
    expect(Date.now() - refusedAt).toBeGreaterThanOrEqual(900);
    await db.close();
  });

  it("lets a read go on once the device's database does not open in time as the document takes the batches let go while it took none, and takes them once it opens", async () => {
    serve(() => applied(2, [{ id: "bd_a", rev: 4 }]));
    const net = connection(false);
    const locks = fakeLocks();
    const db = new OfflineDatabase();
    const { doc } = await open(
      db,
      locks,
      net,
      testSession,
      deviceQueue(db, { secure: true, locks, wait: 10 }),
    );
    // No page shows the document, while another tab's, which closes, keeps
    // a batch.
    doc.takeQueue(false);
    const otherDb = new OfflineDatabase();
    const other = await open(otherDb, locks, net);
    await other.doc.send([rename("bd_a", "Quay", 3)]);
    other.doc.dispose();
    await vi.waitFor(async () => expect((await locks.held()).size).toBe(1));
    // The database is slow to open again, as one that another tab blocks.
    const opening = db.open.bind(db);
    let unblock = () => {};
    const blocked = new Promise<void>((resolve) => {
      unblock = resolve;
    });
    vi.spyOn(db, "open").mockImplementation(async () => {
      await blocked;
      return opening();
    });
    // A page asks for it again, whose loader reads it.
    doc.takeQueue(true);
    await doc.ready;
    expect(doc.store.getState().queued).toBe(0);
    unblock();
    await until(doc, (s) => s.queued === 1);
    expect(shownName(doc)).toBe("Quay");
    await db.close();
    await otherDb.close();
  });

  it("sends a queued batch made before one that needs review, which holds only those after it", async () => {
    const batches = serve(() => applied(2, [{ id: "bd_a", rev: 4 }]));
    const net = connection(false);
    const locks = fakeLocks();
    const db = new OfflineDatabase();
    const closed = await open(db, locks, net);
    await closed.doc.send([rename("bd_a", "Quay", 3)]);
    await closed.doc.send([rename("bd_a", "Pier", 3)]);
    closed.doc.dispose();
    await vi.waitFor(async () => expect(await locks.held()).toEqual(new Set()));
    // The later one was refused for what it does, as in a tab that closed.
    const [first, later] = await db.queued(user, testProject.id);
    await db.saveQueued(
      user,
      testProject.id,
      [
        {
          ...(later as QueueRecord),
          state: "review",
          refusal: { status: 422, at: time },
        },
      ],
      [],
    );
    net.set(true);
    const db2 = new OfflineDatabase();
    const { doc } = await open(db2, locks, net);
    await until(doc, (s) => s.queued === 1);
    expect(batches).toHaveLength(1);
    expect(nameOf(batches[0] ?? [])).toBe("Quay");
    expect(doc.store.getState()).toMatchObject({ queued: 1, review: 1 });
    expect((await db.queued(user, testProject.id)).map((b) => b.id)).toEqual([
      later?.id,
    ]);
    expect(first?.id).not.toBe(later?.id);
    await db.close();
    await db2.close();
  });

  it("keeps for redo a change that undo took out of the queue where the server refused to have it put back for when it was sent, as with too many requests, unless another change was made meanwhile", async () => {
    let refusal: (() => Promise<never>) | undefined;
    let seq = testProject.seq;
    const batches = serve(() => {
      const refuse = refusal;
      refusal = undefined;
      if (refuse !== undefined) {
        return refuse();
      }
      seq++;
      return applied(seq, [{ id: "bd_a", rev: seq + 2 }]);
    });
    const net = connection(false);
    const db = new OfflineDatabase();
    const { doc } = await open(db, fakeLocks(), net);
    await doc.send([rename("bd_a", "Quay", 3)]);
    await doc.undo();
    expect(shownName(doc)).toBe("Harbour");
    net.set(true);
    refusal = () => Promise.reject(refused(429, { code: "rate_limited" }));
    await expect(doc.redo()).rejects.toMatchObject({ status: 429 });
    expect(shownName(doc)).toBe("Harbour");
    expect(doc.store.getState()).toMatchObject({
      redoable: 1,
      redoLocal: true,
    });
    // Refused again, once the page has made another change meanwhile,
    // after which redo puts back nothing that undo took out before it.
    let refuseNow = () => {};
    refusal = () =>
      new Promise<never>((_, reject) => {
        refuseNow = () => reject(refused(429, { code: "rate_limited" }));
      });
    const redoing = doc.redo();
    await vi.waitFor(() => expect(batches).toHaveLength(2));
    const making = doc.send([rename("bd_a", "Pier", 3)]);
    refuseNow();
    await expect(redoing).rejects.toMatchObject({ status: 429 });
    await expect(making).resolves.toMatchObject({ status: "applied" });
    expect(doc.store.getState()).toMatchObject({
      redoable: 0,
      redoLocal: false,
    });
    expect(batches.map(nameOf)).toEqual(["Quay", "Quay", "Pier"]);
    await db.close();
  });

  it("sends no step of the history kept over a reload before the batches that the queue kept, which the document is still taking as the step is asked for", async () => {
    window.sessionStorage.clear();
    onTestFinished(() => window.sessionStorage.clear());
    let seq = testProject.seq;
    const batches = serve(
      () => {
        seq++;
        return applied(
          seq,
          [{ id: "bd_a", rev: seq + 2 }],
          [renameBack("bd_a", "Harbour", seq + 2)],
        );
      },
      () => seq,
    );
    const net = connection(true);
    const locks = fakeLocks();
    const db = new OfflineDatabase();
    const first = await open(
      db,
      locks,
      net,
      testSession,
      deviceQueue(db, { secure: true, locks }),
      { storage: window.sessionStorage },
    );
    await first.doc.send([rename("bd_a", "Quay", 3)]);
    net.set(false);
    await first.doc.send([rename("bd_a", "Pier", 4)]);
    // The tab reloads, online, and its database is slow to open, as one
    // that another tab blocks.
    first.doc.dispose();
    await vi.waitFor(async () => expect(await locks.held()).toEqual(new Set()));
    await db.close();
    net.set(true);
    const again = new OfflineDatabase();
    const opening = again.open.bind(again);
    let unblock = () => {};
    const blocked = new Promise<void>((resolve) => {
      unblock = resolve;
    });
    vi.spyOn(again, "open").mockImplementation(async () => {
      await blocked;
      return opening();
    });
    const log = createErrorLog();
    const queryClient = createQueryClient(log);
    queryClient.setQueryData(sessionQuery.queryKey, testSession);
    const doc = new ProjectDocument(testProject.id, {
      queryClient,
      log,
      now: () => clock,
      online: () => net.online,
      onConnection: (listener) => net.listen(listener),
      queue: deviceQueue(again, { secure: true, locks, wait: 60_000 }),
      random: () => 0.01,
      storage: window.sessionStorage,
    });
    onTestFinished(() => doc.dispose());
    // The history kept over the reload offers its step at once.
    expect(doc.store.getState().undoable).toBe(1);
    const undoing = doc.undo();
    await new Promise((resolve) => setTimeout(resolve, 20));
    unblock();
    await expect(undoing).resolves.toBe(false);
    await until(doc, (s) => s.queued === 0);
    expect(batches.map(nameOf)).toEqual(["Quay", "Pier"]);
    // Whose undo is the latest step now.
    expect(doc.undoesNext(seq)).toBe(true);
    await again.close();
  });

  it("shows the batches that the keeper's document takes as a page asks for it once the page's read of a part that it holds resolves, as of a tab that closed meanwhile", async () => {
    serve(() => applied(2, [{ id: "bd_a", rev: 4 }]));
    const net = connection(false);
    const locks = fakeLocks();
    const hub = fakeBroadcast();
    const log = createErrorLog();
    const queryClient = createQueryClient(log);
    queryClient.setQueryData(sessionQuery.queryKey, testSession);
    const keeperDb = new OfflineDatabase();
    const documents = new Documents({
      queryClient,
      log,
      online: () => net.online,
      onConnection: (listener) => net.listen(listener),
      queue: deviceQueue(keeperDb, {
        secure: true,
        locks,
        broadcast: hub.tab(),
      }),
    });
    onTestFinished(() => documents.close());
    // The tab that keeps the copies holds the project's document, which no
    // page of it shows, with the tree read.
    const kept = documents.get(testProject.id, true);
    documents.acquire(kept, true);
    await kept.ready;
    await kept.load("tree");
    const tabDb = new OfflineDatabase();
    const editing = await open(
      tabDb,
      locks,
      net,
      testSession,
      deviceQueue(tabDb, { secure: true, locks, broadcast: hub.tab() }),
    );
    await editing.doc.send([rename("bd_a", "Quay", 3)]);
    // That tab closes.
    editing.doc.dispose();
    await vi.waitFor(async () => {
      const held = await locks.held();
      expect(
        [...held].filter((name) => name.startsWith("storycreep-queue:")),
      ).toHaveLength(1);
    });
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(shownName(kept)).toBe("Harbour");
    // A page of the keeper's tab asks for the project, as its loader reads
    // the tree, which the document holds.
    const doc = documents.get(testProject.id);
    await doc.load("tree");
    expect(shownName(doc)).toBe("Quay");
    expect(doc.store.getState().queued).toBe(1);
    await keeperDb.close();
    await tabDb.close();
  });

  it("waits, as signing out reads the device, for the writes of a document dropped as the project was found gone, which may keep a change still", async () => {
    serve(() => applied(2, [{ id: "bd_a", rev: 4 }]));
    const net = connection(false);
    const db = new OfflineDatabase();
    const log = createErrorLog();
    const queryClient = createQueryClient(log);
    queryClient.setQueryData(sessionQuery.queryKey, testSession);
    const documents = new Documents({
      queryClient,
      log,
      online: () => net.online,
      onConnection: (listener) => net.listen(listener),
      queue: deviceQueue(db, { secure: true, locks: fakeLocks() }),
    });
    onTestFinished(() => documents.close());
    const doc = documents.get(testProject.id);
    await doc.ready;
    await doc.load("tree");
    const save = db.saveQueued.bind(db);
    let write = () => {};
    const saves = vi
      .spyOn(db, "saveQueued")
      .mockImplementationOnce(async (...args) => {
        await new Promise<void>((resolve) => {
          write = resolve;
        });
        return save(...args);
      });
    const sending = doc.send([rename("bd_a", "Quay", 3)]);
    await vi.waitFor(() => expect(saves).toHaveBeenCalledOnce());
    documents.forget(testProject.id);
    let written = false;
    const writing = documents.whenWritten(60_000).then(() => {
      written = true;
    });
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(written).toBe(false);
    write();
    await writing;
    expect(await db.queued(user)).toHaveLength(1);
    await expect(sending).resolves.toEqual({ status: "queued" });
    await db.close();
  });

  it("holds the content of a board that a queued batch creates, which the server does not have yet, as none, which the batches on top give it, read anew or not", async () => {
    let seq = testProject.seq;
    let created = false;
    const lists = { nodes: 0 };
    const batches: CommandBody[][] = [];
    fakeApi((operation, input) => {
      switch (operation) {
        case "getProject":
          return { ...testProject, seq };
        case "listFolders":
          return { items: [], next_cursor: null };
        case "listBoards":
          return {
            items: [
              board("bd_a", "Harbour"),
              ...(created ? [board("bd_new", "Quay", 1)] : []),
            ],
            next_cursor: null,
          };
        case "listBoardNodes":
        case "listBoardConnections":
          lists.nodes++;
          if (!created) {
            throw refused(404, { code: "not_found" });
          }
          return { items: [], next_cursor: null };
        case "applyCommands": {
          const commands = (input as { body: { commands: CommandBody[] } }).body
            .commands;
          batches.push(structuredClone(commands));
          seq++;
          if (commands[0]?.type === "board.create") {
            created = true;
            return applied(seq, [{ id: "bd_new", rev: 1 }]);
          }
          return applied(seq, [{ id: "nd_new", rev: 1 }]);
        }
      }
      throw new Error(`unexpected ${operation}`);
    });
    const net = connection(false);
    const db = new OfflineDatabase();
    const { doc, log, queryClient } = await open(db, fakeLocks(), net);
    await doc.send([
      { type: "board.create", payload: { id: "bd_new", name: "Quay" } },
    ]);
    // Its page opens it offline, with no read of the server, which has none
    // of it, and the nodes made on it show.
    await expect(doc.load("board:bd_new")).resolves.toBeUndefined();
    expect(lists.nodes).toBe(0);
    await doc.send([
      {
        type: "node.create",
        payload: {
          board_id: "bd_new",
          id: "nd_new",
          type: "branch",
          position: { x: 0, y: 0 },
        },
      },
    ]);
    expect(doc.store.getState().view.nodes.get("nd_new")?.board_id).toBe(
      "bd_new",
    );
    // Online, a reading anew of the project while the queue is held still,
    // as while the page has the device's session without its CSRF token,
    // keeps it, as the server has none of it yet, and so does a page that
    // reads it anew.
    const kept: SignedInBody = { ...testSession, csrf_token: "" };
    queryClient.setQueryData(sessionQuery.queryKey, kept);
    net.set(true);
    await doc.refresh();
    expect(lists.nodes).toBeGreaterThan(0);
    await doc.refresh("board:bd_new");
    expect(doc.hasRead("board:bd_new")).toBe(true);
    expect(doc.store.getState().view.nodes.has("nd_new")).toBe(true);
    expect(batches).toEqual([]);
    expect(log.entries()).toEqual([]);
    queryClient.setQueryData(sessionQuery.queryKey, testSession);
    await until(doc, (s) => s.queued === 0 && s.sending === 0);
    expect(batches).toHaveLength(2);
    expect(doc.doc.nodes.get("nd_new")?.board_id).toBe("bd_new");
    expect(doc.hasRead("board:bd_new")).toBe(true);
    await db.close();
  });

  it("holds the content of a board that a queued batch creates as none where a reading anew began before the server applied the batch, which it answered meanwhile", async () => {
    let seq = testProject.seq;
    let created = false;
    // The reads of the board's content that began before the server had
    // the board wait for gate, and then find none of it.
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    let gated = 0;
    fakeApi(async (operation, input) => {
      switch (operation) {
        case "getProject":
          return { ...testProject, seq };
        case "listFolders":
          return { items: [], next_cursor: null };
        case "listBoards":
          return {
            items: [
              board("bd_a", "Harbour"),
              ...(created ? [board("bd_new", "Quay", 1)] : []),
            ],
            next_cursor: null,
          };
        case "listBoardNodes":
        case "listBoardConnections": {
          if (!created) {
            gated++;
            await gate;
            throw refused(404, { code: "not_found" });
          }
          return {
            items:
              operation === "listBoardNodes"
                ? [
                    {
                      id: "nd_new",
                      project_id: testProject.id,
                      board_id: "bd_new",
                      type: "branch",
                      title: "",
                      position: { x: 0, y: 0 },
                      outputs: [],
                      tags: [],
                      rev: 1,
                      created_at: time,
                      updated_at: time,
                    },
                  ]
                : [],
            next_cursor: null,
          };
        }
        case "applyCommands": {
          const commands = (input as { body: { commands: CommandBody[] } }).body
            .commands;
          seq++;
          if (commands[0]?.type === "board.create") {
            created = true;
            return applied(seq, [{ id: "bd_new", rev: 1 }]);
          }
          return applied(seq, [{ id: "nd_new", rev: 1 }]);
        }
      }
      throw new Error(`unexpected ${operation}`);
    });
    const net = connection(false);
    const db = new OfflineDatabase();
    const { doc, queryClient } = await open(db, fakeLocks(), net);
    await doc.send([
      { type: "board.create", payload: { id: "bd_new", name: "Quay" } },
    ]);
    await doc.load("board:bd_new");
    await doc.send([
      {
        type: "node.create",
        payload: {
          board_id: "bd_new",
          id: "nd_new",
          type: "branch",
          position: { x: 0, y: 0 },
        },
      },
    ]);
    // Online, the project is read anew while the queue is held, and its
    // read of the board waits; meanwhile the queue is sent, and answered.
    const kept: SignedInBody = { ...testSession, csrf_token: "" };
    queryClient.setQueryData(sessionQuery.queryKey, kept);
    net.set(true);
    const reading = doc.refresh();
    await vi.waitFor(() => expect(gated).toBeGreaterThan(0));
    queryClient.setQueryData(sessionQuery.queryKey, testSession);
    await until(doc, (s) => s.queued === 0 && s.sending === 0);
    release();
    await reading;
    // The board's content, which the server gave it after that read, the
    // document holds still, and reads anew.
    await vi.waitFor(() =>
      expect(doc.doc.nodes.get("nd_new")?.board_id).toBe("bd_new"),
    );
    expect(doc.hasRead("board:bd_new")).toBe(true);
    expect(doc.store.getState().view.nodes.has("nd_new")).toBe(true);
    await db.close();
  });
});
