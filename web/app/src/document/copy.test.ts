import {
  type Board,
  type Folder,
  NetworkError,
  type Node,
} from "@storycreep/shared/api";
import { afterEach, describe, expect, it, onTestFinished, vi } from "vitest";
import { createQueryClient, sessionQuery } from "../api";
import { createErrorLog } from "../errors/log";
import { projectQuery } from "../project/queries";
import { testSession } from "../test/app";
import { fakeApi, testProject } from "../test/fake";
import { fakeRealtime } from "../test/realtime";
import { boardPart, type Doc, emptyDoc, projectOf } from "./doc";
import { ProjectDocument, type StartingCopy } from "./store";

// A project's document that starts from the copy of the project that the
// device keeps (PRD FR-OFF-02, plan note N97): it reads none of the parts
// that the copy holds, and follows the project from the copy's seq, which
// the channel brings it up from: a copy as of the project's seq, or, while
// the browser has no connection, of one before; online, it passes over a
// copy behind the project's seq, and one of a seq past the server's.
// Offline, it reads nothing anew.

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

const stamps = {
  created_at: "2026-11-01T10:00:00Z",
  updated_at: "2026-11-01T10:00:00Z",
};

function board(id: string, name = id, rev = 1): Board {
  return {
    id,
    project_id: testProject.id,
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
    project_id: testProject.id,
    kind: "element",
    title: id,
    rev: 1,
    ...stamps,
  } as unknown as Node;
}

/** Returns the device's copy of the test project, as of seq, holding the tree with a board and its content. */
function copyOf(seq: number): StartingCopy {
  const doc: Doc = {
    ...emptyDoc(seq),
    project: projectOf(testProject),
    boards: new Map([["bd_a", board("bd_a")]]),
    nodes: new Map([["nd_1", node("nd_1", "bd_a")]]),
    parts: new Set(["tree", boardPart("bd_a")]),
  };
  return { doc, version: "v1", bytes: 100 };
}

const page = <T>(items: T[]) => ({ items, next_cursor: null });

/** Serves the test project as of seq, with the board of the copy renamed, and returns what it answered, and what it was asked, which once fail is called gets no answer. */
function serve(seq: number, project = () => ({ ...testProject, seq })) {
  const calls: string[] = [];
  const asked: string[] = [];
  let failing = false;
  const fail = () => {
    failing = true;
  };
  fakeApi((operation) => {
    asked.push(operation);
    if (failing) {
      throw new NetworkError(new TypeError("Failed to fetch"));
    }
    calls.push(operation);
    switch (operation) {
      case "getProject":
        return project();
      case "listFolders":
        return page<Folder>([]);
      case "listBoards":
        return page([board("bd_a", "Server's", 2)]);
      case "listBoardNodes":
        return page([node("nd_1", "bd_a")]);
      case "listBoardConnections":
        return page([]);
    }
    throw new Error(`unexpected ${operation}`);
  });
  return { calls, asked, fail };
}

function open(
  copy: StartingCopy | undefined,
  options: { online?: () => boolean; cached?: number } = {},
) {
  const log = createErrorLog();
  const queryClient = createQueryClient(log);
  queryClient.setQueryData(sessionQuery.queryKey, testSession);
  // The project as the query client kept it from an earlier read, at the
  // seq that it had then.
  if (options.cached !== undefined) {
    queryClient.setQueryData(projectQuery(testProject.id).queryKey, {
      ...testProject,
      seq: options.cached,
    });
  }
  const realtime = fakeRealtime();
  const readCopy = vi.fn(async () => copy);
  const doc = new ProjectDocument(testProject.id, {
    queryClient,
    realtime,
    log,
    copies: { readCopy },
    online: options.online,
  });
  onTestFinished(() => doc.dispose());
  return { doc, realtime, readCopy, log };
}

describe("a document that starts from the device's copy", () => {
  // Verifies: FR-OFF-02/AC1
  it("reads none of the parts that the copy holds, and follows the project from its seq", async () => {
    const { calls } = serve(3);
    const { doc, realtime, readCopy } = open(copyOf(3));
    // A part that a page asks for before the copy has been read is the copy's.
    const tree = doc.load("tree");
    await doc.ready;
    await tree;
    await doc.load(boardPart("bd_a"));
    expect(readCopy).toHaveBeenCalledWith(testSession.user_id, testProject.id);
    expect(calls).toEqual(["getProject"]);
    expect(doc.fromCopy).toBe(true);
    expect(doc.hasRead("tree")).toBe(true);
    expect(doc.doc.nodes.get("nd_1")?.board_id).toBe("bd_a");
    expect(doc.settled).toBe(true);
    // The copy is the keeper's to make its first write on, once.
    expect(doc.takeCopy()?.version).toBe("v1");
    expect(doc.takeCopy()).toBeUndefined();
    expect(doc.fromCopy).toBe(true);
    realtime.hold(testProject.id);
    realtime.send(
      testProject.id,
      [{ type: "board.rename", payload: { id: "bd_a", name: "Renamed" } }],
      [{ id: "bd_a", rev: 2 }],
    );
    expect(doc.doc.seq).toBe(4);
    expect(doc.doc.boards.get("bd_a")?.name).toBe("Renamed");
    expect(calls).toEqual(["getProject"]);
  });

  it("is passed over while the browser has a connection where its seq is behind the project's, which the document reads from the server", async () => {
    vi.useFakeTimers();
    const { calls } = serve(5);
    const { doc } = open(copyOf(3));
    await vi.advanceTimersByTimeAsync(0);
    await doc.ready;
    expect(doc.fromCopy).toBe(false);
    expect(doc.doc.seq).toBe(5);
    expect(doc.hasRead("tree")).toBe(false);
    await doc.load("tree");
    expect(doc.doc.boards.get("bd_a")?.name).toBe("Server's");
    // Nothing reads the project anew after, as the channel is not live.
    const before = calls.length;
    await vi.advanceTimersByTimeAsync(20_000);
    expect(calls).toHaveLength(before);
  });

  it("starts from a copy behind the project's seq while the browser has no connection, which the channel brings up once it has", async () => {
    vi.useFakeTimers();
    let connected = false;
    const { calls, asked, fail } = serve(4);
    fail();
    // The project as the page read it before the connection went.
    const { doc, realtime } = open(copyOf(3), {
      online: () => connected,
      cached: 4,
    });
    await vi.advanceTimersByTimeAsync(0);
    await doc.ready;
    expect(doc.fromCopy).toBe(true);
    expect(doc.doc.seq).toBe(3);
    // Offline, nothing reads the project anew, however long.
    await vi.advanceTimersByTimeAsync(20_000);
    expect(asked).toEqual([]);
    connected = true;
    realtime.hold(testProject.id, 4);
    realtime.send(
      testProject.id,
      [{ type: "board.rename", payload: { id: "bd_a", name: "Later" } }],
      [{ id: "bd_a", rev: 2 }],
    );
    expect(doc.doc.seq).toBe(4);
    expect(doc.doc.boards.get("bd_a")?.name).toBe("Later");
    expect(doc.settled).toBe(true);
    expect(asked).toEqual([]);
    expect(calls).toEqual([]);
  });

  it("waits for the channel once, rather than at each reading anew, where it is never live", async () => {
    vi.useFakeTimers();
    const { calls } = serve(3);
    const { doc } = open(copyOf(3));
    await vi.advanceTimersByTimeAsync(0);
    await doc.ready;
    let first = false;
    void doc.refresh("tree").then(() => {
      first = true;
    });
    await vi.advanceTimersByTimeAsync(4_000);
    expect(first).toBe(false);
    await vi.advanceTimersByTimeAsync(1_500);
    expect(first).toBe(true);
    expect(calls).toContain("listBoards");
    // As behind a proxy that refuses the socket: the next reads at once.
    const before = calls.length;
    let second = false;
    void doc.refresh("tree").then(() => {
      second = true;
    });
    await vi.advanceTimersByTimeAsync(10);
    expect(second).toBe(true);
    expect(calls.length).toBeGreaterThan(before);
  });

  it("passes over a copy of a seq past the server's, as of a database restored from an older backup", async () => {
    const { calls } = serve(2);
    const { doc } = open(copyOf(3));
    await doc.ready;
    expect(doc.fromCopy).toBe(false);
    expect(doc.doc.seq).toBe(2);
    expect(doc.hasRead("tree")).toBe(false);
    await doc.load("tree");
    expect(calls).toContain("listBoards");
    expect(doc.doc.boards.get("bd_a")?.name).toBe("Server's");
  });

  it("reads the project anew, rather than pass the copy over, where the query client's read of it is older than the copy", async () => {
    // As a read kept from before another tab wrote the copy.
    const { calls } = serve(3);
    const { doc } = open(copyOf(3), { cached: 2 });
    await doc.ready;
    expect(doc.fromCopy).toBe(true);
    expect(doc.hasRead("tree")).toBe(true);
    expect(calls).toEqual(["getProject"]);
  });

  it("reads the parts from the server where the device keeps no copy", async () => {
    const { calls } = serve(3);
    const { doc } = open(undefined);
    await doc.load("tree");
    expect(doc.fromCopy).toBe(false);
    expect(calls).toEqual(["getProject", "listFolders", "listBoards"]);
  });

  it("lets go, as it reads the project anew, of a board of the copy's that no page asked for, and reads anew one that a page did", async () => {
    vi.useFakeTimers();
    const { calls } = serve(3);
    const { doc } = open(copyOf(3));
    await vi.advanceTimersByTimeAsync(0);
    await doc.ready;
    expect(doc.hasRead(boardPart("bd_a"))).toBe(true);
    // A reading anew of the whole project, as while the channel is not live,
    // once the wait for it is over.
    const refreshed = doc.refresh();
    await vi.advanceTimersByTimeAsync(6_000);
    await refreshed;
    expect(calls).toContain("listBoards");
    expect(calls).not.toContain("listBoardNodes");
    expect(doc.hasRead(boardPart("bd_a"))).toBe(false);
    expect(doc.doc.nodes.size).toBe(0);
    // A board that a page asked for is read anew with the project.
    await doc.load(boardPart("bd_a"));
    const read = calls.filter((c) => c === "listBoardNodes").length;
    await doc.refresh();
    expect(calls.filter((c) => c === "listBoardNodes")).toHaveLength(read + 1);
    expect(doc.hasRead(boardPart("bd_a"))).toBe(true);
    expect(doc.doc.nodes.get("nd_1")?.board_id).toBe("bd_a");
  });
});

describe("a document that holds a board for the device's copy alone", () => {
  it("lets it go as it reads the project anew, rather than read it again at once, until a page asks for it too", async () => {
    const { calls } = serve(3);
    const { doc } = open(undefined);
    const nodes = () => calls.filter((c) => c === "listBoardNodes").length;
    await doc.load("tree");
    // As the keeper's download reads it.
    await doc.load(boardPart("bd_a"), true);
    expect(nodes()).toBe(1);
    await doc.refresh();
    expect(nodes()).toBe(1);
    expect(doc.hasRead(boardPart("bd_a"))).toBe(false);
    expect(doc.doc.nodes.size).toBe(0);
    // Read again by the download, and asked for by a page.
    await doc.load(boardPart("bd_a"), true);
    await doc.load(boardPart("bd_a"));
    expect(nodes()).toBe(2);
    await doc.refresh();
    expect(nodes()).toBe(3);
    expect(doc.hasRead(boardPart("bd_a"))).toBe(true);
  });
});

describe("a document that catches up on the channel", () => {
  it("is not settled until it has the operations up to the seq that its subscription held as of", async () => {
    serve(3);
    const { doc, realtime } = open(undefined);
    await doc.load("tree");
    expect(doc.settled).toBe(true);
    // The server has two operations more, which the channel sends after.
    realtime.hold(testProject.id, 5);
    expect(doc.settled).toBe(false);
    realtime.send(
      testProject.id,
      [{ type: "board.rename", payload: { id: "bd_a", name: "One" } }],
      [{ id: "bd_a", rev: 3 }],
    );
    expect(doc.doc.seq).toBe(4);
    expect(doc.settled).toBe(false);
    realtime.send(
      testProject.id,
      [{ type: "board.rename", payload: { id: "bd_a", name: "Two" } }],
      [{ id: "bd_a", rev: 4 }],
    );
    expect(doc.doc.seq).toBe(5);
    expect(doc.settled).toBe(true);
  });
});

describe("a document while the browser has no connection", () => {
  it("reads nothing anew, and records no request that got no answer", async () => {
    let connected = true;
    const { calls, fail } = serve(3);
    const { doc, log } = open(copyOf(3), { online: () => connected });
    await doc.ready;
    connected = false;
    fail();
    await doc.refresh(boardPart("bd_a"));
    await doc.refresh();
    expect(calls).toEqual(["getProject"]);
    await expect(doc.load(boardPart("bd_b"))).rejects.toThrow(NetworkError);
    expect(log.entries()).toEqual([]);
  });
});
