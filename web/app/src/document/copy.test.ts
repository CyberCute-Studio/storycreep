import {
  type Board,
  type Folder,
  NetworkError,
  type Node,
} from "@storycreep/shared/api";
import { afterEach, describe, expect, it, onTestFinished, vi } from "vitest";
import { createQueryClient, sessionQuery } from "../api";
import { createErrorLog } from "../errors/log";
import { testSession } from "../test/app";
import { fakeApi, testProject } from "../test/fake";
import { fakeRealtime } from "../test/realtime";
import { boardPart, type Doc, emptyDoc, projectOf } from "./doc";
import { ProjectDocument, type StartingCopy } from "./store";

// A project's document that starts from the copy of the project that the
// device keeps (PRD FR-OFF-02, plan note N97): it reads none of the parts
// that the copy holds, and follows the project from the copy's seq, which
// the channel brings it up from, or the project read anew where the
// channel is not live, and passes over a copy of a seq past the server's.
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

/** Serves the test project as of seq, with the board of the copy renamed, and returns what it was asked. */
function serve(seq: number, project = () => ({ ...testProject, seq })) {
  const calls: string[] = [];
  let failing = false;
  const fail = () => {
    failing = true;
  };
  fakeApi((operation) => {
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
  return { calls, fail };
}

function open(
  copy: StartingCopy | undefined,
  options: { online?: () => boolean } = {},
) {
  const log = createErrorLog();
  const queryClient = createQueryClient(log);
  queryClient.setQueryData(sessionQuery.queryKey, testSession);
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
    expect(doc.copy?.version).toBe("v1");
    expect(doc.hasRead("tree")).toBe(true);
    expect(doc.doc.nodes.get("nd_1")?.board_id).toBe("bd_a");
    expect(doc.settled).toBe(true);
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

  it("is brought up to the server's seq by the channel", async () => {
    const { calls } = serve(4);
    const { doc, realtime } = open(copyOf(3));
    await doc.ready;
    expect(doc.doc.seq).toBe(3);
    realtime.hold(testProject.id, 4);
    realtime.send(
      testProject.id,
      [{ type: "board.rename", payload: { id: "bd_a", name: "Later" } }],
      [{ id: "bd_a", rev: 2 }],
    );
    expect(doc.doc.seq).toBe(4);
    expect(doc.doc.boards.get("bd_a")?.name).toBe("Later");
    // A page that shows the board, which reads it anew where the document
    // had it already, waits for the channel, which has brought what changed.
    await doc.refresh(boardPart("bd_a"));
    expect(calls).toEqual(["getProject"]);
  });

  it("reads the project anew where the channel does not bring it up to the server's seq", async () => {
    vi.useFakeTimers();
    const { calls } = serve(5);
    const { doc } = open(copyOf(3));
    await vi.advanceTimersByTimeAsync(0);
    await doc.ready;
    expect(doc.doc.seq).toBe(3);
    await vi.advanceTimersByTimeAsync(10_000);
    await vi.waitFor(() => expect(doc.doc.seq).toBe(5));
    expect(doc.doc.boards.get("bd_a")?.name).toBe("Server's");
    expect(calls).toContain("listBoards");
    expect(calls).toContain("listBoardNodes");
  });

  it("passes over a copy of a seq past the server's, as of a database restored from an older backup", async () => {
    const { calls } = serve(2);
    const { doc } = open(copyOf(3));
    await doc.ready;
    expect(doc.copy).toBeUndefined();
    expect(doc.doc.seq).toBe(2);
    expect(doc.hasRead("tree")).toBe(false);
    await doc.load("tree");
    expect(calls).toContain("listBoards");
    expect(doc.doc.boards.get("bd_a")?.name).toBe("Server's");
  });

  it("reads the parts from the server where the device keeps no copy", async () => {
    const { calls } = serve(3);
    const { doc } = open(undefined);
    await doc.load("tree");
    expect(doc.copy).toBeUndefined();
    expect(calls).toEqual(["getProject", "listFolders", "listBoards"]);
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
