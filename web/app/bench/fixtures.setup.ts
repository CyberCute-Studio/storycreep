// What the canvas's benchmark measures (PRD §9.1, NFR-Q-06): the medium,
// dense, and large fixtures, which cmd/fixturegen loads through the API,
// with an API token, as it loads them for manual testing (plan note N52),
// and boards of 200, 2,000, and 5,000 nodes, whose nodes follow their
// places in their grids, so that the boards of 200 and of 2,000 show the
// same nodes across the grid that they share (FR-EDIT-11 AC1). It runs once,
// before the runs, which start signed in as its account and read what it
// made from a file. Against an instance of the reference tablet's checklist
// (STORYCREEP_BENCH_BASE_URL, docs/canvas-checklist.md), it signs in as the
// account that the checklist names, and writes the addresses of the runs
// that the tablet opens to bench-results/tablet.md.

import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { makeBoard, makeProject } from "../e2e/boards.ts";
import { firstWorkspace } from "../e2e/content.ts";
import { binaries, binary, serverModule } from "../e2e/environment.ts";
import { apiSignIn, expect, registerAccount, test } from "../e2e/fixtures.ts";
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
    const boards: BenchBoards = { medium, dense, large, b200, b2000, b5000 };
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
