import "fake-indexeddb/auto";
import {
  type Board,
  type CommandBody,
  type CommandResultBody,
  NetworkError,
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
import { deviceQueue, type QueueLocks } from "../offline/queue";
import { testSession } from "../test/app";
import { fakeApi, refused, testProject } from "../test/fake";
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
// for what it does is kept, needing review, with every batch after it; and
// another tab's batches are taken once its document has gone, and not
// before.

const time = "2026-11-27T10:00:00Z";
const user = testSession.user_id;

let factory: IDBFactory;

beforeEach(() => {
  factory = new IDBFactory();
  globalThis.indexedDB = factory;
});

afterEach(() => {
  vi.restoreAllMocks();
});

/** A fake of the browser's Web Locks, each exclusive, granted in the order asked for. */
function fakeLocks(): QueueLocks {
  const held = new Set<string>();
  const waiting = new Map<string, (() => void)[]>();
  const hold = (name: string, signal?: AbortSignal) =>
    new Promise<() => void>((resolve, reject) => {
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
 * Serves the test project, whose tree holds the board bd_a, Harbour, and
 * answers each batch with answer, which may throw; returns the batches that
 * the documents sent, each as sent.
 */
function serve(answer: (commands: CommandBody[]) => unknown) {
  const batches: CommandBody[][] = [];
  fakeApi((operation, input) => {
    switch (operation) {
      case "getProject":
        return testProject;
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
 * does, over the device's database db and the browser's locks, with the
 * tree read, and the session given.
 */
async function open(
  db: OfflineDatabase,
  locks: QueueLocks,
  net: ReturnType<typeof connection>,
  session = testSession,
) {
  const log = createErrorLog();
  const queryClient = createQueryClient(log);
  queryClient.setQueryData(sessionQuery.queryKey, session);
  const doc = new ProjectDocument(testProject.id, {
    queryClient,
    log,
    now: () => time,
    online: () => net.online,
    onConnection: (listener) => net.listen(listener),
    queue: deviceQueue(db, { secure: true, locks }),
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
});
