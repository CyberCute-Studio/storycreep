// What the benchmark's setup leaves its runs (fixtures.setup.ts): the
// signed-in account's cookies, and the projects and boards that it loaded,
// in files of the run's own, and where the runs write what they measured.

import { readFileSync } from "node:fs";
import { join } from "node:path";
import { baseURL, binaries } from "../e2e/environment.ts";

/**
 * The instance that the benchmark runs against: the one that Playwright
 * starts, as the end-to-end tests', or, for the reference tablet's
 * checklist, the one that STORYCREEP_BENCH_BASE_URL names, at an address of
 * the LAN that the tablet reaches (docs/canvas-checklist.md).
 */
export const remote = process.env.STORYCREEP_BENCH_BASE_URL !== undefined;

/** The URL of the instance that the benchmark runs against. */
export const benchBase = (
  process.env.STORYCREEP_BENCH_BASE_URL ?? baseURL
).replace(/\/+$/, "");

/** The signed-in account's cookies, which the runs' pages start with. */
export const signedInState = join(binaries, "bench-signed-in.json");

/** The projects and boards that the setup made, by what each is for. */
export const boardsFile = join(binaries, "bench-boards.json");

/** A board of a project that a run opens. */
export interface BenchBoard {
  projectId: string;
  boardId: string;
  /** The rectangle of the board that a run crosses, as x,y,width,height, or none for its content. */
  area?: string;
}

/** What the setup made. */
export interface BenchBoards {
  /** The first board of each fixture. */
  medium: BenchBoard;
  dense: BenchBoard;
  large: BenchBoard;
  /** Boards of 200, 2,000, and 5,000 nodes, of one grid (FR-EDIT-11). */
  b200: BenchBoard;
  b2000: BenchBoard;
  b5000: BenchBoard;
  /** A project of 60 boards that depend on one another, whose dependency view a run draws (NFR-PERF-11), and its first board. */
  deps: BenchBoard;
}

/** Returns what the setup made. */
export function benchBoards(): BenchBoards {
  return JSON.parse(readFileSync(boardsFile, "utf8")) as BenchBoards;
}

/** Where the runs write what they measured: a summary in Markdown, and each run's results as JSON. */
export const resultsDir = new URL("../bench-results/", import.meta.url);
