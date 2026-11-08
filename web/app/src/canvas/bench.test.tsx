import { act, render } from "@testing-library/react";
import { I18nextProvider } from "react-i18next";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createAppI18n } from "../i18n";
import { docOf, testNode } from "../test/canvas";
import Bench from "./bench";
import type { CanvasHandle, FrameRecord } from "./controller";
import { Scene } from "./scene";
import type { Point } from "./viewport";

afterEach(() => {
  vi.useRealTimers();
});

describe("the canvas's benchmark", () => {
  it("puts the node that its edit run moved a step away back where it was as the run ends, before it says that it is done", async () => {
    vi.useFakeTimers({
      toFake: ["setInterval", "clearInterval", "setTimeout", "performance"],
    });
    const scene = new Scene("bd_a");
    let position = { x: 0, y: 0 };
    scene.sync(docOf([testNode("nd_1", 0, 0)]));
    let frame: ((f: FrameRecord) => void) | undefined;
    const handle: CanvasHandle = {
      viewport: () => ({ x: -100, y: -100, zoom: 1 }),
      setViewport: () => {},
      size: () => ({ width: 800, height: 600 }),
      scene,
      ready: () => true,
      kind: () => "canvas",
      textures: () => 0,
      onFrame: (listener) => {
        frame = listener;
        return () => {
          frame = undefined;
        };
      },
    };
    // Each edit applies, and the next frame draws the node where it went.
    const edits: Point[] = [];
    const edit = async (places: ReadonlyMap<string, Point>) => {
      const to = places.get("nd_1") as Point;
      edits.push(to);
      position = to;
      scene.sync(docOf([testNode("nd_1", position.x, position.y)]));
      frame?.({} as FrameRecord);
      return { applied: true };
    };
    const { container } = render(
      <I18nextProvider i18n={createAppI18n(["en"], document.documentElement)}>
        <Bench
          handle={handle}
          options={{ scenario: "edit", seconds: 0.8, warm: 0 }}
          edit={edit}
        />
      </I18nextProvider>,
    );
    const section = container.querySelector("[data-bench-phase]");
    // The run starts, edits once, a step across, and ends before the edit
    // that would move the node back.
    await act(() => vi.advanceTimersByTimeAsync(1100));
    expect(edits).toEqual([
      { x: 8, y: 0 },
      { x: 0, y: 0 },
    ]);
    expect(section?.getAttribute("data-bench-phase")).toBe("done");
    expect(
      JSON.parse(section?.getAttribute("data-bench-result") ?? "{}").edit,
    ).toMatchObject({ count: 1 });
  });
});
