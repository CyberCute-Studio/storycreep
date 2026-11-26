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
import type { ComponentEntry } from "../document/doc";
import { Documents } from "../document/documents";
import { createErrorLog } from "../errors/log";
import { testAccount, testInstance, testSession } from "../test/app";
import { fakeApi, refused, testProject, testWorkspace } from "../test/fake";
import { fakeRealtime } from "../test/realtime";
import { copySource, OfflineDatabase } from "./db";
import { OfflineKeeper } from "./keeper";

// The keeper of the projects that the device keeps offline (PRD FR-OFF-02,
// plan note N97), over the fake API, the fake channel, and fake-indexeddb:
// a pin downloads the whole project, every board's content and every
// component whole, which an operation keeps current; the last opened
// project is pinned, and its pin moves to the next; unpinning and signing
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

/** Serves the test projects: each with boards bd_a and bd_b of two nodes and one, a variable, and a component, which reads whole. */
function serve() {
  const calls: string[] = [];
  const projects = new Map(
    [testProject, lighthouse, tower].map((p) => [p.id, p]),
  );
  fakeApi((operation, input) => {
    calls.push(operation);
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
        return page([
          board(project ?? "", `bd_a${project}`),
          board(project ?? "", `bd_b${project}`),
        ]);
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
        return { ...jo, description: "The hero.", attributes: {} };
    }
    throw new Error(`unexpected ${operation}`);
  });
  return calls;
}

function setup(options: { secure?: boolean } = {}) {
  const log = createErrorLog();
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
    storage: { persist },
    writeDelay: 0,
    retryDelay: 20,
  });
  keeper.start();
  onTestFinished(async () => {
    keeper.stop();
    documents.close();
    await db.close();
  });
  return {
    keeper,
    db,
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
  };
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
  it("is pinned, and its pin moves to the next project opened, whose copy the one before's gives way to unless it was chosen", async () => {
    serve();
    const { keeper, db } = setup();
    keeper.opened(testProject.id, testProject.name);
    await statusOf(keeper, testProject.id, "ready");
    expect(keeper.store.getState().pins.get(testProject.id)).toMatchObject({
      auto: true,
      chosen: false,
    });
    keeper.opened(lighthouse.id, lighthouse.name);
    await statusOf(keeper, lighthouse.id, "ready");
    expect(keeper.store.getState().pins.has(testProject.id)).toBe(false);
    expect(
      await db.readCopy(testSession.user_id, testProject.id),
    ).toBeUndefined();

    await keeper.pin(lighthouse.id, lighthouse.name);
    keeper.opened(tower.id, tower.name);
    await statusOf(keeper, tower.id, "ready");
    expect(keeper.store.getState().pins.get(lighthouse.id)).toMatchObject({
      auto: false,
      chosen: true,
    });
    expect(await db.readCopy(testSession.user_id, lighthouse.id)).toBeDefined();
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
});
