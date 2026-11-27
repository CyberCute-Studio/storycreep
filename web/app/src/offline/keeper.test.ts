import "fake-indexeddb/auto";
import type {
  Board,
  ComponentType,
  Node,
  SignedInBody,
  Variable,
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
import {
  accountQuery,
  createQueryClient,
  instanceQuery,
  sessionQuery,
} from "../api";
import { type ComponentEntry, emptyDoc, projectOf } from "../document/doc";
import { Documents } from "../document/documents";
import { createErrorLog } from "../errors/log";
import { testAccount, testInstance, testSession } from "../test/app";
import { fakeApi, refused, testProject, testWorkspace } from "../test/fake";
import { fakeRealtime } from "../test/realtime";
import { copySource, OfflineDatabase, openWait } from "./db";
import { OfflineKeeper } from "./keeper";

// The keeper of the projects that the device keeps offline (PRD FR-OFF-02,
// plan note N97), over the fake API, the fake channel, and fake-indexeddb:
// a pin downloads the whole project, every board's content and every
// component whole, which an operation keeps current; the last opened
// project is pinned, and the pins of those opened before it go once its
// copy is written, but for the copies of those chosen; unpinning and signing
// out take the copies off the device at once; and a copy that the
// browser's quota refuses is not kept, which the pin says.

beforeEach(() => {
  globalThis.indexedDB = new IDBFactory();
});

afterEach(() => {
  vi.restoreAllMocks();
});

const stamps = {
  created_at: "2026-11-01T10:00:00Z",
  updated_at: "2026-11-01T10:00:00Z",
};

const lighthouse = {
  ...testProject,
  id: "prj_lighthouse",
  name: "Lighthouse",
  slug: "lighthouse",
};
const tower = { ...testProject, id: "prj_tower", name: "Tower", slug: "tower" };

function board(projectId: string, id: string, name = id, rev = 1): Board {
  return {
    id,
    project_id: projectId,
    name,
    slug: id.slice("bd_".length),
    description: "",
    color: "none",
    order: "V",
    rev,
    ...stamps,
  } as Board;
}

function node(id: string, boardId: string): Node {
  return {
    id,
    board_id: boardId,
    kind: "element",
    title: id,
    rev: 1,
    ...stamps,
  } as unknown as Node;
}

const variable = {
  id: "var_gold",
  key: "gold",
  name: "Gold",
  type: "int",
  default: 0,
  rev: 1,
  ...stamps,
} as unknown as Variable;
const hero = {
  id: "ct_hero",
  key: "hero",
  name: "Hero",
  icon: "none",
  attributes: [],
  rev: 1,
  ...stamps,
} as unknown as ComponentType;
const jo = {
  id: "cmp_jo",
  component_type_id: "ct_hero",
  key: "jo",
  name: "Jo",
  rev: 1,
  ...stamps,
} as unknown as ComponentEntry;

const page = <T>(items: T[]) => ({ items, next_cursor: null });

/**
 * Serves the test projects: each with boards bd_a and bd_b of two nodes and
 * one, or those that boards returns, a variable, and a component, which
 * reads whole; an operation that fail returns an error for throws it, as
 * the server's answer would be.
 */
function serve(
  options: {
    fail?: (operation: string, input: unknown) => Error | undefined;
    component?: () => unknown;
    boards?: (projectId: string) => Board[] | undefined;
  } = {},
) {
  const calls: string[] = [];
  const times: number[] = [];
  const projects = new Map(
    [testProject, lighthouse, tower].map((p) => [p.id, p]),
  );
  fakeApi((operation, input) => {
    calls.push(operation);
    times.push(Date.now());
    const failure = options.fail?.(operation, input);
    if (failure !== undefined) {
      throw failure;
    }
    const path =
      (input as { path?: Record<string, string> } | undefined)?.path ?? {};
    const project = path.project_id;
    switch (operation) {
      case "getProject": {
        const found = projects.get(project ?? "");
        if (found === undefined) {
          throw refused(404, { code: "not_found" });
        }
        return found;
      }
      case "listWorkspaces":
        return page([testWorkspace]);
      case "listFolders":
        return page([]);
      case "listBoards":
        return page(
          options.boards?.(project ?? "") ?? [
            board(project ?? "", `bd_a${project}`),
            board(project ?? "", `bd_b${project}`),
          ],
        );
      case "listBoardNodes":
        return page(
          path.board_id?.startsWith("bd_a")
            ? [
                node(`nd_1${project}`, path.board_id),
                node(`nd_2${project}`, path.board_id),
              ]
            : [node(`nd_3${project}`, path.board_id ?? "")],
        );
      case "listBoardConnections":
        return page([]);
      case "listVariables":
        return page([variable]);
      case "listComponentTypes":
        return page([hero]);
      case "listComponents":
        return page([jo]);
      case "getComponent":
        return (
          options.component?.() ?? {
            ...jo,
            description: "The hero.",
            attributes: {},
          }
        );
    }
    throw new Error(`unexpected ${operation}`);
  });
  return Object.assign(calls, { times });
}

function setup(
  options: {
    secure?: boolean;
    retryDelay?: number;
    writeDelay?: number;
    pace?: { burst: number; interval: number };
    /** Called with the device's database before the keeper starts. */
    beforeStart?: (db: OfflineDatabase) => void;
  } = {},
) {
  const log = createErrorLog();
  // The other tabs' channel, which a test speaks on as another tab would.
  const heard = new Set<() => void>();
  const queryClient = createQueryClient(log);
  queryClient.setQueryData(instanceQuery.queryKey, testInstance);
  queryClient.setQueryData(sessionQuery.queryKey, testSession);
  queryClient.setQueryData(accountQuery.queryKey, testAccount);
  const realtime = fakeRealtime();
  const db = new OfflineDatabase();
  let connected = true;
  const listeners = new Set<(online: boolean) => void>();
  const documents = new Documents({
    queryClient,
    realtime,
    log,
    copies: copySource(db, true),
    online: () => connected,
  });
  const persist = vi.fn(async () => true);
  const keeper = new OfflineKeeper({
    queryClient,
    documents,
    log,
    db,
    secure: options.secure ?? true,
    online: () => connected,
    onConnection: (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    lock: (write) => void write(),
    broadcast: {
      post: () => {},
      listen: (listener) => {
        heard.add(listener);
        return () => heard.delete(listener);
      },
    },
    storage: { persist },
    writeDelay: options.writeDelay ?? 0,
    retryDelay: options.retryDelay ?? 20,
    pace: options.pace ?? { burst: 1, interval: 0 },
  });
  options.beforeStart?.(db);
  keeper.start();
  onTestFinished(async () => {
    keeper.stop();
    await keeper.idle();
    documents.close();
    await db.close();
  });
  return {
    keeper,
    db,
    documents,
    realtime,
    queryClient,
    persist,
    log,
    setOnline(online: boolean) {
      connected = online;
      for (const listener of listeners) {
        listener(online);
      }
    },
    /** Tells the keeper that another tab changed the pins. */
    hear() {
      for (const listener of heard) {
        listener();
      }
    },
  };
}

/** Has the device refuse each entity that a copy writes while full says so, as for its quota. */
function quota(full: () => boolean) {
  const put = IDBObjectStore.prototype.put;
  vi.spyOn(IDBObjectStore.prototype, "put").mockImplementation(function (
    this: IDBObjectStore,
    ...args: Parameters<IDBObjectStore["put"]>
  ) {
    if (full() && this.name === "entities") {
      throw new DOMException(
        "The quota has been exceeded.",
        "QuotaExceededError",
      );
    }
    return put.apply(this, args);
  });
}

/** Waits until the keeper's pin of projectId has status. */
async function statusOf(
  keeper: OfflineKeeper,
  projectId: string,
  status: string,
): Promise<void> {
  await vi.waitFor(
    () =>
      expect(keeper.store.getState().pins.get(projectId)?.status).toBe(status),
    {
      timeout: 4000,
    },
  );
}

describe("pinning a project", () => {
  // Verifies: FR-OFF-02/AC1
  it("downloads the whole project, every board's content and every component whole, and asks the browser to keep it", async () => {
    serve();
    const { keeper, db, persist } = setup();
    await vi.waitFor(() => expect(keeper.available).toBe(true));
    await keeper.pin(testProject.id, testProject.name);
    expect(persist).toHaveBeenCalledOnce();
    expect(keeper.store.getState().pins.get(testProject.id)).toMatchObject({
      chosen: true,
      auto: false,
    });
    await statusOf(keeper, testProject.id, "ready");
    const copy = await db.readCopy(testSession.user_id, testProject.id);
    const id = testProject.id;
    expect([...(copy?.doc.boards.keys() ?? [])].sort()).toEqual([
      `bd_a${id}`,
      `bd_b${id}`,
    ]);
    expect([...(copy?.doc.nodes.keys() ?? [])].sort()).toEqual([
      `nd_1${id}`,
      `nd_2${id}`,
      `nd_3${id}`,
    ]);
    expect([...(copy?.doc.variables.keys() ?? [])]).toEqual(["var_gold"]);
    expect(copy?.doc.components.get("cmp_jo")?.description).toBe("The hero.");
    expect(copy?.doc.components.get("cmp_jo")?.attributes).toEqual({});
    expect(copy?.record.project.role).toBe("owner");
    expect(copy?.record.workspace.id).toBe(testWorkspace.id);
    expect(keeper.store.getState().pins.get(id)?.copy?.seq).toBe(1);
    // What a page that loads offline starts from: the session, without its
    // CSRF token, the account, and the instance.
    const kept = await db.session();
    expect(kept?.session.user_id).toBe(testSession.user_id);
    expect(kept?.session.csrf_token).toBe("");
    expect(kept?.account).toEqual(testAccount);
    expect(kept?.instance).toEqual(testInstance);
    expect(await keeper.reads.project(testSession.user_id, id)).toMatchObject({
      id,
      role: "owner",
    });
    expect(
      await keeper.reads.workspace(testSession.user_id, testWorkspace.id),
    ).toMatchObject({
      id: testWorkspace.id,
    });
  });

  it("keeps the copy current as operations apply, and downloads a board that one creates", async () => {
    serve();
    const { keeper, db, realtime } = setup();
    await keeper.pin(testProject.id, testProject.name);
    await statusOf(keeper, testProject.id, "ready");
    const id = testProject.id;
    realtime.hold(id);
    realtime.send(
      id,
      [{ type: "board.rename", payload: { id: `bd_a${id}`, name: "Quay" } }],
      [{ id: `bd_a${id}`, rev: 2 }],
    );
    await vi.waitFor(
      async () => {
        const copy = await db.readCopy(testSession.user_id, id);
        expect(copy?.doc.seq).toBe(2);
        expect(copy?.doc.boards.get(`bd_a${id}`)?.name).toBe("Quay");
        expect(copy?.doc.nodes.size).toBe(3);
      },
      { timeout: 4000 },
    );
  });

  // Verifies: FR-OFF-02/AC3
  it("takes the project's copy off the device at once once it is unpinned", async () => {
    serve();
    const { keeper, db } = setup();
    await keeper.pin(testProject.id, testProject.name);
    await statusOf(keeper, testProject.id, "ready");
    await keeper.unpin(testProject.id);
    expect(keeper.store.getState().pins.size).toBe(0);
    expect(
      await db.readCopy(testSession.user_id, testProject.id),
    ).toBeUndefined();
    expect(await db.copies(testSession.user_id)).toEqual([]);
    const tx = (await db.open()).transaction("entities");
    expect(await tx.store.count()).toBe(0);
    // With no project kept, nothing of the session is either.
    expect(await db.session()).toBeUndefined();
  });

  it("downloads nothing while the browser has no connection, and does once it has", async () => {
    const calls = serve();
    const { keeper, setOnline } = setup();
    setOnline(false);
    await keeper.pin(testProject.id, testProject.name);
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(keeper.store.getState().pins.get(testProject.id)?.status).toBe(
      "downloading",
    );
    expect(calls.filter((c) => c === "listBoardNodes")).toEqual([]);
    setOnline(true);
    await statusOf(keeper, testProject.id, "ready");
  });
});

describe("the last opened project", () => {
  it("is pinned, and the pin of the one before goes, with its copy, once the copy of the next project opened is written", async () => {
    serve();
    const { keeper, db } = setup();
    keeper.opened(testProject.id, testProject.name);
    await statusOf(keeper, testProject.id, "ready");
    expect(keeper.store.getState().pins.get(testProject.id)).toMatchObject({
      auto: true,
      openedBefore: false,
      chosen: false,
    });
    keeper.opened(lighthouse.id, lighthouse.name);
    await statusOf(keeper, lighthouse.id, "ready");
    await vi.waitFor(() =>
      expect(keeper.store.getState().pins.has(testProject.id)).toBe(false),
    );
    expect(
      await db.readCopy(testSession.user_id, testProject.id),
    ).toBeUndefined();
    expect(keeper.store.getState().pins.get(lighthouse.id)?.auto).toBe(true);
  });

  it("is pinned at once while the browser has no connection, keeping the copy of the one before, which goes once its own copy is written", async () => {
    serve();
    const { keeper, db, setOnline } = setup();
    keeper.opened(testProject.id, testProject.name);
    await statusOf(keeper, testProject.id, "ready");
    setOnline(false);
    keeper.opened(lighthouse.id, lighthouse.name);
    await keeper.idle();
    expect(keeper.store.getState().pins.get(lighthouse.id)).toMatchObject({
      auto: true,
      status: "downloading",
    });
    expect(keeper.store.getState().pins.get(testProject.id)).toMatchObject({
      auto: false,
      openedBefore: true,
      status: "ready",
    });
    expect(
      await db.readCopy(testSession.user_id, testProject.id),
    ).toBeDefined();
    setOnline(true);
    await statusOf(keeper, lighthouse.id, "ready");
    await vi.waitFor(() =>
      expect(keeper.store.getState().pins.has(testProject.id)).toBe(false),
    );
    expect(
      await db.readCopy(testSession.user_id, testProject.id),
    ).toBeUndefined();
  });

  it("keeps the copy of the one before where its own copy cannot be written, as for the quota", async () => {
    serve();
    // Nothing is tried again of itself within the test.
    const { keeper, db } = setup({ retryDelay: 60_000 });
    let full = false;
    quota(() => full);
    keeper.opened(testProject.id, testProject.name);
    await statusOf(keeper, testProject.id, "ready");
    full = true;
    keeper.opened(lighthouse.id, lighthouse.name);
    await statusOf(keeper, lighthouse.id, "failed");
    await keeper.idle();
    expect(keeper.store.getState().pins.get(lighthouse.id)).toMatchObject({
      auto: true,
      failure: "quota",
    });
    expect(keeper.store.getState().pins.get(testProject.id)).toMatchObject({
      openedBefore: true,
      status: "ready",
    });
    expect(
      await db.readCopy(testSession.user_id, testProject.id),
    ).toBeDefined();
  });

  it("drops the pins of every project opened before it once its copy is written, and none at the copy of one opened before it", async () => {
    let towerAnswers = false;
    serve({
      fail: (_, input) =>
        !towerAnswers &&
        (input as { path?: { project_id?: string } } | undefined)?.path
          ?.project_id === tower.id
          ? refused(403, { code: "forbidden" })
          : undefined,
    });
    const { keeper, db, setOnline } = setup();
    keeper.opened(testProject.id, testProject.name);
    await statusOf(keeper, testProject.id, "ready");
    setOnline(false);
    keeper.opened(lighthouse.id, lighthouse.name);
    keeper.opened(tower.id, tower.name);
    await keeper.idle();
    const pins = () => keeper.store.getState().pins;
    expect(pins().get(tower.id)?.auto).toBe(true);
    expect(pins().get(lighthouse.id)?.openedBefore).toBe(true);
    expect(pins().get(testProject.id)?.openedBefore).toBe(true);
    // The lighthouse's copy is written before the tower's, which drops
    // nothing, as the tower is the project opened last.
    setOnline(true);
    await statusOf(keeper, lighthouse.id, "ready");
    await keeper.idle();
    expect(pins().has(testProject.id)).toBe(true);
    expect(
      await db.readCopy(testSession.user_id, testProject.id),
    ).toBeDefined();
    towerAnswers = true;
    await statusOf(keeper, tower.id, "ready");
    await vi.waitFor(() => expect([...pins().keys()]).toEqual([tower.id]));
    expect(await db.copies(testSession.user_id)).toMatchObject([
      { project_id: tower.id },
    ]);
  });

  it("keeps the copy of one opened before it that the user chose, no longer pinned automatically", async () => {
    serve();
    const { keeper, db, setOnline } = setup();
    keeper.opened(lighthouse.id, lighthouse.name);
    await keeper.pin(lighthouse.id, lighthouse.name);
    await statusOf(keeper, lighthouse.id, "ready");
    setOnline(false);
    keeper.opened(tower.id, tower.name);
    await keeper.idle();
    expect(keeper.store.getState().pins.get(lighthouse.id)).toMatchObject({
      chosen: true,
      openedBefore: true,
    });
    setOnline(true);
    await statusOf(keeper, tower.id, "ready");
    await vi.waitFor(() =>
      expect(
        keeper.store.getState().pins.get(lighthouse.id)?.openedBefore,
      ).toBe(false),
    );
    expect(keeper.store.getState().pins.get(lighthouse.id)).toMatchObject({
      chosen: true,
      auto: false,
    });
    expect(await db.readCopy(testSession.user_id, lighthouse.id)).toBeDefined();
    expect(
      (await db.pins(testSession.user_id)).find(
        (p) => p.project_id === lighthouse.id,
      ),
    ).toMatchObject({ auto: false });
  });

  it("is pinned again each time that it is opened, as once its pin went meanwhile, here or in another tab", async () => {
    serve();
    const { keeper, db, hear } = setup();
    keeper.opened(testProject.id, testProject.name);
    await statusOf(keeper, testProject.id, "ready");
    // Removed from this device, and opened again.
    await keeper.unpin(testProject.id);
    expect(keeper.store.getState().pins.has(testProject.id)).toBe(false);
    keeper.opened(testProject.id, testProject.name);
    await statusOf(keeper, testProject.id, "ready");
    // Another tab opens the lighthouse, whose copy it writes, which takes
    // the harbour's pin out, and this tab opens the harbour again.
    const now = new Date().toISOString();
    await db.markOpened(
      testSession.user_id,
      lighthouse.id,
      () => ({
        user_id: testSession.user_id,
        project_id: lighthouse.id,
        name: lighthouse.name,
        chosen: false,
        auto: true,
        pinned_at: now,
        status: "ready",
      }),
      now,
    );
    await db.dropBeforeLast(testSession.user_id);
    hear();
    await vi.waitFor(() =>
      expect(keeper.store.getState().pins.has(testProject.id)).toBe(false),
    );
    keeper.opened(testProject.id, testProject.name);
    await statusOf(keeper, testProject.id, "ready");
    expect(keeper.store.getState().pins.get(testProject.id)?.auto).toBe(true);
  });

  it("drops the copy of one opened before it that the user switches off, once its own copy is written", async () => {
    serve();
    const { keeper, db, hear, setOnline } = setup();
    keeper.opened(lighthouse.id, lighthouse.name);
    await keeper.pin(lighthouse.id, lighthouse.name);
    await statusOf(keeper, lighthouse.id, "ready");
    // The tower, opened after it, has its copy written, but the pins
    // opened before it are still there, as of a page that closed in
    // between, whose next keeper has nothing to write of the tower.
    setOnline(false);
    const now = new Date().toISOString();
    await db.markOpened(
      testSession.user_id,
      tower.id,
      () => ({
        user_id: testSession.user_id,
        project_id: tower.id,
        name: tower.name,
        chosen: false,
        auto: true,
        pinned_at: now,
        status: "ready",
      }),
      now,
    );
    hear();
    await vi.waitFor(() =>
      expect(keeper.store.getState().pins.get(lighthouse.id)).toMatchObject({
        chosen: true,
        openedBefore: true,
      }),
    );
    await keeper.unchoose(lighthouse.id);
    expect(keeper.store.getState().pins.has(lighthouse.id)).toBe(false);
    expect(
      await db.readCopy(testSession.user_id, lighthouse.id),
    ).toBeUndefined();
  });
});

describe("a project that the account can no longer read", () => {
  it("is kept no more, as once it went to the trash", async () => {
    serve();
    const { keeper, db } = setup();
    await keeper.pin("prj_gone", "Gone");
    await vi.waitFor(
      () => expect(keeper.store.getState().pins.has("prj_gone")).toBe(false),
      { timeout: 4000 },
    );
    expect(await db.pins(testSession.user_id)).toEqual([]);
  });
});

describe("the device's records of the account", () => {
  it("are taken off the device once the page signs out, or is of another account", async () => {
    serve();
    const { keeper, db, queryClient } = setup();
    await keeper.pin(testProject.id, testProject.name);
    await statusOf(keeper, testProject.id, "ready");
    const kim: SignedInBody = { ...testSession, user_id: "usr_kim" };
    queryClient.setQueryData(sessionQuery.queryKey, kim);
    await vi.waitFor(async () =>
      expect(await db.pins(testSession.user_id)).toEqual([]),
    );
    expect(await db.copies(testSession.user_id)).toEqual([]);
    expect(await db.session()).toBeUndefined();
    expect(keeper.store.getState().userId).toBe("usr_kim");
    queryClient.setQueryData(sessionQuery.queryKey, null);
    await vi.waitFor(() =>
      expect(keeper.store.getState().userId).toBeUndefined(),
    );
  });

  it("are not made anew by a tab that has yet to find that another tab signed out", async () => {
    serve();
    const { keeper, db, hear } = setup();
    await keeper.pin(testProject.id, testProject.name);
    await statusOf(keeper, testProject.id, "ready");
    // Another tab signs out, which takes the account's records off the
    // device; this one's session is the account's still, as its page has
    // yet to ask for it.
    await db.deleteAllBut(undefined);
    hear();
    await vi.waitFor(() => expect(keeper.store.getState().pins.size).toBe(0));
    keeper.opened(lighthouse.id, lighthouse.name);
    await keeper.pin(tower.id, tower.name);
    await keeper.idle();
    await new Promise((resolve) => setTimeout(resolve, 100));
    await keeper.idle();
    expect(await db.pins(testSession.user_id)).toEqual([]);
    expect(await db.copies(testSession.user_id)).toEqual([]);
    expect(await db.session()).toBeUndefined();
  });

  it("are followed once the device's database opens, though it did not as the page started", async () => {
    serve();
    const { keeper, queryClient } = setup({
      beforeStart: (db) => {
        const refused = new Error("The database could not be opened.");
        vi.spyOn(db, "open")
          .mockRejectedValueOnce(refused)
          .mockRejectedValueOnce(refused);
      },
    });
    await keeper.idle();
    expect(keeper.store.getState()).toMatchObject({
      available: false,
      userId: undefined,
    });
    // The session read again, as at the page's next read of it.
    queryClient.setQueryData(sessionQuery.queryKey, { ...testSession });
    await vi.waitFor(() =>
      expect(keeper.store.getState()).toMatchObject({
        available: true,
        userId: testSession.user_id,
      }),
    );
    await keeper.pin(testProject.id, testProject.name);
    await statusOf(keeper, testProject.id, "ready");
  });

  it("keeps nothing on a page that is not a secure context", async () => {
    serve();
    const { keeper, db } = setup({ secure: false });
    expect(keeper.store.getState().available).toBe(false);
    keeper.opened(testProject.id, testProject.name);
    await keeper.pin(testProject.id, testProject.name);
    expect(keeper.store.getState().pins.size).toBe(0);
    expect(await db.pins(testSession.user_id)).toEqual([]);
  });
});

describe("a copy that the browser's quota refuses", () => {
  // Verifies: FR-OFF-02/AC2
  it("is not kept, and the pin says why, until room is made", async () => {
    serve();
    const { keeper, db, log } = setup();
    const put = IDBObjectStore.prototype.put;
    let full = true;
    // Once the pin is made, the device refuses even the pin's status, as a
    // device whose storage is full may.
    let pinned = false;
    vi.spyOn(IDBObjectStore.prototype, "put").mockImplementation(function (
      this: IDBObjectStore,
      ...args: Parameters<IDBObjectStore["put"]>
    ) {
      if (
        full &&
        (this.name === "entities" || (pinned && this.name === "pins"))
      ) {
        throw new DOMException(
          "The quota has been exceeded.",
          "QuotaExceededError",
        );
      }
      return put.apply(this, args);
    });
    await keeper.pin(testProject.id, testProject.name);
    pinned = true;
    await statusOf(keeper, testProject.id, "failed");
    expect(keeper.store.getState().pins.get(testProject.id)).toMatchObject({
      failure: "quota",
    });
    expect(
      keeper.store.getState().pins.get(testProject.id)?.copy,
    ).toBeUndefined();
    expect(
      await db.readCopy(testSession.user_id, testProject.id),
    ).toBeUndefined();
    const tx = (await db.open()).transaction("entities");
    expect(await tx.store.count()).toBe(0);
    // The user's to mend, not an error of the app's.
    expect(log.entries()).toEqual([]);

    // Room made, as by taking another project off the device, has it tried again.
    full = false;
    keeper.opened(lighthouse.id, lighthouse.name);
    await statusOf(keeper, lighthouse.id, "ready");
    await keeper.unpin(lighthouse.id);
    await statusOf(keeper, testProject.id, "ready");
  });

  it("is tried again once another project's copy has gone in another tab", async () => {
    serve();
    // Nothing is tried again of itself within the test.
    const { keeper, db, hear } = setup({ retryDelay: 60_000 });
    let full = false;
    quota(() => full);
    await keeper.pin(lighthouse.id, lighthouse.name);
    await statusOf(keeper, lighthouse.id, "ready");
    full = true;
    await keeper.pin(testProject.id, testProject.name);
    await statusOf(keeper, testProject.id, "failed");
    full = false;
    await db.changePin(testSession.user_id, lighthouse.id, () => undefined);
    hear();
    await statusOf(keeper, testProject.id, "ready");
  });

  it("is not shown of a project unpinned meanwhile, which pinned again downloads", async () => {
    serve();
    const { keeper } = setup();
    await keeper.pin(testProject.id, testProject.name);
    await statusOf(keeper, testProject.id, "ready");
    await keeper.unpin(testProject.id);
    // The write that the quota refused as the project was unpinned.
    await keeper.failed(testSession.user_id, testProject.id, "quota");
    await keeper.pin(testProject.id, testProject.name);
    expect(keeper.store.getState().pins.get(testProject.id)).toMatchObject({
      status: "downloading",
    });
    expect(
      keeper.store.getState().pins.get(testProject.id)?.failure,
    ).toBeUndefined();
    await statusOf(keeper, testProject.id, "ready");
  });

  it("is not shown of a project whose automatic pin went once the copy of the one opened after it was written, which pinned again downloads", async () => {
    serve();
    const { keeper, setOnline } = setup({ retryDelay: 60_000 });
    let full = true;
    quota(() => full);
    keeper.opened(testProject.id, testProject.name);
    await statusOf(keeper, testProject.id, "failed");
    full = false;
    keeper.opened(lighthouse.id, lighthouse.name);
    await vi.waitFor(() =>
      expect(keeper.store.getState().pins.has(testProject.id)).toBe(false),
    );
    // Pinned again while the browser has no connection, the copy waits to
    // be downloaded, as nothing has tried it since.
    setOnline(false);
    await keeper.pin(testProject.id, testProject.name);
    expect(keeper.store.getState().pins.get(testProject.id)).toMatchObject({
      status: "downloading",
    });
    expect(
      keeper.store.getState().pins.get(testProject.id)?.failure,
    ).toBeUndefined();
  });
});

describe("a project's download", () => {
  it("reads at the pace of the keeper's downloads, once it has read as many as it reads at once", async () => {
    const calls = serve();
    const { keeper } = setup({ pace: { burst: 1, interval: 150 } });
    await keeper.pin(testProject.id, testProject.name);
    await statusOf(keeper, testProject.id, "ready");
    // The variables, the components, the two boards' content, and the
    // component whole, each a turn after the one before.
    const boards = calls.times.filter((_, i) => calls[i] === "listBoardNodes");
    expect(boards).toHaveLength(2);
    expect(boards[1] - boards[0]).toBeGreaterThanOrEqual(120);
  });

  it("reads again what a pass of reads left unread a writeDelay after, as a component that keeps changing as it is read", async () => {
    // The component reads without its values, as though an operation had
    // changed it each time while it was read.
    const calls = serve({ component: () => ({ ...jo, description: "" }) });
    const { keeper } = setup({ writeDelay: 150, retryDelay: 60_000 });
    await keeper.pin(testProject.id, testProject.name);
    const reads = () =>
      calls.times.filter((_, i) => calls[i] === "getComponent");
    // Each pass tries the component three times (readComponent).
    await vi.waitFor(() => expect(reads().length).toBeGreaterThanOrEqual(6), {
      timeout: 4000,
    });
    const [, , last, next] = reads();
    expect(next - last).toBeGreaterThanOrEqual(140);
    // Downloading still, rather than a copy that could not be written.
    expect(keeper.store.getState().pins.get(testProject.id)?.status).toBe(
      "downloading",
    );
  });

  it("is tried again at once once the browser is online again, though a read failed a moment before", async () => {
    let failing = true;
    serve({
      fail: (operation) =>
        failing && operation === "listBoards"
          ? refused(503, { code: "unavailable" })
          : undefined,
    });
    // Nothing is tried again of itself within the test.
    const { keeper, setOnline } = setup({ retryDelay: 60_000 });
    await keeper.pin(testProject.id, testProject.name);
    await new Promise((resolve) => setTimeout(resolve, 100));
    failing = false;
    setOnline(false);
    setOnline(true);
    await statusOf(keeper, testProject.id, "ready");
  });

  it("is no failure where a board goes to the trash as it is read, which the next pass finds gone", async () => {
    const id = testProject.id;
    let send = () => {};
    let trashed = false;
    const calls = serve({
      fail: (operation, input) => {
        if (
          operation !== "listBoardNodes" ||
          !(input as { path: { board_id: string } }).path.board_id.startsWith(
            "bd_b",
          )
        ) {
          return undefined;
        }
        // The operation that took the board to the trash, which the
        // channel brings as the board's read is refused.
        trashed = true;
        send();
        send = () => {};
        return refused(404, { code: "not_found" });
      },
      boards: (project) =>
        trashed ? [board(project, `bd_a${project}`)] : undefined,
    });
    // Nothing is tried again of itself within the test.
    const { keeper, realtime } = setup({ retryDelay: 60_000 });
    send = () =>
      realtime.send(
        id,
        [{ type: "board.delete", payload: { id: `bd_b${id}` } }],
        [{ id: `bd_b${id}`, rev: 2 }],
      );
    const statuses: string[] = [];
    keeper.store.subscribe((state) => {
      statuses.push(state.pins.get(id)?.status ?? "none");
    });
    await keeper.pin(id, testProject.name);
    await statusOf(keeper, id, "ready");
    expect(calls.filter((c) => c === "listBoardNodes")).toHaveLength(2);
    expect(statuses).not.toContain("failed");
    expect(keeper.store.getState().pins.get(id)?.failure).toBeUndefined();
  });

  it("reads the tree again where a board goes to the trash as it is read while the channel is not live, and writes the copy without it", async () => {
    const id = testProject.id;
    let trashed = false;
    const calls = serve({
      fail: (operation, input) => {
        if (
          operation !== "listBoardNodes" ||
          !(input as { path: { board_id: string } }).path.board_id.startsWith(
            "bd_b",
          )
        ) {
          return undefined;
        }
        // No operation comes, as behind a proxy that refuses the socket.
        trashed = true;
        return refused(404, { code: "not_found" });
      },
      boards: (project) =>
        trashed ? [board(project, `bd_a${project}`)] : undefined,
    });
    // Nothing is tried again of itself within the test.
    const { keeper, db } = setup({ retryDelay: 60_000 });
    const statuses: string[] = [];
    keeper.store.subscribe((state) => {
      statuses.push(state.pins.get(id)?.status ?? "none");
    });
    await keeper.pin(id, testProject.name);
    await statusOf(keeper, id, "ready");
    expect(statuses).not.toContain("failed");
    expect(calls.filter((c) => c === "listBoards")).toHaveLength(2);
    const copy = await db.readCopy(testSession.user_id, id);
    expect([...(copy?.doc.boards.keys() ?? [])]).toEqual([`bd_a${id}`]);
  });

  it("says that the copy could not be written where the project's tree cannot be read", async () => {
    serve({
      fail: (operation) =>
        operation === "listBoards"
          ? refused(500, { code: "internal" })
          : undefined,
    });
    const { keeper } = setup({ retryDelay: 60_000 });
    await keeper.pin(testProject.id, testProject.name);
    await statusOf(keeper, testProject.id, "failed");
    expect(keeper.store.getState().pins.get(testProject.id)?.failure).toBe(
      "error",
    );
  });

  it("says that the copy could not be written where the project's workspace cannot be read", async () => {
    serve({
      fail: (operation) =>
        operation === "listWorkspaces"
          ? refused(403, { code: "forbidden" })
          : undefined,
    });
    const { keeper } = setup({ retryDelay: 60_000 });
    await keeper.pin(testProject.id, testProject.name);
    await statusOf(keeper, testProject.id, "failed");
    expect(keeper.store.getState().pins.get(testProject.id)?.failure).toBe(
      "error",
    );
  });
});

describe("a kept project's copy", () => {
  it("is written while operations keep coming, at the latest some writeDelays after the first", async () => {
    serve();
    const { keeper, db, realtime } = setup({ writeDelay: 40 });
    await keeper.pin(testProject.id, testProject.name);
    await statusOf(keeper, testProject.id, "ready");
    const id = testProject.id;
    realtime.hold(id);
    let rev = 1;
    // An operation every 10 milliseconds, each of which would put off a
    // write a writeDelay after the last change.
    const sending = setInterval(() => {
      rev++;
      realtime.send(
        id,
        [
          {
            type: "board.rename",
            payload: { id: `bd_a${id}`, name: `Quay ${rev}` },
          },
        ],
        [{ id: `bd_a${id}`, rev }],
      );
    }, 10);
    try {
      await vi.waitFor(
        async () =>
          expect(
            (await db.readCopy(testSession.user_id, id))?.doc.seq,
          ).toBeGreaterThan(1),
        { timeout: 2_000 },
      );
    } finally {
      clearInterval(sending);
    }
  });

  it("has the boards that a reading anew of the project let go read again by the download, before it is written", async () => {
    const calls = serve();
    const { keeper, db, documents } = setup();
    await keeper.pin(testProject.id, testProject.name);
    await statusOf(keeper, testProject.id, "ready");
    const nodes = () => calls.filter((c) => c === "listBoardNodes").length;
    expect(nodes()).toBe(2);
    // As while the channel is not live: the project is read anew, without
    // the boards that the download read.
    await documents.get(testProject.id).refresh();
    expect(nodes()).toBe(2);
    await vi.waitFor(() => expect(nodes()).toBe(4), { timeout: 4000 });
    await vi.waitFor(
      async () => {
        const copy = await db.readCopy(testSession.user_id, testProject.id);
        expect(copy?.doc.nodes.size).toBe(3);
        expect(copy?.doc.components.get("cmp_jo")?.attributes).toEqual({});
      },
      { timeout: 4000 },
    );
    await statusOf(keeper, testProject.id, "ready");
  });

  it("is not taken back to an older seq by a document whose channel is not live, which writes once it is", async () => {
    serve();
    const user = testSession.user_id;
    const { keeper, db, realtime } = setup();
    await vi.waitFor(() => expect(keeper.available).toBe(true));
    // The copy that another tab wrote, at a seq past the document's, as
    // the server's, whose channel has yet to bring it up to it.
    await db.changePin(user, testProject.id, () => ({
      user_id: user,
      project_id: testProject.id,
      name: testProject.name,
      chosen: true,
      auto: false,
      pinned_at: "2026-11-01T10:00:00Z",
      status: "ready",
    }));
    await db.writeCopy(
      {
        user_id: user,
        project_id: testProject.id,
        seq: 5,
        project: { ...testProject, seq: 5 },
        workspace: {
          id: testWorkspace.id,
          name: testWorkspace.name,
          slug: testWorkspace.slug,
          default_locale: "en",
          role: "owner",
          membership: true,
        },
        saved_at: "2026-11-01T10:00:00Z",
      },
      { ...emptyDoc(5), project: projectOf(testProject) },
      undefined,
    );
    await keeper.pin(testProject.id, testProject.name);
    await new Promise((resolve) => setTimeout(resolve, 200));
    expect((await db.readCopy(user, testProject.id))?.doc.seq).toBe(5);
    // Live, the document is what the server has, as after a restore.
    realtime.hold(testProject.id);
    await vi.waitFor(
      async () =>
        expect((await db.readCopy(user, testProject.id))?.doc.seq).toBe(1),
      { timeout: 4000 },
    );
  });

  it("is said not kept where the device refuses the session that a page loaded offline starts from", async () => {
    serve();
    let full = true;
    const put = IDBObjectStore.prototype.put;
    vi.spyOn(IDBObjectStore.prototype, "put").mockImplementation(function (
      this: IDBObjectStore,
      ...args: Parameters<IDBObjectStore["put"]>
    ) {
      if (full && this.name === "sessions") {
        throw new DOMException(
          "The quota has been exceeded.",
          "QuotaExceededError",
        );
      }
      return put.apply(this, args);
    });
    const { keeper, db } = setup({ retryDelay: 20 });
    await keeper.pin(testProject.id, testProject.name);
    await statusOf(keeper, testProject.id, "failed");
    expect(keeper.store.getState().pins.get(testProject.id)?.failure).toBe(
      "quota",
    );
    expect(await db.session()).toBeUndefined();
    full = false;
    await statusOf(keeper, testProject.id, "ready");
    expect((await db.session())?.user_id).toBe(testSession.user_id);
  });
});

describe("the lock of the writer of the copies", () => {
  it("is let go at once by a keeper that stopped before the browser gave it the lock", async () => {
    const log = createErrorLog();
    const queryClient = createQueryClient(log);
    let write: (() => Promise<void>) | undefined;
    const db = new OfflineDatabase();
    onTestFinished(() => db.close());
    const keeper = new OfflineKeeper({
      queryClient,
      documents: new Documents({ queryClient, log }),
      log,
      db,
      secure: true,
      online: () => true,
      onConnection: () => () => {},
      // As while another tab holds the lock.
      lock: (granted) => {
        write = granted;
      },
    });
    keeper.start();
    keeper.stop();
    await keeper.idle();
    // The other tab is gone, and the browser gives it the lock.
    let released = false;
    void write?.().then(() => {
      released = true;
    });
    await vi.waitFor(() => expect(released).toBe(true));
  });
});

describe("what the device keeps, as a page that the server does not answer reads it", () => {
  it("is none where the device's database does not open in time, rather than wait for it", async () => {
    vi.useFakeTimers();
    onTestFinished(() => {
      vi.useRealTimers();
    });
    const db = new OfflineDatabase();
    vi.spyOn(db, "open").mockReturnValue(new Promise(() => {}));
    const log = createErrorLog();
    const queryClient = createQueryClient(log);
    const keeper = new OfflineKeeper({
      queryClient,
      documents: new Documents({ queryClient, log }),
      log,
      db,
      secure: true,
      online: () => true,
      onConnection: () => () => {},
      lock: () => {},
    });
    const session = keeper.reads.session();
    const project = keeper.reads.project(testSession.user_id, testProject.id);
    await vi.advanceTimersByTimeAsync(openWait);
    await expect(session).resolves.toBeUndefined();
    await expect(project).resolves.toBeUndefined();
  });
});
