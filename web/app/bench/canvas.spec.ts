import { appendFileSync, mkdirSync, writeFileSync } from "node:fs";
import type { CDPSession, Page, TestInfo } from "@playwright/test";
import { test } from "../e2e/boards.ts";
import { expect } from "../e2e/fixtures.ts";
import type { BenchResult } from "../src/canvas/bench.tsx";
import { type BenchBoard, benchBoards, resultsDir } from "./state.ts";

// The canvas's benchmark harness (PRD §9.1, plan item 59): scripted runs of
// a board's canvas (src/canvas/bench.tsx), in a desktop browser and on an
// emulated tablet, whose processor is four times slower than the runner's
// and whose network is a LAN's. The runner is not the reference tablet nor
// the reference desktop, and draws WebGL in software, so the runs report
// the frame rates of NFR-PERF-01, NFR-PERF-02, and FR-EDIT-01 AC3, and the
// tablet's load time of NFR-PERF-04, which the reference tablet's checklist
// holds to their targets (docs/canvas-checklist.md); they hold the runner
// to what it can tell: that drawing is bounded by what the canvas shows
// (FR-EDIT-11 AC1), that panning does not grow the heap (FR-EDIT-11 AC2,
// NFR-PERF-18), and the desktop's load time of the large fixture (NFR-PERF-05).
// Each run's results go to bench-results/, which CI's job publishes.

/** How long a run that pans for memory pans, after its warm-up, in seconds: five minutes on the tablet's checklist. */
const panSeconds = Number(process.env.STORYCREEP_BENCH_PAN_SECONDS ?? 60);

/** How long a run that measures frame rates pans and zooms, after its warm-up, in seconds. */
const frameSeconds = Number(process.env.STORYCREEP_BENCH_FRAME_SECONDS ?? 20);

/** The emulated tablet's slowdown of the runner's processor. */
const tabletSlowdown = 4;

function tablet(info: TestInfo): boolean {
  return info.project.name === "tablet";
}

/** Emulates the tablet's processor and LAN on page, on the tablet's runs. */
async function emulate(page: Page, info: TestInfo): Promise<CDPSession> {
  const cdp = await page.context().newCDPSession(page);
  if (tablet(info)) {
    await cdp.send("Emulation.setCPUThrottlingRate", { rate: tabletSlowdown });
    await cdp.send("Network.enable");
    await cdp.send("Network.emulateNetworkConditions", {
      offline: false,
      latency: 2,
      downloadThroughput: (100 * 1e6) / 8,
      uploadThroughput: (100 * 1e6) / 8,
    });
  }
  return cdp;
}

/** Opens board with the benchmark's run of query, and returns the page's locator of the run. */
async function start(
  page: Page,
  board: BenchBoard,
  query: Record<string, string>,
) {
  const search = new URLSearchParams({
    renderer: "webgl",
    ...(board.area === undefined ? {} : { area: board.area }),
    ...query,
  });
  await page.goto(
    `/app/projects/${board.projectId}/boards/${board.boardId}?${search}`,
  );
  return page.locator("[data-bench-phase]");
}

/** Waits for the run of bench to end, and returns what it measured. */
async function result(
  bench: ReturnType<Page["locator"]>,
  seconds: number,
): Promise<BenchResult> {
  await expect(bench).toHaveAttribute("data-bench-phase", "done", {
    timeout: (seconds + 180) * 1000,
  });
  return JSON.parse(
    (await bench.getAttribute("data-bench-result")) ?? "{}",
  ) as BenchResult;
}

/** Returns the JavaScript heap's size in MiB, once the garbage is collected. */
async function heap(cdp: CDPSession): Promise<number> {
  await cdp.send("HeapProfiler.collectGarbage");
  await cdp.send("HeapProfiler.collectGarbage");
  const { usedSize } = await cdp.send("Runtime.getHeapUsage");
  return usedSize / 2 ** 20;
}

const ms = (v: number | undefined) =>
  v === undefined ? "—" : `${v.toFixed(1)} ms`;

/** Writes what a run measured to the results, and to the test's report. */
async function report(
  info: TestInfo,
  target: string,
  board: string,
  r: BenchResult,
  line: string,
  extra: Record<string, unknown> = {},
): Promise<void> {
  const profile = tablet(info)
    ? `tablet (CPU ${tabletSlowdown}× slower, LAN)`
    : "desktop";
  const row = `| ${target} | ${profile} | ${board} | ${r.renderer ?? "—"} | ${line} |`;
  mkdirSync(resultsDir, { recursive: true });
  const summary = new URL("summary.md", resultsDir);
  appendFileSync(summary, `${row}\n`);
  const name = `${target}-${info.project.name}-${board}`.replace(
    /[^\w.-]+/g,
    "_",
  );
  writeFileSync(
    new URL(`${name}.json`, resultsDir),
    JSON.stringify({ target, profile, board, ...r, ...extra }, null, 2),
  );
  info.annotations.push({
    type: target,
    description: `${profile}, ${board}: ${line}`,
  });
  await info.attach(`${name}.json`, {
    body: JSON.stringify({ ...r, ...extra }, null, 2),
    contentType: "application/json",
  });
  console.log(`${target}, ${profile}, ${board}: ${line}`);
}

/** The frames of a run, as a line of the results. */
function frames(r: BenchResult): string {
  return `${r.fps.toFixed(1)} fps, frame p95 ${ms(r.frame.p95)}, work p50 ${ms(r.work.p50)}, p95 ${ms(r.work.p95)}, ${r.visible.mean.toFixed(0)} nodes in view`;
}

test.describe("the canvas's benchmark", () => {
  // Verifies: NFR-PERF-04, NFR-PERF-05
  test("the first frame of a board, from the start of the page's load", async ({
    page,
  }, info) => {
    const boards = benchBoards();
    await emulate(page, info);
    // The tablet opens the medium fixture over its LAN (NFR-PERF-04), and
    // the desktop the large one, which reads the board that it opens alone
    // (NFR-PERF-05).
    const [target, name, board, limit] = tablet(info)
      ? (["NFR-PERF-04", "medium", boards.medium, 3000] as const)
      : (["NFR-PERF-05", "large", boards.large, 6000] as const);
    const r = await result(await start(page, board, { bench: "load" }), 0);
    expect(r.rendered).toBeDefined();
    await report(
      info,
      target,
      name,
      r,
      `first frame ${ms(r.rendered)} (target < ${limit} ms)`,
    );
    if (!tablet(info)) {
      expect(r.rendered ?? Number.POSITIVE_INFINITY).toBeLessThan(limit);
    }
  });

  // Verifies: NFR-PERF-01, NFR-PERF-02, FR-EDIT-01/AC3
  test("frame rates of panning and zooming the medium and dense fixtures", async ({
    page,
  }, info) => {
    const boards = benchBoards();
    await emulate(page, info);
    const runs = tablet(info)
      ? ([
          ["NFR-PERF-01", "medium", boards.medium],
          ["FR-EDIT-01/AC3", "dense", boards.dense],
        ] as const)
      : ([["NFR-PERF-02", "medium", boards.medium]] as const);
    for (const [target, name, board] of runs) {
      const r = await result(
        await start(page, board, {
          bench: "panzoom",
          seconds: String(frameSeconds),
        }),
        frameSeconds,
      );
      expect(r.frames).toBeGreaterThan(0);
      await report(info, target, name, r, frames(r));
    }
  });

  // Verifies: FR-EDIT-11/AC1
  test("frame time of a board of 200 nodes and of one of 2,000 at the same views", async ({
    page,
  }, info) => {
    test.skip(!tablet(info), "the reference tablet's");
    const boards = benchBoards();
    await emulate(page, info);
    const seconds = Math.max(10, frameSeconds);
    const of = async (board: BenchBoard) =>
      result(
        await start(page, board, { bench: "pan", seconds: String(seconds) }),
        seconds,
      );
    const small = await of(boards.b200);
    const large = await of(boards.b2000);
    const ratio = large.work.mean / Math.max(small.work.mean, 1e-6);
    await report(info, "FR-EDIT-11/AC1", "200 nodes", small, frames(small));
    await report(
      info,
      "FR-EDIT-11/AC1",
      "2,000 nodes",
      large,
      `${frames(large)}; work ${(ratio * 100).toFixed(0)}% of the 200 nodes' (target ≤ 120%)`,
      { ratio },
    );
    // Both show the same nodes.
    expect(large.visible.mean).toBeCloseTo(small.visible.mean, 0);
    // Within 20%, or within a quarter of a millisecond, which the runner's
    // timer and its other work cannot tell apart.
    if (large.work.mean - small.work.mean > 0.25) {
      expect(ratio).toBeLessThanOrEqual(1.2);
    }
  });

  // Verifies: FR-EDIT-11/AC2, NFR-PERF-18
  test("memory while panning for a while", async ({ page }, info) => {
    const boards = benchBoards();
    const cdp = await emulate(page, info);
    // The desktop pans the board of 5,000 nodes (FR-EDIT-11 AC2), and the
    // tablet the medium fixture (NFR-PERF-18).
    const [target, name, board] = tablet(info)
      ? (["NFR-PERF-18", "medium", boards.medium] as const)
      : (["FR-EDIT-11/AC2", "5,000 nodes", boards.b5000] as const);
    // It warms up for a pass of the area first, after which PixiJS's
    // caches of text, which are bounded, hold what the area shows.
    const bench = await start(page, board, {
      bench: "pan",
      seconds: String(panSeconds),
      warm: "pass",
    });
    await expect(bench).toHaveAttribute("data-bench-phase", "running", {
      timeout: 300_000,
    });
    const warm = await heap(cdp);
    const r = await result(bench, panSeconds);
    const end = await heap(cdp);
    const growth = (end - warm) / warm;
    await report(
      info,
      target,
      name,
      r,
      `JS heap ${warm.toFixed(1)} MiB after the warm-up, ${end.toFixed(1)} MiB after ${panSeconds} s, ${(growth * 100).toFixed(1)}% (target < 10%); GPU textures ${r.textures.start ?? "—"} to ${r.textures.end ?? "—"}; ${frames(r)}`,
      { heapWarm: warm, heapEnd: end, growth },
    );
    expect(growth).toBeLessThan(0.1);
    // The Canvas 2D renderer keeps no textures on a GPU. One of the GPU's
    // that tells of none could not read PixiJS's count of them, as a
    // version of PixiJS that keeps it otherwise, which fails here, rather
    // than leave the check of the textures out unseen.
    if (r.renderer !== "canvas") {
      expect(r.textures.start, "the GPU's textures").toBeDefined();
      expect(r.textures.end, "the GPU's textures").toBeDefined();
      expect(r.textures.end ?? 0).toBeLessThanOrEqual(
        (r.textures.start ?? 0) * 1.1 + 2,
      );
    }
  });
});
