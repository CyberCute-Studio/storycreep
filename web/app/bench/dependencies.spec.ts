import { appendFileSync, mkdirSync, writeFileSync } from "node:fs";
import type { TestInfo } from "@playwright/test";
import { test } from "../e2e/boards.ts";
import { expect } from "../e2e/fixtures.ts";
import type { DependencyBenchResult } from "../src/dependencies/bench.tsx";
import { benchBoards, resultsDir } from "./state.ts";

// The dependency view's benchmark (PRD NFR-PERF-11, FR-DEP-02 AC3, plan note
// N77): the view of a project of 60 boards that depend on one another, in a
// desktop browser and on an emulated tablet, whose processor is four times
// slower than the runner's and whose network is a LAN's, as the canvas's
// runs emulate it (canvas.spec.ts), from the start of the page's load to the
// frame after the one that first drew its graph. The runner is neither
// reference device, so the tablet's run reports what it measured, which the
// reference tablet's checklist holds to its target of 1 s
// (docs/canvas-checklist.md); the desktop's is held to it here.

/** The target of NFR-PERF-11, in milliseconds. */
const target = 1000;

/** The emulated tablet's slowdown of the runner's processor, as the canvas's runs have it. */
const tabletSlowdown = 4;

function tablet(info: TestInfo): boolean {
  return info.project.name === "tablet";
}

test.describe("the dependency view's benchmark", () => {
  // Verifies: NFR-PERF-11, FR-DEP-02/AC3
  test("the first drawing of the graph of 60 boards, from the start of the page's load", async ({
    page,
  }, info) => {
    const { deps } = benchBoards();
    if (tablet(info)) {
      const cdp = await page.context().newCDPSession(page);
      await cdp.send("Emulation.setCPUThrottlingRate", {
        rate: tabletSlowdown,
      });
      await cdp.send("Network.enable");
      await cdp.send("Network.emulateNetworkConditions", {
        offline: false,
        latency: 2,
        downloadThroughput: (100 * 1e6) / 8,
        uploadThroughput: (100 * 1e6) / 8,
      });
    }
    await page.goto(`/app/projects/${deps.projectId}/dependencies?bench=load`);
    const bench = page.locator("[data-bench-phase]");
    await expect(bench).toHaveAttribute("data-bench-phase", "done", {
      timeout: 120_000,
    });
    const r = JSON.parse(
      (await bench.getAttribute("data-bench-result")) ?? "{}",
    ) as DependencyBenchResult;
    expect(r.boards).toBe(60);
    expect(r.pairs).toBeGreaterThan(100);
    const profile = tablet(info)
      ? `tablet (CPU ${tabletSlowdown}× slower, LAN)`
      : "desktop";
    const line = `graph drawn ${r.rendered.toFixed(1)} ms, ${r.pairs} dependencies of ${r.edges} edges (target < ${target} ms)`;
    mkdirSync(resultsDir, { recursive: true });
    appendFileSync(
      new URL("summary.md", resultsDir),
      `| NFR-PERF-11 | ${profile} | 60 boards | — | ${line} |\n`,
    );
    writeFileSync(
      new URL(`NFR-PERF-11-${info.project.name}-60_boards.json`, resultsDir),
      JSON.stringify({ target: "NFR-PERF-11", profile, ...r }, null, 2),
    );
    info.annotations.push({
      type: "NFR-PERF-11",
      description: `${profile}, 60 boards: ${line}`,
    });
    console.log(`NFR-PERF-11, ${profile}, 60 boards: ${line}`);
    if (!tablet(info)) {
      expect(r.rendered).toBeLessThan(target);
    }
  });
});
