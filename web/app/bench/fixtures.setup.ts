// What the canvas's benchmark measures (PRD §9.1, NFR-Q-06): the medium,
// dense, and large fixtures, which cmd/fixturegen loads through the API,
// with an API token, as it loads them for manual testing (plan note N52),
// boards of 200, 2,000, and 5,000 nodes, whose nodes follow their places in
// their grids, so that the boards of 200 and of 2,000 show the same nodes
// across the grid that they share (FR-EDIT-11 AC1), and a project of 60
// boards that depend on one another, whose dependency view a run draws
// (NFR-PERF-11, plan note N77). It runs once,
// before the runs, which start signed in as its account and read what it
// made from a file. Against an instance of the reference tablet's checklist
// (STORYCREEP_BENCH_BASE_URL, docs/canvas-checklist.md), it signs in as the
// account that the checklist names, and writes the addresses of the runs
// that the tablet opens to bench-results/tablet.md.

import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { APIRequestContext } from "@playwright/test";
import { newId } from "@storycreep/shared/ids";
import { makeBoard, makeProject } from "../e2e/boards.ts";
import { apply, type Command, firstWorkspace } from "../e2e/content.ts";
import { binaries, binary, serverModule } from "../e2e/environment.ts";
import {
  type ApiSession,
  apiSignIn,
  expect,
  registerAccount,
  test,
} from "../e2e/fixtures.ts";
import {
  type BenchBoard,
  type BenchBoards,
  benchBase,
  boardsFile,
  remote,
  resultsDir,
  signedInState,
} from "./state.ts";

/** The first of the three seeds of the fixtures, which an instance loads once each, of any size (plan note N52). */
const seed = Number(process.env.STORYCREEP_BENCH_SEED ?? 1);

test("loads the fixtures and the boards that the runs measure", async ({
  request,
}) => {
  const session = remote
    ? await apiSignIn(
        request,
        process.env.STORYCREEP_BENCH_EMAIL ?? "",
        process.env.STORYCREEP_BENCH_PASSWORD ?? "",
      )
    : await apiSignIn(
        request,
        (await registerAccount(request, "Bea Bench")).email,
      );
  const workspace = await firstWorkspace(request);
  const created = await request.post(
    `/api/v1/workspaces/${workspace.id}/tokens`,
    {
      headers: { "X-CSRF-Token": session.csrf },
      data: { name: "The canvas's benchmark", scopes: ["project:write"] },
    },
  );
  expect(created.status(), await created.text()).toBe(201);
  const token = (await created.json()) as { id: string; secret: string };
  const dir = mkdtempSync(join(tmpdir(), "storycreep-bench-"));
  const tokenFile = join(dir, "token");
  writeFileSync(tokenFile, `${token.secret}\n`, { mode: 0o600 });
  try {
    /** Loads the fixture of size, of the seed after the first by, into a project of its own, and returns its first board. */
    const fixture = async (
      size: "medium" | "dense" | "large",
      by: number,
    ): Promise<BenchBoard> => {
      const projectId = await makeProject(request, session, `Fixture ${size}`);
      const started = Date.now();
      const args = [
        "-size",
        size,
        "-seed",
        String(seed + by),
        "-server",
        benchBase,
        "-project",
        projectId,
        "-token-file",
        tokenFile,
      ];
      // The binary that the end-to-end tests' server built, or, against
      // another instance, the command itself.
      const run = remote
        ? spawnSync("go", ["run", "./cmd/fixturegen", ...args], {
            cwd: serverModule,
            stdio: "inherit",
            timeout: 25 * 60_000,
          })
        : spawnSync(binary("fixturegen"), args, {
            stdio: "inherit",
            timeout: 25 * 60_000,
          });
      expect(run.status, `fixturegen -size ${size}`).toBe(0);
      console.log(
        `The ${size} fixture loaded in ${Math.round((Date.now() - started) / 1000)} s.`,
      );
      const res = await request.get(
        `/api/v1/projects/${projectId}/boards?limit=1`,
      );
      expect(res.status(), await res.text()).toBe(200);
      const { items } = (await res.json()) as { items: { id: string }[] };
      expect(items.length).toBeGreaterThan(0);
      return { projectId, boardId: items[0].id };
    };
    const medium = await fixture("medium", 0);
    const dense = await fixture("dense", 1);
    const large = await fixture("large", 2);
    const projectId = await makeProject(
      request,
      session,
      "Boards of FR-EDIT-11",
    );
    const of = async (
      count: number,
      grid?: { cols: number; near: number },
    ): Promise<BenchBoard> => ({
      projectId,
      boardId: (
        await makeBoard(
          request,
          session,
          projectId,
          `${count} nodes`,
          count,
          grid,
        )
      ).boardId,
    });
    // FR-EDIT-11 AC1 compares a board of 200 nodes, a grid of 20 columns
    // and 10 rows, with one of 2,000, of the same grid and 1,800 more far
    // from it, across the grid's area, where both show the same nodes.
    const grid = { cols: 20, near: 200 };
    const area = "0,0,6320,1928";
    const b200 = { ...(await of(200, grid)), area };
    const b2000 = { ...(await of(2000, grid)), area };
    // FR-EDIT-11 AC2 pans the board of 5,000 nodes, across 500 of them,
    // 25 columns and 20 rows, again and again, so that once a pass has
    // filled PixiJS's caches of text, which are bounded, a heap that grows
    // is a leak's.
    const b5000 = { ...(await of(5000)), area: "0,0,7920,3928" };
    const deps = await dependencyProject(request, session);
    const boards: BenchBoards = {
      medium,
      dense,
      large,
      b200,
      b2000,
      b5000,
      deps,
    };
    mkdirSync(binaries, { recursive: true });
    writeFileSync(boardsFile, JSON.stringify(boards));
    await request.storageState({ path: signedInState });
    // The results' summary, which each run adds a row to, and the runs that
    // the reference tablet opens.
    rmSync(resultsDir, { recursive: true, force: true });
    mkdirSync(resultsDir, { recursive: true });
    writeFileSync(
      new URL("summary.md", resultsDir),
      [
        "## The canvas's benchmark",
        "",
        "The runner is neither reference device: its frame rates are its own, which the reference tablet's checklist holds to their targets (docs/canvas-checklist.md).",
        "",
        "| Target | Profile | Board | Renderer | Result |",
        "| --- | --- | --- | --- | --- |",
        "",
      ].join("\n"),
    );
    const url = (b: BenchBoard, query: string) =>
      `${benchBase}/app/projects/${b.projectId}/boards/${b.boardId}?${query}${b.area === undefined ? "" : `&area=${b.area}`}`;
    writeFileSync(
      new URL("tablet.md", resultsDir),
      [
        "# The reference tablet's runs",
        "",
        `- NFR-PERF-04, medium: ${url(medium, "bench=load")}`,
        `- NFR-PERF-01, medium: ${url(medium, "bench=panzoom&seconds=60")}`,
        `- FR-EDIT-01 AC3, dense: ${url(dense, "bench=panzoom&seconds=60")}`,
        `- FR-EDIT-11 AC1, 200 nodes: ${url(b200, "bench=pan&seconds=30")}`,
        `- FR-EDIT-11 AC1, 2,000 nodes: ${url(b2000, "bench=pan&seconds=30")}`,
        `- FR-EDIT-11 AC2, 5,000 nodes: ${url(b5000, "bench=pan&seconds=300&warm=pass")}`,
        `- NFR-PERF-18, medium: ${url(medium, "bench=pan&seconds=300&warm=pass")}`,
        `- NFR-PERF-06, medium: ${url(medium, "bench=edit&seconds=30")}`,
        `- NFR-PERF-05, large, on the reference desktop: ${url(large, "bench=load")}`,
        `- NFR-PERF-11, 60 boards: ${benchBase}/app/projects/${deps.projectId}/dependencies?bench=load`,
        "",
      ].join("\n"),
    );
  } finally {
    rmSync(dir, { recursive: true, force: true });
    // The token goes once the fixtures have loaded.
    await request.delete(
      `/api/v1/workspaces/${workspace.id}/tokens/${token.id}`,
      { headers: { "X-CSRF-Token": session.csrf } },
    );
  }
});

/** How many boards the project of the dependency view's run has (NFR-PERF-11). */
const dependencyBoards = 60;

/**
 * Makes a project of dependencyBoards boards that depend on one another, as
 * the dependency view's run draws it (NFR-PERF-11), and returns it with its
 * first board. Board i has a scene that assigns the variable v_i, whose
 * choice reads v of the board before it, in a ring of every board, a
 * cycle, and v of another board; every third board has a jumper to the
 * scene of the board 13 after it, and every fifth a choice that reads
 * whether the scene of the board 29 after it was visited: some 150
 * dependencies, of every kind, between pairs of boards.
 */
async function dependencyProject(
  request: APIRequestContext,
  session: ApiSession,
): Promise<BenchBoard> {
  const projectId = await makeProject(
    request,
    session,
    "Dependencies of NFR-PERF-11",
  );
  const n = dependencyBoards;
  const boards = Array.from({ length: n }, () => newId("board"));
  const scenes = Array.from({ length: n }, () => newId("node"));
  const commands: Command[] = [];
  for (let i = 0; i < n; i++) {
    commands.push(
      {
        type: "board.create",
        payload: { id: boards[i], name: `Chapter ${i + 1}` },
      },
      {
        type: "variable.create",
        payload: {
          id: newId("variable"),
          key: `v_${i}`,
          type: "bool",
          scope: "global",
        },
      },
    );
  }
  for (let i = 0; i < n; i++) {
    const end = newId("node");
    const reads = [
      `v_${(i + n - 1) % n}`,
      `v_${(i * 7 + 3) % n}`,
      ...(i % 5 === 0 ? [`visited(node("${scenes[(i + 29) % n]}"))`] : []),
    ];
    commands.push(
      {
        type: "node.create",
        payload: {
          id: scenes[i],
          board_id: boards[i],
          type: "element",
          kind: "narration",
          title: `Scene ${i + 1}`,
          position: { x: 0, y: 0 },
          on_exit: `v_${i} = true`,
        },
      },
      {
        type: "node.create",
        payload: {
          id: end,
          board_id: boards[i],
          type: "element",
          kind: "narration",
          title: `End ${i + 1}`,
          position: { x: 400, y: 0 },
          is_ending: true,
        },
      },
      {
        type: "connection.create",
        payload: {
          id: newId("connection"),
          from_node_id: scenes[i],
          to_node_id: end,
          condition: reads.join(" && "),
        },
      },
      {
        type: "board.set_entry_node",
        payload: { id: boards[i], node_id: scenes[i] },
      },
    );
    if (i % 3 === 0) {
      const jumper = newId("node");
      const j = (i + 13) % n;
      commands.push(
        {
          type: "node.create",
          payload: {
            id: jumper,
            board_id: boards[i],
            type: "jumper",
            title: `On to ${j + 1}`,
            position: { x: 0, y: 300 },
          },
        },
        {
          type: "node.update",
          payload: {
            id: jumper,
            target: { board_id: boards[j], node_id: scenes[j] },
          },
        },
      );
    }
  }
  await apply(request, session, projectId, commands);
  return { projectId, boardId: boards[0] };
}
