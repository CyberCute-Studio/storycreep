import "fake-indexeddb/auto";
import type { Board, Node } from "@storycreep/shared/api";
import { forceCloseDatabase, IDBFactory } from "fake-indexeddb";
import { unwrap } from "idb";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { boardPart, type Doc, emptyDoc } from "../document/doc";
import { testProject, testWorkspace } from "../test/fake";
import type { Workspace } from "../workspace/queries";
import {
  type CopyRecord,
  copyFormat,
  copySource,
  type KeptSession,
  OfflineDatabase,
  overQuota,
  type PinRecord,
} from "./db";

// The device's database of what it keeps offline (PRD FR-OFF-02), over the
// IndexedDB of fake-indexeddb: a copy written whole and read back as the
// document that it was, one written as what changed alone, the pins, and
// the records of each account, which signing out takes out.

let db: OfflineDatabase;

beforeEach(() => {
  globalThis.indexedDB = new IDBFactory();
  db = new OfflineDatabase();
});

afterEach(async () => {
  await db.close();
  vi.restoreAllMocks();
});

const user = "usr_jo";
const workspace: Workspace = {
  id: testWorkspace.id,
  name: testWorkspace.name,
  slug: testWorkspace.slug,
  default_locale: "en",
  role: "owner",
  membership: true,
};

function board(id: string, rev = 1): Board {
  return {
    id,
    project_id: testProject.id,
    name: id,
    slug: id,
    description: "",
    color: "none",
    order: "V",
    rev,
    created_at: "2026-11-01T10:00:00Z",
    updated_at: "2026-11-01T10:00:00Z",
  } as Board;
}

function node(id: string, boardId: string, rev = 1): Node {
  return {
    id,
    board_id: boardId,
    kind: "element",
    title: id,
    rev,
  } as unknown as Node;
}

/** Returns a document of the test project, as of seq, holding the tree and the content of the boards given. */
function docOf(seq: number, boards: Board[], nodes: Node[]): Doc {
  const { role: _role, seq: _seq, ...project } = testProject;
  return {
    ...emptyDoc(seq),
    project,
    boards: new Map(boards.map((b) => [b.id, b])),
    nodes: new Map(nodes.map((n) => [n.id, n])),
    parts: new Set(["tree", ...boards.map((b) => boardPart(b.id))]),
  };
}

function recordOf(
  seq: number,
): Omit<CopyRecord, "format" | "version" | "bytes" | "parts"> {
  return {
    user_id: user,
    project_id: testProject.id,
    seq,
    project: { ...testProject, seq },
    workspace,
    saved_at: "2026-11-26T10:00:00Z",
  };
}

function pinOf(userId = user, projectId = testProject.id): PinRecord {
  return {
    user_id: userId,
    project_id: projectId,
    name: "Harbour",
    chosen: true,
    auto: false,
    pinned_at: "2026-11-26T10:00:00Z",
    status: "downloading",
  };
}

async function pin(p = pinOf()): Promise<void> {
  await db.changePin(p.user_id, p.project_id, () => p);
}

describe("the device's copies", () => {
  // Verifies: FR-OFF-02/AC1
  it("reads a copy back as the document that was written, every part of it", async () => {
    await pin();
    const doc = docOf(
      7,
      [board("bd_a"), board("bd_b")],
      [node("nd_1", "bd_a")],
    );
    const written = await db.writeCopy(recordOf(7), doc, undefined);
    expect(written?.doc).toBe(doc);
    const copy = await db.readCopy(user, testProject.id);
    expect(copy?.doc.seq).toBe(7);
    expect(copy?.doc.project?.id).toBe(testProject.id);
    expect(copy?.doc.project).not.toHaveProperty("role");
    expect([...(copy?.doc.boards.keys() ?? [])].sort()).toEqual([
      "bd_a",
      "bd_b",
    ]);
    expect(copy?.doc.nodes.get("nd_1")).toEqual(node("nd_1", "bd_a"));
    expect([...(copy?.doc.parts ?? [])].sort()).toEqual(
      ["board:bd_a", "board:bd_b", "tree"].sort(),
    );
    expect(copy?.record.project.role).toBe("owner");
    expect(copy?.record.version).toBe(written?.version);
    expect(copy?.record.bytes).toBeGreaterThan(0);
    expect((await db.pins(user))[0].status).toBe("ready");
  });

  it("writes what changed alone on the write that it was made on, and the whole document otherwise", async () => {
    await pin();
    const first = docOf(
      1,
      [board("bd_a"), board("bd_b")],
      [node("nd_1", "bd_a"), node("nd_2", "bd_a")],
    );
    const one = await db.writeCopy(recordOf(1), first, undefined);
    const second: Doc = {
      ...first,
      seq: 2,
      boards: new Map([["bd_a", board("bd_a", 2)]]),
      nodes: new Map([
        ["nd_1", first.nodes.get("nd_1") as Node],
        ["nd_3", node("nd_3", "bd_a")],
      ]),
      parts: new Set(["tree", "board:bd_a"]),
    };
    const two = await db.writeCopy(recordOf(2), second, one);
    let copy = await db.readCopy(user, testProject.id);
    expect(copy?.doc.seq).toBe(2);
    expect([...(copy?.doc.boards.values() ?? [])]).toEqual([board("bd_a", 2)]);
    expect([...(copy?.doc.nodes.keys() ?? [])].sort()).toEqual([
      "nd_1",
      "nd_3",
    ]);
    expect(copy?.record.bytes).toBe(two?.bytes);
    // What changed alone counts the bytes as the whole document's write does.
    const whole = await db.writeCopy(recordOf(2), second, undefined);
    expect(whole?.bytes).toBe(two?.bytes);

    // A write made on a write that the device no longer has, as of another
    // tab that wrote since, writes the whole document, without what the
    // copy held besides.
    const stale: Doc = {
      ...second,
      seq: 3,
      nodes: new Map([["nd_9", node("nd_9", "bd_a")]]),
    };
    await db.writeCopy(recordOf(3), stale, one);
    copy = await db.readCopy(user, testProject.id);
    expect([...(copy?.doc.nodes.keys() ?? [])]).toEqual(["nd_9"]);
    expect([...(copy?.doc.boards.keys() ?? [])]).toEqual(["bd_a"]);
  });

  it("sizes each entity once, though a document read anew holds new ones of them all", async () => {
    await pin();
    const first = docOf(
      1,
      [board("bd_a"), board("bd_b")],
      [node("nd_1", "bd_a"), node("nd_2", "bd_a")],
    );
    const one = await db.writeCopy(recordOf(1), first, undefined);
    const anew = docOf(
      2,
      [board("bd_a"), board("bd_b")],
      [node("nd_1", "bd_a"), node("nd_2", "bd_a")],
    );
    const stringify = vi.spyOn(JSON, "stringify");
    const two = await db.writeCopy(recordOf(2), anew, one);
    const sized = stringify.mock.calls.filter(
      ([value]) =>
        typeof value === "object" && value !== null && "rev" in value,
    );
    // The new entities alone, not those of the write that it is made on too.
    expect(sized).toHaveLength(4);
    expect(two?.bytes).toBe(one?.bytes);
  });

  it("counts the bytes of its records as their JSON takes in UTF-8", async () => {
    await pin();
    // A board named in a script of three bytes a character in UTF-8.
    const named = { ...board("bd_a"), name: "港".repeat(1000) };
    const written = await db.writeCopy(
      recordOf(1),
      docOf(1, [named], []),
      undefined,
    );
    expect(written?.bytes).toBeGreaterThan(3000);
  });

  it("writes nothing of a project that is not pinned", async () => {
    const doc = docOf(1, [board("bd_a")], []);
    expect(await db.writeCopy(recordOf(1), doc, undefined)).toBeUndefined();
    await pin();
    expect(await db.readCopy(user, testProject.id)).toBeUndefined();
  });

  // Verifies: FR-OFF-02/AC2
  it("leaves the copy that was there, or none, once the browser refuses a write", async () => {
    await pin();
    const first = docOf(1, [board("bd_a")], [node("nd_1", "bd_a")]);
    const one = await db.writeCopy(recordOf(1), first, undefined);
    const put = IDBObjectStore.prototype.put;
    let puts = 0;
    vi.spyOn(IDBObjectStore.prototype, "put").mockImplementation(function (
      this: IDBObjectStore,
      ...args: Parameters<IDBObjectStore["put"]>
    ) {
      if (++puts === 3) {
        throw new DOMException(
          "The quota has been exceeded.",
          "QuotaExceededError",
        );
      }
      return put.apply(this, args);
    });
    const bigger = docOf(
      2,
      [board("bd_a", 2)],
      [
        node("nd_1", "bd_a", 2),
        node("nd_2", "bd_a"),
        node("nd_3", "bd_a"),
        node("nd_4", "bd_a"),
      ],
    );
    const failure = await db
      .writeCopy(recordOf(2), bigger, undefined)
      .catch((e: unknown) => e);
    expect(overQuota(failure)).toBe(true);
    vi.restoreAllMocks();
    const copy = await db.readCopy(user, testProject.id);
    expect(copy?.doc.seq).toBe(1);
    expect(copy?.record.version).toBe(one?.version);
    expect([...(copy?.doc.nodes.values() ?? [])]).toEqual([
      node("nd_1", "bd_a"),
    ]);

    // A first write refused leaves no copy at all.
    await db.changePin(user, "prj_other", () => pinOf(user, "prj_other"));
    puts = 0;
    vi.spyOn(IDBObjectStore.prototype, "put").mockImplementation(function (
      this: IDBObjectStore,
      ...args: Parameters<IDBObjectStore["put"]>
    ) {
      if (++puts === 2) {
        throw new DOMException(
          "The quota has been exceeded.",
          "QuotaExceededError",
        );
      }
      return put.apply(this, args);
    });
    const other = { ...recordOf(1), project_id: "prj_other" };
    await expect(db.writeCopy(other, bigger, undefined)).rejects.toThrow(
      "quota",
    );
    vi.restoreAllMocks();
    expect(await db.readCopy(user, "prj_other")).toBeUndefined();
    expect(await db.copies(user)).toHaveLength(1);
  });
});

describe("the device's pins", () => {
  // Verifies: FR-OFF-02/AC3
  it("takes a pin out with its copy once it is neither chosen nor the last opened", async () => {
    await pin({ ...pinOf(), auto: true });
    await db.writeCopy(
      recordOf(1),
      docOf(1, [board("bd_a")], [node("nd_1", "bd_a")]),
      undefined,
    );
    await db.changePin(
      user,
      testProject.id,
      (p) => p && { ...p, chosen: false },
    );
    expect(await db.readCopy(user, testProject.id)).toBeDefined();
    expect(
      await db.changePin(
        user,
        testProject.id,
        (p) => p && { ...p, auto: false },
      ),
    ).toBeUndefined();
    expect(await db.pins(user)).toEqual([]);
    expect(await db.copies(user)).toEqual([]);
    const tx = (await db.open()).transaction("entities");
    expect(await tx.store.count()).toBe(0);
  });

  it("names a pin as the project of its copy last written, as once it was renamed", async () => {
    await pin();
    await db.writeCopy(
      {
        ...recordOf(1),
        project: { ...testProject, seq: 1, name: "Lighthouse" },
      },
      docOf(1, [board("bd_a")], []),
      undefined,
    );
    expect((await db.pins(user))[0].name).toBe("Lighthouse");
  });

  it("keeps the status of a pin, and why it failed, until a write succeeds", async () => {
    await pin();
    await db.setStatus(user, testProject.id, "failed", "quota");
    expect((await db.pins(user))[0]).toMatchObject({
      status: "failed",
      failure: "quota",
    });
    await db.writeCopy(recordOf(1), docOf(1, [], []), undefined);
    const after = (await db.pins(user))[0];
    expect(after.status).toBe("ready");
    expect(after).not.toHaveProperty("failure");
  });
});

/** Returns the session that the device keeps of u, saved at savedAt. */
function sessionOf(u: string, savedAt = "2026-11-26T10:00:00Z"): KeptSession {
  return {
    user_id: u,
    session: { user_id: u, csrf_token: "", session: {} as never },
    account: {
      id: u,
      email: `${u}@example.com`,
      display_name: u,
      email_verified: true,
    },
    instance: {
      email_configured: false,
      setup_pending: false,
      signup_policy: "open",
      default_locale: "en",
    },
    saved_at: savedAt,
  };
}

describe("the device's records of each account", () => {
  it("takes out every account's records but the one kept, or every one's", async () => {
    for (const u of ["usr_jo", "usr_kim"]) {
      // The records of two accounts, as of a device of an earlier version,
      // which recorded no account's (DeviceRecord).
      await (await db.open()).delete("device", "account");
      await pin(pinOf(u));
      await db.writeCopy(
        { ...recordOf(1), user_id: u },
        docOf(1, [board("bd_a")], []),
        undefined,
      );
      await db.putSession(
        sessionOf(
          u,
          u === "usr_jo" ? "2026-11-26T10:00:00Z" : "2026-11-26T11:00:00Z",
        ),
      );
    }
    expect((await db.session())?.user_id).toBe("usr_kim");
    await db.deleteAllBut("usr_jo");
    expect(await db.pins("usr_kim")).toEqual([]);
    expect(await db.copies("usr_kim")).toEqual([]);
    expect((await db.session())?.user_id).toBe("usr_jo");
    expect(await db.readCopy("usr_jo", testProject.id)).toBeDefined();
    await db.deleteAllBut(undefined);
    expect(await db.session()).toBeUndefined();
    expect(await db.pins("usr_jo")).toEqual([]);
    const tx = (await db.open()).transaction("entities");
    expect(await tx.store.count()).toBe(0);
  });

  it("makes no pin of an account once it has taken its records out, as of a tab that has yet to find that another signed out", async () => {
    // A device that has pinned nothing has nothing written to it as pages
    // follow their sessions.
    await db.deleteAllBut(undefined);
    await db.deleteAllBut(user);
    expect(await (await db.open()).count("device")).toBe(0);
    await pin();
    await db.deleteAllBut(undefined);
    const now = "2026-11-26T12:00:00Z";
    await db.changePin(user, testProject.id, () => pinOf());
    await db.markOpened(
      user,
      "prj_other",
      () => ({ ...pinOf(user, "prj_other"), auto: true }),
      now,
    );
    expect(await db.pins(user)).toEqual([]);
    // Nor of one but the account whose records it keeps since.
    await db.deleteAllBut("usr_kim");
    await db.changePin(user, testProject.id, () => pinOf());
    expect(await db.pins(user)).toEqual([]);
    await pin(pinOf("usr_kim"));
    expect(await db.pins("usr_kim")).toHaveLength(1);
    // The account signed in again, as a tab finds, is kept again.
    await db.deleteAllBut(user);
    await pin();
    await db.markOpened(
      user,
      "prj_other",
      () => ({ ...pinOf(user, "prj_other"), auto: true }),
      now,
    );
    expect(await db.pins(user)).toHaveLength(2);
  });

  it("keeps an account's session only while the account pins a project", async () => {
    // As a write whose session follows the removal of the account's last
    // pin, in this tab or another.
    await db.putSession(sessionOf(user));
    expect(await db.session()).toBeUndefined();
    await pin();
    await db.putSession(sessionOf(user));
    expect((await db.session())?.user_id).toBe(user);
  });
});

describe("the device's database", () => {
  it("is opened again once the browser has closed it, as once the site's data was cleared", async () => {
    await pin();
    const first = await db.open();
    const closed = new Promise((resolve) =>
      first.addEventListener("close", resolve),
    );
    // fake-indexeddb types the connection that it closes as its class.
    forceCloseDatabase(
      unwrap(first) as unknown as Parameters<typeof forceCloseDatabase>[0],
    );
    await closed;
    expect(await db.pins(user)).toHaveLength(1);
    expect(await db.open()).not.toBe(first);
  });
});

describe("where a project's document finds the device's copy", () => {
  it("finds it on the device, and none where the database does not open in a while", async () => {
    await pin();
    await db.writeCopy(recordOf(1), docOf(1, [board("bd_a")], []), undefined);
    expect(await copySource(db, true).seqOf(user, testProject.id)).toBe(1);
    const copy = await copySource(db, true).readCopy(user, testProject.id);
    expect(copy?.doc.boards.has("bd_a")).toBe(true);
    expect(
      await copySource(db, false).seqOf(user, testProject.id),
    ).toBeUndefined();
    expect(
      await copySource(db, false).readCopy(user, testProject.id),
    ).toBeUndefined();
    // A browser whose open of the database never settles, which would else
    // keep the project's document from starting, though the server answers.
    const stuck = new OfflineDatabase();
    vi.spyOn(stuck, "open").mockReturnValue(new Promise(() => {}));
    expect(
      await copySource(stuck, true, 10).seqOf(user, testProject.id),
    ).toBeUndefined();
    expect(
      await copySource(stuck, true, 10).readCopy(user, testProject.id),
    ).toBeUndefined();
  });

  it("tells the copy's seq without its entities", async () => {
    await pin();
    await db.writeCopy(recordOf(1), docOf(1, [board("bd_a")], []), undefined);
    const reads = vi.spyOn(IDBObjectStore.prototype, "getAll");
    expect(await db.copySeq(user, testProject.id)).toBe(1);
    expect(await db.copySeq(user, "prj_other")).toBeUndefined();
    expect(reads).not.toHaveBeenCalled();
  });

  it("finds none of a copy of another format, as of another version of the app, which a write replaces whole", async () => {
    await pin();
    const first = docOf(1, [board("bd_a")], []);
    const one = await db.writeCopy(recordOf(1), first, undefined);
    const [record] = await db.copies(user);
    expect(record.format).toBe(copyFormat);
    // Another version's copy, of another format, with an entity of its own.
    const connection = await db.open();
    await connection.put("copies", { ...record, format: copyFormat + 1 });
    await connection.put("entities", {
      user_id: user,
      project_id: testProject.id,
      id: "nd_old",
      kind: "nodes",
      entity: node("nd_old", "bd_a"),
    });
    expect(await db.copySeq(user, testProject.id)).toBeUndefined();
    expect(await db.readCopy(user, testProject.id)).toBeUndefined();
    // A write made on what this version wrote before writes the whole
    // document in its place, rather than what changed alone.
    await db.writeCopy(recordOf(2), { ...first, seq: 2 }, one);
    expect(await db.copySeq(user, testProject.id)).toBe(2);
    const copy = await db.readCopy(user, testProject.id);
    expect([...(copy?.doc.nodes.keys() ?? [])]).toEqual([]);
  });
});

describe("the device's queue of changes", () => {
  /** Returns a batch of the queue of user's test project, of id, made at order. */
  function batch(id: string, order: number, owner = "own") {
    return {
      id,
      owner,
      maker: owner,
      atop: [],
      order,
      format: 1,
      time: "2026-11-27T10:00:00Z",
      commands: [],
      key: "[]",
      base: [],
      before: [],
      parts: [],
      retries: 3,
      sent: false,
      history: true,
      state: "pending" as const,
    };
  }

  it("keeps an account's changes, in order, until they are removed, through an ended session and another account's signing in", async () => {
    await db.saveQueued(
      user,
      testProject.id,
      [batch("b2", 2), batch("b1", 1)],
      [],
    );
    await db.saveQueued("usr_other", testProject.id, [batch("b3", 3)], []);
    expect((await db.queued(user)).map((b) => b.id)).toEqual(["b1", "b2"]);
    // Another account signs in on the device, and then the session ends.
    await db.deleteAllBut("usr_other");
    await db.deleteAllBut(undefined);
    expect((await db.queued(user)).map((b) => b.id)).toEqual(["b1", "b2"]);
    expect((await db.queued("usr_other")).map((b) => b.id)).toEqual(["b3"]);
    await db.saveQueued(user, testProject.id, [], ["b1"]);
    expect((await db.queued(user)).map((b) => b.id)).toEqual(["b2"]);
    // Signing out, once the user decided.
    await db.deleteQueued(user);
    expect(await db.queued(user)).toEqual([]);
    expect(await db.queued("usr_other")).toHaveLength(1);
  });

  it("gives a document the batches of the owners that it may take, as its own, and none of another format", async () => {
    await db.saveQueued(
      user,
      testProject.id,
      [
        batch("b1", 1, "gone"),
        batch("b2", 2, "open"),
        { ...batch("b3", 3, "gone"), format: 2 },
      ],
      [],
    );
    const taken = await db.takeQueued(
      user,
      testProject.id,
      "me",
      (owner) => owner === "gone",
    );
    expect(taken.map((b) => [b.id, b.owner])).toEqual([["b1", "me"]]);
    expect(
      (await db.queued(user, testProject.id)).map((b) => [b.id, b.owner]),
    ).toEqual([
      ["b1", "me"],
      ["b2", "open"],
      ["b3", "gone"],
    ]);
  });
});
