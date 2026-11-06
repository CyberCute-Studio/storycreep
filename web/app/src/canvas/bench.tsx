// The canvas's benchmark (PRD §9.1, NFR-PERF-01, 02, 04, 05, 18, FR-EDIT-01
// AC3, FR-EDIT-11): a scripted run of the canvas that a board's page starts
// with ?bench=…, as the canvas harness's tests in CI do and as the
// reference tablet's checklist does by hand (docs/canvas-checklist.md). It
// drives the viewport along a fixed path, by time, so that every device
// sees the same frames, records each frame's interval and the page's work
// in it, and shows what it measured, which the harness reads from the
// element's data-bench-result. ?bench=load moves nothing, and shows when
// the board's first frame was drawn, from the start of the page's load.

import { formatNumber } from "@storycreep/shared/i18n";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  type BenchOptions,
  type CanvasHandle,
  type FrameRecord,
  renderedMark,
} from "./controller";
import { anchorAt, type Rect, type Viewport, visibleRect } from "./viewport";

/**
 * How long a run waits before it records, in seconds, unless its options
 * say otherwise, so that fonts and textures are made first.
 */
export const warmUp = 2;

/** How fast the path moves on the screen, in CSS pixels a second. */
const speed = 900;

/** The zooms that panzoom swings between, and how long one swing takes, in seconds. */
const zoomRange = [0.25, 1.5] as const;
const zoomPeriod = 6;

/** What a run measured. */
export interface BenchResult {
  scenario: BenchOptions["scenario"];
  seconds: number;
  renderer: string | undefined;
  /** The board's nodes. */
  boardNodes: number;
  /** The frames recorded, after the warm-up. */
  frames: number;
  fps: number;
  /** The intervals between frames, in milliseconds. */
  frame: Stats;
  /** The page's work in a frame, building and drawing it, in milliseconds. */
  work: Stats;
  /** The nodes in the canvas, and in the renderer's margin. */
  visible: { mean: number; max: number };
  held: { max: number };
  /** The GPU's textures at the end of the warm-up and at the end. */
  textures: { start: number | undefined; end: number | undefined };
  /** The page's JavaScript heap, in MiB, where the browser tells it. */
  heap: { start: number; end: number; max: number } | undefined;
  /** When the board's first frame was drawn, in milliseconds from the start of the page's load. */
  rendered: number | undefined;
}

/** The mean, the median, the 95th percentile, and the maximum of some values. */
export interface Stats {
  mean: number;
  p50: number;
  p95: number;
  max: number;
}

/** Returns the statistics of values. */
export function statsOf(values: readonly number[]): Stats {
  if (values.length === 0) {
    return { mean: 0, p50: 0, p95: 0, max: 0 };
  }
  const sorted = [...values].sort((a, b) => a - b);
  const at = (p: number) =>
    sorted[
      Math.min(sorted.length - 1, Math.ceil((p / 100) * sorted.length) - 1)
    ];
  return {
    mean: values.reduce((a, b) => a + b, 0) / values.length,
    p50: at(50),
    p95: at(95),
    max: sorted[sorted.length - 1],
  };
}

/**
 * Returns the viewport of the run at seconds into it, over area, of the
 * board, in a canvas of size: a serpentine across it, row after row, at a
 * steady speed on the screen, at a zoom of 1 for pan, and swinging between
 * 25% and 150% for panzoom.
 */
export function pathAt(
  scenario: "pan" | "panzoom",
  seconds: number,
  area: Rect,
  size: { width: number; height: number },
): Viewport {
  const zoom =
    scenario === "pan"
      ? 1
      : Math.exp(
          Math.log(zoomRange[0]) +
            (Math.log(zoomRange[1]) - Math.log(zoomRange[0])) *
              ((1 - Math.cos((2 * Math.PI * seconds) / zoomPeriod)) / 2),
        );
  // The rows are a canvas apart, at a zoom of 1, so that a pass shows the
  // whole area, each row what the row before did not.
  const gap = Math.max(1, size.height);
  const rows = rowsOf(area, size);
  const width = Math.max(1, area.width);
  const along = (seconds * speed) % (rows * width);
  const row = Math.floor(along / width);
  const into = along - row * width;
  const x = row % 2 === 0 ? area.x + into : area.x + width - into;
  const y = area.y + Math.min(area.height, row * gap);
  return anchorAt({ x, y }, { x: size.width / 2, y: size.height / 2 }, zoom);
}

/** Returns how many rows the path takes to cross area in a canvas of size. */
function rowsOf(area: Rect, size: { height: number }): number {
  return Math.max(1, Math.ceil(area.height / Math.max(1, size.height)) + 1);
}

/**
 * Returns how long the path takes to cross area once, in seconds, in a
 * canvas of size: the warm-up of a run that measures memory, after which
 * PixiJS's caches of text, which are bounded, hold what the area shows.
 */
export function passOf(area: Rect, size: { height: number }): number {
  return (rowsOf(area, size) * Math.max(1, area.width)) / speed;
}

/** Returns the JavaScript heap's size, in MiB, where the browser tells it (Chrome's performance.memory). */
function heapMiB(): number | undefined {
  const memory = (performance as { memory?: { usedJSHeapSize?: number } })
    .memory;
  return memory?.usedJSHeapSize === undefined
    ? undefined
    : memory.usedJSHeapSize / 2 ** 20;
}

/** The benchmark's run and its results, under the canvas. */
export default function Bench({
  handle,
  options,
}: {
  handle: CanvasHandle;
  options: BenchOptions;
}) {
  const { t, i18n } = useTranslation();
  const [phase, setPhase] = useState<"waiting" | "warm" | "running" | "done">(
    "waiting",
  );
  const [result, setResult] = useState<BenchResult>();

  useEffect(() => {
    let started: number | undefined;
    let ended = false;
    const intervals: number[] = [];
    const work: number[] = [];
    const visible: number[] = [];
    let held = 0;
    let last: number | undefined;
    let textureStart: number | undefined;
    const heap: number[] = [];
    let lastHeap = 0;
    const { scenario } = options;
    const finish = () => {
      ended = true;
      stop();
      window.clearInterval(waiting);
      const mark = performance.getEntriesByName(renderedMark)[0];
      setResult({
        scenario,
        seconds: options.seconds,
        renderer: handle.kind(),
        boardNodes: handle.scene.nodes.size,
        frames: work.length,
        fps:
          intervals.length === 0
            ? 0
            : 1000 / (intervals.reduce((a, b) => a + b, 0) / intervals.length),
        frame: statsOf(intervals),
        work: statsOf(work),
        visible: { mean: statsOf(visible).mean, max: statsOf(visible).max },
        held: { max: held },
        textures: { start: textureStart, end: handle.textures() },
        heap:
          heap.length === 0
            ? undefined
            : {
                start: heap[0],
                end: heap[heap.length - 1],
                max: Math.max(...heap),
              },
        rendered: mark?.startTime,
      });
      setPhase("done");
    };
    const areaOf = () =>
      options.area ??
      handle.scene.contentBounds() ??
      visibleRect(handle.viewport(), handle.size());
    let warm = warmUp;
    const onFrame = (f: FrameRecord) => {
      if (ended || started === undefined) {
        return;
      }
      const elapsed = (f.time - started) / 1000;
      if (elapsed >= warm) {
        if (textureStart === undefined) {
          textureStart = handle.textures();
          setPhase("running");
        }
        if (last !== undefined) {
          intervals.push(f.time - last);
        }
        work.push(f.work);
        visible.push(f.stats.visible);
        held = Math.max(held, f.stats.nodes);
        if (f.time - lastHeap >= 1000) {
          lastHeap = f.time;
          const mib = heapMiB();
          if (mib !== undefined) {
            heap.push(mib);
          }
        }
        last = f.time;
      }
      if (elapsed >= warm + options.seconds) {
        finish();
        return;
      }
      // The next frame's viewport, a frame on.
      if (scenario !== "load") {
        handle.setViewport(
          pathAt(scenario, elapsed + 1 / 60, areaOf(), handle.size()),
        );
      }
    };
    const stop = handle.onFrame(onFrame);
    // The run starts once the board's content has been drawn.
    const waiting = window.setInterval(() => {
      if (ended || !handle.ready()) {
        return;
      }
      window.clearInterval(waiting);
      if (scenario === "load") {
        finish();
        return;
      }
      started = performance.now();
      warm =
        options.warm === "pass"
          ? warmUp + passOf(areaOf(), handle.size())
          : (options.warm ?? warmUp);
      setPhase("warm");
      handle.setViewport(pathAt(scenario, 0, areaOf(), handle.size()));
    }, 50);
    return () => {
      ended = true;
      stop();
      window.clearInterval(waiting);
    };
  }, [handle, options]);

  const ms = (value: number | undefined) =>
    value === undefined
      ? t("bench.none")
      : t("bench.ms", {
          value: formatNumber(i18n.language, value, {
            maximumFractionDigits: 2,
          }),
        });
  const n = (value: number | undefined, digits = 0) =>
    value === undefined
      ? t("bench.none")
      : formatNumber(i18n.language, value, { maximumFractionDigits: digits });
  return (
    <section
      aria-labelledby="bench-title"
      className="space-y-2 rounded-md border border-border bg-surface p-4"
      data-bench-phase={phase}
      data-bench-result={
        result === undefined ? undefined : JSON.stringify(result)
      }
    >
      <h2 id="bench-title" className="text-lg font-semibold">
        {t("bench.title", { scenario: options.scenario })}
      </h2>
      <p role="status">
        {phase === "done"
          ? t("bench.done")
          : phase === "running"
            ? t("bench.recording")
            : t("bench.warming")}
      </p>
      {result !== undefined && (
        <dl className="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-1">
          <dt>{t("bench.renderer")}</dt>
          <dd>{result.renderer ?? t("bench.none")}</dd>
          <dt>{t("bench.board_nodes")}</dt>
          <dd>{n(result.boardNodes)}</dd>
          <dt>{t("bench.rendered")}</dt>
          <dd>{ms(result.rendered)}</dd>
          <dt>{t("bench.fps")}</dt>
          <dd>{n(result.fps, 1)}</dd>
          <dt>{t("bench.frame")}</dt>
          <dd>
            {t("bench.stats", {
              p50: ms(result.frame.p50),
              p95: ms(result.frame.p95),
              max: ms(result.frame.max),
            })}
          </dd>
          <dt>{t("bench.work")}</dt>
          <dd>
            {t("bench.stats", {
              p50: ms(result.work.p50),
              p95: ms(result.work.p95),
              max: ms(result.work.max),
            })}
          </dd>
          <dt>{t("bench.visible")}</dt>
          <dd>{n(result.visible.mean, 1)}</dd>
          <dt>{t("bench.heap")}</dt>
          <dd>
            {result.heap === undefined
              ? t("bench.none")
              : t("bench.heap_value", {
                  start: n(result.heap.start, 1),
                  end: n(result.heap.end, 1),
                  max: n(result.heap.max, 1),
                })}
          </dd>
          <dt>{t("bench.textures")}</dt>
          <dd>
            {t("bench.textures_value", {
              start: n(result.textures.start),
              end: n(result.textures.end),
            })}
          </dd>
        </dl>
      )}
    </section>
  );
}
